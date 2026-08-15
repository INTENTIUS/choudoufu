// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
)

// This file is issue #136's second half: the seeding pass that gives the
// hand overrides a machine source.
//
// The overrides exist because the AWS provider enforces requirements the
// schema never expresses - an ExactlyOneOf, a RequiredWith, a ValidateFunc
// checking a string's shape - so the generic required-only pass produces a
// configuration terraform validate refuses. Their evidence today is "someone
// ran validate once and this is what it said". The provider's own
// documentation page for each type opens with a working configuration that
// by construction satisfies those same constraints, and importdocs-gen now
// extracts its literal arguments into live/import-grammar.json.
//
// So the ordering is: schema-required pass, then this seed, then Apply. The
// override still runs last and still wins, which makes this additive - every
// existing override keeps its behaviour, and each one can be retired
// individually once the seed is shown to cover it.
//
// # It does not run for a type that still has an override
//
// The first version ran the seed and then let Apply overwrite what it
// disagreed with, on the reasoning that the override runs last and so still
// wins. That reasoning is wrong, and terraform validate said so:
// aws_lambda_layer_version's page sets filename, its override sets s3_bucket
// and s3_key, and the provider marks those mutually exclusive. The override
// only wins on arguments it SETS; a seeded argument it never mentions
// survives beside it. One cohort failed to validate for exactly that.
//
// So an override suppresses the seed for its whole type. That keeps the
// retirement path the issue describes intact and makes each step checkable:
// delete an override, and the seed takes over for that type; run validate,
// and find out whether the page covers what the override was carrying.
//
// # What it will not do
//
// Where it does run, it sets an argument only when all four hold:
//
//   - The generic pass either did not set it, or set it to the placeholder
//     genericExprText produces for its type. That placeholder is the
//     generator saying it had nothing to offer - "placeholder", 0, false -
//     and it is what the overrides were written to replace: the required
//     status argument on aws_s3_bucket_versioning is filled with
//     "placeholder", which is not a member of its enum. A value valueExpr
//     computed on purpose (a parent reference, an identity name, a role
//     ref) is left alone, because that one carries meaning the page cannot
//     know - the doc's own "example-bucket" would collide across cohorts.
//   - The example marked it complete. An argument whose block had a
//     cross-resource reference dropped is evidence, not a configuration -
//     see ExampleArgument.Incomplete, and the SSE case that produced it.
//   - The schema has an argument of that name at that path, and it is
//     settable. A page can name an argument this provider version renamed
//     or made computed, and writing it produces a configuration that fails
//     for a new reason.
//   - The path's blocks are all single-instance. A list-nested block needs
//     to know which element the value belongs to, which one page's example
//     does not say.
//
// Nothing here names a resource type. The moment this file needs to know
// about aws_s3_bucket_versioning specifically, the override has been moved
// rather than retired, and issue #136 says so explicitly.

// exampleSeed is the per-type literal arguments the provider's own
// documentation sets, read from live/import-grammar.json.
type exampleSeed map[string][]seedArgument

type seedArgument struct {
	Path       []string `json:"path"`
	Value      string   `json:"value"`
	IsString   bool     `json:"is_string"`
	Incomplete bool     `json:"incomplete"`
}

