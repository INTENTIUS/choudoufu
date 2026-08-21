// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #343. `live-plan` runs the plan graph with SkipRefresh, so a
// projected object's AttrSensitivePaths is the ONLY sensitivity the plan's
// "before" side ever has, while its "after" side is re-marked from the
// schema on every run. The claim under test is therefore always about
// AttrSensitivePaths on what the projection wrote, never about a boolean:
// an unmarked before against a marked after is a perpetual in-place update
// that OpenTofu's own renderer annotates "The value is unchanged", and that
// diff is invisible to anything that only asks "did the build succeed".

// sensitivePathStrings renders one instance's recorded sensitive paths as
// comparable strings, straight off the encoded object the projection put
// into prior state - the same bytes the plan graph decodes.
func sensitivePathStrings(t *testing.T, res *Result, addr addrs.AbsResourceInstance) []string {
	t.Helper()
	inst := res.State.ResourceInstance(addr)
	if inst == nil || inst.Current == nil {
		t.Fatalf("%s has no current object in the projection", addr)
	}
	out := make([]string, 0, len(inst.Current.AttrSensitivePaths))
	for _, pvm := range inst.Current.AttrSensitivePaths {
		var b strings.Builder
		for _, step := range pvm.Path {
			switch s := step.(type) {
			case cty.GetAttrStep:
				b.WriteString("." + s.Name)
			case cty.IndexStep:
				b.WriteString("[" + s.Key.GoString() + "]")
			}
		}
		out = append(out, b.String())
	}
	return out
}

// TestMaterializeMarksASensitiveAttributeFromTheSchema is #343's core claim
// on the concrete-cloud path: an object the provider handed back unmarked -
// which is every object, since the plugin protocol has nowhere to put a mark -
// reaches prior state carrying the sensitivity its own schema declares.
//
// The claim holds today and held before #343 was filed; [importAndRead] has
// applied schema.Block.ValueMarks since this fork's first commit. What did
// not exist was this test, so the property was true by accident of nobody
// having deleted the line. Deleting it now turns this red.
//
// aws_ssm_parameter.value is the case, and it is a measured one rather than
// an invented one: live/wo-sweep.json records it as optional, computed and
// Sensitive in hashicorp/aws 6.59.0, which is why fakeSensitive models it.
func TestMaterializeMarksASensitiveAttributeFromTheSchema(t *testing.T) {
	cfg := loadConfig(t, estateDir(t))
	resolutions := resolveOrFail(t, cfg)

	cloud := newFakeCloud()
	cloud.put("aws_ssm_parameter", "/tofu-receipts/stateless-e2e/demo-effect", map[string]string{
		"id": "/tofu-receipts/stateless-e2e/demo-effect", "name": "/tofu-receipts/stateless-e2e/demo-effect",
		"type": "String", "value": "the-secret",
	})
	cloud.put("aws_cloudwatch_log_group", "/stateless-e2e/app", map[string]string{
		"id": "/stateless-e2e/app", "name": "/stateless-e2e/app",
	})

	res, diags := Build(context.Background(), cfg, resolutions, cloud.providers(t))
	assertNoErrors(t, diags)

	param := mustAddr(t, `aws_ssm_parameter.demo_effect`)
	got := sensitivePathStrings(t, res, param)
	if len(got) != 1 || got[0] != ".value" {
		t.Fatalf("AttrSensitivePaths of %s = %v, want exactly [.value] - "+
			"without it the plan's before side is unmarked, its after side is marked, "+
			"and the estate replans dirty on this attribute forever", param, got)
	}

	// The value itself must still be there and still be right. A mark that
	// arrived by losing the value would pass the assertion above.
	obj, err := res.State.ResourceInstance(param).Current.Decode(
		fakeSchemas()["aws_ssm_parameter"].Block.ImpliedType())
	if err != nil {
		t.Fatalf("decoding the materialized object: %s", err)
	}
	val, valMarks := obj.Value.GetAttr("value").Unmark()
	if _, ok := valMarks[marks.Sensitive]; !ok {
		t.Errorf("the decoded value attribute carries %v, want a sensitive mark", valMarks)
	}
	if val.AsString() != "the-secret" {
		t.Errorf("value = %q, want %q", val.AsString(), "the-secret")
	}

	// The negative control on the same run: a type whose schema marks
	// nothing sensitive gains nothing. Without this the test would still
	// pass if the projection marked everything.
	group := mustAddr(t, `aws_cloudwatch_log_group.app`)
	if got := sensitivePathStrings(t, res, group); len(got) != 0 {
		t.Errorf("AttrSensitivePaths of %s = %v, want none: nothing in its schema is Sensitive", group, got)
	}
}

