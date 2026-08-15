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

	// seed is the literal arguments each type's own documentation page
	// sets, from live/import-grammar.json (issue #136). Empty when the
	// artifact is absent, which makes the seeding pass a no-op and this
	// generator produce exactly what it produced before it existed.
	seed exampleSeed

	// cfnRequired is each TF type's root-required members per
	// CloudFormation's registry schema, from
	// live/registry-schema-facts.json joined through live/mapping.json
	// (issues #155, #174) - the second, independent source for members the
	// wire schema under-declares. Empty when either artifact is absent,
	// which makes the pass a no-op.
	cfnRequired map[string][]string

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

	// modulePrefix is prepended to the tofu-address literal [render]
	// writes into a taggable resource's tags, so that a -module-wrap run's
	// hardcoded marker names the address the resource actually has once
	// the module call wraps it - "module.wrapped.aws_eip.app", not
	// "aws_eip.app" - rather than one the stamping pass would immediately
	// reject as a conflict on the first plan. Empty for an unwrapped run,
	// which is every run before issue #59, 59b. Unused when moduleKeyVar is
	// set (see below): a keyed module's prefix is not a fixed string.
	modulePrefix string

	// moduleKeyVar is the name of the variable a -module-wrap -module-keys
	// run's wrapped module declares to receive its own module instance's
	// for_each key (issue #59, 59c) - "key" for every run today, but named
	// via this field rather than a hardcoded literal at the one place
	// [render] builds the tofu-address expression, so the two stay in sync
	// even if a future caller wants a different name. Empty for every run
	// before 59c and for an unkeyed -module-wrap run: [render] falls back
	// to the plain literal address exactly as it always has.
	//
	// A keyed module's instances share one HCL body for the wrapped
	// resource's tags argument (there is exactly one resource block on
	// disk, expanded N times by the module call's own for_each), so the
	// tofu-address this generator writes has to be an expression that
	// varies per real instance rather than a fixed string - the same
	// reason a resource's own for_each writes count.index or each.key into
	// its own tofu-address. A module call has no each.key of its own
	// visible inside the module it calls, though - OpenTofu does not
	// implicitly pass one - so the wrapped module declares a variable and
	// the call passes each.key into it explicitly
	// ([moduleWrapKeyedMainTF]/[wrappedVariablesTF]), the ordinary idiom
	// for any value that must vary per module instance.
	moduleKeyVar string
}