// loadExampleSeed reads the artifact. A missing file is not fatal: the seed
// is additive, and a generator run without it produces exactly what it
// produced before this pass existed.
func loadExampleSeed(root string) (exampleSeed, error) {
	raw, err := os.ReadFile(filepath.Join(root, "live", "import-grammar.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading live/import-grammar.json: %w", err)
	}
	var art struct {
		Rows []struct {
			TFType           string         `json:"tf_type"`
			ExampleArguments []seedArgument `json:"example_arguments"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(raw, &art); err != nil {
		return nil, fmt.Errorf("decoding live/import-grammar.json: %w", err)
	}
	out := make(exampleSeed, len(art.Rows))
	for _, r := range art.Rows {
		if len(r.ExampleArguments) > 0 {
			out[r.TFType] = r.ExampleArguments
		}
	}
	return out, nil
}

// seedFromExample writes the documented literals the generic pass left
// unset, and reports which paths it set so the README's provenance can name
// them the way it names an override.
func (g *generator) seedFromExample(body *hclwrite.Body, block *configschema.Block, tfType string) []string {
	if _, overridden := typeOverrides[tfType]; overridden {
		return nil
	}
	args := g.seed[tfType]
	if len(args) == 0 || block == nil {
		return nil
	}

	identityArg, _ := identityArgName(tfType)

	var applied []string
	for _, arg := range args {
		if arg.Incomplete || len(arg.Path) == 0 {
			continue
		}
		// Naming arguments belong to the generator, not to the page. A
		// documented example names things "example" and "test_role", and
		// every cohort rendering the same type would then ask AWS for the
		// same name - which either collides or silently adopts somebody
		// else's resource. valueExpr already treats this class specially,
		// and looksLikeName is its own predicate for it, so the seed defers
		// to the same rule rather than inventing a second one.
		//
		// Found in review rather than by reasoning: the first version wrote
		// name = "example" onto aws_secretsmanager_secret in the security
		// cohort.
		if leaf := arg.Path[len(arg.Path)-1]; leaf == identityArg || looksLikeName(leaf) {
			continue
		}
		target, attr, ok := resolveSeedPath(body, block, arg.Path)
		if !ok {
			continue
		}
		if !attr.Optional && !attr.Required {
			continue // computed-only: writing it is an error, not a fix
		}
		name := arg.Path[len(arg.Path)-1]
		if existing := target.GetAttribute(name); existing != nil && !isGenericPlaceholder(existing, attr.Type) {
			continue // valueExpr computed this on purpose; do not overwrite it
		}
		target.SetAttributeRaw(name, exprTokens(seedLiteral(arg)))
		applied = append(applied, joinPath(arg.Path))
	}
	return applied
}

// resolveSeedPath walks a path's leading block names, creating each block if
// the generic pass did not, and returns the body to write into plus the
// schema attribute the last element names.
//
// A block that can hold more than one instance stops the walk. "which
// element does this value belong to" is a question one page's example does
// not answer, and picking the first is the kind of guess that produces a
// configuration nobody can explain later.
func resolveSeedPath(body *hclwrite.Body, block *configschema.Block, path []string) (*hclwrite.Body, *configschema.Attribute, bool) {
	cur := body
	curBlock := block

	for _, name := range path[:len(path)-1] {
		nb, ok := curBlock.BlockTypes[name]
		if !ok || nb == nil {
			return nil, nil, false
		}
		if !singleInstance(nb) {
			return nil, nil, false
		}
		existing := cur.FirstMatchingBlock(name, nil)
		if existing == nil {
			existing = cur.AppendNewBlock(name, nil)
		}
		cur = existing.Body()
		curBlock = &nb.Block
	}

	attr, ok := curBlock.Attributes[path[len(path)-1]]
	if !ok || attr == nil {
		return nil, nil, false
	}
	return cur, attr, true
}

// isGenericPlaceholder reports whether an attribute still holds the value
// genericExprText produces for its type, meaning valueExpr reached its
// fallthrough and the generator has no opinion about this argument.
func isGenericPlaceholder(attr *hclwrite.Attribute, ty cty.Type) bool {
	got := strings.TrimSpace(string(attr.Expr().BuildTokens(nil).Bytes()))
	return got == genericExprText(ty)
}

// singleInstance reports whether a nested block holds exactly one instance,
// so a path through it is unambiguous.
func singleInstance(nb *configschema.NestedBlock) bool {
	switch nb.Nesting {
	case configschema.NestingSingle, configschema.NestingGroup:
		return true
	case configschema.NestingList, configschema.NestingSet:
		return nb.MinItems == 1 && nb.MaxItems == 1
	default:
		return false
	}
}

// seedLiteral renders the value as HCL source.
func seedLiteral(arg seedArgument) string {
	if arg.IsString {
		return strconv.Quote(arg.Value)
	}
	return arg.Value
}

func joinPath(path []string) string {
	out := ""
	for i, p := range path {
		if i > 0 {
			out += "."
		}
		out += p
	}
	return out
}
