// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/substrate"
)

// GitHub issue #2138, the maintainer's option (a): the record rung reads an
// azurerm identity attribute the block does not declare from a source
// tools/azurerm-id-gen generates (live/azurerm-ids.json's
// identity_sources): a segment of a parent's ARM id argument, parsed with
// the grammar of the id type azurerm's own validator parses that argument
// as; a renamed argument; or a constant. These tests pin each source the
// way TestAzurermIdentityRouteAgreesWithTheTemplate pins the 103 templates:
// against what the provider says, here the checks azurerm's generated
// resource identity test makes, carried in the identity extract and read
// from the provider source apart from the generator
// ([readAzurermIdentityTests]).

// azurermRecordableIdentitySchemaTypes is how many of azurerm 5.10.0's 196
// identity-schema types strict { markers "record" } may select. It was 103
// after #2135. TO CHANGE IT, say in the commit which types moved and why.
const azurermRecordableIdentitySchemaTypes = 191

// azurermSourceDisagreement compares one type's generated identity sources
// with its identity schema, its arguments and azurerm's identity test, all
// from the extract, and with its ARM id template from the artifact. It
// returns one line per difference, naming the attribute.
func azurermSourceDisagreement(row azurermIDsRow, ty azurermIdentityExtractType) []string {
	var out []string
	settable := func(arg string) bool {
		f := ty.Arguments[arg]
		return arg != "id" && (f == "required" || f == "optional" || f == "optional+computed")
	}
	own := strings.Split(row.Template, "/")
	position := func(parts []string, segment string) int {
		at := -1
		for i, p := range parts {
			if p == "{"+segment+"}" {
				if at >= 0 {
					return -2
				}
				at = i
			}
		}
		return at
	}
	identity := map[string]bool{}
	for _, a := range ty.Identity {
		identity[a] = true
	}
	var attrs []string
	for a := range row.IdentitySources {
		attrs = append(attrs, a)
		if !identity[a] {
			out = append(out, fmt.Sprintf("a source is recorded for %s, which the identity schema does not name", a))
		}
	}
	for _, a := range ty.Identity {
		if _, ok := row.IdentitySources[a]; !ok {
			out = append(out, fmt.Sprintf("identity attribute %s has no source", a))
		}
	}
	sort.Strings(attrs)
	bySegment := map[string]string{}
	for _, a := range attrs {
		src := row.IdentitySources[a]
		check, tested := ty.IdentityTest[a]
		if !tested {
			out = append(out, fmt.Sprintf("%s: azurerm's identity test does not check it", a))
			continue
		}
		set := 0
		for _, v := range []string{src.Argument, src.Within, src.Provider, src.Constant} {
			if v != "" {
				set++
			}
		}
		if set != 1 {
			out = append(out, fmt.Sprintf("%s: %d sources, not one", a, set))
			continue
		}
		at := position(own, src.Segment)
		if at < 0 {
			out = append(out, fmt.Sprintf("%s: segment {%s} is not exactly once in %s", a, src.Segment, row.Template))
			continue
		}
		if prev, dup := bySegment[src.Segment]; dup {
			out = append(out, fmt.Sprintf("%s: segment {%s} is %s's too", a, src.Segment, prev))
		}
		bySegment[src.Segment] = a
		switch {
		case src.Argument != "":
			if check.Check != "matches-state" || check.Path != src.Argument {
				out = append(out, fmt.Sprintf("%s: read from argument %s, and azurerm's identity test checks %+v", a, src.Argument, check))
			}
			if !settable(src.Argument) {
				out = append(out, fmt.Sprintf("%s: read from %s, which a configuration does not set", a, src.Argument))
			}
		case src.Constant != "":
			if check.Check != "value" || check.String != src.Constant {
				out = append(out, fmt.Sprintf("%s: the constant %q, and azurerm's identity test checks %+v", a, src.Constant, check))
			}
		case src.Provider != "":
			if check.Check != "value" || !strings.HasPrefix(check.Expr, "data.Subscriptions.") {
				out = append(out, fmt.Sprintf("%s: the provider's %s, and azurerm's identity test checks %+v", a, src.Provider, check))
			}
			if at == 0 || !strings.EqualFold(own[at-1], "subscriptions") {
				out = append(out, fmt.Sprintf("%s: the provider's subscription, in segment {%s}, which is not a subscription's", a, src.Segment))
			}
		case src.Within != "":
			if check.Check != "contains" || check.Path != src.Within {
				out = append(out, fmt.Sprintf("%s: read inside %s, and azurerm's identity test checks %+v", a, src.Within, check))
			}
			if !settable(src.Within) {
				out = append(out, fmt.Sprintf("%s: read inside %s, which a configuration does not set", a, src.Within))
			}
			parent := strings.Split(src.ParentTemplate, "/")
			pat := position(parent, src.ParentSegment)
			if pat != at {
				out = append(out, fmt.Sprintf("%s: segment {%s} is position %d of %s and {%s} position %d of %s", a, src.Segment, at, row.Template, src.ParentSegment, pat, src.ParentTemplate))
				continue
			}
			for k := 0; k <= at; k++ {
				ov, pv := strings.HasPrefix(own[k], "{"), strings.HasPrefix(parent[k], "{")
				if ov != pv || (!ov && !strings.EqualFold(own[k], parent[k])) {
					out = append(out, fmt.Sprintf("%s: %s and %s part ways at %q against %q, before segment {%s}", a, row.Template, src.ParentTemplate, own[k], parent[k], src.Segment))
					break
				}
			}
			// The parse the record rung runs, on an id of the parent's
			// shape whose every value names its own segment.
			sample := azurermFilledTemplate(src.ParentTemplate, func(seg string) string { return "v-" + seg })
			if got, ok := substrate.ARMIDSegment(src.ParentTemplate, sample, src.ParentSegment); !ok || got != "v-"+src.ParentSegment {
				out = append(out, fmt.Sprintf("%s: parsing %s for {%s} gives %q, %v", a, sample, src.ParentSegment, got, ok))
			}
		}
	}
	for _, p := range own {
		if strings.HasPrefix(p, "{") {
			if _, ok := bySegment[strings.Trim(p, "{}")]; !ok {
				out = append(out, fmt.Sprintf("segment %s of %s is no identity attribute's, so the identity does not name the object whole", p, row.Template))
			}
		}
	}
	// The template's own leading parent-id argument, where it has one, is
	// read by tools/azurerm-id-gen's argument-name rules. Where the sources
	// read inside the same argument, azurerm's validator says what id it is,
	// and the two have to be the same id.
	if len(row.Components) > 0 && row.Components[0].ARMID && len(row.Components[0].Attrs) == 1 {
		arg, seg := row.Components[0].Attrs[0], row.Components[0].Segment
		at := position(own, seg)
		for _, a := range attrs {
			src := row.IdentitySources[a]
			if src.Within != arg {
				continue
			}
			if at < 0 || azurermShape(strings.Join(own[:at+1], "/")) != azurermShape(src.ParentTemplate) {
				out = append(out, fmt.Sprintf("the template reads %s as the id up to {%s}, and azurerm parses %s as %s", arg, seg, arg, src.ParentTemplate))
			}
			break
		}
	}
	return out
}

