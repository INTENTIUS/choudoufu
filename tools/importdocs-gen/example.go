// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
)

// This file is issue #136's first half. tools/estate-gen carries 6,463
// lines of hand-written per-type overrides, each existing because the AWS
// provider enforces a requirement through plan-time validation that never
// reaches configschema.Attribute.Required - an ExactlyOneOf, a RequiredWith,
// a ValidateFunc checking a string's shape. Their evidence today is "the
// generic pass's output was run through terraform validate and this is what
// it refused", which is a fact about a run somebody did once.
//
// The provider's own documentation page for each type opens with a working
// configuration, written by its maintainers, which by construction satisfies
// those constraints. This tool already fetches and caches every one of those
// pages. So the same facts are available with better provenance - "the
// provider's own documented example sets this" - checkable on every
// regeneration and moving with the provider version.
//
// The issue asked for one decision to be made before any code: whether the
// Example Usage block survives into live/import-grammar.json, because if not
// the extraction belongs here rather than in estate-gen. It does not; the
// artifact carried seven fields and none of them was the example. The cache
// is markdown with fenced terraform blocks, so this is the tool that reads
// it, and the result is a new artifact field.
//
// # Literals only, and that is the whole design
//
// A page's example is a working configuration, which means it wires
// resources to each other: aws_s3_bucket_versioning's example sets
// `bucket = aws_s3_bucket.example.id`. estate-gen renders its own siblings
// with its own names, so seeding that reference produces a dangling one.
//
// So this extracts only arguments whose value is self-contained - a string,
// number or bool literal, or a list of them - and drops every expression
// that reads anything else. That is not a limitation worked around; it is
// the partition the issue already describes. The overrides a page can retire
// are the enum-member and JSON-policy ones, whose values are literals. The
// ones that must stay hand-written are the cross-resource helpers, whose
// values are references, and no single type's page knows what siblings a
// cohort renders anyway.

// ExampleArgument is one argument the documented example sets to a literal.
type ExampleArgument struct {
	// Path is the argument's position, outermost first:
	// ["versioning_configuration", "status"] for a nested block's
	// attribute, ["bucket"] for a top-level one.
	Path []string `json:"path"`

	// Value is the literal, rendered as the source spelled it. Numbers and
	// bools are rendered unquoted so a consumer can tell them from strings.
	Value string `json:"value"`

	// IsString is whether Value needs quoting when written back out.
	IsString bool `json:"is_string,omitempty"`

	// Incomplete marks an argument whose own block had a sibling that was
	// dropped because it REFERENCED something else - another resource, a
	// variable, a data source.
	//
	// It is the difference between a seed that helps and one that breaks
	// things, and it was found by measuring rather than by reasoning.
	// aws_s3_bucket_server_side_encryption_configuration's example is:
	//
	//	apply_server_side_encryption_by_default {
	//	  kms_master_key_id = aws_kms_key.mykey.arn
	//	  sse_algorithm     = "aws:kms"
	//	}
	//
	// The reference drops out and the literal stays, so a naive seed writes
	// sse_algorithm = "aws:kms" with no key - which the provider rejects,
	// and which is exactly why estate-gen's existing override hardcodes
	// "AES256" instead. Taking the literal alone would have made that type
	// worse while looking like progress.
	//
	// A dropped sibling that merely had no literal rendering - a map, an
	// object - does not set this. That absence is this extraction's own
	// limit rather than evidence of coupling, and treating the two the same
	// marked aws_iam_role's assume_role_policy incomplete because a tags
	// map sat beside it, which says nothing about whether the policy can
	// stand alone. A reference is a claim that the block is wired to
	// something; an unrenderable literal is a claim about this parser.
	//
	// A consumer should treat an incomplete block as evidence about what
	// the arguments are, not as a configuration to paste.
	Incomplete bool `json:"incomplete,omitempty"`
}

// exampleFenceRe matches a fenced code block whose language is one the
// provider's docs use for configuration. Older pages say "hcl", newer ones
// "terraform"; both appear in the 6.59.0 cache.
var exampleFenceRe = regexp.MustCompile("(?s)```(?:terraform|hcl)\\n(.*?)```")

// exampleSection returns the "## Example Usage" heading's content, from the
// heading to the next level-2 heading or end of file. Sub-headings stay
// inside: s3_bucket_versioning's example lives under "### With Versioning
// Enabled", and a parser that stopped at any heading would find nothing.
func exampleSection(doc string) (string, bool) {
	lines := strings.Split(doc, "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimRight(l, " \t"), "## Example Usage") {
			start = i
			break
		}
	}
	if start == -1 {
		return "", false
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n"), true
}

