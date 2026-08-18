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
	"github.com/intentius/choudoufu/internal/live/markers"
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

// ambiguousIdentityArgs is the set of argument names that more than one
// type in identity.DefaultTable self-identifies by - computed from the
// table itself, not hand-listed, so it grows and shrinks with admission
// batches instead of needing a matching edit here. "function_name" has
// exactly one owner in the table (aws_lambda_function) and is absent from
// this set; "bucket" has ten and is a member; so does "name" itself, whose
// 120-odd owners are why parentRef's fallback used to name it as a literal
// before this census replaced the one name it happened to have been
// written against with the whole family it turned out to belong to (issue
// #231 - the #136 fix caught bare "name" only, missing seventeen other
// argument names with the identical multi-owner shape, "bucket" and
// "resource_arn" among the worst offenders at ten and seven owners).
var ambiguousIdentityArgs = computeAmbiguousIdentityArgs()

func computeAmbiguousIdentityArgs() map[string]bool {
	owners := map[string]int{}
	for t := range identity.DefaultTable {
		if arg, ok := identityArgName(t); ok {
			owners[arg]++
		}
	}
	ambiguous := map[string]bool{}
	for arg, n := range owners {
		if n > 1 {
			ambiguous[arg] = true
		}
	}
	return ambiguous
}