// azurermFilledTemplate is template with every {segment} replaced by
// value(segment).
func azurermFilledTemplate(template string, value func(string) string) string {
	parts := strings.Split(template, "/")
	for i, p := range parts {
		if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
			parts[i] = value(p[1 : len(p)-1])
		}
	}
	return strings.Join(parts, "/")
}

// azurermShape is a template with its value segments' names dropped and its
// letters folded.
func azurermShape(template string) string {
	return strings.ToLower(azurermFilledTemplate(template, func(string) string { return "{}" }))
}

// TestAzurermIdentitySourcesAgreeWithTheIdentityTest is #2138's pin. Every
// azurerm 5.10.0 identity-schema type either carries a source for each of
// its identity attributes, each agreeing with azurerm's identity test and
// with the type's ARM id template ([azurermSourceDisagreement]), or a
// problem, which is then the reason strict-markers-unrecordable refuses it
// by. For each type with sources, a record is read off an applied object
// built from the sources, through the record rung's own readers, and has
// to rebuild into that object's id. The 103 types the identity-schema
// route admits on their own keep the plan #2135 gave them: nothing in it
// is derived.
func TestAzurermIdentitySourcesAgreeWithTheIdentityTest(t *testing.T) {
	ex, schemas := loadAzurermIdentityExtract(t)
	_, rows := loadAzurermIDsRows(t)
	cloud := substrate.AzurermCloudIdentityAttrs()
	names := make([]string, 0, len(ex.Types))
	for name := range ex.Types {
		names = append(names, name)
	}
	sort.Strings(names)
	recordable := 0
	for _, name := range names {
		row, ty := rows[name], ex.Types[name]
		why := SelectedLocatedRefusal(name, schemas)
		if row.IdentitySourcesProblem != "" {
			if len(row.IdentitySources) != 0 {
				t.Errorf("%s: both sources and a problem", name)
			}
			if !strings.Contains(why, row.IdentitySourcesProblem) {
				t.Errorf("%s: refused %q, which does not say the problem %q", name, why, row.IdentitySourcesProblem)
			}
			continue
		}
		for _, line := range azurermSourceDisagreement(row, ty) {
			t.Errorf("%s: %s", name, line)
		}
		if why != "" {
			t.Errorf("%s: every identity attribute has a source and the selection is refused: %s", name, why)
			continue
		}
		recordable++
		plan, ok := LocatedIdentityPlanFor(name, schemas[name])
		if !ok {
			t.Errorf("%s: not recordable", name)
			continue
		}
		if IdentitySchemaRouteAdmits(schemas, name, cloud) && plan.Derived() {
			t.Errorf("%s: the identity-schema route admits it, and its record plan derives %v", name, plan.DerivedComponents)
		}
		obj, id, values := azurermSampleObject(row, ty)
		comps, ok := LocatedIdentity(obj, plan.Components)
		if !ok {
			t.Errorf("%s: the components %v are not read off %#v", name, plan.Components, obj)
			continue
		}
		fromProvider, ok := LocatedProviderIdentity(plan, func(CloudValue) (string, bool) { return values["subscription_id"], true })
		if !ok {
			t.Errorf("%s: provider components %v unanswered", name, plan.ProviderComponents)
			continue
		}
		derived, ok := LocatedDerivedIdentity(plan, obj)
		if !ok {
			t.Errorf("%s: derived components %v not read off the object", name, plan.DerivedComponents)
			continue
		}
		rec := map[string]string{}
		for _, m := range []map[string]string{comps, fromProvider, derived} {
			for k, v := range m {
				rec[k] = v
			}
		}
		if len(rec) != len(ty.Identity) {
			t.Errorf("%s: record %v does not hold the identity %v", name, rec, ty.Identity)
		}
		for _, a := range ty.Identity {
			if rec[a] != values[a] {
				t.Errorf("%s: record holds %s = %q, the object's is %q", name, a, rec[a], values[a])
			}
		}
		if rebuilt, ok := substrate.AzurermRecordImportID(name, rec); !ok || !strings.EqualFold(rebuilt, id) {
			t.Errorf("%s: the record rebuilds to %q (%v), the object's id is %q", name, rebuilt, ok, id)
		}
	}
	if recordable != azurermRecordableIdentitySchemaTypes {
		t.Errorf("%d of %d azurerm identity-schema types are recordable, pinned %d", recordable, len(names), azurermRecordableIdentitySchemaTypes)
	}
}

