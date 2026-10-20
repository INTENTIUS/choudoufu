// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hcldec"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is the GCP half of the node-path stamp (GitHub issue #1119,
// under #1121's ruling): the AWS tag branch's write, into a hashicorp/google
// resource's top-level labels map. The map has the tags map's shape, so
// everything after the shape checks is [NodeResolver.stampMarkerElems], shared
// with the tag branch: the same conflict refusal, the same untag release,
// the same slot. What differs is the address, which a label cannot spell
// and so carries encoded ([markers.GoogleAddressValue]), and the estate,
// which has to be a legal label value.
//
// Only "labels" is written. The provider merges it with default_labels into
// terraform_labels and reads the object's whole label set back into
// effective_labels; both are computed, and a configuration cannot set them.

// stampedGoogleLabels returns labelsVal with this instance's markers added,
// preserving every label the configuration already declares and the
// value's own marks, exactly as [NodeResolver.stampedTags] does for a tags
// map.
func (n *NodeResolver) stampedGoogleLabels(addr addrs.AbsResourceInstance, labelsVal cty.Value) (cty.Value, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	if !markers.ValidGoogleLabelValue(n.Estate) {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryMarkerNotALabel,
			fmt.Sprintf("%s cannot carry this estate's ownership marker: %s. Rename the estate in the live block, or keep this resource out of a GCP estate.", addr, markers.NotAGoogleLabelValue(n.Estate))))
		return labelsVal, diags
	}

	labelsVal, labelsMarks := labelsVal.Unmark()

	elems := map[string]cty.Value{}
	switch {
	case labelsVal.IsNull():
		// No labels argument in configuration: the markers are the map.
	case !labelsVal.IsKnown():
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, "Cannot set ownership markers on an unresolved labels value",
			fmt.Sprintf("%s's labels argument is not yet known, so its ownership markers could not be set at the node; the configuration's own value is used unchanged.", addr)))
		return labelsVal.WithMarks(labelsMarks), diags
	case !labelsVal.Type().IsMapType():
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, "Cannot merge ownership markers into this labels value",
			fmt.Sprintf("%s's labels argument evaluated to a %s, not a map; the configuration's own value is used unchanged.", addr, labelsVal.Type().FriendlyName())))
		return labelsVal.WithMarks(labelsMarks), diags
	case labelsVal.LengthInt() > 0:
		for it := labelsVal.ElementIterator(); it.Next(); {
			k, v := it.Element()
			if v.Type() != cty.String && v.Type() != cty.DynamicPseudoType {
				diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, "Cannot merge ownership markers into this labels value",
					fmt.Sprintf("%s's labels argument holds a non-string value at key %q; the configuration's own value is used unchanged.", addr, k.AsString())))
				return labelsVal.WithMarks(labelsMarks), diags
			}
			elems[k.AsString()] = v
		}
	}

	diags = diags.Append(n.stampMarkerElems(addr, elems, markers.GoogleAddressValue(addr), markers.EscapeAddress(addr.String()), false))
	if diags.HasErrors() {
		return labelsVal.WithMarks(labelsMarks), diags
	}
	return cty.MapVal(elems).WithMarks(labelsMarks), diags
}

// mirrorGoogleMarkerLabels is the read side of the GCP stamp. The
// provider's labels map is non-authoritative: its read keeps only the keys
// the prior's labels names (tpgresource.SetLabels at hashicorp/google
// v8.6.0, google/tpgresource/labels.go). A state file's prior names the
// marker keys, because the apply that wrote them recorded them; a prior
// re-derived from configuration does not, since the configuration this
// fork seeds from is unstamped. Without this the read drops the markers
// from labels, and every cache-less plan proposes writing them again.
//
// So every marker key the object carries in effective_labels, its whole
// label set in GCP, is put back into labels with the live value: exactly
// what a stock state file would hold after the apply that wrote it. A
// marker the object does not carry stays absent, and the plan writes it,
// which is the repair it should be. Any other value, and any type that is
// not the google-labels surface, is returned unchanged.
func mirrorGoogleMarkerLabels(v cty.Value, block *configschema.Block, defaults cty.Value) cty.Value {
	if _, ok := markers.GoogleLabelSurface(block); !ok {
		return v
	}
	if v == cty.NilVal || v.IsMarked() || v.IsNull() || !v.IsKnown() || !v.Type().IsObjectType() {
		return v
	}
	if !v.Type().HasAttribute(markers.GoogleEffectiveLabelsAttr) || !v.Type().HasAttribute(markers.GoogleLabelsAttr) {
		return v
	}
	eff := v.GetAttr(markers.GoogleEffectiveLabelsAttr)
	labels := v.GetAttr(markers.GoogleLabelsAttr)
	if eff.IsMarked() || labels.IsMarked() || eff.IsNull() || !eff.IsKnown() || !labels.IsKnown() || !eff.CanIterateElements() {
		return v
	}
	elems := map[string]cty.Value{}
	if !labels.IsNull() {
		for it := labels.ElementIterator(); it.Next(); {
			k, val := it.Element()
			elems[k.AsString()] = val
		}
	}
	changed := false
	for key, live := range markers.GoogleMarkerValues(eff) {
		if have, ok := elems[key]; ok && have.IsKnown() && !have.IsNull() && have.RawEquals(live) {
			continue
		}
		elems[key] = live
		changed = true
	}
	attrs := v.AsValueMap()
	if changed {
		attrs[markers.GoogleLabelsAttr] = cty.MapVal(elems)
	}
	if tl, ok := googleTerraformLabels(eff, elems, defaults); ok && v.Type().HasAttribute(markers.GoogleTerraformLabelsAttr) {
		attrs[markers.GoogleTerraformLabelsAttr] = tl
		changed = true
	}
	if !changed {
		return v
	}
	return cty.ObjectVal(attrs)
}