// TestMaterializeMarksSensitivityInsideANestedBlock is the derivation claim.
// The fix is one call to the schema's own [configschema.Block.ValueMarks],
// which descends nested blocks and nested object attributes, so the rule
// reaches an attribute no top-level scan would - and reaches it without any
// type name in the control flow.
//
// It matters because the real population is not top-level-only:
// live/wo-sweep.json's findings for hashicorp/aws 6.59.0 include
// aws_lb_listener's default_action.authenticate_oidc.client_secret, three
// levels down, which is exactly this shape.
func TestMaterializeMarksSensitivityInsideANestedBlock(t *testing.T) {
	dir := t.TempDir()
	const src = `
resource "fake_listener" "app" {
  default_action {
    authenticate_oidc {
      client_secret = "shh"
    }
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	cfg := loadConfig(t, dir)
	addr := mustAddr(t, `fake_listener.app`)

	inner := &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"client_secret": {Type: cty.String, Optional: true, Sensitive: true},
			"issuer":        {Type: cty.String, Optional: true},
		},
	}
	schema := providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id": {Type: cty.String, Computed: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"default_action": {
				Nesting: configschema.NestingList,
				Block: configschema.Block{
					BlockTypes: map[string]*configschema.NestedBlock{
						"authenticate_oidc": {Nesting: configschema.NestingList, Block: *inner},
					},
				},
			},
		},
	}}

	live := cty.ObjectVal(map[string]cty.Value{
		"id": cty.StringVal("listener-1"),
		"default_action": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"authenticate_oidc": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
				"client_secret": cty.StringVal("shh"),
				"issuer":        cty.StringVal("https://example.invalid"),
			})}),
		})}),
	})

	provAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("fake")}
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"fake_listener": schema},
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{
			TypeName: r.TypeName,
			State:    cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal(r.Target.ID), "default_action": cty.NullVal(live.Type().AttributeType("default_action"))}),
		}}}
	}
	p.ReadResourceFn = func(providers.ReadResourceRequest) providers.ReadResourceResponse {
		return providers.ReadResourceResponse{NewState: live}
	}

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: addr, Class: identity.ClassConcrete, ImportID: "listener-1"},
	}, SingleProvider(provAddr, p))
	assertNoErrors(t, diags)

	got := sensitivePathStrings(t, res, addr)
	want := `.default_action[cty.NumberIntVal(0)].authenticate_oidc[cty.NumberIntVal(0)].client_secret`
	if len(got) != 1 || got[0] != want {
		t.Errorf("AttrSensitivePaths = %v, want exactly [%s] - the schema walk did not descend the nested blocks", got, want)
	}
}

// TestMaterializedSensitiveAttrCannotCompoundAnImportIdentity pins the
// consequence GitHub issue #343 expected the marking to have, and which it
// already had: b.live carries the marked value, so a child whose import
// identity is composed from a parent's Sensitive attribute is REFUSED
// instead of quietly writing that secret into a cloud tag in plaintext.
//
// #343 called this "a currently-silent leak" that its fix would turn into "a
// new refusal ... no estate has been measured for it". Neither half was so:
// the refusal is not new, and the measurement below says it fires for
// nothing in the fixture population.
//
// Refusing is the safe direction and it is measured, not assumed: across the
// 116 PARENT_DERIVED formulas in internal/live/check/testdata/identity-golden.txt
// the only parent attributes ever composed are id, arn, zone_id, url and a
// handful of *_arn identity attributes - none Sensitive in any of the 61
// admitted types live/wo-sweep.json records a sensitive attribute for. So
// this refusal fires for no configuration in the fixture population, and
// this test is the only place it is exercised at all.
func TestMaterializedSensitiveAttrCannotCompoundAnImportIdentity(t *testing.T) {
	dir := t.TempDir()
	const src = `
resource "aws_ssm_parameter" "secret" {
  name  = "/app/secret"
  type  = "SecureString"
  value = "the-secret"
}

resource "aws_cloudwatch_log_group" "derived" {
  name = "/app/derived"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	cfg := loadConfig(t, dir)

	param := mustAddr(t, `aws_ssm_parameter.secret`)
	child := mustAddr(t, `aws_cloudwatch_log_group.derived`)

	cloud := newFakeCloud()
	cloud.put("aws_ssm_parameter", "/app/secret", map[string]string{
		"id": "/app/secret", "name": "/app/secret", "type": "SecureString", "value": "the-secret",
	})
	cloud.put("aws_cloudwatch_log_group", "the-secret-suffix", map[string]string{
		"id": "the-secret-suffix", "name": "/app/derived",
	})

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: param, Class: identity.ClassConcrete, ImportID: "/app/secret"},
		derivedOn(child, param, "value", "-suffix"),
	}, cloud.providers(t))

	if !diags.HasErrors() {
		t.Fatal("an import identity composed from a Sensitive attribute was accepted; it would be written into a cloud tag in plaintext")
	}
	if !hasDiag(diags, "Cannot read a parent's identity from the projection", "is marked (sensitive or ephemeral)") {
		t.Errorf("wrong diagnostics:\n%s", renderDiags(diags))
	}
	assertOmitted(t, res, map[string]Reason{`aws_cloudwatch_log_group.derived`: ReasonFailed})
	if !res.Has(param) {
		t.Error("the parent itself should still be in the projection; only the child that tried to read its secret failed")
	}

	// The negative control: the same shape composed from a NON-sensitive
	// attribute of the same parent still resolves. Without this, a fix that
	// refused every parent-derived formula would pass the assertion above.
	cloud2 := newFakeCloud()
	cloud2.put("aws_ssm_parameter", "/app/secret", map[string]string{
		"id": "/app/secret", "name": "/app/secret", "type": "SecureString", "value": "the-secret",
	})
	cloud2.put("aws_cloudwatch_log_group", "/app/secret-suffix", map[string]string{
		"id": "/app/secret-suffix", "name": "/app/derived",
	})
	res2, diags2 := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: param, Class: identity.ClassConcrete, ImportID: "/app/secret"},
		derivedOn(child, param, "name", "-suffix"),
	}, cloud2.providers(t))
	assertNoErrors(t, diags2)
	assertMaterialized(t, res2, []string{`aws_cloudwatch_log_group.derived`, `aws_ssm_parameter.secret`})
}