// azurermSampleObject is an applied object of row's type as the sources
// say azurerm builds one: every identity attribute a value naming it
// ("v-storage_account_name"), a constant its constant; each argument that
// supplies one that value; each parent id argument the parent's id with
// this object's values in the segments the two share and its own beyond;
// and id the type's own template filled from the identity. values is the
// identity it should record.
func azurermSampleObject(row azurermIDsRow, ty azurermIdentityExtractType) (obj cty.Value, id string, values map[string]string) {
	values = map[string]string{}
	bySegment := map[string]string{}
	for a, src := range row.IdentitySources {
		v := "v-" + a
		if src.Constant != "" {
			v = src.Constant
		}
		values[a] = v
		bySegment[src.Segment] = v
	}
	id = azurermFilledTemplate(row.Template, func(seg string) string { return bySegment[seg] })
	own := strings.Split(id, "/")
	attrs := map[string]cty.Value{"id": cty.StringVal(id)}
	for arg := range ty.Arguments {
		if _, set := attrs[arg]; !set {
			attrs[arg] = cty.NullVal(cty.String)
		}
	}
	for a := range values {
		if _, declared := ty.Arguments[a]; declared {
			attrs[a] = cty.StringVal(values[a])
		}
	}
	for a, src := range row.IdentitySources {
		switch {
		case src.Argument != "":
			attrs[src.Argument] = cty.StringVal(values[a])
		case src.Within != "":
			parts := strings.Split(src.ParentTemplate, "/")
			for i, p := range parts {
				if strings.HasPrefix(p, "{") {
					if i < len(own) && strings.HasPrefix(strings.Split(row.Template, "/")[i], "{") {
						parts[i] = own[i]
					} else {
						parts[i] = "p-" + strings.Trim(p, "{}")
					}
				}
			}
			attrs[src.Within] = cty.StringVal(strings.Join(parts, "/"))
		}
	}
	return cty.ObjectVal(attrs), id, values
}

