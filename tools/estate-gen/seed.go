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
//   - Its block is complete, OR it is replacing a placeholder the generic
//     pass already wrote. A block whose cross-resource reference was
//     dropped is evidence, not a configuration - see
//     ExampleArgument.Incomplete, and the SSE case that produced it - so
//     an argument from one may never ADD anything: no new attribute, no
//     new block. But where the generic pass has already admitted it had
//     nothing to offer, the page's literal for that exact argument is
//     still the best evidence available, and refusing it kept 72 override
//     types hand-written whose documented literal was byte-identical to
//     the override's (measured 2026-08-15).
//
//     "Its block is complete" is now a question about THIS run rather
//     than about the page (issue #177's complementary move): a dropped
//     reference whose target type this cohort renders is wired back up by
//     seedRefExpr, and a block every reference of which is wired is a
//     configuration again. The SSE guard becomes mechanical instead of
//     incidental - the s3 cohort renders no aws_kms_key, so
//     kms_master_key_id stays unanswered and sse_algorithm still may not
//     create its block; a cohort that rendered the key would have a
//     complete example and could.
//   - The schema has an argument of that name at that path, and it is
//     settable. A page can name an argument this provider version renamed
//     or made computed, and writing it produces a configuration that fails
//     for a new reason.
//   - Every block on the path holds at most one instance IN THE RENDERED
//     BODY. "Which element does this value belong to" is a question about
//     instances, not about the schema's cap: an unbounded list block the
//     generic pass rendered zero or one of has exactly one possible
//     answer, while two rendered instances have two and stop the walk.
//     The schema's MaxItems said "at most one" for none of the four
//     fixtures issue #174 names - aws_bedrock_inference_profile's
//     model_source is an unbounded list in the wire schema even though
//     the SDK requires exactly one - which is why the earlier
//     schema-shaped version of this rule reached none of them.
//   - The example set the argument's path once. importdocs-gen flattens
//     repeated unlabelled blocks into one path, so a path that appears
//     twice is two example elements merged, and which one wins is a
//     guess. (Two elements setting DISJOINT argument names are still
//     indistinguishable from one element in the artifact; recording an
//     element index at extraction is the fix, and until then that case
//     seeds a merged element.)
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

	// Reference, RefType and RefAttr are issue #177's extraction of what a
	// dropped cross-resource wiring pointed at. A reference entry carries
	// no value: it records that the example set this argument to some
	// other resource's attribute, which is what seedRefExpr needs to
	// decide whether this run can wire the same thing.
	Reference bool   `json:"reference"`
	RefType   string `json:"ref_type"`
	RefAttr   string `json:"ref_attr"`

	// Element is a repeated block's later instance. The generic pass
	// renders exactly one instance of any block, so only element zero has
	// a place to land.
	Element int `json:"element"`
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

	// A repeated block's later element has nowhere to land: the generic
	// pass renders exactly one instance, so element zero is the only
	// answer to "which element does this belong to". Dropped before any
	// counting so a later element cannot make element zero look duplicated.
	writable := args[:0:0]
	for _, arg := range args {
		if arg.Element > 0 || len(arg.Path) == 0 {
			continue
		}
		writable = append(writable, arg)
	}
	args = writable

	// A path the example set twice is two repeated block elements merged
	// flat by the extraction; which one wins is a guess, so neither does.
	// (Element-carrying duplicates were already dropped above; what this
	// still catches is a page setting one path twice inside one element.)
	pathCount := map[string]int{}
	for _, arg := range args {
		pathCount[joinPath(arg.Path)]++
	}

	// The complementary move issue #174 named and #177's extraction made
	// possible. A dropped cross-resource reference is only a hole while
	// this run has nothing to wire it to; seedRefExpr asks the roster, and
	// a block every reference of which resolves is WIRED - its literals
	// stop being evidence-only and its own creation stops being a guess.
	//
	// The SSE guard survives untouched, and mechanically rather than by
	// coincidence: the s3 cohort renders no aws_kms_key, so
	// apply_server_side_encryption_by_default's kms_master_key_id resolves
	// to nothing, the block stays unwired, and sse_algorithm may still not
	// create it. The same walk with the key rendered would be a complete
	// configuration, which is exactly the difference the flag was standing
	// in for.
	refExpr := map[string]string{}
	hasRef := map[string]bool{}
	unwired := map[string]bool{}
	for _, arg := range args {
		if !arg.Reference {
			continue
		}
		prefix := blockPrefix(arg.Path)
		hasRef[prefix] = true
		expr, ok := g.seedRefExpr(tfType, arg)
		if !ok || pathCount[joinPath(arg.Path)] > 1 {
			unwired[prefix] = true
			continue
		}
		refExpr[joinPath(arg.Path)] = expr
	}
	// complete reports whether an argument is a configuration rather than
	// evidence: a literal whose block dropped no reference this run cannot
	// answer, or a reference this run can.
	complete := func(arg seedArgument) bool {
		prefix := blockPrefix(arg.Path)
		wired := hasRef[prefix] && !unwired[prefix]
		if arg.Reference {
			return wired
		}
		return !arg.Incomplete || wired
	}

	// completePaths is every path a complete, non-duplicated argument can
	// write - the set the creation gate checks a new block's required
	// members against.
	completePaths := map[string]bool{}
	// incompleteIn marks each block prefix that holds an incomplete
	// argument: that block dropped a reference nothing here answers, so no
	// argument - not even a complete one nested deeper - may create it.
	// This is the SSE guard extended to the block itself: creating the
	// block plants the configuration with its reference half missing,
	// whichever argument's path the walk arrived by.
	incompleteIn := map[string]bool{}
	for _, arg := range args {
		if pathCount[joinPath(arg.Path)] > 1 {
			continue
		}
		if !complete(arg) {
			incompleteIn[blockPrefix(arg.Path)] = true
		} else {
			completePaths[joinPath(arg.Path)] = true
		}
	}

	var applied []string
	for _, arg := range args {
		if pathCount[joinPath(arg.Path)] > 1 {
			continue
		}
		value := ""
		if arg.Reference {
			expr, ok := refExpr[joinPath(arg.Path)]
			if !ok {
				continue // nothing rendered here answers it
			}
			value = expr
		} else {
			// A TOP-LEVEL naming argument belongs to the generator, not to
			// the page. A documented example names things "example" and
			// "test_role", and every cohort rendering the same type would
			// then ask AWS for the same name - which either collides or
			// silently adopts somebody else's resource. valueExpr already
			// treats this class specially, and looksLikeName is its own
			// predicate for it, so the seed defers to the same rule rather
			// than inventing a second one. (Found in review rather than by
			// reasoning: the first version wrote name = "example" onto
			// aws_secretsmanager_secret in the security cohort.)
			//
			// A NESTED member named "name" is a different thing: a selector
			// or key the provider validates against a closed set - ecs
			// setting's name = "containerInsights", a parameter block's
			// name - with no collision to cause. Skipping those left
			// created blocks missing the very member the provider requires
			// (issue #174).
			//
			// A resolved REFERENCE is exempt from all of this: it invents
			// no name at all, it points at a resource this run renders
			// under this cohort's own label, and valueExpr already ranks
			// every reference tier above both naming tiers.
			if len(arg.Path) == 1 {
				if leaf := arg.Path[0]; leaf == identityArg || looksLikeName(leaf) {
					continue
				}
			}
			value = seedLiteral(arg)
		}
		argComplete := complete(arg)
		mayCreate := func(prefix []string, nb *configschema.NestedBlock) bool {
			if !argComplete || incompleteIn[joinPath(prefix)] {
				return false
			}
			return requiredCovered(prefix, &nb.Block, completePaths)
		}
		target, attr, ok := resolveSeedPath(body, block, arg.Path, mayCreate)
		if !ok {
			continue
		}
		if !attr.Optional && !attr.Required {
			continue // computed-only: writing it is an error, not a fix
		}
		name := arg.Path[len(arg.Path)-1]
		existing := target.GetAttribute(name)
		if !argComplete {
			// Evidence, not a configuration: it may correct a placeholder
			// the generic pass admitted it had nothing for, and only that.
			if existing == nil || !isGenericPlaceholder(existing, attr.Type) {
				continue
			}
		} else if existing != nil && !isGenericPlaceholder(existing, attr.Type) {
			continue // valueExpr computed this on purpose; do not overwrite it
		}
		target.SetAttributeRaw(name, exprTokens(value))
		applied = append(applied, joinPath(arg.Path))
	}
	return applied
}

