// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
)

// resourceAddr is one resource block's address: its provider-local type and
// the local label this generator gave it (every requested coverage type
// gets "app"; an auto-added supporting resource gets the cohort name, the
// same convention live/e2e/estates/lambda/iam.tf's aws_iam_role.lambda
// already uses by hand).
type resourceAddr struct {
	Type  string
	Label string
}

func (a resourceAddr) String() string { return a.Type + "." + a.Label }

// resourceKind says whether a resourceAddr is a requested coverage row or a
// supporting resource this generator added on its own to satisfy another
// resource's required argument (live/e2e/estates/lambda/README.md's
// "Supporting, not coverage" distinction, made mechanical).
type resourceKind int

const (
	kindCoverage resourceKind = iota
	kindSupporting
)

// planned is one resource this run will render, plus the bookkeeping the
// README's provenance table and file placement need.
type planned struct {
	Addr resourceAddr
	Kind resourceKind
}

// generator holds everything one estate-gen run needs to answer "what does
// resource X's required argument Y evaluate to": the schemas, which other
// resources this run is also rendering (so a required argument that is a
// parent reference can point at them), and the cohort's naming convention.
type generator struct {
	cohort  string
	schemas providers.GetProviderSchemaResponse

	// order is every resource this run renders, coverage rows first
	// (sorted by type), then supporting resources (sorted by type) - the
	// file layout and the README table walk this order.
	order []planned

	// byType maps a provider-local type name to the resourceAddr this run
	// gave it, for every resource in order. A type appears at most once:
	// two coverage types that would otherwise both need the same
	// supporting resource (two Lambda types both needing an execution
	// role) share the one auto-added instance, the same sharing
	// live/e2e/estates/lambda/lambda.tf's two aws_iam_role.lambda
	// references already do by hand.
	byType map[string]resourceAddr
}

// iamRoleRefExpr is the one curated cross-type alias this generator knows
// that the identity table cannot derive on its own (identityArgName below):
// a "role" or "*_role_arn" required argument names another resource's ARN,
// not its identity-table argument, because the identity table's Components
// describe how to reconstruct *this* resource's own import identity from
// configuration, not which attribute of some other resource a caller wants
// to reference. See planCohort and valueExpr.
func (g *generator) iamRoleRefExpr() (string, bool) {
	addr, ok := g.byType["aws_iam_role"]
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%s.arn", addr), true
}

// isRoleArg reports whether argName is the curated role-reference alias:
// exactly "role", or any argument ending "_role_arn" (permissions_config's
// capacity_provider_operator_role_arn, in the Lambda cohort).
func isRoleArg(argName string) bool {
	return argName == "role" || strings.HasSuffix(argName, "_role_arn")
}

// identityArgName returns the argument name that carries typeName's own
// client-set identity, per internal/live/identity's table - the "identity
// arguments named from the identity table" issue #56 asks for - when that
// type has a single-component, self-named identity (attr(argName) alone;
// every simple client-named row in DefaultTable is exactly this shape).
// Server-assigned types and composite identities (more than one component)
// return ok=false: their identity is not one configuration argument this
// generator can name a placeholder for.
func identityArgName(typeName string) (string, bool) {
	entry, ok := identity.LookupType(typeName)
	if !ok || entry.ServerAssigned || len(entry.Components) != 1 {
		return "", false
	}
	c := entry.Components[0]
	if len(c.Attrs) == 0 {
		return "", false
	}
	return c.Attrs[0], true
}

// planCohort decides the full set of resources a run over requested types
// will render: the requested types themselves (coverage rows) plus
// whichever supporting resources their required arguments need
// (isRoleArg's aws_iam_role today; the mechanism is not specific to IAM,
// see the comment on iamRoleRefExpr). Order is deterministic: coverage rows
// sorted by type, then supporting resources sorted by type.
func planCohort(cohort string, schemas providers.GetProviderSchemaResponse, requested []string) (*generator, error) {
	g := &generator{
		cohort:  cohort,
		schemas: schemas,
		byType:  map[string]resourceAddr{},
	}

	sortedRequested := append([]string(nil), requested...)
	sort.Strings(sortedRequested)

	for _, t := range sortedRequested {
		if _, ok := schemas.ResourceTypes[t]; !ok {
			return nil, fmt.Errorf("%s: not in the provider's schema (%s %s)", t, providerSource, providerVersion)
		}
		addr := resourceAddr{Type: t, Label: "app"}
		g.order = append(g.order, planned{Addr: addr, Kind: kindCoverage})
		g.byType[t] = addr
	}

	needsRole := false
	for _, p := range g.order {
		for name := range requiredArgNames(schemas.ResourceTypes[p.Addr.Type].Block) {
			if isRoleArg(name) {
				needsRole = true
			}
		}
		if ov, ok := typeOverrides[p.Addr.Type]; ok && ov.NeedsIAMRole {
			needsRole = true
		}
	}
	if needsRole {
		if _, already := g.byType["aws_iam_role"]; !already {
			addr := resourceAddr{Type: "aws_iam_role", Label: cohort}
			g.order = append(g.order, planned{Addr: addr, Kind: kindSupporting})
			g.byType["aws_iam_role"] = addr
		}
	}

	return g, nil
}