// TestMaterializeRecordComposesSchemaSensitivityWithTheRecords is the
// record-backed half of #343, and the reason GitHub issue #344's population
// is smaller than it looks: a record written before this fork persisted
// sensitivity at all still materializes MARKED, because the schema is
// consulted on the way out as well as the record.
//
// The record here is written by [encodeRecordPayload] from an unmarked
// value, which is byte-for-byte what a pre-sensitivity record is - the
// property recordPayload.SensitiveAttrs's own doc comment states and
// TestSensitivePathsUseTheStateFilesOwnShape checks.
func TestMaterializeRecordComposesSchemaSensitivityWithTheRecords(t *testing.T) {
	cfg := loadConfig(t, writeNullResourceFixture(t))
	addr := mustAddr(t, `null_resource.trigger`)

	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	const prefix = "tofu-records/schema-marks"

	payload, err := encodeRecordPayload(cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("recorded"),
		"triggers": cty.MapVal(map[string]cty.Value{"input": cty.StringVal("value")}),
	}), nil, states.ObjectReady)
	if err != nil {
		t.Fatalf("encoding the pre-sensitivity record: %s", err)
	}
	if strings.Contains(string(payload), "sensitive_attributes") {
		t.Fatalf("the fixture record is not the pre-sensitivity shape: %s", payload)
	}
	if _, err := store.PutIfVersion(context.Background(), RecordKey(prefix, addr), payload, ""); err != nil {
		t.Fatalf("seeding the record: %s", err)
	}

	// A provider whose schema marks triggers sensitive - the config-derived
	// case recordPayload.SensitiveAttrs's doc comment names, standing in for
	// any record-backed type whose schema declares sensitivity after the
	// record was written.
	schema := providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":       {Type: cty.String, Computed: true},
			"triggers": {Type: cty.Map(cty.String), Optional: true, Sensitive: true},
		},
	}}
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"null_resource": schema},
		},
	}
	p.ConfigureProviderCalled = true

	res, diags := BuildWith(context.Background(), cfg,
		[]identity.Resolution{{Addr: addr, Class: identity.ClassRecordBacked}},
		SingleProvider(nullProvider, p),
		Options{RecordStore: store, RecordKeyPrefix: prefix})
	assertNoErrors(t, diags)

	if got := sensitivePathStrings(t, res, addr); len(got) != 1 || got[0] != ".triggers" {
		t.Errorf("AttrSensitivePaths = %v, want exactly [.triggers]: the schema's sensitivity has to compose with the record's, "+
			"or a record written before the attribute was sensitive replans dirty forever", got)
	}
}