// googleTerraformLabels rebuilds terraform_labels the way the provider's
// own diff computes it (tpgresource.setLabelsFields at v8.6.0): the
// provider's default_labels, the attribution label when it is on and the
// object already carries it, and the labels map, each key valued from what
// the object carries. Its read keeps only the keys the prior names
// (flatten*TerraformLabels), and a prior re-derived from configuration
// names none, so without this every cache-less plan proposes the whole map
// again. False when defaults was not seeded ([builder.googleLabelDefaults]
// could not read the provider block), and terraform_labels is left as read.
func googleTerraformLabels(eff cty.Value, labels map[string]cty.Value, defaults cty.Value) (cty.Value, bool) {
	if defaults == cty.NilVal || defaults.IsMarked() || defaults.IsNull() || !defaults.Type().IsObjectType() {
		return cty.NilVal, false
	}
	if eff.IsMarked() || eff.IsNull() || !eff.IsKnown() || !eff.CanIterateElements() {
		return cty.NilVal, false
	}
	keys := map[string]bool{}
	for k := range labels {
		keys[k] = true
	}
	defaultKeys := defaults.GetAttr("keys")
	attribution := defaults.GetAttr("attribution")
	if defaultKeys.IsMarked() || attribution.IsMarked() {
		return cty.NilVal, false
	}
	for it := defaultKeys.ElementIterator(); it.Next(); {
		k, _ := it.Element()
		keys[k.AsString()] = true
	}
	if attribution.True() {
		keys[googleAttributionKey] = true
	}
	out := map[string]cty.Value{}
	for it := eff.ElementIterator(); it.Next(); {
		k, live := it.Element()
		if !keys[k.AsString()] || live.IsMarked() || live.IsNull() || !live.IsKnown() {
			continue
		}
		out[k.AsString()] = live
	}
	if len(out) == 0 {
		return cty.MapValEmpty(cty.String), true
	}
	return cty.MapVal(out), true
}

// googleLabelDefaultsKey is the attrsSeed entry carrying what
// [mirrorGoogleMarkerLabels] needs to rebuild terraform_labels: the
// provider block's default_labels keys and whether it adds the attribution
// label. Like [metadataSeedKey], it can never collide with a schema
// attribute name, and [withSeededAttrs] never sends it to the provider.
const googleLabelDefaultsKey = "\x00google_label_defaults"

// googleAttributionKey is the label hashicorp/google adds to what it
// creates when add_terraform_attribution_label is true, its default
// (transport_tpg.AttributionKey at v8.6.0).
const googleAttributionKey = "goog-terraform-provisioned"

