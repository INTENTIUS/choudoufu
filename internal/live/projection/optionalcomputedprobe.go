// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"log"
	"sort"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// The Optional+Computed probe (INTENTIUS/choudoufu#2269).
//
// GitHub issue #2261 fixed an in-place aws_ssm_parameter update by seeding
// the recorded insecure_value into the read's prior, because the provider's
// Read writes the parameter's value into insecure_value only when the prior
// already has insecure_value set, and into the sensitive value otherwise. A
// parameter with no stored record yet (before its first apply under this
// estate writes one) had nothing to seed: the read from a bare import stub
// put the live value into value, and the plan chained both attributes to
// unknown, so the update would apply as a no-op.
//
// The configured value cannot simply be seeded in the record's place.
// [configuredAttrsSeed] leaves Optional+Computed values out on purpose: a
// provider that keeps such an attribute from the prior without reading it
// would hand the configured value straight back, and the plan would hide
// drift. So the probe asks the provider which kind it is. When the
// configuration sets a flat Optional+Computed string attribute to a known
// value, no record seeds it, and the first read left it null, the instance
// is read a second time from the same stub with that attribute set to
// [optionalComputedProbeSentinel]. An attribute that comes back as anything
// other than the sentinel was read from the cloud, and that second read is
// what a state-backed prior would have produced, so it is kept, with every
// attribute that only echoed the sentinel put back to null. When every probed
// attribute echoes the sentinel, or the second read fails, the first read
// stands and nothing changes.
//
// A marked configured value takes part (INTENTIUS/choudoufu#2278). A
// sensitive input variable is the ordinary way to produce one, and before
// #2278 the probe skipped it, so a parameter whose insecure_value came from
// a sensitive variable still planned unknown. The sentinel the second read
// carries is never marked, and the configured value itself never crosses
// the plugin channel. What comes back is marked, because [readImported]
// restores configuration marks on every read, and cty panics when a marked
// value is compared. So the compare unmarks the second read with its paths,
// compares the unmarked attributes against the sentinel, and puts the same
// marks back on the result it keeps. An attribute that is Sensitive in the
// schema is still not probed.

// optionalComputedProbeKey is the reserved [readPrep.attrsSeed] key that
// carries the probed attribute names (a list of strings) to [readImported].
// [withSeededAttrs] skips it, since no schema has an attribute of that name.
const optionalComputedProbeKey = "\x00optional_computed_probe"

// optionalComputedProbeSentinel is the value a probed attribute is given in
// the second read's prior. Nothing in a cloud holds it, so an attribute that
// comes back holding it was not read.
const optionalComputedProbeSentinel = "\x00choudoufu-optional-computed-probe"