// TestMarkSchemaSensitiveKeepsMarksItDidNotAdd is the other half of the
// composition. A value can reach the encode already carrying marks that did
// not come from the schema, and dropping one on the way past would be the
// identical defect pointed the other way: the plan's before side would lose
// a mark its after side still has.
func TestMarkSchemaSensitiveKeepsMarksItDidNotAdd(t *testing.T) {
	block := &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"secret": {Type: cty.String, Optional: true, Sensitive: true},
		"other":  {Type: cty.String, Optional: true},
	}}
	val := cty.ObjectVal(map[string]cty.Value{
		"secret": cty.StringVal("s"),
		"other":  cty.StringVal("o").Mark(marks.Sensitive),
	})

	_, pvms := markSchemaSensitive(val, block).UnmarkDeepWithPaths()
	got := map[string]bool{}
	for _, pvm := range pvms {
		if len(pvm.Path) != 1 {
			t.Fatalf("unexpected path %#v", pvm.Path)
		}
		got[pvm.Path[0].(cty.GetAttrStep).Name] = true
	}
	if !got["secret"] || !got["other"] || len(got) != 2 {
		t.Errorf("marked attributes = %v, want both secret (from the schema) and other (already on the value)", got)
	}
}

// TestMarkSchemaSensitiveIsIdentityWhenNothingIsSensitive pins the common
// case as doing literally nothing. Every type without a Sensitive attribute
// goes through this call on every materialization, and a fix that rebuilt
// each of those values would be paying for a walk that changes nothing.
func TestMarkSchemaSensitiveIsIdentityWhenNothingIsSensitive(t *testing.T) {
	block := &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"a": {Type: cty.String, Optional: true},
	}}
	val := cty.ObjectVal(map[string]cty.Value{"a": cty.StringVal("x")})
	if got := markSchemaSensitive(val, block); !got.RawEquals(val) {
		t.Errorf("markSchemaSensitive changed a value no schema marks: %#v", got)
	}
	if got := markSchemaSensitive(cty.NilVal, block); got != cty.NilVal {
		t.Errorf("markSchemaSensitive(NilVal) = %#v, want NilVal", got)
	}
	if got := markSchemaSensitive(val, nil); !got.RawEquals(val) {
		t.Errorf("markSchemaSensitive with no schema changed the value")
	}
}

// TestMarkSchemaSensitiveDoesNotAddDeprecationMarks holds the subject=nil
// argument in place. A deprecation mark carries a diagnostic OpenTofu raises
// against a CONFIGURATION; putting one on prior state read back out of the
// cloud would raise it against an argument nobody wrote. Upstream's refresh
// passes nil for the same reason, and this is the check that a later edit
// cannot quietly start passing an address instead.
func TestMarkSchemaSensitiveDoesNotAddDeprecationMarks(t *testing.T) {
	block := &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"old": {Type: cty.String, Optional: true, Deprecated: true, DeprecationMessage: "use new"},
	}}
	val := cty.ObjectVal(map[string]cty.Value{"old": cty.StringVal("x")})
	if got := markSchemaSensitive(val, block); !got.RawEquals(val) {
		t.Errorf("a deprecated-but-not-sensitive attribute was marked: %#v", got)
	}
}

// TestCombineValueMarksMergesOnePathOnce pins the property the reproduction
// of internal/tofu's combinePathValueMarks exists for. Two entries at the
// same path merge into one, because
// [states.ResourceInstanceObject.Encode] writes one AttrSensitivePaths entry
// per PathValueMarks and a duplicated pair would be persisted twice - which
// changes a record's bytes and therefore, via SeedRecordForInstance, whether
// a re-migration is a no-op.
func TestCombineValueMarksMergesOnePathOnce(t *testing.T) {
	type other struct{ name string }
	pathA := cty.GetAttrPath("a")
	pathB := cty.GetAttrPath("b")

	left := []cty.PathValueMarks{{Path: pathA, Marks: cty.NewValueMarks(marks.Sensitive)}}
	right := []cty.PathValueMarks{
		{Path: pathA, Marks: cty.NewValueMarks(other{"x"})},
		{Path: pathB, Marks: cty.NewValueMarks(marks.Sensitive)},
	}

	got := combineValueMarks(left, right)
	if len(got) != 2 {
		t.Fatalf("combineValueMarks produced %d entries, want 2 (a merged, b added): %#v", len(got), got)
	}
	for _, pvm := range got {
		if pvm.Path.Equals(pathA) && len(pvm.Marks) != 2 {
			t.Errorf("the merged entry at .a carries %v, want both marks", pvm.Marks)
		}
	}
	// The receiver's own map must not have been mutated: it can be shared
	// with a value the caller still holds.
	if len(left[0].Marks) != 1 {
		t.Errorf("combineValueMarks mutated its left argument's mark set: %v", left[0].Marks)
	}

	if got := combineValueMarks(nil, right); len(got) != 2 {
		t.Errorf("combineValueMarks(nil, right) = %#v, want right", got)
	}
	if got := combineValueMarks(left, nil); len(got) != 1 {
		t.Errorf("combineValueMarks(left, nil) = %#v, want left", got)
	}
}