// googleLabelDefaults reads the provider block providerAddr names for the
// two arguments terraform_labels is computed from: the keys of
// default_labels, and add_terraform_attribution_label (default true). ok is
// false when the block has an argument this static read cannot evaluate,
// and then nothing is seeded and terraform_labels is left as the read
// returned it. No block at all is the provider's defaults: no default
// labels, attribution on.
func (b *builder) googleLabelDefaults(ctx context.Context, providerAddr addrs.AbsProviderConfig) (out cty.Value, ok bool) {
	defaults := func(keys []cty.Value, attribution bool) cty.Value {
		set := cty.SetValEmpty(cty.String)
		if len(keys) > 0 {
			set = cty.SetVal(keys)
		}
		return cty.ObjectVal(map[string]cty.Value{"keys": set, "attribution": cty.BoolVal(attribution)})
	}
	if b.cfg == nil {
		return cty.NilVal, false
	}
	modCfg := b.cfg.Descendent(providerAddr.Module)
	if modCfg == nil || modCfg.Module == nil {
		return cty.NilVal, false
	}
	local := modCfg.Module.LocalNameForProvider(providerAddr.Provider)
	if providerAddr.Alias != "" {
		local += "." + providerAddr.Alias
	}
	pc := modCfg.Module.ProviderConfigs[local]
	if pc == nil || pc.Config == nil {
		return defaults(nil, true), true
	}

	// The same guard [configuredTagsSeed] puts around the same decode.
	defer func() {
		if rec := recover(); rec != nil {
			out, ok = cty.NilVal, false
		}
	}()
	spec := hcldec.ObjectSpec{
		"default_labels":                  &hcldec.AttrSpec{Name: "default_labels", Type: cty.Map(cty.String)},
		"add_terraform_attribution_label": &hcldec.AttrSpec{Name: "add_terraform_attribution_label", Type: cty.Bool},
	}
	refs, refDiags := lang.References(addrs.ParseRef, hcldec.Variables(pc.Config, spec))
	if refDiags.HasErrors() {
		return cty.NilVal, false
	}
	hclCtx := &hcl.EvalContext{}
	if eval := modCfg.Module.StaticEvaluator; eval != nil {
		ident := configs.StaticIdentifier{Module: providerAddr.Module, Subject: "provider." + local, DeclRange: pc.DeclRange}
		evalCtx, ctxDiags := eval.EvalContext(ctx, ident, refs)
		if ctxDiags.HasErrors() {
			return cty.NilVal, false
		}
		if evalCtx != nil {
			hclCtx = evalCtx
		}
	} else if len(refs) > 0 {
		return cty.NilVal, false
	}
	val, _, valDiags := hcldec.PartialDecode(pc.Config, spec, hclCtx)
	if valDiags.HasErrors() || val == cty.NilVal || val.IsNull() || !val.IsWhollyKnown() {
		return cty.NilVal, false
	}
	attribution := true
	if a := val.GetAttr("add_terraform_attribution_label"); a.IsMarked() {
		return cty.NilVal, false
	} else if !a.IsNull() {
		attribution = a.True()
	}
	var keys []cty.Value
	if dl := val.GetAttr("default_labels"); dl.IsMarked() {
		// A sensitive default_labels map is not read, even for its keys.
		return cty.NilVal, false
	} else if !dl.IsNull() {
		for it := dl.ElementIterator(); it.Next(); {
			k, _ := it.Element()
			keys = append(keys, k)
		}
	}
	return defaults(keys, attribution), true
}

// SummaryGoogleLabelDefaultsUnread names the warning
// [builder.warnGoogleDefaultsUnread] raises.
const SummaryGoogleLabelDefaultsUnread = "Provider default labels not known at plan time"

// warnGoogleDefaultsUnread says, once per provider configuration, that its
// default_labels or add_terraform_attribution_label could not be read
// statically, so terraform_labels was not rebuilt and the plan shows it.
// Guessing the map would hide a real change behind an invented one.
func (b *builder) warnGoogleDefaultsUnread(providerAddr addrs.AbsProviderConfig) {
	key := providerAddr.String()
	if b.googleDefaultsWarned[key] {
		return
	}
	if b.googleDefaultsWarned == nil {
		b.googleDefaultsWarned = map[string]bool{}
	}
	b.googleDefaultsWarned[key] = true
	b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryGoogleLabelDefaultsUnread,
		fmt.Sprintf("%s sets default_labels or add_terraform_attribution_label from something not known until apply (a variable with no value yet, a data source, another resource), so this run could not rebuild the terraform_labels the provider reads back empty without a state file. Each labelled resource through it will show terraform_labels as a change on a plan built without the cache. The change is the provider's own bookkeeping and the apply leaves the resource as it is. Set those arguments from literals or variables with known values to remove it.", providerAddr)))
}

// propagatedMarkerDiag refuses a configuration that writes an ownership
// marker by hand into a labels map GCP copies onto the resources the type
// creates ([markers.GooglePropagatedLabels]): an instance template's,
// which lands on every VM a managed instance group makes from it. Each VM
// would then carry this estate's marker with no block declaring it, and
// the sweep would report every one as an orphan. It is the
// [SummaryMarkerConflict] refusal, for a marker this run never writes.
func propagatedMarkerDiag(addr addrs.AbsResourceInstance, config cty.Value, schema providers.Schema) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if _, ok := markers.GooglePropagatedLabels(schema.Block); !ok {
		return diags
	}
	if config == cty.NilVal || config.IsMarked() || config.IsNull() || !config.IsKnown() || !config.Type().IsObjectType() || !config.Type().HasAttribute(markers.GoogleLabelsAttr) {
		return diags
	}
	labels := config.GetAttr(markers.GoogleLabelsAttr)
	if labels.IsMarked() || labels.IsNull() || !labels.IsKnown() || !labels.CanIterateElements() {
		return diags
	}
	for it := labels.ElementIterator(); it.Next(); {
		k, _ := it.Element()
		switch key := k.AsString(); key {
		case markers.TagEstate, markers.TagAddress, markers.TagSlot:
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryMarkerConflict, fmt.Sprintf(
				"%s declares the %q label, but %s's labels are copied onto every instance created from it and are not the template's own, so this run never writes a marker there. Every VM a managed instance group makes from this template would carry the marker with no block declaring it, and each would read as an orphan of the estate. Remove %q from its labels.",
				addr, key, addr.Resource.Resource.Type, key)))
		}
	}
	return diags
}
