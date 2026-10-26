// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configload"
	"github.com/intentius/choudoufu/internal/encryption"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plans/planfile"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/states/statefile"
	tfversion "github.com/intentius/choudoufu/version"
)

// jointTestPlanFile is the bytes of a plan file whose plan holds vars.
func jointTestPlanFile(t *testing.T, vars map[string]cty.Value) []byte {
	t.Helper()
	vv := map[string]plans.DynamicValue{}
	eph := map[string]bool{}
	for name, v := range vars {
		dv, err := plans.NewDynamicValue(v, cty.DynamicPseudoType)
		if err != nil {
			t.Fatal(err)
		}
		vv[name], eph[name] = dv, false
	}
	sf := &statefile.File{TerraformVersion: tfversion.SemVer, Lineage: "joint", State: states.NewState(), EncryptionStatus: encryption.StatusSatisfied}
	path := filepath.Join(t.TempDir(), "joint.plan")
	err := planfile.Create(path, planfile.CreateArgs{
		ConfigSnapshot:       configload.NewEmptySnapshot(),
		PreviousRunStateFile: sf,
		StateFile:            sf,
		Plan: &plans.Plan{
			Changes:            &plans.Changes{Resources: []*plans.ResourceInstanceChangeSrc{}, Outputs: []*plans.OutputChangeSrc{}},
			DriftedResources:   []*plans.ResourceInstanceChangeSrc{},
			VariableValues:     vv,
			EphemeralVariables: eph,
			Backend:            plans.Backend{Type: "local", Config: plans.DynamicValue([]byte("config placeholder")), Workspace: "default"},
			Checks:             &states.CheckResults{},
			PrevRunState:       sf.State,
			PriorState:         sf.State,
		},
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

// TestJointPlanFilesLeaveOutOtherUnitsSensitiveInputs is INTENTIUS/choudoufu#2270:
// each unit's plan file carries the joint plan with the other units'
// sensitive inputs left out (the plan's variable values, show -json's
// variables and the units' Vars), and keeps its own; an apply of one unit's
// file, where the session is gone, puts the others back from their own
// files, found by the layout the files were planned into.
func TestJointPlanFilesLeaveOutOtherUnitsSensitiveInputs(t *testing.T) {
	outDir := t.TempDir()
	dbPass := jointVarOf(t, cty.StringVal("hunter2"))
	dbPass.Sensitive = true
	appToken := jointVarOf(t, cty.StringVal("s3cret"))
	appToken.Sensitive = true
	units := []jointUnit{
		{ID: "db", Dir: "/r/db", Seq: 1, Out: filepath.Join(outDir, "db", "tfplan"),
			Vars: map[string]jointVar{"password": dbPass, "name": jointVarOf(t, cty.StringVal("orders"))}},
		{ID: "app", Dir: "/r/app", Seq: 2, Out: filepath.Join(outDir, "app", "tfplan"),
			Vars: map[string]jointVar{"token": appToken}},
	}
	_, inputs, err := renderJointRootInputs("/r/.root", units)
	if err != nil {
		t.Fatal(err)
	}
	plan := jointTestPlanFile(t, map[string]cty.Value{
		"db__password": cty.StringVal("hunter2"),
		"app__token":   cty.StringVal("s3cret"),
	})
	doc := []byte(`{"variables":{"db__password":{"value":"hunter2"},"app__token":{"value":"s3cret"}}}`)

	stubs := map[string]jointStub{}
	for _, u := range units {
		b, err := jointBundleFor(u.ID, plan, doc, units, nil, inputs)
		if err != nil {
			t.Fatal(err)
		}
		stub := jointStub{Magic: 1, Session: "/gone", Unit: u.ID, Serial: 3, Units: []string{"db", "app"}, Run: "run-1", Tool: "terragrunt", Bundle: b}
		stubs[u.ID] = stub
		body, err := json.Marshal(stub)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(u.Out), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(u.Out, body, 0o600); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), map[string]string{"db": "s3cret", "app": "hunter2"}[u.ID]) {
			t.Errorf("unit %s's plan file holds the other unit's sensitive input in clear", u.ID)
		}
	}

	appVars, err := planfile.ReadVariableValues(stubs["app"].Bundle.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := appVars["db__password"]; ok {
		t.Error("app's plan file holds db__password")
	}
	if _, ok := appVars["app__token"]; !ok {
		t.Error("app's plan file lost its own app__token")
	}
	for _, u := range stubs["app"].Bundle.Units {
		if u.ID == "db" && (!u.Vars["password"].Withheld || string(u.Vars["password"].Value) != "null" || u.Vars["name"].Withheld) {
			t.Errorf("db's Vars in app's file = %+v; want password withheld and null, name kept", u.Vars)
		}
	}

	// The apply job: the files sit in another directory, laid out as planned.
	moved := t.TempDir()
	for _, u := range units {
		dst := filepath.Join(moved, u.ID, "tfplan")
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			t.Fatal(err)
		}
		body, _ := os.ReadFile(u.Out)
		if err := os.WriteFile(dst, body, 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Remove(u.Out)
	}
	app := stubs["app"]
	if err := jointRestoreWithheld(app, filepath.Join(moved, "app", "tfplan")); err != nil {
		t.Fatalf("restoring db's inputs for app's apply: %v", err)
	}
	restored, err := planfile.ReadVariableValues(app.Bundle.Plan)
	if err != nil {
		t.Fatal(err)
	}
	dv, ok := restored["db__password"]
	if !ok {
		t.Fatal("db__password was not put back into the plan")
	}
	got, err := dv.Decode(cty.DynamicPseudoType)
	if err != nil || !got.RawEquals(cty.StringVal("hunter2")) {
		t.Errorf("db__password restored as %#v (%v), want \"hunter2\"", got, err)
	}
	for _, u := range app.Bundle.Units {
		if u.ID == "db" && (u.Vars["password"].Withheld || string(u.Vars["password"].Value) != `"hunter2"`) {
			t.Errorf("db's password in the rebuilt units = %+v", u.Vars["password"])
		}
	}

	// Without db's file the apply refuses rather than apply without it.
	// (The restore above filled app's bundle in place, so app's file is read
	// again.)
	_ = os.Remove(filepath.Join(moved, "db", "tfplan"))
	again, ok := readJointStub(filepath.Join(moved, "app", "tfplan"))
	if !ok {
		t.Fatal("app's plan file does not read back")
	}
	if err := jointRestoreWithheld(again, filepath.Join(moved, "app", "tfplan")); err == nil || !strings.Contains(err.Error(), "db") {
		t.Errorf("restore with db's file gone = %v, want an error naming db", err)
	}
}