// TestNormalizeIdentityAttrsAsksTheProviderWithAnUnmarkedValue is the
// interaction the schema marking has with GitHub issue #281's
// identity-spelling normalization, and it was found by #343's scouting
// rather than by a failing estate - which is the whole reason it needs a
// test.
//
// builder.normalizeIdentityAttrs asks the provider "what would you store if
// this were a brand-new create", using the object the provider itself just
// returned as the configuration. That object is marked from the schema by
// importAndRead, and cty's msgpack encoder refuses a marked value outright
// ("value has marks, so it cannot be serialized"). The refusal arrives as an
// ordinary provider diagnostic, and normalizeIdentityAttrs' error branch
// treats any provider diagnostic as "leave the attributes as ReadResource
// returned them" - so #281 was silently off for every type with a Sensitive
// attribute anywhere in its schema, with one TRACE line as the only evidence.
//
// tofu.MockProvider does not marshal, so no mock can reproduce the failure
// itself. What is asserted instead is the property that makes it impossible:
// the value put to the provider carries no marks. The mark stripped here is
// re-derived from the schema by the caller and never from this round trip.
func TestNormalizeIdentityAttrsAsksTheProviderWithAnUnmarkedValue(t *testing.T) {
	dir := t.TempDir()
	const src = `
resource "fake_thing" "app" {
  name = "app"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	cfg := loadConfig(t, dir)
	addr := mustAddr(t, `fake_thing.app`)

	schema := providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":     {Type: cty.String, Computed: true},
			"name":   {Type: cty.String, Required: true},
			"secret": {Type: cty.String, Optional: true, Computed: true, Sensitive: true},
		},
	}}
	live := cty.ObjectVal(map[string]cty.Value{
		"id":     cty.StringVal("thing-1"),
		"name":   cty.StringVal("app"),
		"secret": cty.StringVal("shh"),
	})

	provAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("fake")}
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"fake_thing": schema},
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{
			TypeName: r.TypeName,
			State: cty.ObjectVal(map[string]cty.Value{
				"id": cty.StringVal(r.Target.ID), "name": cty.NullVal(cty.String), "secret": cty.NullVal(cty.String),
			}),
		}}}
	}
	p.ReadResourceFn = func(providers.ReadResourceRequest) providers.ReadResourceResponse {
		return providers.ReadResourceResponse{NewState: live}
	}
	asked := 0
	p.PlanResourceChangeFn = func(r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		asked++
		for name, v := range map[string]cty.Value{"Config": r.Config, "ProposedNewState": r.ProposedNewState, "PriorState": r.PriorState} {
			if v != cty.NilVal && v.ContainsMarked() {
				t.Errorf("%s put to the provider contains a mark; cty's msgpack encoder refuses it and #281's normalization silently stops running", name)
			}
		}
		return providers.PlanResourceChangeResponse{PlannedState: r.ProposedNewState}
	}

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: addr, Class: identity.ClassConcrete, ImportID: "thing-1", IdentityValues: map[string]string{"name": "app"}},
	}, SingleProvider(provAddr, p))
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{`fake_thing.app`})

	if asked == 0 {
		t.Fatal("the provider was never asked for a create-time plan, so this test asserted nothing")
	}
	// The marks are still on what gets persisted: unmarking is for the
	// question put to the provider, not for the projection.
	if got := sensitivePathStrings(t, res, addr); len(got) != 1 || got[0] != ".secret" {
		t.Errorf("AttrSensitivePaths = %v, want exactly [.secret]", got)
	}
}