// requiredArgNames is the set of top-level attribute names a block's own
// schema marks Required. Nested nesting/optionality is handled separately
// by fillBlock; this is only the shallow scan planCohort needs to decide
// whether a type needs the role alias.
func requiredArgNames(b *configschema.Block) map[string]bool {
	out := map[string]bool{}
	if b == nil {
		return out
	}
	for name, attr := range b.Attributes {
		if attr.Required {
			out[name] = true
		}
	}
	return out
}

// blockRequired reports whether a nested block must appear at least once:
// NestingList/NestingSet/NestingMap with MinItems >= 1, or NestingSingle
// with both MinItems and MaxItems set to 1 (configschema.NestedBlock's own
// documented special case for "this single block is required").
// NestingGroup is never required by this rule - it exists for blocks the
// provider fills in with defaults, never ones a caller must supply.
func blockRequired(nb *configschema.NestedBlock) bool {
	switch nb.Nesting {
	case configschema.NestingSingle:
		return nb.MinItems == 1 && nb.MaxItems == 1
	case configschema.NestingList, configschema.NestingSet, configschema.NestingMap:
		return nb.MinItems >= 1
	default:
		return false
	}
}

// taggable mirrors internal/live/stamp's predicate (also duplicated in
// tools/survey-gen/classify.go and tools/row-gen): a top-level, settable
// tags argument typed map(string). estate tags are only ever added to a
// type this returns true for.
func taggable(b *configschema.Block) bool {
	if b == nil {
		return false
	}
	attr, ok := b.Attributes["tags"]
	if !ok || attr == nil || (!attr.Optional && !attr.Required) {
		return false
	}
	ty := attr.Type
	if !ty.IsMapType() {
		return false
	}
	et := ty.ElementType()
	return et == cty.String || et == cty.DynamicPseudoType
}

// render builds one resource block's full text - its coverage/provenance
// comment plus its HCL body - and returns it along with the override
// reasons actually applied, for the README's provenance table.
func (g *generator) render(p planned) (string, []string) {
	f := hclwrite.NewEmptyFile()
	body := f.Body()
	rb := body.AppendNewBlock("resource", []string{p.Addr.Type, p.Addr.Label})
	rBody := rb.Body()

	block := g.schemas.ResourceTypes[p.Addr.Type].Block
	g.fillBlock(rBody, block, p.Addr, true)

	var overrides []string
	if ov, ok := typeOverrides[p.Addr.Type]; ok {
		ov.Apply(g, rBody, p.Addr)
		overrides = ov.Reasons
	}

	if taggable(block) {
		rBody.SetAttributeRaw("tags", exprTokens(fmt.Sprintf(`{
    tofu-estate  = local.estate_tag
    tofu-address = %q
  }`, p.Addr.String())))
	}

	comment := g.comment(p, overrides)
	return comment + string(f.Bytes()), overrides
}

// comment is the coverage/provenance header every generated resource
// carries - issue #56's "generated by estate-gen from <inputs>; overrides:
// none" - rendered as a plain HCL line comment block so a reader sees it
// exactly the way live/e2e/estates/lambda/lambda.tf's hand-written coverage
// comments read.
func (g *generator) comment(p planned, overrides []string) string {
	var b strings.Builder
	kind := "Coverage"
	if p.Kind == kindSupporting {
		kind = "Supporting, not coverage - added only so " + p.Addr.String() + "'s referring resources have a value to point at"
	}
	fmt.Fprintf(&b, "# %s: generated by estate-gen from %s %s and the identity table (%s).\n",
		kind, providerSource, providerVersion, "internal/live/identity/table.go")
	if len(overrides) == 0 {
		b.WriteString("# overrides: none\n")
	} else {
		fmt.Fprintf(&b, "# overrides: %s\n", strings.Join(overrides, "; "))
	}
	return b.String()
}