// identityArgName returns the argument name that carries typeName's own
// client-set identity, per internal/live/identity's table - the "identity
// arguments named from the identity table" issue #56 asks for - when that
// type has a single-component, self-named identity (attr(argName) alone;
// every simple client-named row in DefaultTable is exactly this shape).
// Server-assigned types and composite identities (more than one component)
// return ok=false: their identity is not one configuration argument this
// generator can name a placeholder for.
//
// A single component carrying a [identity.Component.Cloud] property is the
// same case in different clothes (issue #292): its Attrs name an argument
// the provider documents as DEFAULTING TO that cloud property when omitted
// (aws_glue_data_catalog_encryption_settings's whole identity is its
// account-shared catalog_id), which makes the value neither client-set nor
// this type's own to hand out as a "parent" - every sibling in the account
// shares it, so treating it as one belongs to the same wrong-reading
// identityComponentArgs guards on the fill side. Without this, parentRef's
// same-named-owner search (the only other reader of this function) finds
// exactly one type in the whole table whose identity is bare "catalog_id",
// and hands out a reference to THAT one specific instance's own identity to
// every other Glue type that merely shares the account-scoped argument
// name - the cross-resource reference issue #292 reports. Returning
// ok=false here instead leaves the argument to the same account/region
// fallback the resolver itself already reads when configuration omits it
// (internal/live/identity/resolve.go's cloudValueFor).
func identityArgName(typeName string) (string, bool) {
	entry, ok := identity.LookupType(typeName)
	if !ok || entry.ServerAssigned || len(entry.Components) != 1 {
		return "", false
	}
	c := entry.Components[0]
	if len(c.Attrs) == 0 || c.Cloud != identity.CloudNone {
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
// the way real configurations do.
//
// A component carrying a [identity.Component.Cloud] property is skipped
// for the identical reason (issue #292): its Attrs name an argument the
// provider documents as DEFAULTING TO that cloud property when omitted (a
// Glue catalog_id defaults to the caller's own account, an AWS provider
// "region" argument defaults to the run's own region -
// internal/live/identity/resolve.go's own resolver reads exactly this
// fallback through [resolver.cloudValueFor] when the component's Attrs are
// absent from configuration). The value is never one client-set
// configuration or another sibling resource's state actually carries - it
// is a property every instance in the account/region shares - so forcing
// it into the fill list can only produce something wrong: a value that
// looks like a reference but is one specific sibling's identity, not the
// shared account/region (issue #292's aws_glue_catalog_database.catalog_id
// pointed at aws_glue_data_catalog_encryption_settings.app.catalog_id), or
// a generic placeholder string sitting where a real region/account id
// belongs ("region = \"placeholder\"", the same shape on
// aws_sns_topic/aws_sqs_queue and others once catalog_id's cross-reference
// case is fixed but the Cloud check is not). Leaving the argument out
// entirely is legal (Optional and Computed - a Required Cloud-bearing
// argument, none exist in DefaultTable today, would still be forced in by
// fillBlock's own schema.Required pass, untouched by this function) and
// exercises the same documented account/region fallback a real
// configuration omitting it would. Server-assigned types return nil, as
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
		if len(c.Attrs) == 0 || c.Default != "" || c.Cloud != identity.CloudNone {
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
		if _, _, _, wired := g.siblingRef(p.Addr.Type, idArg); wired {
			continue // a rendered sibling already answers it
		}
		_, suffix, _ := refArgSuffix(idArg)
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

	// A reference-shaped seed argument (issue #177's extraction, read by
	// seedRefExpr) already names the sibling type the argument's own
	// documented example wired it to - not a guess from the argument's
	// name and the child's service segment, but the page's own working
	// configuration. The loop above stops at the identity argument
	// because a naming guess with no rendered sibling could chain
	// unboundedly; a seed reference carries no such risk; it is at most
	// one hop (the artifact records what the example wired the argument
	// to, not a further chain from there), and pruneUnreferencedSupporting
	// below removes it again if nothing in the rendered text ends up
	// wired to it - so adding it is free when it turns out unneeded and
	// is what completes an Optional-in-schema, required-in-practice
	// argument the required-only pass never visits at all: aws_nat_gateway's
	// subnet_id is Optional, so the pass renders an empty body, and only a
	// rendered aws_subnet sibling gives seedFromExample somewhere to wire
	// it (the same shape aws_volume_attachment's volume_id has against
	// aws_ebs_volume, Required this time, but with no same-service naming
	// match for siblingRef to find on its own).
	//
	// Skipped entirely for a type an override still owns: its Apply runs
	// last and always wins (seedFromExample's own suppression rule), so a
	// seed reference under an override would never reach the rendered
	// text - pulling in its sibling anyway added an aws_dynamodb_table
	// wired to nothing but another stray parentRef match, found and
	// removed while measuring this change.
	for _, p := range g.order {
		if _, overridden := typeOverrides[p.Addr.Type]; overridden {
			continue // its Apply always wins over a seeded reference - see seedFromExample
		}
		for _, arg := range g.seed[p.Addr.Type] {
			if !arg.Reference || arg.RefType == "" || arg.RefType == p.Addr.Type {
				continue
			}
			if arg.Element > 0 || len(arg.Path) == 0 {
				continue // a later repeated-block element; seedFromExample never lands it either
			}
			leaf := arg.Path[len(arg.Path)-1]
			if _, _, ok := g.parentRef(p.Addr.Type, leaf); ok {
				continue // already resolved another way; do not add a competing sibling
			}
			if _, _, _, ok := g.siblingRef(p.Addr.Type, leaf); ok {
				continue // same - siblingForBase's tiebreak is one-shot with no
				// fallback, so a newly added, ultimately-unwireable candidate
				// (aws_appsync_api's page names it for aws_appsync_channel_namespace's
				// own api_id, a DIFFERENT AppSync resource than this cohort's
				// curated aws_appsync_graphql_api) can beat the sibling that
				// already answers the argument on the base/suffix tiebreak and
				// leave the argument wired to nothing - found and fixed while
				// measuring this change.
			}
			if _, inCohort := g.byType[arg.RefType]; inCohort {
				continue
			}
			if _, inSchema := schemas.ResourceTypes[arg.RefType]; !inSchema {
				continue
			}
			if _, admitted := identity.LookupType(arg.RefType); !admitted {
				continue
			}
			supporting[arg.RefType] = true
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

// taggable is [markers.Taggable] - the predicate the run itself applies -
// rather than the copy of its first four clauses this used to carry.
// Estate tags are only ever added to a type it returns true for, so a copy
// that answered "yes" where the run answers "no" would write a marker into
// a field the provider rejects. See tools/survey-gen/classify.go's taggable
// for what the missing fifth clause is and what it measures against the
// real schemas.
func taggable(b *configschema.Block) bool {
	return markers.Taggable(b)
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
//     aws_ec2_managed_prefix_list), a bare identity-component argument
//     whose service-qualified parent type is rendered here (the codeartifact
//     dependents' "domain" against aws_codeartifact_domain), or the plural
//     "<base>_ids"/"<base>_arns" over a list/set(string) argument (the same
//     base resolution, wrapped in a single-element list literal -
//     aws_ecs_daemon's capacity_provider_arns against
//     aws_ecs_capacity_provider). The referenced attribute comes from the
//     sibling's schema and the identity table, not a guess - see refAttr.
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
		if parent, attrName, plural, ok := g.siblingRef(addr.Type, argName); ok {
			if plural {
				return fmt.Sprintf("[%s.%s]", parent, attrName)
			}
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
//
// # An ambiguous argument name carries no target hint at all
//
// "function_name", "cluster_name", "event_bus_name" each spell out WHICH
// kind of sibling they mean, which is why a type that does not itself
// compete for the identity argument can safely take the lexicographically-
// first same-named candidate: at most one type in identity.DefaultTable
// self-identifies by "function_name", so there is nothing to pick between.
// A name in ambiguousIdentityArgs has no such property - "bucket" alone has
// ten owners in the table, "name" itself has over a hundred (aws_iam_group,
// aws_athena_workgroup, aws_cloudwatch_event_bus among them) - so a type
// that merely HAS a required argument by one of these names without itself
// being one of the owners (a server-assigned type like
// aws_cognito_user_pool, whose real identity is its minted id) collides
// with whichever self-named type happened to sort first in the same
// cohort - aws_cognito_user_pool.name was wired to aws_iam_group.name, an
// unrelated resource that only shares the word "name" (found retiring
// #136's overrides: the override existed to give the type back its own
// placeholder).
//
// The first fix here (#136) only dropped the case bare "name" has no
// business being in, on the premise that a qualified name like
// "resource_arn" or "domain_name" was safe because it spelled out a
// target. A census of identity.DefaultTable disproved that (#231): 17
// other argument names have the identical multi-owner shape - two API
// gateway generations both self-identify by "domain_name", RDS and
// Redshift both by "cluster_identifier", and so on for 15 more. None of
// those pairs has a full prefix relation to fall back on either, so the
// alphabetic tiebreak was exactly as unsupported for them as it was for
// bare "name". ambiguousIdentityArgs generalizes the rule from that one
// literal to the property that actually matters - an argument name is
// unsafe to hand out to the first same-named candidate whenever more than
// one admitted type self-identifies by it, however the name is spelled -
// so it takes the same path as any other unmatched argument - selfType's
// own placeholder (valueExpr's later tiers).
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
		if ambiguousIdentityArgs[argName] {
			// No prefix relation to fall back on, and no target hint in
			// the argument name either: selfType is not one of argName's
			// several owners in identity.DefaultTable, so accepting a
			// same-named candidate here has no evidence behind it beyond
			// the name occurring more than once.
			return resourceAddr{}, "", false
		}
		// selfType has no competing claim on argName, and argName itself
		// names its target ("function_name" can only mean a Lambda
		// function): any candidate is safely a parent, and the
		// lexicographically-first one is the deterministic pick when more
		// than one type happens to share it.
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
// "<base>_arn" (singular) with a non-empty base ("prefix_list_id" ->
// "prefix_list", "id"), or "<base>_ids"/"<base>_arns" (plural - a
// list/set-typed argument naming more than one sibling of the same kind,
// "capacity_provider_arns" -> "capacity_provider", "arn", plural=true).
// Any other shape returns two empty strings. AWS's own argument naming
// convention is the rule here, singular or plural alike: an argument named
// after another resource kind plus "_id"/"_arn"[s] carries that resource's
// exported handle(s), never an independent value (issue #173's escaping
// shape 1; the plural form is the same convention pluralized - see
// siblingRef, which is what actually decides whether a rendered sibling
// answers it, and whether the schema backs the plural reading up).
//
// "_name" is a third suffix, deliberately unioned with "id"/"arn" here
// rather than kept as its own case: aws_eks_access_entry's "cluster_name"
// is the same shape ("<base>_id" naming a sibling by its exported handle)
// with "name" as the handle instead of "id"/"arn" - eksClusterNameRef and
// cognitoUserGroupNameRef were both hand-written for exactly this gap
// (issue #136). Unlike "id"/"arn", a "_name" suffix is not always a
// reference - "domain_name" often names a literal DNS string, not a
// sibling - so siblingRef gates the "name" suffix on the argument being
// one of selfType's OWN identity components (identityComponentAttr), the
// same restriction the bare (unsuffixed) shape already carries. That
// keeps the blast radius to the cases with the highest correctness stakes
// (an identity-bearing argument feeding a live marker) rather than every
// "_name"-suffixed argument in the schema, most of which are not
// references at all.
func refArgSuffix(argName string) (base, suffix string, plural bool) {
	for _, s := range []string{"id", "arn", "name"} {
		if b, ok := strings.CutSuffix(argName, "_"+s); ok && b != "" {
			return b, s, false
		}
		if b, ok := strings.CutSuffix(argName, "_"+s+"s"); ok && b != "" {
			return b, s, true
		}
	}
	return "", "", false
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
	if b, _, _ := refArgSuffix(argName); b != "" {
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
		return refAttrAllowed(siblingType, argName, n, identityBound)
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

// refAttrAllowed is the identity-boundness half of refAttr's choice, split
// out so the documented-reference seed (seedRefExpr) asks the same question
// rather than restating it. argName is the slot being wired on the CHILD,
// attrName the sibling attribute a reference would read, and identityBound
// whether argName is one of the child's own identity components.
//
// When the slot is identity-bound, identity resolution reads the reference
// and refuses any attribute outside the sibling's own IdentityAttrs ("Not an
// identity attribute"). An IdentityAttrs attribute's VALUE is the sibling's
// import identity, so the reference is also only correct when that identity
// IS the value the slot wants: a sibling whose identity is an assembled ARN
// (any Literal or Cloud component) fails that for every non-_arn slot.
func refAttrAllowed(siblingType, argName, attrName string, identityBound bool) bool {
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
		if a == attrName {
			return true
		}
	}
	return false
}

// isStringListArg reports whether selfType's own wire schema types argName
// as a list or set of strings - the schema half of the plural sibling-
// reference gate (siblingRef): an argument name shaped like "<base>_arns"
// says the argument names more than one sibling of the same kind, but only
// a list/set(string) argument can actually hold the single-element list
// literal that reading produces, so a name that merely happens to end in
// "s" over a scalar-typed argument (there are none among AWS's own
// "_arn"/"_id" conventions today, but nothing rules one out for a future
// provider release) is left to fall through to the ordinary placeholder
// rather than being wrapped in "[...]" against a slot that cannot take a
// list.
func isStringListArg(schemas providers.GetProviderSchemaResponse, selfType, argName string) bool {
	res, ok := schemas.ResourceTypes[selfType]
	if !ok || res.Block == nil {
		return false
	}
	attr, ok := res.Block.Attributes[argName]
	if !ok {
		return false
	}
	ty := attr.Type
	return (ty.IsListType() || ty.IsSetType()) && ty.ElementType() == cty.String
}

// siblingRef is issue #173's rule-level fix for reference arguments
// parentRef cannot see: parentRef only links an argument that shares its
// NAME with a sibling's identity-table argument, while a reference-by-id
// argument (prefix_list_id, framework_id, bot_id) never does, and a bare
// identity-component argument can name a parent whose own identity is a
// different word entirely (aws_codeartifact_domain's identity is its ARN
// assembly, but its dependents' "domain" argument still names it).
//
// Three shapes, all derived from the argument name (plus, for the third,
// the argument's own schema type) and the roster this run already holds -
// no per-type table:
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
//   - "<base>_ids"/"<base>_arns": the plural of the first shape - same
//     base resolution via siblingForBase, gated additionally on the
//     argument's own schema type actually being a list/set of strings
//     (isStringListArg), since the argument name alone cannot tell a
//     genuine plural handle (aws_ecs_daemon's capacity_provider_arns, one
//     ARN per referenced aws_ecs_capacity_provider) from a coincidence.
//     The returned attrName is still the sibling's single attribute
//     (siblingForBase/refAttr do not know how many sibling instances
//     exist, only which one this run rendered); the caller wraps it in a
//     single-element list, the same shape a hand-written config would use
//     to point a plural argument at exactly one real resource.
func (g *generator) siblingRef(selfType, argName string) (parent resourceAddr, attrName string, plural bool, ok bool) {
	base, suffix, isPlural := refArgSuffix(argName)
	if base != "" {
		identityBound := identityComponentAttr(selfType, argName)
		if suffix == "name" && !identityBound {
			// "_name" is not always a reference the way "_id"/"_arn" are
			// (refArgSuffix's own doc comment): scoped to the argument
			// being one of selfType's own identity components, so this
			// only fires for the highest-stakes case (eksClusterNameRef's
			// shape) rather than every "_name"-suffixed argument in the
			// schema.
			return resourceAddr{}, "", false, false
		}
		if isPlural && !isStringListArg(g.schemas, selfType, argName) {
			return resourceAddr{}, "", false, false
		}
		t, found := g.siblingForBase(selfType, base)
		if !found {
			return resourceAddr{}, "", false, false
		}
		if selfIdArg, owns := identityArgName(selfType); owns && selfIdArg == argName && !strings.HasPrefix(selfType, t+"_") {
			return resourceAddr{}, "", false, false
		}
		attr, found := g.refAttr(t, argName, suffix, identityBound)
		if !found {
			return resourceAddr{}, "", false, false
		}
		return g.byType[t], attr, isPlural, true
	}

	if !identityComponentAttr(selfType, argName) {
		return resourceAddr{}, "", false, false
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
		return addr, attr, false, true
	}
	return resourceAddr{}, "", false, false
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
			// creating arguments, which this never does. A REFERENCE entry
			// carries no literal at all (issue #177), so there is nothing
			// here to mirror; the parent renders whatever seedRefExpr wired
			// it to, and this side cannot spell that.
			if len(arg.Path) != 1 || arg.Path[0] != argName || arg.Reference {
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