// configuredOptionalComputedProbe names the attributes the probe is for:
// flat, top-level, non-write-only string attributes that are Optional and
// Computed, are not Sensitive, are not an identity attribute or a marker
// carrier ([residueSeedNeverSeeds]), and are set by configuration to a known,
// non-null value, marked or not. seeded is the seed the read already carries; a name in it
// (a residue record's value, #2261) needs no probe.
func configuredOptionalComputedProbe(ctx context.Context, eval *configs.StaticEvaluator, modPath addrs.Module, rc *configs.Resource, schema providers.Schema, dataSchemas map[string]providers.Schema, seeded map[string]cty.Value) []string {
	if eval == nil || rc == nil || schema.Block == nil {
		return nil
	}
	identityAttrs := residueIdentityAttrs(schema)
	ident := configs.StaticIdentifier{
		Module:    modPath,
		Subject:   rc.Addr().String(),
		DeclRange: rc.DeclRange,
	}
	var names []string
	for name, attr := range schema.Block.Attributes {
		if attr == nil || !attr.Optional || !attr.Computed || attr.Sensitive || attr.WriteOnly || attr.NestedType != nil || !attr.Type.Equals(cty.String) {
			continue
		}
		if identityAttrs[name] || residueSeedNeverSeeds[name] {
			continue
		}
		if _, ok := seeded[name]; ok {
			continue
		}
		// configuredAttrSeed strips the value's marks into its second return.
		// A marked (sensitive) configured value is probed too (#2278): the
		// probe sends only the sentinel, and its compare keeps marks.
		val, _, ok := configuredAttrSeed(ctx, eval, ident, rc, attr, name, dataSchemas)
		if !ok || val == cty.NilVal || val.IsNull() || !val.IsWhollyKnown() {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// withOptionalComputedProbe adds names to seed under
// [optionalComputedProbeKey].
func withOptionalComputedProbe(seed map[string]cty.Value, names []string) map[string]cty.Value {
	if len(names) == 0 {
		return seed
	}
	vals := make([]cty.Value, len(names))
	for i, n := range names {
		vals[i] = cty.StringVal(n)
	}
	if seed == nil {
		seed = make(map[string]cty.Value, 1)
	}
	seed[optionalComputedProbeKey] = cty.ListVal(vals)
	return seed
}

// takeOptionalComputedProbe splits the probe names out of seed, returning
// the names and a copy of seed without the key. ok is false when seed holds
// no probe.
func takeOptionalComputedProbe(seed map[string]cty.Value) (names []string, rest map[string]cty.Value, ok bool) {
	v, has := seed[optionalComputedProbeKey]
	if !has {
		return nil, seed, false
	}
	rest = make(map[string]cty.Value, len(seed)-1)
	for k, sv := range seed {
		if k != optionalComputedProbeKey {
			rest[k] = sv
		}
	}
	if v.IsNull() || !v.IsKnown() || v.IsMarked() || !v.Type().IsListType() || !v.Type().ElementType().Equals(cty.String) {
		return nil, rest, false
	}
	for it := v.ElementIterator(); it.Next(); {
		_, ev := it.Element()
		if !ev.IsNull() && ev.IsKnown() && !ev.IsMarked() {
			names = append(names, ev.AsString())
		}
	}
	return names, rest, len(names) > 0
}

// probeOptionalComputed is the second read. first is the first read's
// object, stub the import object both reads start from (before any seed),
// and seed the first read's seed without the probe key.
func probeOptionalComputed(ctx context.Context, provider providers.Interface, schema providers.Schema, typeName, importID string, stub states.ResourceInstanceObject, seed map[string]cty.Value, configMarks []cty.PathValueMarks, manifestKeys *manifestKeyLookup, names []string, first *states.ResourceInstanceObject) *states.ResourceInstanceObject {
	if first == nil || first.Value == cty.NilVal || first.Value.IsNull() {
		return first
	}
	firstVal, _ := first.Value.UnmarkDeep()
	var probed []string
	for _, n := range names {
		if firstVal.Type().HasAttribute(n) && firstVal.GetAttr(n).IsNull() {
			probed = append(probed, n)
		}
	}
	if len(probed) == 0 {
		return first
	}

	probeSeed := make(map[string]cty.Value, len(seed)+len(probed))
	for k, v := range seed {
		probeSeed[k] = v
	}
	for _, n := range probed {
		probeSeed[n] = cty.StringVal(optionalComputedProbeSentinel)
	}
	second, _, status, diags := readImported(ctx, provider, schema, typeName, importID, &stub, probeSeed, configMarks, manifestKeys, nil)
	if status != statusMaterialized || second == nil || diags.HasErrors() {
		log.Printf("[TRACE] projection: the Optional+Computed probe read of %s %q did not materialize; keeping the first read", typeName, importID)
		return first
	}
	// Unmark with paths, compare, re-mark (#2278): see the file comment.
	secondVal, secondMarks := second.Value.UnmarkDeepWithPaths()
	read := false
	restore := map[string]cty.Value{}
	for _, n := range probed {
		v := secondVal.GetAttr(n)
		if v.IsKnown() && !v.IsNull() && v.AsString() == optionalComputedProbeSentinel {
			restore[n] = cty.NullVal(cty.String)
			continue
		}
		read = true
	}
	if !read {
		log.Printf("[TRACE] projection: the provider keeps %v of %s from the prior without reading it; keeping the first read", probed, typeName)
		return first
	}
	if len(restore) > 0 {
		if restored, ok := withSeededAttrs(secondVal, restore, nil); ok {
			secondVal = restored
		}
	}
	if len(secondMarks) > 0 {
		secondVal = secondVal.MarkWithPaths(secondMarks)
	}
	second.Value = secondVal
	log.Printf("[TRACE] projection: %s %q read %v back from the cloud when the prior named them; using that read (INTENTIUS/choudoufu#2269)", typeName, importID, probed)
	return second
}

// readImportedProbing is [readImported] for a seed that carries the probe
// key: the first read, then [probeOptionalComputed].
func readImportedProbing(ctx context.Context, provider providers.Interface, schema providers.Schema, typeName, importID string, obj *states.ResourceInstanceObject, names []string, seed map[string]cty.Value, configMarks []cty.PathValueMarks, manifestKeys *manifestKeyLookup, diags tfdiags.Diagnostics) (*states.ResourceInstanceObject, cty.Value, materializeStatus, tfdiags.Diagnostics) {
	stub := *obj
	first, importStub, status, diags := readImported(ctx, provider, schema, typeName, importID, obj, seed, configMarks, manifestKeys, diags)
	if status != statusMaterialized {
		return first, importStub, status, diags
	}
	return probeOptionalComputed(ctx, provider, schema, typeName, importID, stub, seed, configMarks, manifestKeys, names, first), importStub, status, diags
}