// fillBlock recursively fills a block's required content: every attribute
// its own schema marks Required, plus - only at root, i.e. the resource's
// own top-level block, never inside a nested block - the argument that
// carries the type's identity per internal/live/identity's table, whether
// or not the schema itself requires it (aws_s3_bucket's own "bucket" is
// Optional+Computed in the wire schema, but the identity table needs it set
// to a known value for the fixture to mean anything).
func (g *generator) fillBlock(body *hclwrite.Body, b *configschema.Block, addr resourceAddr, root bool) {
	names := map[string]bool{}
	for name, attr := range b.Attributes {
		if attr.Required {
			names[name] = true
		}
	}
	identityArg := ""
	if root {
		if argName, ok := identityArgName(addr.Type); ok {
			identityArg = argName
			names[argName] = true
		}
	}

	ordered := make([]string, 0, len(names))
	for name := range names {
		if name == identityArg {
			continue
		}
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	if identityArg != "" {
		ordered = append([]string{identityArg}, ordered...)
	}

	for _, name := range ordered {
		attr := b.Attributes[name]
		expr := g.valueExpr(addr, name, attr.Type, root)
		body.SetAttributeRaw(name, exprTokens(expr))
	}

	var blockNames []string
	for name, nb := range b.BlockTypes {
		if blockRequired(nb) {
			blockNames = append(blockNames, name)
		}
	}
	sort.Strings(blockNames)
	for _, name := range blockNames {
		nb := b.BlockTypes[name]
		n := nb.MinItems
		if n < 1 {
			n = 1
		}
		for i := 0; i < n; i++ {
			nested := body.AppendNewBlock(name, nil)
			g.fillBlock(nested.Body(), &nb.Block, addr, false)
		}
	}
}

// valueExpr is the value (or reference expression) for one required
// argument, in priority order:
//
//  1. The curated role alias (isRoleArg): a reference to the cohort's
//     supporting aws_iam_role, the one cross-type link this generator
//     knows by hand rather than by reading it off a table (see
//     iamRoleRefExpr).
//  2. A parent reference: some *other* resource this run is also
//     rendering carries this same argument name as its own identity-table
//     argument (aws_s3_bucket_policy's "bucket" and aws_s3_bucket's own
//     "bucket" are the same name), so the value is a reference to that
//     resource's same-named attribute rather than an independent
//     placeholder - this is what keeps a generated cohort's child
//     resources actually pointed at its parent, live/e2e/estates/lambda's
//     hand-written role -> function/capacity-provider wiring made
//     mechanical.
//  3. This resource's own identity argument (identityArgName): a
//     deterministic, type-derived placeholder name,
//     "tofu-<cohort>-cohort-<suffix>".
//  4. A required string argument that merely looks like a name (looksLikeName)
//     but carries no identity-table row at all - a server-assigned type's
//     own naming argument, such as aws_lambda_layer_version's layer_name -
//     gets the same deterministic placeholder name tier 3 would have used,
//     rather than the generic "placeholder" literal, since many such
//     arguments carry the provider's own charset/length validation and a
//     descriptive, unique name is the safer generic guess.
//  5. Anything else: a type-driven generic placeholder (genericExprText).
func (g *generator) valueExpr(addr resourceAddr, argName string, ty cty.Type, root bool) string {
	if isRoleArg(argName) {
		if ref, ok := g.iamRoleRefExpr(); ok {
			return ref
		}
	}
	if root {
		if parent, attrName, ok := g.parentRef(addr.Type, argName); ok {
			return fmt.Sprintf("%s.%s", parent, attrName)
		}
		if idArg, ok := identityArgName(addr.Type); ok && idArg == argName {
			return fmt.Sprintf("%q", fmt.Sprintf("tofu-%s-cohort-%s", g.cohort, identitySuffix(g.cohort, addr.Type)))
		}
		if ty == cty.String && looksLikeName(argName) {
			return fmt.Sprintf("%q", fmt.Sprintf("tofu-%s-cohort-%s", g.cohort, identitySuffix(g.cohort, addr.Type)))
		}
	}
	return genericExprText(ty)
}

// looksLikeName reports whether argName is shaped like a naming argument
// ("name" or "*_name") - the class of required string argument a real AWS
// resource almost always constrains by charset or length, which is why
// valueExpr treats it more carefully than an arbitrary required string.
func looksLikeName(argName string) bool {
	return argName == "name" || strings.HasSuffix(argName, "_name")
}

// parentRef looks for another resource this run is rendering whose own
// identity-table argument is named argName, excluding selfType. Finding one
// is what makes argName a parent reference rather than selfType's own
// identity.
//
// The identity table alone cannot always tell a genuine parent link from
// coincidence: aws_lambda_event_source_mapping has no identity component of
// its own, so its "function_name" argument unambiguously names
// aws_lambda_function - candidates is decisive on its own. But
// aws_s3_bucket_policy and aws_s3_bucket both carry the *same* shape
// (Components: []Component{attr("bucket")}) - the table records that a
// bucket policy is "identified by the bucket's own name"
// (internal/live/identity/table.go's comment), which is indistinguishable,
// from Components alone, from two unrelated types that both happen to
// self-identify by "name" (aws_iam_role and aws_lambda_capacity_provider,
// which produced a real dependency cycle in this generator's own
// lambda-cohort trial run before this rule existed).
//
// The tiebreaker is AWS's own naming convention: a genuine child type's
// name is its parent type's name plus "_" plus a suffix
// (aws_s3_bucket_policy is aws_s3_bucket + "_policy"). When selfType
// independently owns argName as its own identity too, a same-named
// candidate is only accepted as the parent when selfType's name is
// prefixed by the candidate's - never the reverse, which is what keeps
// aws_s3_bucket pointed at nothing (it is the shortest name in its own
// group) while every one of its five children points at it, and keeps
// aws_iam_role and aws_lambda_capacity_provider - neither a prefix of the
// other - both keeping their own "name" placeholder instead of an
// arbitrary, cycle-prone pick between them.
func (g *generator) parentRef(selfType, argName string) (parent resourceAddr, attrName string, ok bool) {
	var candidates []string
	for t := range g.byType {
		if t == selfType {
			continue
		}
		if idArg, idOK := identityArgName(t); idOK && idArg == argName {
			candidates = append(candidates, t)
		}
	}
	if len(candidates) == 0 {
		return resourceAddr{}, "", false
	}
	sort.Strings(candidates)

	selfIdArg, selfOwnsIt := identityArgName(selfType)
	if !selfOwnsIt || selfIdArg != argName {
		// selfType has no competing claim on argName: any candidate is
		// safely a parent, and the lexicographically-first one is the
		// deterministic pick when more than one type happens to share it.
		return g.byType[candidates[0]], argName, true
	}

	for _, c := range candidates {
		if strings.HasPrefix(selfType, c+"_") {
			return g.byType[c], argName, true
		}
	}
	return resourceAddr{}, "", false
}

// identitySuffix derives a short, deterministic, per-type word for an
// identity placeholder's name: typeName with the leading "aws_" and, when
// present, the cohort's own name segment stripped (so "lambda" is not
// repeated inside "tofu-lambda-cohort-lambda-function"), underscores turned
// to hyphens. Not an attempt to reproduce any specific hand-written
// fixture's word choice - just a rule guaranteed to differ across the
// types in one cohort, which is all a placeholder name needs.
func identitySuffix(cohort, typeName string) string {
	s := strings.TrimPrefix(typeName, "aws_")
	s = strings.TrimPrefix(s, cohort+"_")
	s = strings.ReplaceAll(s, "_", "-")
	if s == "" {
		s = strings.ReplaceAll(typeName, "_", "-")
	}
	return s
}

// exprTokens parses src as an HCL expression and returns its tokens, so
// SetAttributeRaw can install any expression - a literal, a traversal like
// aws_iam_role.lambda.arn, or an object constructor like tags's - built as
// plain text rather than assembled token-by-token. hclwrite.ParseConfig
// never fails on the fixed, generator-built strings this function is
// called with; a diags error here is this tool's own bug, not a user
// input problem, so it panics rather than threading an error through every
// valueExpr caller.
func exprTokens(src string) hclwrite.Tokens {
	f, diags := hclwrite.ParseConfig([]byte("x = "+src+"\n"), "estate-gen-expr.tf", hcl.InitialPos)
	if diags.HasErrors() {
		panic(fmt.Sprintf("estate-gen: BUG: generated an unparseable expression %q: %s", src, diags.Error()))
	}
	attr := f.Body().GetAttribute("x")
	return attr.Expr().BuildTokens(nil)
}

// genericExprText is the generic, type-driven placeholder used when no
// identity argument, parent reference or override applies: this is the
// "deterministic placeholder values by type" issue #56 asks for, and
// deliberately nothing cleverer - a required argument whose provider-side
// validation needs a specific shape (valid JSON, an enum member, an ARN
// pattern) needs a typeOverrides entry, not a smarter generic guess here.
func genericExprText(ty cty.Type) string {
	switch {
	case ty == cty.String:
		return `"placeholder"`
	case ty == cty.Number:
		return "0"
	case ty == cty.Bool:
		return "false"
	case ty.IsListType(), ty.IsSetType(), ty.IsTupleType():
		return "[" + genericExprText(ty.ElementType()) + "]"
	case ty.IsMapType(), ty.IsObjectType():
		return "{}"
	default:
		return `"placeholder"`
	}
}

// formatWithBinary shells out to fmtBin (ordinarily "terraform") to
// canonicalize a directory's *.tf files - column-aligning attribute "="
// signs the way live/e2e/estates/lambda's hand-written files are aligned,
// which hclwrite's own token-level API does not do on its own. This is a
// pure function of the input, so it does not threaten determinism: the
// same generated bytes always format to the same output.
func formatWithBinary(fmtBin, dir string, run func(name string, args ...string) ([]byte, error)) error {
	out, err := run(fmtBin, "fmt", dir)
	if err != nil {
		return fmt.Errorf("%s fmt %s: %w\n%s", fmtBin, dir, err, out)
	}
	return nil
}
