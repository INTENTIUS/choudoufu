// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configload"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/encryption"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plans/planfile"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/states/statefile"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
	tfversion "github.com/intentius/choudoufu/version"
)

// INTENTIUS/choudoufu#2277's tests: a joint plan file drops every sensitive
// planned value and keeps an HMAC of each under the planning session's key;
// an apply plans again, which recomputes them, and refuses when one differs
// or the key is not there.

const (
	jsvOld     = "hunter2-old-value"
	jsvPlanned = "hunter2-planned-value"
	jsvMoved   = "hunter3-moved-value"
)

var jsvProvider = addrs.NewDefaultProvider("test")

func jsvSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":       {Type: cty.String, Computed: true},
		"name":     {Type: cty.String, Optional: true},
		"password": {Type: cty.String, Optional: true, Sensitive: true},
	}}}
}

func jsvAddr() addrs.AbsResourceInstance {
	return addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "test_thing", Name: "a"}.
		Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance.Child("db", addrs.NoKey))
}

func jsvProviderConfig() addrs.AbsProviderConfig {
	return addrs.AbsProviderConfig{Provider: jsvProvider, Module: addrs.RootModule}
}

func jsvObj(name, password string) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id": cty.StringVal("a1"), "name": cty.StringVal(name), "password": cty.StringVal(password),
	})
}

// jsvPlan is the joint plan of one unit, db: an update of
// module.db.test_thing.a whose password moves from jsvOld to password, and
// a sensitive root output db__password of it.
func jsvPlan(t *testing.T, password string) *plans.Plan {
	t.Helper()
	ty := jsvSchema().Block.ImpliedType()
	before, err := plans.NewDynamicValue(jsvObj("orders", jsvOld), ty)
	if err != nil {
		t.Fatal(err)
	}
	after, err := plans.NewDynamicValue(jsvObj("orders", password), ty)
	if err != nil {
		t.Fatal(err)
	}
	sens := []cty.PathValueMarks{{Path: cty.GetAttrPath("password"), Marks: cty.NewValueMarks(marks.Sensitive)}}
	outAfter, err := plans.NewDynamicValue(cty.StringVal(password), cty.DynamicPseudoType)
	if err != nil {
		t.Fatal(err)
	}
	outBefore, err := plans.NewDynamicValue(cty.NullVal(cty.DynamicPseudoType), cty.DynamicPseudoType)
	if err != nil {
		t.Fatal(err)
	}
	state := states.NewState()
	state.EnsureModule(jsvAddr().Module).SetResourceInstanceCurrent(jsvAddr().Resource, &states.ResourceInstanceObjectSrc{
		Status:             states.ObjectReady,
		AttrsJSON:          []byte(`{"id":"a1","name":"orders","password":"` + jsvOld + `"}`),
		AttrSensitivePaths: sens,
	}, jsvProviderConfig(), addrs.NoKey)
	return &plans.Plan{
		Changes: &plans.Changes{
			Resources: []*plans.ResourceInstanceChangeSrc{{
				Addr: jsvAddr(), PrevRunAddr: jsvAddr(), ProviderAddr: jsvProviderConfig(),
				ChangeSrc: plans.ChangeSrc{Action: plans.Update, Before: before, After: after, BeforeValMarks: sens, AfterValMarks: sens},
			}},
			Outputs: []*plans.OutputChangeSrc{{
				Addr:      addrs.OutputValue{Name: "db__password"}.Absolute(addrs.RootModuleInstance),
				ChangeSrc: plans.ChangeSrc{Action: plans.Create, Before: outBefore, After: outAfter},
				Sensitive: true,
			}},
		},
		DriftedResources:   []*plans.ResourceInstanceChangeSrc{},
		VariableValues:     map[string]plans.DynamicValue{},
		EphemeralVariables: map[string]bool{},
		Backend:            plans.Backend{Type: "local", Config: plans.DynamicValue([]byte("config placeholder")), Workspace: "default"},
		Checks:             &states.CheckResults{},
		PrevRunState:       state,
		PriorState:         state,
	}
}

func jsvSchemas() map[addrs.Provider]providers.ProviderSchema {
	return map[addrs.Provider]providers.ProviderSchema{jsvProvider: {
		Provider:      providers.Schema{Block: &configschema.Block{}},
		ResourceTypes: map[string]providers.Schema{"test_thing": jsvSchema()},
	}}
}

