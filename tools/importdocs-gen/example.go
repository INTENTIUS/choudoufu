// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"regexp"
	"sort"
	"strconv"
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

	// IsList marks a Value that is a whole HCL list literal of primitives
	// ("[\"example\"]", issue #177) rather than a scalar; a consumer
	// writes it raw, exactly like a number or bool.
	IsList bool `json:"is_list,omitempty"`

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

	// Reference records a dropped cross-resource wiring instead of
	// discarding it (issue #177): the example set this path to an
	// expression reading something else, and RefType/RefAttr say what -
	// "aws_kms_key"/"arn" for `kms_master_key_id = aws_kms_key.mykey.arn`,
	// "var"/"name" for a variable. A reference entry carries no Value; it
	// is evidence about how the example wires this argument, which is
	// exactly what a consumer needs to decide whether an incomplete block
	// could be completed from its own rendered siblings.
	Reference bool   `json:"reference,omitempty"`
	RefType   string `json:"ref_type,omitempty"`
	RefAttr   string `json:"ref_attr,omitempty"`

	// Element is which repeated unlabelled block element of its type this
	// argument came from, counting from zero (issue #177): without it, two
	// `rule { }` blocks setting disjoint keys flatten into one path set
	// indistinguishable from a single element. Zero - the only element, or
	// the first - is omitted from the artifact.
	Element int `json:"element,omitempty"`
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
// One fence's block wins - taking a union across variants would merge
// mutually exclusive settings, which is how a seed starts producing
// configurations the provider rejects. WHICH fence (issue #177): the first
// whose extraction is fully literal (no dropped reference, no incomplete
// block); failing that, the fence with the most REACHABLE literals
// (reachableLiterals) - a page may lead with a wired-up variant and keep
// the self-contained one second, which is exactly aws_sagemaker_workteam's
// shape: its "Cognito Usage" members are all references while its "Oidc
// Usage" carries the literal oidc_member_definition the seed wants, and
// both variants reference the shared workforce, so neither is clean.
// Failing every literal, the first fence with any entry - a reference-only
// extraction is still evidence, and the reference rule in
// tools/estate-gen/seed.go can wire it back up.
func exampleArguments(doc, tfType string) []ExampleArgument {
	section, ok := exampleSection(doc)
	if !ok {
		return nil
	}
	var best, firstAny []ExampleArgument
	bestComplete := 0
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
			args := literalArguments(blk.Body, nil)
			if len(args) == 0 {
				continue
			}
			clean := true
			for _, a := range args {
				if a.Reference || a.Incomplete {
					clean = false
				}
			}
			complete := reachableLiterals(args)
			if clean && complete > 0 {
				return args
			}
			if complete > bestComplete {
				best, bestComplete = args, complete
			}
			if firstAny == nil {
				firstAny = args
			}
		}
	}
	if best != nil {
		return best
	}
	return firstAny
}