// seedRefExpr resolves one documented reference argument against this run's
// roster: importdocs-gen records what an example wired an argument to
// (ref_type/ref_attr, issue #177) instead of discarding it, so when this run
// already renders a resource of that type the same wiring is available here,
// under this cohort's own label.
//
// Four gates, none of which names a type:
//
//   - The run renders the referenced type. A var, a local, a path, a data
//     source or a type this cohort does not carry is never a key of byType,
//     so this is also the whole filter against non-resource references, and
//     it is what keeps the s3 cohort's dropped aws_kms_key dropped.
//   - It is not this type itself. A page wiring one instance to another of
//     the same type is describing two resources; this run renders one.
//   - The referenced attribute exists in the sibling's schema for THIS
//     provider version. A page can name an attribute since renamed or made
//     internal, and wiring one writes a reference to something nothing
//     exports.
//   - refAttrAllowed, shared with refAttr rather than restated: an
//     identity-bound slot may only read an attribute in the sibling's own
//     IdentityAttrs, and only when that identity IS the value it wants.
//     Identity resolution refuses anything else outright.
func (g *generator) seedRefExpr(selfType string, arg seedArgument) (string, bool) {
	if !arg.Reference || arg.RefType == "" || arg.RefAttr == "" || len(arg.Path) == 0 {
		return "", false
	}
	if arg.RefType == selfType {
		return "", false
	}
	addr, rendered := g.byType[arg.RefType]
	if !rendered {
		return "", false
	}
	res, ok := g.schemas.ResourceTypes[arg.RefType]
	if !ok || res.Block == nil {
		return "", false
	}
	if a, ok := res.Block.Attributes[arg.RefAttr]; !ok || a == nil {
		return "", false
	}
	leaf := arg.Path[len(arg.Path)-1]
	// Identity components are top-level argument names (no entry in
	// internal/live/identity's table reads a nested path), so a nested slot
	// is never identity-bound and keeps the like-named attribute, which is
	// the right VALUE - refAttr's own reasoning for a non-identity argument.
	identityBound := len(arg.Path) == 1 && identityComponentAttr(selfType, leaf)
	if !refAttrAllowed(arg.RefType, leaf, arg.RefAttr, identityBound) {
		return "", false
	}
	return addr.String() + "." + arg.RefAttr, true
}