// jsvPlanFile is the bytes of a plan file of jsvPlan, with its schemas.
func jsvPlanFile(t *testing.T, password string) []byte {
	t.Helper()
	plan := jsvPlan(t, password)
	sf := &statefile.File{TerraformVersion: tfversion.SemVer, Lineage: "joint", State: plan.PriorState, EncryptionStatus: encryption.StatusSatisfied}
	path := filepath.Join(t.TempDir(), "joint.plan")
	err := planfile.Create(path, planfile.CreateArgs{
		ConfigSnapshot:       configload.NewEmptySnapshot(),
		PreviousRunStateFile: sf,
		StateFile:            sf,
		Plan:                 plan,
		Schemas:              jsvSchemas(),
	}, encryption.PlanEncryptionDisabled())
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// jsvDoc is the show -json of jsvPlan, the shape the bundle carries.
func jsvDoc(password string) []byte {
	return []byte(`{
  "planned_values": {
    "outputs": {"db__password": {"sensitive": true, "value": "` + password + `"}},
    "root_module": {"child_modules": [{"address": "module.db", "resources": [{"address": "module.db.test_thing.a",
      "values": {"id": "a1", "name": "orders", "password": "` + password + `"}, "sensitive_values": {"password": true}}]}]}
  },
  "prior_state": {"values": {"root_module": {"child_modules": [{"resources": [{"address": "module.db.test_thing.a",
      "values": {"id": "a1", "name": "orders", "password": "` + jsvOld + `"}, "sensitive_values": {"password": true}}]}]}}},
  "resource_changes": [{"address": "module.db.test_thing.a", "mode": "managed", "change": {"actions": ["update"],
    "before": {"id": "a1", "name": "orders", "password": "` + jsvOld + `"}, "before_sensitive": {"password": true},
    "after": {"id": "a1", "name": "orders", "password": "` + password + `"}, "after_sensitive": {"password": true}, "after_unknown": {}}}],
  "output_changes": {"db__password": {"actions": ["create"], "before": null, "after": "` + password + `", "before_sensitive": false, "after_sensitive": true}}
}`)
}

// jsvBytesOf is every byte a plan file of a joint run holds that a reader
// could get at: the stub's JSON, the carried show -json, and each entry of
// the carried plan file decompressed.
func jsvBytesOf(t *testing.T, body []byte) [][]byte {
	t.Helper()
	var stub jointStub
	if err := json.Unmarshal(body, &stub); err != nil {
		t.Fatal(err)
	}
	out := [][]byte{body, stub.Bundle.Doc}
	zr, err := zip.NewReader(bytes.NewReader(stub.Bundle.Plan), int64(len(stub.Bundle.Plan)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
	return out
}

func jsvKey() []byte { return bytes.Repeat([]byte{7}, 32) }

// TestJointPlanFileHoldsNoSensitiveValues: the plan file of a unit holds
// none of the sensitive planned values' bytes, in the plan's changes, its
// state snapshots, its outputs or the show -json it carries, nor the key;
// it keeps an HMAC of each known one.
func TestJointPlanFileHoldsNoSensitiveValues(t *testing.T) {
	raw := jsvPlanFile(t, jsvPlanned)
	if !bytes.Contains(jsvUnzipAll(t, raw), []byte(jsvPlanned)) {
		t.Fatal("the fixture's plan file does not hold the planned secret; the test would prove nothing")
	}
	key := jsvKey()
	plan, doc, hashes, err := jointDropSensitive(raw, jsvDoc(jsvPlanned), key)
	if err != nil {
		t.Fatal(err)
	}
	units := []jointUnit{{ID: "db", Dir: "/r/db", Seq: 1, Out: "/out/db/tfplan"}}
	b, err := jointBundleFor("db", plan, doc, units, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	b.Scrubbed, b.Sensitive = true, hashes
	body, err := json.Marshal(jointStub{Magic: 1, Session: "/gone", Unit: "db", Serial: 1, Units: []string{"db"}, Run: "run-1", Tool: "terragrunt", Bundle: b})
	if err != nil {
		t.Fatal(err)
	}
	for i, part := range jsvBytesOf(t, body) {
		for _, secret := range []string{jsvOld, jsvPlanned} {
			if bytes.Contains(part, []byte(secret)) {
				t.Errorf("part %d of the plan file holds the sensitive value %q", i, secret)
			}
		}
		if bytes.Contains(part, key) {
			t.Errorf("part %d of the plan file holds the session's key", i)
		}
	}
	if !bytes.Contains(doc, []byte(`"orders"`)) {
		t.Error("the carried show -json lost a value that is not sensitive")
	}

	// before and after of the change, and after of the output: three
	// hashes, each naming unit db. The output's null before is not one.
	want := map[string]bool{
		"module.db.test_thing.a|before|false": true,
		"module.db.test_thing.a|after|false":  true,
		"output.db__password|after|true":      true,
	}
	if len(hashes) != len(want) {
		t.Fatalf("hashes = %+v, want %d", hashes, len(want))
	}
	for _, h := range hashes {
		k := h.Address + "|" + h.Side + "|" + map[bool]string{true: "true", false: "false"}[h.Output]
		if !want[k] || h.Unit != "db" || len(h.HMAC) != 64 || h.Raw {
			t.Errorf("unexpected hash %+v", h)
		}
	}

	// The same value under another session's key hashes differently.
	_, _, other, err := jointDropSensitive(raw, jsvDoc(jsvPlanned), bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if other[0].HMAC == hashes[0].HMAC {
		t.Error("two session keys gave the same HMAC")
	}
}

func jsvUnzipAll(t *testing.T, raw []byte) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var all []byte
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		all = append(all, b...)
	}
	return all
}

// jsvApproved is the approval an apply of the scrubbed file reads.
func jsvApproved(t *testing.T, scrubbed []byte) *approvedPlan {
	t.Helper()
	path := filepath.Join(t.TempDir(), "joint.plan")
	if err := os.WriteFile(path, scrubbed, 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := planfile.Open(path, encryption.PlanEncryptionDisabled())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := reader.ReadPlan()
	if err != nil {
		t.Fatal(err)
	}
	return &approvedPlan{Path: "/out/db/tfplan", Plan: plan, Rerun: "terragrunt run --all plan"}
}

func jsvRunSchemas() *tofu.Schemas {
	return &tofu.Schemas{Providers: jsvSchemas()}
}

// TestJointApplyRecomputesSensitiveValues: the apply's own plan, with the
// same sensitive values, matches every hash; the values are put back into
// the approved plan and the approval comparison finds nothing to refuse.
func TestJointApplyRecomputesSensitiveValues(t *testing.T) {
	key := jsvKey()
	scrubbed, _, hashes, err := jointDropSensitive(jsvPlanFile(t, jsvPlanned), jsvDoc(jsvPlanned), key)
	if err != nil {
		t.Fatal(err)
	}
	approved := jsvApproved(t, scrubbed)
	refused := false
	check := &jointSensitiveCheck{Hashes: hashes, Key: key}
	guard := check.guard(approved, approvalGuard(approved, &refused), &refused)
	if diags := guard(jsvPlan(t, jsvPlanned), jsvRunSchemas()); diags.HasErrors() || refused {
		t.Fatalf("apply with the same sensitive values refused: %s", diags.Err())
	}
	ch, err := approved.Plan.Changes.Resources[0].Decode(&providers.Schema{Block: jsvSchema().Block})
	if err != nil {
		t.Fatal(err)
	}
	if got := ch.After.GetAttr("password"); !got.RawEquals(cty.StringVal(jsvPlanned)) {
		t.Errorf("the approved plan's password after the check = %#v, want the recomputed value", got)
	}
}

// TestJointApplyRefusesChangedSensitiveValue: the apply's own plan writes
// another password than the reviewed one. The check refuses, exit status 3,
// naming the unit and the address, and never the value; without the key it
// refuses the same way.
func TestJointApplyRefusesChangedSensitiveValue(t *testing.T) {
	key := jsvKey()
	scrubbed, _, hashes, err := jointDropSensitive(jsvPlanFile(t, jsvPlanned), jsvDoc(jsvPlanned), key)
	if err != nil {
		t.Fatal(err)
	}

	approved := jsvApproved(t, scrubbed)
	refused := false
	innerRan := false
	inner := func(*plans.Plan, *tofu.Schemas) tfdiags.Diagnostics { innerRan = true; return nil }
	guard := (&jointSensitiveCheck{Hashes: hashes, Key: key}).guard(approved, inner, &refused)
	diags := guard(jsvPlan(t, jsvMoved), jsvRunSchemas())
	if !diags.HasErrors() || !refused {
		t.Fatal("apply with a changed sensitive value was not refused")
	}
	if innerRan {
		t.Error("the approval comparison ran after the sensitive check refused")
	}
	msg := diags.Err().Error()
	for _, want := range []string{"unit db", "module.db.test_thing.a", "after password", "output.db__password", "terragrunt run --all plan"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not say %q:\n%s", want, msg)
		}
	}
	for _, secret := range []string{jsvOld, jsvPlanned, jsvMoved} {
		if strings.Contains(msg, secret) {
			t.Errorf("refusal prints the sensitive value %q", secret)
		}
	}
	if strings.Contains(msg, "before password") {
		t.Errorf("refusal names the before value, which did not move:\n%s", msg)
	}

	// No key: refused, naming the unit and address.
	approved = jsvApproved(t, scrubbed)
	refused = false
	guard = (&jointSensitiveCheck{Hashes: hashes}).guard(approved, nil, &refused)
	diags = guard(jsvPlan(t, jsvPlanned), jsvRunSchemas())
	if !diags.HasErrors() || !refused || !strings.Contains(diags.Err().Error(), "unit db: module.db.test_thing.a") {
		t.Errorf("apply without the key = %v, want a refusal naming unit db and module.db.test_thing.a", diags.Err())
	}
	detail := jointMissingKeyDetail("/out/db/tfplan", "/tmp/choudoufu-joint-501/run-1", hashes, os.ErrNotExist)
	if !strings.Contains(detail, "unit db: module.db.test_thing.a (sensitive after password)") || !strings.Contains(detail, "/tmp/choudoufu-joint-501/run-1") {
		t.Errorf("missing-key refusal does not name the unit, address and session:\n%s", detail)
	}
}

// TestJointSessionKey: the key is made once, 0600, in the session.
func TestJointSessionKey(t *testing.T) {
	s := &jointSession{dir: t.TempDir()}
	a, err := s.sensitiveKey()
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.sensitiveKey()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) || len(a) != 32 {
		t.Errorf("the session key changed or is not 32 bytes")
	}
	info, err := os.Stat(s.path(jointKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("key file mode = %v, want 0600", info.Mode().Perm())
	}
}