// tofuAddressLiteral is the tofu-address value [render] writes into a
// taggable resource's tags: a plain quoted literal for an unwrapped or
// statically-wrapped run, and - when moduleKeyVar is set (59c, issue #59
// phase 3, a keyed -module-wrap run) - an interpolated template that reads
// the instance's own key from that variable, so one shared resource block
// produces a distinct, correct tofu-address for each of the module's
// instances at real apply time.
func (g *generator) tofuAddressLiteral(addr resourceAddr) string {
	if g.moduleKeyVar == "" {
		return fmt.Sprintf("%q", g.modulePrefix+addr.String())
	}
	return fmt.Sprintf(`"module.%s[\"${var.%s}\"].%s"`, wrappedModuleDir, g.moduleKeyVar, addr.String())
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

// identityComponentArgs returns every argument name a type's identity
// components read (the first alternative of each), in component order, for
// fillBlock's root pass: a composite identity's arguments must be set for
// the fixture to mean anything, whether or not the wire schema requires
// them (aws_cloudwatch_event_rule's "name" and aws_lambda_permission's
// "statement_id" are Optional - the provider invents a value when they are
// omitted, and an invented value is exactly what a live-marker fixture
// cannot round-trip). A component carrying a Default is skipped on
// purpose: the provider documents what omission means there (the "default"
// event bus), so leaving it out both is legal and exercises the fallback
// the way real configurations do. Server-assigned types return nil, as
// ever - their identity is not configuration's to write. identityArgName
// above stays the single-component question the parent-synthesis and
// sibling-pairing sites ask; this is the fill-stage question only.
func identityComponentArgs(typeName string) []string {
	entry, ok := identity.LookupType(typeName)
	if !ok || entry.ServerAssigned {
		return nil
	}
	var out []string
	for _, c := range entry.Components {
		if len(c.Attrs) == 0 || c.Default != "" {
			continue
		}
		out = append(out, c.Attrs[0])
	}
	return out
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

	// Loaded here rather than by the caller so that main and both test
	// entry points cannot disagree about whether the seed is present. A
	// generator that renders one way under test and another in the sweep is
	// how a drift check comes to certify the wrong thing.
	if root, err := repoRoot(); err == nil {
		seed, err := loadExampleSeed(root)
		if err != nil {
			return nil, err
		}
		g.seed = seed
		cfnReq, err := loadCFNRequired(root)
		if err != nil {
			return nil, err
		}
		g.cfnRequired = cfnReq
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
	supporting := map[string]bool{}
	for _, p := range g.order {
		for name := range requiredArgNames(schemas.ResourceTypes[p.Addr.Type].Block) {
			if isRoleArg(name) {
				needsRole = true
			}
		}
		if ov, ok := typeOverrides[p.Addr.Type]; ok {
			if ov.NeedsIAMRole {
				needsRole = true
			}
			for _, t := range ov.NeedsSupporting {
				supporting[t] = true
			}
		}
	}
	if needsRole {
		supporting["aws_iam_role"] = true
	}

	// A coverage type whose own identity argument names a parent type this
	// run does not render gets that parent generated as a supporting
	// resource - the treatment the shared aws_iam_role donor already gets,
	// derived from the same reference rule siblingRef applies, rather than
	// a hand NeedsSupporting entry (issue #173's escaping shape 2:
	// aws_ecr_lifecycle_policy's "repository" names an aws_ecr_repository
	// that lives in a different cohort, so the tier-4 self-name was a
	// syntactically valid name for a repository existing nowhere). The
	// parent type is constructed from the argument name and the child's own
	// service segment (constructedParentTypes), must exist in the
	// provider's schema and the identity table, and - for a suffixed
	// identity argument - must prefix the child's own name, mirroring
	// siblingRef's tiebreaker so a parent is only synthesized where the
	// reference would actually be wired. Scoped to the identity argument on
	// purpose: a non-identity reference argument with no rendered sibling
	// keeps its placeholder rather than pulling in a supporting chain this
	// generator cannot bound.
	for _, p := range g.order {
		idArg, ok := identityArgName(p.Addr.Type)
		if !ok {
			continue
		}
		block := schemas.ResourceTypes[p.Addr.Type].Block
		if block == nil {
			continue
		}
		if attr, ok := block.Attributes[idArg]; !ok || attr == nil || (!attr.Required && !attr.Optional) {
			continue // fillBlock would never emit it, so nothing would reference the parent
		}
		if _, _, wired := g.siblingRef(p.Addr.Type, idArg); wired {
			continue // a rendered sibling already answers it
		}
		_, suffix := refArgSuffix(idArg)
		for _, cand := range constructedParentTypes(p.Addr.Type, idArg) {
			if suffix != "" && !strings.HasPrefix(p.Addr.Type, cand+"_") {
				continue
			}
			if _, inCohort := g.byType[cand]; inCohort {
				break
			}
			if _, inSchema := schemas.ResourceTypes[cand]; !inSchema {
				continue
			}
			if _, admitted := identity.LookupType(cand); !admitted {
				continue
			}
			supporting[cand] = true
			break
		}
	}
	supportingSorted := make([]string, 0, len(supporting))
	for t := range supporting {
		supportingSorted = append(supportingSorted, t)
	}
	sort.Strings(supportingSorted)
	for _, t := range supportingSorted {
		if _, already := g.byType[t]; already {
			continue
		}
		if _, ok := schemas.ResourceTypes[t]; !ok {
			return nil, fmt.Errorf("supporting type %s: not in the provider's schema (%s %s)", t, providerSource, providerVersion)
		}
		addr := resourceAddr{Type: t, Label: cohort}
		g.order = append(g.order, planned{Addr: addr, Kind: kindSupporting})
		g.byType[t] = addr
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

	// Schema-required pass, then the documented example, then CFN's root
	// required (which only fills what is still unset), then the hand
	// override - which still runs last and still wins, so this is additive
	// (issues #136, #174).
	var overrides []string
	if seeded := g.seedFromExample(rBody, block, p.Addr.Type); len(seeded) > 0 {
		overrides = append(overrides, "doc example: "+strings.Join(seeded, ", "))
	}
	if cfnApplied := g.applyCFNRequired(rBody, block, p.Addr); len(cfnApplied) > 0 {
		overrides = append(overrides, "cfn-required (live/registry-schema-facts.json): "+strings.Join(cfnApplied, ", "))
	}
	if ov, ok := typeOverrides[p.Addr.Type]; ok {
		ov.Apply(g, rBody, p.Addr)
		overrides = append(overrides, ov.Reasons...)
	}

	if taggable(block) {
		rBody.SetAttributeRaw("tags", exprTokens(fmt.Sprintf(`{
    tofu-estate  = local.estate_tag
    tofu-address = %s
  }`, g.tofuAddressLiteral(p.Addr))))
	}

	comment := g.comment(p, overrides)
	return comment + string(f.Bytes()), overrides
}

// renderCounted builds a count-scaled resource block: N replicas of one
// schema-simple type, via HCL's count meta-argument, rather than N literal
// resource blocks. This is estate-gen's answer to issue #64's scale
// benchmark: planCohort/render's unit is "one resource per admitted type",
// which never reaches the hundreds or thousands of instances a scale
// estate needs, but the identity and tagging logic those functions already
// apply at one instance's granularity is exactly what each of the N
// instances needs too - so this reuses identityArgName and taggable rather
// than inventing a parallel notion of "how does this type identify and tag
// itself", and lets HCL's own count.index fan the single block out to N
// live resources instead of this tool ever holding N resource bodies in
// memory or on disk.
//
// "Schema-simple" is deliberately narrow: p.Addr.Type must have a
// single-component, self-named identity (identityArgName), that identity
// argument must be the type's only required top-level argument, it must
// carry no required nested block, and it must be taggable - so every
// instance's own tofu-address stays distinguishable by tag alone, the same
// invariant every other estate-gen output relies on. A type that fails any
// of these is refused with an error naming which rule it broke, rather
// than this function guessing at a value for a required argument it does
// not understand well enough to vary safely across N instances.
func (g *generator) renderCounted(p planned, count int) (string, error) {
	block := g.schemas.ResourceTypes[p.Addr.Type].Block

	idArg, ok := identityArgName(p.Addr.Type)
	if !ok {
		return "", fmt.Errorf("%s: -count requires a type with a single-component, self-named identity (see internal/live/identity.LookupType)", p.Addr.Type)
	}
	// The identity argument itself is allowed to be schema-required or not
	// (aws_s3_bucket's "bucket" is Optional+Computed in the wire schema,
	// the same case fillBlock's own root-only special case handles) - what
	// this refuses is any *other* required argument, which this function
	// has no schema-driven way to vary safely across N instances.
	var otherRequired []string
	for name := range requiredArgNames(block) {
		if name != idArg {
			otherRequired = append(otherRequired, name)
		}
	}
	if len(otherRequired) > 0 {
		sort.Strings(otherRequired)
		return "", fmt.Errorf("%s: -count requires the identity argument (%s) to be the type's only required argument; also required: %s", p.Addr.Type, idArg, strings.Join(otherRequired, ", "))
	}
	for name, nb := range block.BlockTypes {
		if blockRequired(nb) {
			return "", fmt.Errorf("%s: -count requires no required nested blocks; found %q", p.Addr.Type, name)
		}
	}
	if !taggable(block) {
		return "", fmt.Errorf("%s: -count requires a taggable type, so each instance's tofu-address stays distinguishable", p.Addr.Type)
	}

	f := hclwrite.NewEmptyFile()
	body := f.Body()
	rb := body.AppendNewBlock("resource", []string{p.Addr.Type, p.Addr.Label})
	rBody := rb.Body()
	rBody.SetAttributeRaw("count", exprTokens(fmt.Sprintf("%d", count)))

	name := fmt.Sprintf("tofu-%s-cohort-%s-${count.index}", g.cohort, identitySuffix(g.cohort, p.Addr.Type))
	rBody.SetAttributeRaw(idArg, exprTokens(fmt.Sprintf("%q", name)))

	address := p.Addr.String() + ":${count.index}"
	rBody.SetAttributeRaw("tags", exprTokens(fmt.Sprintf(`{
    tofu-estate  = local.estate_tag
    tofu-address = %q
  }`, address)))

	comment := fmt.Sprintf("# Coverage: generated by estate-gen -count=%d from %s %s and the identity table (internal/live/identity/table.go).\n"+
		"# %d instances via HCL count, not %d literal blocks - issue #64's scale benchmark.\n", count, providerSource, providerVersion, count, count)
	return comment + string(f.Bytes()), nil
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
		for i, argName := range identityComponentArgs(addr.Type) {
			// Only when the schema says the attribute is configurable. The
			// identity table describes how a value IDENTIFIES the resource,
			// not where it is written: aws_s3control_multi_region_access_point
			// is identified by its top-level "name", but that attribute is
			// Computed-only - the configurable name lives at details.name -
			// and emitting it produced 'Can't configure a value for "name"',
			// which cost the s3 cohort its acceptance-tier pass when the
			// type was adopted (#108; found by the tier, reverted, fixed
			// here as the rule-level gap it is).
			if attr, ok := b.Attributes[argName]; ok && (attr.Required || attr.Optional) {
				if i == 0 {
					identityArg = argName
				}
				names[argName] = true
			}
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
//  3. A sibling reference (siblingRef, issue #173): the argument's own
//     name says which other resource it points at - "<base>_id"/"<base>_arn"
//     naming a sibling type this run also renders (prefix_list_id against
//     aws_ec2_managed_prefix_list), or a bare identity-component argument
//     whose service-qualified parent type is rendered here (the codeartifact
//     dependents' "domain" against aws_codeartifact_domain). The referenced
//     attribute comes from the sibling's schema and the identity table, not
//     a guess - see refAttr.
//  4. This resource's own identity argument (identityArgName): a
//     deterministic, type-derived placeholder name,
//     "tofu-<cohort>-cohort-<suffix>".
//  5. A required string argument that merely looks like a name (looksLikeName)
//     but carries no identity-table row at all - a server-assigned type's
//     own naming argument, such as aws_lambda_layer_version's layer_name -
//     gets the same deterministic placeholder name tier 4 would have used,
//     rather than the generic "placeholder" literal, since many such
//     arguments carry the provider's own charset/length validation and a
//     descriptive, unique name is the safer generic guess.
//  6. Anything else: a type-driven generic placeholder (genericExprText).
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
		if parent, attrName, ok := g.siblingRef(addr.Type, argName); ok {
			return fmt.Sprintf("%s.%s", parent, attrName)
		}
		if lit, ok := g.pairedSeedLiteral(addr.Type, argName); ok {
			return lit
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

// refArgSuffix splits a reference-shaped argument name: "<base>_id" or
// "<base>_arn" with a non-empty base ("prefix_list_id" -> "prefix_list",
// "id"). Any other shape returns two empty strings. AWS's own argument
// naming convention is the rule here: an argument named after another
// resource kind plus "_id"/"_arn" carries that resource's exported handle,
// never an independent value (issue #173's escaping shape 1).
func refArgSuffix(argName string) (base, suffix string) {
	for _, s := range []string{"id", "arn"} {
		if b, ok := strings.CutSuffix(argName, "_"+s); ok && b != "" {
			return b, s
		}
	}
	return "", ""
}

// serviceSegment is a type name's first underscore-separated word after
// "aws_" - "ecr" for aws_ecr_lifecycle_policy - the provider's own service
// grouping convention, used both to scope suffix matches (a "_route_table"
// suffix match across services is a coincidence, not a reference) and to
// construct a partitioned-away parent's type name.
func serviceSegment(typeName string) string {
	rest := strings.TrimPrefix(typeName, "aws_")
	if i := strings.Index(rest, "_"); i >= 0 {
		return rest[:i]
	}
	return rest
}

// constructedParentTypes derives the type names argName could be naming as
// a parent, from the argument name and selfType's own service segment
// alone: "aws_<service>_<base>" and "aws_<base>", for the argument name
// as-is and with a trailing "_id"/"_arn" stripped. selfType itself is never
// a candidate - which is also what keeps a type whose identity argument is
// its own name (aws_codeartifact_repository's "repository" constructs
// aws_codeartifact_repository, itself) from referencing anything.
func constructedParentTypes(selfType, argName string) []string {
	service := serviceSegment(selfType)
	bases := []string{argName}
	if b, _ := refArgSuffix(argName); b != "" {
		bases = append(bases, b)
	}
	seen := map[string]bool{selfType: true}
	var out []string
	for _, b := range bases {
		for _, cand := range []string{"aws_" + service + "_" + b, "aws_" + b} {
			if !seen[cand] {
				seen[cand] = true
				out = append(out, cand)
			}
		}
	}
	return out
}

// identityComponentAttr reports whether argName is one of typeName's own
// identity components per internal/live/identity's table - the gate for the
// bare (unsuffixed) sibling-reference shape: an identity-bearing argument
// must name a real resource for the fixture's markers to mean anything,
// while a bare argument outside the identity ("policy", "description") says
// nothing about what it points at.
func identityComponentAttr(typeName, argName string) bool {
	entry, ok := identity.LookupType(typeName)
	if !ok || entry.ServerAssigned {
		return false
	}
	for _, c := range entry.Components {
		for _, a := range c.Attrs {
			if a == argName {
				return true
			}
		}
	}
	return false
}

// siblingForBase finds the rendered sibling type a "<base>_id"/"<base>_arn"
// argument is naming: the type spelled exactly "aws_<base>" (any service -
// "network_acl_id" against aws_network_acl), else a type whose name ends in
// "_<base>" within selfType's own service segment ("prefix_list_id" against
// aws_ec2_managed_prefix_list; the service scope is what keeps
// aws_vpc_endpoint_route_table_association's route_table_id off the
// unrelated aws_ec2_transit_gateway_route_table). Among several same-service
// matches the one sharing the longest name prefix with selfType wins
// (aws_fsx_ontap_storage_virtual_machine's file_system_id lands on
// aws_fsx_ontap_file_system, not the lustre/openzfs/windows siblings), then
// lexicographic order - deterministic either way.
func (g *generator) siblingForBase(selfType, base string) (string, bool) {
	selfService := serviceSegment(selfType)
	var exact string
	var suffixed []string
	for t := range g.byType {
		if t == selfType {
			continue
		}
		stripped := strings.TrimPrefix(t, "aws_")
		switch {
		case stripped == base:
			exact = t
		case strings.HasSuffix(stripped, "_"+base) && serviceSegment(t) == selfService:
			suffixed = append(suffixed, t)
		}
	}
	if exact != "" {
		return exact, true
	}
	if len(suffixed) == 0 {
		return "", false
	}
	sort.Slice(suffixed, func(i, j int) bool {
		pi, pj := commonPrefixLen(suffixed[i], selfType), commonPrefixLen(suffixed[j], selfType)
		if pi != pj {
			return pi > pj
		}
		return suffixed[i] < suffixed[j]
	})
	return suffixed[0], true
}

func commonPrefixLen(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// refAttr picks which of the sibling's attributes the reference reads,
// from the sibling's own schema and the identity table rather than a guess:
// an attribute named exactly like the argument when the sibling exports one
// (aws_workspacesweb_portal's portal_arn, aws_bedrockagent_agent's
// agent_id), else - for a suffixed argument - the plain "id"/"arn" the
// suffix names, else - for a bare argument - the sibling's own identity
// argument (aws_ecr_repository's "name" for a "repository" argument) and
// finally its "id". Every choice is gated on the attribute existing in the
// sibling's schema; when none does, the reference is not wired at all.
func (g *generator) refAttr(siblingType, argName, suffix string, identityBound bool) (string, bool) {
	res, ok := g.schemas.ResourceTypes[siblingType]
	if !ok || res.Block == nil {
		return "", false
	}
	has := func(n string) bool {
		_, ok := res.Block.Attributes[n]
		return ok
	}
	// When the argument being wired is an identity component of the CHILD,
	// identity resolution will read the reference and refuse any attribute
	// outside the sibling's own IdentityAttrs ("Not an identity attribute")
	// - which the first version of this rule tripped on
	// aws_transfer_web_app_customization.web_app_id reading the sibling's
	// like-named but non-identity web_app_id (its IdentityAttrs are id and
	// arn). A non-identity argument keeps the like-named attribute, which
	// is the right VALUE: a Connect child's routing_profile_id must not
	// become the sibling's composite id.
	//
	// An IdentityAttrs attribute's VALUE is the sibling's import identity
	// (table.go's own definition), so the reference is also only correct
	// when that identity IS the value the slot wants. A sibling whose
	// identity is an assembled ARN (any Literal or Cloud component) fails
	// that for every non-_arn slot: aws_codeartifact_domain's id is the
	// domain's ARN, and a child's bare "domain" slot fed the ARN composes
	// a wrong identity and a value the API rejects. Those pairs render
	// matching literals instead (pairedSeedLiteral).
	allowed := func(n string) bool {
		if !identityBound {
			return true
		}
		entry, found := identity.LookupType(siblingType)
		if !found {
			return true // schema-fallback sibling: no ledger to restrict against
		}
		if !strings.HasSuffix(argName, "_arn") {
			for _, c := range entry.Components {
				if c.Literal != "" || c.Cloud != "" {
					return false
				}
			}
		}
		for _, a := range entry.IdentityAttrs {
			if a == n {
				return true
			}
		}
		return false
	}
	if has(argName) && allowed(argName) {
		return argName, true
	}
	if suffix != "" {
		if has(suffix) && allowed(suffix) {
			return suffix, true
		}
		return "", false
	}
	if idArg, ok := identityArgName(siblingType); ok && has(idArg) && allowed(idArg) {
		return idArg, true
	}
	if has("id") && allowed("id") {
		return "id", true
	}
	return "", false
}

// siblingRef is issue #173's rule-level fix for reference arguments
// parentRef cannot see: parentRef only links an argument that shares its
// NAME with a sibling's identity-table argument, while a reference-by-id
// argument (prefix_list_id, framework_id, bot_id) never does, and a bare
// identity-component argument can name a parent whose own identity is a
// different word entirely (aws_codeartifact_domain's identity is its ARN
// assembly, but its dependents' "domain" argument still names it).
//
// Two shapes, both derived from the argument name and the roster this run
// already holds - no per-type table:
//
//   - "<base>_id"/"<base>_arn": base resolves to a rendered sibling type
//     (siblingForBase). When the argument is also selfType's own identity
//     argument, the same tiebreaker parentRef uses applies: only a
//     candidate whose name prefixes selfType's is a parent rather than a
//     coincidence, which is what keeps aws_dms_s3_endpoint's endpoint_id
//     its own name instead of a reference to aws_dms_endpoint.
//   - bare: the argument is one of selfType's own identity components and
//     its service-qualified construction (constructedParentTypes) is a
//     rendered sibling - the codeartifact dependents' "domain".
func (g *generator) siblingRef(selfType, argName string) (parent resourceAddr, attrName string, ok bool) {
	base, suffix := refArgSuffix(argName)
	if base != "" {
		t, found := g.siblingForBase(selfType, base)
		if !found {
			return resourceAddr{}, "", false
		}
		if selfIdArg, owns := identityArgName(selfType); owns && selfIdArg == argName && !strings.HasPrefix(selfType, t+"_") {
			return resourceAddr{}, "", false
		}
		attr, found := g.refAttr(t, argName, suffix, identityComponentAttr(selfType, argName))
		if !found {
			return resourceAddr{}, "", false
		}
		return g.byType[t], attr, true
	}

	if !identityComponentAttr(selfType, argName) {
		return resourceAddr{}, "", false
	}
	for _, cand := range constructedParentTypes(selfType, argName) {
		addr, found := g.byType[cand]
		if !found {
			continue
		}
		attr, found := g.refAttr(cand, argName, "", true)
		if !found {
			continue
		}
		return addr, attr, true
	}
	return resourceAddr{}, "", false
}

// pairedSeedLiteral renders the same documented literal the sibling that
// owns argName renders for it, for an identity-bound argument no reference
// can carry (an assembled-identity parent; see refAttr's allowed). Both
// sides read the seed through the same lookup, so the pair matches by
// construction rather than by the coincidence that held before issue #173:
// aws_codeartifact_domain seeds domain = "example" from its page, and its
// three dependents render the same "example" here. Without a seed literal
// both sides fall through to the same generic placeholder, which pairs
// too.
func (g *generator) pairedSeedLiteral(selfType, argName string) (string, bool) {
	if !identityComponentAttr(selfType, argName) {
		return "", false
	}
	for _, cand := range constructedParentTypes(selfType, argName) {
		if _, rendered := g.byType[cand]; !rendered {
			continue
		}
		if _, overridden := typeOverrides[cand]; overridden {
			return "", false // the override owns the parent's value; do not guess it
		}
		if idArg, ok := identityArgName(cand); (ok && idArg == argName) || looksLikeName(argName) {
			return "", false // the seed defers these on the parent too; no literal to mirror
		}
		for _, arg := range g.seed[cand] {
			// Incomplete literals count: seedFromExample writes them onto
			// the parent when they replace a placeholder, and the value is
			// what pairs - the structural caution behind the flag is about
			// creating arguments, which this never does.
			if len(arg.Path) != 1 || arg.Path[0] != argName {
				continue
			}
			return seedLiteral(arg), true
		}
		return "", false
	}
	return "", false
}

// pruneUnreferencedSupporting drops every supporting resource whose
// address no other rendered block references, and returns the texts that
// remain, keeping g.order and g.byType in step. A supporting resource
// exists only so a referring resource has a value to point at (the
// kindSupporting comment's own definition); when the reference that
// justified it never lands in the rendered text - a hand override
// replacing the wired reference with its own literal is the case that
// exists today - emitting the resource anyway would put an unreferenced,
// unexplained block in the cohort. Iterates to a fixpoint so a supporting
// resource referenced only by another pruned one goes too. texts must be
// parallel to g.order.
func (g *generator) pruneUnreferencedSupporting(texts []string) []string {
	for {
		removed := false
		for i, p := range g.order {
			if p.Kind != kindSupporting {
				continue
			}
			needle := p.Addr.String() + "."
			referenced := false
			for j, other := range texts {
				if j != i && strings.Contains(other, needle) {
					referenced = true
					break
				}
			}
			if referenced {
				continue
			}
			g.order = append(g.order[:i], g.order[i+1:]...)
			texts = append(texts[:i], texts[i+1:]...)
			delete(g.byType, p.Addr.Type)
			removed = true
			break
		}
		if !removed {
			return texts
		}
	}
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