// blockPrefix is the block an argument's leaf sits in - "" for a top-level
// argument.
func blockPrefix(path []string) string { return joinPath(path[:len(path)-1]) }

// resolveSeedPath walks a path's leading block names, creating each block
// the generic pass did not - when mayCreate allows it - and returns the
// body to write into plus the schema attribute the last element names.
//
// A block the body already holds two instances of stops the walk: "which
// element does this value belong to" has two answers there, and picking the
// first is the kind of guess that produces a configuration nobody can
// explain later. Zero or one rendered instance has exactly one answer,
// whatever the schema's cap says - the wire schema types
// required-in-practice one-of blocks as unbounded all-optional lists
// (issue #174), so a cap-shaped rule reaches none of them. A map-nested
// block stops the walk either way: its instances are keyed, and the page's
// path carries no key.
func resolveSeedPath(body *hclwrite.Body, block *configschema.Block, path []string, mayCreate func(prefix []string, nb *configschema.NestedBlock) bool) (*hclwrite.Body, *configschema.Attribute, bool) {
	cur := body
	curBlock := block

	for i, name := range path[:len(path)-1] {
		nb, ok := curBlock.BlockTypes[name]
		if !ok || nb == nil {
			return nil, nil, false
		}
		switch nb.Nesting {
		case configschema.NestingSingle, configschema.NestingGroup,
			configschema.NestingList, configschema.NestingSet:
		default:
			return nil, nil, false
		}
		instances := matchingBlocks(cur, name)
		switch len(instances) {
		case 0:
			if !mayCreate(path[:i+1], nb) {
				return nil, nil, false
			}
			instances = append(instances, cur.AppendNewBlock(name, nil))
		case 1:
		default:
			return nil, nil, false
		}
		cur = instances[0].Body()
		curBlock = &nb.Block
	}

	attr, ok := curBlock.Attributes[path[len(path)-1]]
	if !ok || attr == nil {
		return nil, nil, false
	}
	return cur, attr, true
}

// requiredCovered reports whether a block about to be created at prefix
// would arrive complete: every attribute its schema marks Required has a
// complete documented literal at prefix+name, and every required nested
// block is itself reached and covered. An example that leaves a required
// member out - a cross-resource reference the extraction dropped, a map
// literal it cannot render - is evidence about the block, not a
// configuration of it, and creating the block anyway is how the fixture
// picked up setting { value = "enabled" } with no name, and an
// advanced_backup_setting missing its required backup_options (issue
// #174's review of this rule's first regeneration).
func requiredCovered(prefix []string, b *configschema.Block, completePaths map[string]bool) bool {
	for name, attr := range b.Attributes {
		if attr == nil || !attr.Required {
			continue
		}
		p := append(append([]string{}, prefix...), name)
		if !completePaths[joinPath(p)] {
			return false
		}
	}
	for name, nb := range b.BlockTypes {
		if nb == nil || !blockRequired(nb) {
			continue
		}
		sub := append(append([]string{}, prefix...), name)
		if !anyPathUnder(completePaths, joinPath(sub)) {
			return false // nothing would create the required sub-block
		}
		if !requiredCovered(sub, &nb.Block, completePaths) {
			return false
		}
	}
	return true
}

// anyPathUnder reports whether any complete documented path sits inside the
// given block prefix.
func anyPathUnder(completePaths map[string]bool, prefix string) bool {
	for p := range completePaths {
		if strings.HasPrefix(p, prefix+".") {
			return true
		}
	}
	return false
}

// matchingBlocks is every unlabelled nested block of the given type in the
// body - the instance count resolveSeedPath's ambiguity rule reads.
func matchingBlocks(body *hclwrite.Body, name string) []*hclwrite.Block {
	var out []*hclwrite.Block
	for _, b := range body.Blocks() {
		if b.Type() == name && len(b.Labels()) == 0 {
			out = append(out, b)
		}
	}
	return out
}

// isGenericPlaceholder reports whether an attribute still holds the value
// genericExprText produces for its type, meaning valueExpr reached its
// fallthrough and the generator has no opinion about this argument.
func isGenericPlaceholder(attr *hclwrite.Attribute, ty cty.Type) bool {
	got := strings.TrimSpace(string(attr.Expr().BuildTokens(nil).Bytes()))
	return got == genericExprText(ty)
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