// TestAzurermSourceDisagreementNamesTheAttribute is the comparison above
// shown failing, on real rows with one fact changed each.
func TestAzurermSourceDisagreementNamesTheAttribute(t *testing.T) {
	ex, _ := loadAzurermIdentityExtract(t)
	_, rows := loadAzurermIDsRows(t)
	mutate := func(name string, change func(*azurermIDsRow, *azurermIdentityExtractType)) string {
		row, ty := rows[name], ex.Types[name]
		row.IdentitySources = clonedSources(row.IdentitySources)
		ty.IdentityTest = clonedChecks(ty.IdentityTest)
		change(&row, &ty)
		return strings.Join(azurermSourceDisagreement(row, ty), "\n")
	}
	if got := mutate("azurerm_storage_container", func(*azurermIDsRow, *azurermIdentityExtractType) {}); got != "" {
		t.Fatalf("azurerm_storage_container disagrees as generated: %s", got)
	}
	for _, c := range []struct {
		name, want string
		change     func(*azurermIDsRow, *azurermIdentityExtractType)
	}{
		{"azurerm_storage_container", "storage_account_name: segment {storageAccountName} is position", func(r *azurermIDsRow, _ *azurermIdentityExtractType) {
			s := r.IdentitySources["storage_account_name"]
			s.ParentSegment = "resourceGroupName"
			r.IdentitySources["storage_account_name"] = s
		}},
		{"azurerm_storage_container", "part ways", func(r *azurermIDsRow, _ *azurermIdentityExtractType) {
			s := r.IdentitySources["storage_account_name"]
			s.ParentTemplate = strings.Replace(s.ParentTemplate, "Microsoft.Storage", "Microsoft.Compute", 1)
			r.IdentitySources["storage_account_name"] = s
		}},
		{"azurerm_storage_container", "storage_account_name: read inside storage_account_id, and azurerm's identity test checks", func(_ *azurermIDsRow, ty *azurermIdentityExtractType) {
			ty.IdentityTest["storage_account_name"] = azurermIdentityTestCheck{Check: "matches-state", Path: "storage_account_id"}
		}},
		{"azurerm_dns_cname_record", "record_type: the constant \"CNAME\", and azurerm's identity test checks", func(_ *azurermIDsRow, ty *azurermIdentityExtractType) {
			ty.IdentityTest["record_type"] = azurermIdentityTestCheck{Check: "value", String: "TXT"}
		}},
		{"azurerm_dns_cname_record", "dns_zone_name: read from argument zone_name, and azurerm's identity test checks", func(_ *azurermIDsRow, ty *azurermIdentityExtractType) {
			ty.IdentityTest["dns_zone_name"] = azurermIdentityTestCheck{Check: "matches-state", Path: "name"}
		}},
		{"azurerm_dns_cname_record", "identity attribute record_type has no source", func(r *azurermIDsRow, _ *azurermIdentityExtractType) {
			delete(r.IdentitySources, "record_type")
		}},
	} {
		if got := mutate(c.name, c.change); !strings.Contains(got, c.want) {
			t.Errorf("%s changed: disagreement %q does not say %q", c.name, got, c.want)
		}
	}
}

func clonedSources(in map[string]azurermIDsSource) map[string]azurermIDsSource {
	out := make(map[string]azurermIDsSource, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func clonedChecks(in map[string]azurermIdentityTestCheck) map[string]azurermIdentityTestCheck {
	out := make(map[string]azurermIdentityTestCheck, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
