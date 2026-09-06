// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package approval

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
)

// The maintainer ruling on PR #889: the approval covers planned values too.
// Everything here asserts the RENDERING and the named attributes, never a
// boolean, because the failure mode this closes is precisely a comparison
// that reported agreement it had not established.

// TestRenderValue_isCanonical pins what the digest is made of, by value.
func TestRenderValue_isCanonical(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  cty.Value
		want string
	}{
		{"a string carries its type", cty.StringVal("3"), "s:3"},
		{"a number carries its type", cty.NumberIntVal(3), "n:3"},
		{"a bool carries its type", cty.True, "b:true"},
		{"null is not the empty string", cty.NullVal(cty.String), "null"},
		{
			"a map is rendered with its keys sorted",
			cty.MapVal(map[string]cty.Value{"z": cty.StringVal("1"), "a": cty.StringVal("2")}),
			"obj{a=s:2,z=s:1}",
		},
		{
			"a list keeps its order, because there the order is the value",
			cty.ListVal([]cty.Value{cty.StringVal("b"), cty.StringVal("a")}),
			"list[s:b,s:a]",
		},
		{
			"a set is rendered by sorted element, because a set has no order",
			cty.SetVal([]cty.Value{cty.StringVal("b"), cty.StringVal("a")}),
			"set{s:a,s:b}",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, unknown := renderValue(tc.val)
			if unknown {
				t.Errorf("a wholly known value was reported unknown")
			}
			if got != tc.want {
				t.Errorf("rendered %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRenderValue_setOrderIsNotADifference: the same set built in two orders
// must render identically, or every plan over a set-valued argument becomes a
// coin flip.
func TestRenderValue_setOrderIsNotADifference(t *testing.T) {
	one, _ := renderValue(cty.SetVal([]cty.Value{cty.StringVal("a"), cty.StringVal("b"), cty.StringVal("c")}))
	two, _ := renderValue(cty.SetVal([]cty.Value{cty.StringVal("c"), cty.StringVal("a"), cty.StringVal("b")}))
	if one != two {
		t.Errorf("the same set rendered two ways:\n%s\n%s", one, two)
	}
}

// TestRenderValue_unknownIsOneConstant: an unknown renders as one token
// whatever it is refined to, so two plans that differ only in how much they
// have worked out about it cannot disagree.
func TestRenderValue_unknownIsOneConstant(t *testing.T) {
	plain, unknown := renderValue(cty.UnknownVal(cty.String))
	if !unknown {
		t.Fatalf("an unknown value was not reported unknown")
	}
	if plain != unknownToken {
		t.Errorf("an unknown rendered %q, want %q", plain, unknownToken)
	}
	refined, _ := renderValue(cty.UnknownVal(cty.String).Refine().StringPrefixFull("vpc-").NewValue())
	if refined != plain {
		t.Errorf("a refined unknown rendered %q and a bare one %q; a refinement is not part of what was approved", refined, plain)
	}
}

// TestRenderValue_sensitiveIsHashedNotPrinted: the digest has to move when a
// secret moves, and it must never carry the secret.
func TestRenderValue_sensitiveIsHashedNotPrinted(t *testing.T) {
	one, _ := renderValue(cty.StringVal("hunter2").Mark(marks.Sensitive))
	two, _ := renderValue(cty.StringVal("hunter3").Mark(marks.Sensitive))
	if strings.Contains(one, "hunter2") || strings.Contains(two, "hunter3") {
		t.Fatalf("a sensitive value was rendered in plaintext: %q / %q", one, two)
	}
	if !strings.HasPrefix(one, "sensitive:sha256:") {
		t.Errorf("a sensitive value rendered %q, want a sensitive:sha256: digest", one)
	}
	if one == two {
		t.Errorf("two different secrets rendered the same digest %q, so a moved secret would read as no change", one)
	}
	again, _ := renderValue(cty.StringVal("hunter2").Mark(marks.Sensitive))
	if again != one {
		t.Errorf("the same secret rendered two digests, %q and %q; the digest has to be stable across runs", one, again)
	}
}

// TestRenderElements_sensitiveContainerCollapsesToOneDigest is the guard
// internal/live/marksafe holds this package to, asserted rather than assumed.
//
// renderElements unmarks before it iterates, which is what makes the
// ElementIterator call provable. Unmarking must not turn into dropping the
// mark: a container marked sensitive as a whole has to collapse to one digest
// over its elements, so no element of it is ever rendered into something a
// caller can print.
func TestRenderElements_sensitiveContainerCollapsesToOneDigest(t *testing.T) {
	secretList := cty.ListVal([]cty.Value{cty.StringVal("hunter2"), cty.StringVal("hunter3")}).Mark(marks.Sensitive)

	parts, _ := renderElements(secretList)
	joined := strings.Join(parts, ",")
	if strings.Contains(joined, "hunter2") || strings.Contains(joined, "hunter3") {
		t.Fatalf("a sensitive container rendered its elements in plaintext: %q", joined)
	}
	if len(parts) != 1 || !strings.HasPrefix(parts[0], "sensitive:sha256:") {
		t.Errorf("a sensitive container rendered %d part(s) %q, want one sensitive:sha256: digest", len(parts), joined)
	}

	// And the digest still moves when the secret does, or a changed secret
	// would read as no change.
	moved, _ := renderElements(cty.ListVal([]cty.Value{cty.StringVal("hunter2"), cty.StringVal("hunter4")}).Mark(marks.Sensitive))
	if strings.Join(moved, ",") == joined {
		t.Errorf("two different sensitive containers rendered the same digest %q", joined)
	}
}

// TestCompareValues_unknownsAreExcluded is the "matched file stays matched"
// half of the ruling: an attribute that is unknown on either side is not a
// difference, whatever the other side says about it.
func TestCompareValues_unknownsAreExcluded(t *testing.T) {
	approved := Values{Decoded: true, After: map[string]Attr{
		"arn":  {Text: unknownToken, Unknown: true},
		"name": {Text: "s:app"},
	}}
	fresh := Values{Decoded: true, After: map[string]Attr{
		"arn":  {Text: unknownToken, Unknown: true},
		"name": {Text: "s:app"},
	}}
	if got := CompareValues(approved, fresh); len(got) != 0 {
		t.Errorf("two identical plans disagreed about %v", got)
	}

	// And the asymmetric case: one plan worked out what the other did not.
	fresh.After["arn"] = Attr{Text: "s:arn:aws:logs:::app"}
	if got := CompareValues(approved, fresh); len(got) != 0 {
		t.Errorf("an attribute unknown on one side was read as a difference: %v", got)
	}
}

// TestCompareValues_namesTheAttributesThatMoved, on both sides, because a
// drift in what a change is being made FROM matters as much as one in what it
// is being made TO.
func TestCompareValues_namesTheAttributesThatMoved(t *testing.T) {
	approved := Values{
		Decoded: true,
		Before:  map[string]Attr{"retention_in_days": {Text: "n:1"}, "name": {Text: "s:app"}},
		After:   map[string]Attr{"retention_in_days": {Text: "n:3"}, "name": {Text: "s:app"}},
	}
	fresh := Values{
		Decoded: true,
		Before:  map[string]Attr{"retention_in_days": {Text: "n:7"}, "name": {Text: "s:app"}},
		After:   map[string]Attr{"retention_in_days": {Text: "n:3"}, "name": {Text: "s:app"}},
	}
	got := strings.Join(CompareValues(approved, fresh), ",")
	if got != "before.retention_in_days" {
		t.Errorf("the compared attributes are %q, want %q - a drift in what the update starts from", got, "before.retention_in_days")
	}

	fresh.After["retention_in_days"] = Attr{Text: "n:14"}
	got = strings.Join(CompareValues(approved, fresh), ",")
	if got != "after.retention_in_days,before.retention_in_days" {
		t.Errorf("the compared attributes are %q, want both sides named", got)
	}
}

// TestCompareValues_undecodedIsNeverAgreement: a change whose values could
// not be read must not be reported as agreeing. It is compared by address,
// action and identity and no further, and the code says so rather than
// producing an empty digest that looks like a match.
func TestCompareValues_undecodedIsNeverAgreement(t *testing.T) {
	decoded := Values{Decoded: true, After: map[string]Attr{"name": {Text: "s:app"}}}
	if got := CompareValues(decoded, Values{}); len(got) != 0 {
		t.Errorf("an undecoded side produced attribute names %v; it has nothing to say", got)
	}
	if (Values{}).Decoded {
		t.Errorf("the zero Values claims to be decoded")
	}
}

// TestCompareValues_aMissingObjectIsNamedAsOne: one plan has an object on a
// side and the other does not.
func TestCompareValues_aMissingObjectIsNamedAsOne(t *testing.T) {
	approved := Values{Decoded: true, BeforeNull: true, After: map[string]Attr{"name": {Text: "s:app"}}}
	fresh := Values{Decoded: true, Before: map[string]Attr{"name": {Text: "s:app"}}, After: map[string]Attr{"name": {Text: "s:app"}}}
	got := strings.Join(CompareValues(approved, fresh), ",")
	if got != "before (one plan has no object here)" {
		t.Errorf("compared attributes %q, want the whole side named", got)
	}
}

// TestValuesOf_readsThroughTheProviderSchema is the wiring: a real encoded
// change, a real schema, and the rendered attributes by value. Without this
// every test above could pass over a valuesOf that returned nothing.
func TestValuesOf_readsThroughTheProviderSchema(t *testing.T) {
	schema := &providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":                {Type: cty.String, Computed: true},
			"name":              {Type: cty.String, Required: true},
			"retention_in_days": {Type: cty.Number, Optional: true},
		},
	}}
	ty := schema.Block.ImpliedType()

	before, err := plans.NewDynamicValue(cty.ObjectVal(map[string]cty.Value{
		"id":                cty.StringVal("lg-1"),
		"name":              cty.StringVal("app"),
		"retention_in_days": cty.NumberIntVal(1),
	}), ty)
	if err != nil {
		t.Fatalf("encoding before: %s", err)
	}
	after, err := plans.NewDynamicValue(cty.ObjectVal(map[string]cty.Value{
		"id":                cty.StringVal("lg-1"),
		"name":              cty.StringVal("app"),
		"retention_in_days": cty.NumberIntVal(3),
	}), ty)
	if err != nil {
		t.Fatalf("encoding after: %s", err)
	}

	addr := mustAddr(t, "aws_cloudwatch_log_group.app")
	change := &plans.ResourceInstanceChangeSrc{
		Addr:        addr,
		PrevRunAddr: addr,
		ProviderAddr: addrs.AbsProviderConfig{
			Module:   addrs.RootModule,
			Provider: addrs.NewDefaultProvider("aws"),
		},
		ChangeSrc: plans.ChangeSrc{Action: plans.Update, Before: before, After: after},
	}

	got := valuesOf(change, func(addrs.Provider, addrs.ResourceMode, string) *providers.Schema { return schema })
	if !got.Decoded {
		t.Fatalf("a change with a schema and both sides encoded did not decode")
	}
	if got.Before["retention_in_days"].Text != "n:1" {
		t.Errorf("before.retention_in_days rendered %q, want %q", got.Before["retention_in_days"].Text, "n:1")
	}
	if got.After["retention_in_days"].Text != "n:3" {
		t.Errorf("after.retention_in_days rendered %q, want %q", got.After["retention_in_days"].Text, "n:3")
	}

	// And the whole point: the same change against a different planned
	// value is a difference, with the attribute named.
	moved, err := plans.NewDynamicValue(cty.ObjectVal(map[string]cty.Value{
		"id":                cty.StringVal("lg-1"),
		"name":              cty.StringVal("app"),
		"retention_in_days": cty.NumberIntVal(14),
	}), ty)
	if err != nil {
		t.Fatalf("encoding the moved value: %s", err)
	}
	movedChange := *change
	movedChange.After = moved
	freshValues := valuesOf(&movedChange, func(addrs.Provider, addrs.ResourceMode, string) *providers.Schema { return schema })
	if names := strings.Join(CompareValues(got, freshValues), ","); names != "after.retention_in_days" {
		t.Errorf("the comparison named %q, want %q", names, "after.retention_in_days")
	}

	// No schema for the type: compared by address, action and identity, and
	// honest about it.
	if valuesOf(change, nil).Decoded {
		t.Errorf("a change with no schema claimed its values were read")
	}
}