// exampleArguments extracts the literal arguments the doc's own Example
// Usage sets on a resource of tfType.
//
// The first block declaring the type wins. Pages often show several
// variants - "With Versioning Enabled", "With Versioning Disabled",
// "Object Lock" - and the first is the ordinary case the page leads with,
// which is the one a generated fixture wants. Taking a union across
// variants would merge mutually exclusive settings, which is how a seed
// starts producing configurations the provider rejects.
func exampleArguments(doc, tfType string) []ExampleArgument {
	section, ok := exampleSection(doc)
	if !ok {
		return nil
	}
	for _, m := range exampleFenceRe.FindAllStringSubmatch(section, -1) {
		body, diags := hclsyntax.ParseConfig([]byte(m[1]), "example.tf", hcl.InitialPos)
		if diags.HasErrors() || body == nil {
			// A page whose example does not parse is a page this extraction
			// says nothing about. The provider's docs include deliberately
			// partial snippets and the occasional typo, and inventing a
			// value from one is worse than having none.
			continue
		}
		syn, ok := body.Body.(*hclsyntax.Body)
		if !ok {
			continue
		}
		for _, blk := range syn.Blocks {
			if blk.Type != "resource" || len(blk.Labels) != 2 || blk.Labels[0] != tfType {
				continue
			}
			if args := literalArguments(blk.Body, nil); len(args) > 0 {
				return args
			}
		}
	}
	return nil
}

// literalArguments walks one block for arguments that are self-contained
// literals, recursing into nested blocks so that
// versioning_configuration { status = "Enabled" } is reached.
func literalArguments(body *hclsyntax.Body, prefix []string) []ExampleArgument {
	var out []ExampleArgument
	dropped := 0

	names := make([]string, 0, len(body.Attributes))
	for name := range body.Attributes {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		attr := body.Attributes[name]
		// A value that reads anything - another resource, a variable, a
		// local, a data source - is exactly what must not be seeded. Zero
		// variables is the test, and it is the whole cross-resource filter.
		if len(attr.Expr.Variables()) > 0 {
			dropped++
			continue
		}
		val, diags := attr.Expr.Value(exampleEvalContext())
		if diags.HasErrors() || val.IsNull() || !val.IsWhollyKnown() {
			// An impure or unknown call. Not a reference, but not something
			// this can vouch for either, so it does not contaminate.
			continue
		}
		if arg, ok := renderLiteral(append(append([]string{}, prefix...), name), val); ok {
			out = append(out, arg)
		}
	}
	if dropped > 0 {
		for i := range out {
			out[i].Incomplete = true
		}
	}

	for _, blk := range body.Blocks {
		if len(blk.Labels) > 0 {
			// A labelled nested block is a repeated structure whose label
			// is part of its identity; seeding one without knowing what
			// the label means is guessing.
			continue
		}
		out = append(out, literalArguments(blk.Body, append(append([]string{}, prefix...), blk.Type))...)
	}
	return out
}

// exampleEvalContext is the evaluation context a documented example is read
// under: no variables at all, and the pure functions the provider's own docs
// use to build literal values.
//
// jsonencode is the one that matters. The provider writes every IAM policy
// in its examples as jsonencode({...}), including aws_iam_role's
// assume_role_policy - which is one of the two override classes issue #136
// names as a page already answering. Evaluating it here turns that example
// into the literal JSON string the override was carrying by hand.
//
// Only pure functions belong here. A function whose result depends on when
// or where it runs would make this artifact non-reproducible, which is the
// property every other generated artifact in this tree is held to.
func exampleEvalContext() *hcl.EvalContext {
	return &hcl.EvalContext{
		Functions: map[string]function.Function{
			"jsonencode": stdlib.JSONEncodeFunc,
			"lower":      stdlib.LowerFunc,
			"upper":      stdlib.UpperFunc,
			"join":       stdlib.JoinFunc,
			"format":     stdlib.FormatFunc,
		},
	}
}

// renderLiteral turns a cty value into the artifact's rendering, refusing
// anything it cannot spell exactly.
func renderLiteral(path []string, val cty.Value) (ExampleArgument, bool) {
	switch {
	case val.Type() == cty.String:
		return ExampleArgument{Path: path, Value: val.AsString(), IsString: true}, true
	case val.Type() == cty.Bool:
		if val.True() {
			return ExampleArgument{Path: path, Value: "true"}, true
		}
		return ExampleArgument{Path: path, Value: "false"}, true
	case val.Type() == cty.Number:
		bf := val.AsBigFloat()
		return ExampleArgument{Path: path, Value: bf.Text('f', -1)}, true
	}
	return ExampleArgument{}, false
}