// reachableLiterals is the variant-preference score: how many of this
// fence's literals a consumer could actually write, rather than how many it
// contains.
//
// The two differ, and counting the wrong one picked the wrong variant twice
// (found by regenerating the estate cohorts against the pinned 6.59.0 cache
// and reading the diff, 2026-08-15). Three shapes are unreachable by this
// artifact's own stated contract:
//
//   - A later block element. Only element zero has a place to land, so
//     aws_secretsmanager_secret_rotation's partner-integration variant
//     scored 2 on its second external_secret_rotation_metadata element and
//     beat the Basic variant, whose rotation_rules {
//     automatically_after_days = 30 } is the one thing the fixture needed.
//   - A literal inside a block that dropped a reference. That block may not
//     be created, so nothing inside it can be written:
//     aws_ssm_maintenance_window_task's Run Command variant scored 2 on the
//     parameter block nested inside run_command_parameters, which drops
//     three references, and beat the Automation variant's reachable
//     document_version.
//   - A literal already marked Incomplete, or a reference, which were never
//     counted.
//
// The resource's own body is not a block a consumer creates - it always
// exists - so a reference at the root does not make the whole variant
// unreachable. A root literal beside a dropped root reference is already
// Incomplete and excluded on that ground.
func reachableLiterals(args []ExampleArgument) int {
	refIn := map[string]bool{}
	for _, a := range args {
		if a.Reference && len(a.Path) > 1 {
			refIn[strings.Join(a.Path[:len(a.Path)-1], ".")] = true
		}
	}
	n := 0
	for _, a := range args {
		if a.Reference || a.Incomplete || a.Element > 0 {
			continue
		}
		reachable := true
		for i := 1; i < len(a.Path); i++ {
			if refIn[strings.Join(a.Path[:i], ".")] {
				reachable = false
				break
			}
		}
		if reachable {
			n++
		}
	}
	return n
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
		// The wiring is recorded rather than discarded (issue #177): what
		// the example reads is the fact a consumer needs to judge whether
		// the block could be completed from its own siblings.
		if vars := attr.Expr.Variables(); len(vars) > 0 {
			refType, refAttr := referenceParts(vars[0])
			out = append(out, ExampleArgument{
				Path:      append(append([]string{}, prefix...), name),
				Reference: true,
				RefType:   refType,
				RefAttr:   refAttr,
			})
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
			if !out[i].Reference {
				out[i].Incomplete = true
			}
		}
	}

	elementOf := map[string]int{}
	for _, blk := range body.Blocks {
		if len(blk.Labels) > 0 {
			// A labelled nested block is a repeated structure whose label
			// is part of its identity; seeding one without knowing what
			// the label means is guessing.
			continue
		}
		element := elementOf[blk.Type]
		elementOf[blk.Type]++
		nested := literalArguments(blk.Body, append(append([]string{}, prefix...), blk.Type))
		if element > 0 {
			// A repeated unlabelled block's later elements carry their
			// index (issue #177), so two elements setting disjoint keys
			// stop flattening into something indistinguishable from one.
			for i := range nested {
				if nested[i].Element == 0 {
					nested[i].Element = element
				}
			}
		}
		out = append(out, nested...)
	}
	return out
}

// referenceParts names what a dropped expression reads: the traversal's
// root and, past a resource's own label, the attribute it takes.
// aws_kms_key.mykey.arn -> ("aws_kms_key", "arn"); var.name ->
// ("var", "name"); a bare root -> (root, "").
func referenceParts(t hcl.Traversal) (refType, refAttr string) {
	refType = t.RootName()
	for _, step := range t {
		if a, ok := step.(hcl.TraverseAttr); ok {
			refAttr = a.Name
		}
	}
	if strings.HasPrefix(refType, "aws_") {
		// The first attribute step is the resource's local name
		// ("mykey"), not an attribute; when it is the ONLY step, the
		// example referenced the resource object itself.
		steps := 0
		for _, step := range t {
			if _, ok := step.(hcl.TraverseAttr); ok {
				steps++
			}
		}
		if steps < 2 {
			refAttr = ""
		}
	}
	return refType, refAttr
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
	case val.Type().IsTupleType() || val.Type().IsListType() || val.Type().IsSetType():
		// A list of primitives is spelled back as the HCL list literal
		// (issue #177: aws_sagemaker_workteam's groups = ["example"]).
		// Value holds the whole expression, so IsString stays false - a
		// consumer writes it raw - and IsList says which case this is.
		var elems []string
		for it := val.ElementIterator(); it.Next(); {
			_, ev := it.Element()
			inner, ok := renderLiteral(nil, ev)
			if !ok || inner.IsList {
				return ExampleArgument{}, false
			}
			if inner.IsString {
				elems = append(elems, strconv.Quote(inner.Value))
			} else {
				elems = append(elems, inner.Value)
			}
		}
		return ExampleArgument{Path: path, Value: "[" + strings.Join(elems, ", ") + "]", IsList: true}, true
	}
	return ExampleArgument{}, false
}
