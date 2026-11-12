// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #2393's follow-ups 1 to 3: the plan's count reaches the
// capacity check, the orphan sweep has a dry run, and the store's
// announcements are diagnostics.

var randomProvider = addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("random")}

func randomPasswordSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":     {Type: cty.String, Computed: true},
		"length": {Type: cty.Number, Required: true},
		"result": {Type: cty.String, Computed: true, Sensitive: true},
	}}}
}

func randomSchemas() *tofu.Schemas {
	return &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		randomProvider.Provider: {
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"random_password": randomPasswordSchema()},
		},
	}}
}

// planOf is a plan of the given actions on random_password instances; a
// create's result is unknown, the way a plan has it.
func planOf(t *testing.T, actions map[string]plans.Action) *plans.Plan {
	t.Helper()
	schema := randomPasswordSchema()
	ty := schema.Block.ImpliedType()
	planned := cty.ObjectVal(map[string]cty.Value{"id": cty.UnknownVal(cty.String), "length": cty.NumberIntVal(16), "result": cty.UnknownVal(cty.String)})
	prior := cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("none"), "length": cty.NumberIntVal(16), "result": cty.StringVal("x")})
	changes := plans.NewChanges()
	for a, action := range actions {
		addr := mustAddr(t, a)
		before, after := prior, planned
		switch action {
		case plans.Create:
			before = cty.NullVal(ty)
		case plans.Delete:
			after = cty.NullVal(ty)
		case plans.NoOp:
			after = prior
		}
		src, err := (&plans.ResourceInstanceChange{
			Addr: addr, PrevRunAddr: addr, ProviderAddr: randomProvider,
			Change: plans.Change{Action: action, Before: before, After: after},
		}).Encode(&schema)
		if err != nil {
			t.Fatal(err)
		}
		changes.Resources = append(changes.Resources, src)
	}
	return &plans.Plan{Changes: changes}
}

// TestPlannedSecretsCapacityCountsEveryCreate is follow-up 1. The plan
// creates more record-backed secrets than the standard tier holds, one
// value each. The store's lower bound at the first write saw that write's
// one parameter and nothing after it, so the apply started and would have
// met the ceiling partway. Counted from the plan, the post-plan step
// refuses and the first write is refused too, with the plan's number.
func TestPlannedSecretsCapacityCountsEveryCreate(t *testing.T) {
	ctx := context.Background()
	r := newSSMRig(t)
	s := r.open()

	actions := map[string]plans.Action{}
	for i := 0; i <= SSMStandardParameterCeiling; i++ {
		actions[fmt.Sprintf("random_password.p%d", i)] = plans.Create
	}
	diags := CheckPlannedSecretsCapacity(ctx, s, planOf(t, actions), randomSchemas())
	if !diags.HasErrors() || diags[0].Description().Summary != SummarySecretsCapacityExceeded {
		t.Fatalf("post-plan diagnostics = %v, want %q", diags.ErrWithWarnings(), SummarySecretsCapacityExceeded)
	}
	if want := fmt.Sprintf("reference %d standard-tier", SSMStandardParameterCeiling+1); !strings.Contains(diags[0].Description().Detail, want) {
		t.Errorf("the refusal does not carry the plan's count (%q): %s", want, diags[0].Description().Detail)
	}

	_, err := SeedRecordForInstance(ctx, NewRecordEnvelopeStore(s, r.prefix), mustAddr(t, "random_password.p0"), randomProvider, passwordValue("x"), nil, states.ObjectReady)
	var refused *SecretsCapacityError
	if !errors.As(err, &refused) || r.ssm.putCount() != 0 || len(r.log) != 0 {
		t.Errorf("first write after the plan: err=%v, %d creates, log %q; want SecretsCapacityError and nothing written", err, r.ssm.putCount(), r.log)
	}
}

// TestPlanCountIsTheEstateAfterThePlan pins the arithmetic: records the
// store holds, less the plan's deletes, with each written record at the
// larger of now and planned, read from the bucket for records this run
// never read.
func TestPlanCountIsTheEstateAfterThePlan(t *testing.T) {
	ctx := context.Background()
	r := newSSMRig(t)
	writer := NewRecordEnvelopeStore(r.open(), r.prefix)
	// Two references each: the result and the private bytes.
	seedPassword(t, writer, "random_password.keep", "k", []byte("p"))
	seedPassword(t, writer, "random_password.gone", "g", []byte("p"))
	seedPassword(t, writer, "random_password.same", "s", []byte("p"))

	s := r.open() // a run that has read nothing yet
	writes, err := PlannedSecretWrites(planOf(t, map[string]plans.Action{
		"random_password.gone": plans.Delete,
		"random_password.same": plans.NoOp,
		"random_password.keep": plans.Update, // planned 1, holds 2: stays 2
		"random_password.new1": plans.Create,
		"random_password.new2": plans.Create,
	}).Changes, randomSchemas())
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.PlanCount(ctx, writes)
	if err != nil {
		t.Fatal(err)
	}
	if want := (SecretParameterCount{Standard: 2 + 2 + 1 + 1}); n != want {
		t.Errorf("PlanCount = %+v, want %+v", n, want)
	}
}

// TestCountSensitivePathsSkipsKnownNulls: a known null moves nothing, an
// unknown will hold a value, and a path marked twice counts once.
func TestCountSensitivePathsSkipsKnownNulls(t *testing.T) {
	block := &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"a": {Type: cty.String, Sensitive: true, Optional: true},
		"b": {Type: cty.String, Sensitive: true, Optional: true},
		"c": {Type: cty.String, Optional: true},
	}}
	val := cty.ObjectVal(map[string]cty.Value{
		"a": cty.UnknownVal(cty.String),
		"b": cty.NullVal(cty.String),
		"c": cty.StringVal("x").Mark(marks.Sensitive),
	})
	if got := countSensitivePaths(val, block.ValueMarks); got != 2 {
		t.Errorf("countSensitivePaths = %d, want 2 (a unknown, c marked; b null)", got)
	}
}

// TestSSMSecretsAnnouncementsAreDiagnostics is follow-up 3: with no
// Announce hook, the advanced-tier announcement and a contract waiver are
// warning diagnostics a command takes, each once, and nothing is written to
// standard error.
func TestSSMSecretsAnnouncementsAreDiagnostics(t *testing.T) {
	r := newSSMRig(t)
	r.waive = []string{"parameter_access"}
	r.ssm.denyProbe = true
	s, err := NewSSMSecretsStore(r.s3, SSMSecretsConfig{
		Client: r.ssm, KMS: r.kms, AllowInsecure: r.waive, KMSKeyID: testKMSKey,
		Path: "/choudoufu/prod/secrets", RecordPrefix: r.prefix, Now: r.clock.Now,
		Sleep: func(time.Duration) {},
	})
	if err != nil {
		t.Fatal(err)
	}

	stderr := captureStderr(t, func() {
		seedPassword(t, NewRecordEnvelopeStore(s, r.prefix), "tls_private_key.big", strings.Repeat("k", 5000), nil)
	})
	if stderr != "" {
		t.Errorf("the store wrote to standard error: %q", stderr)
	}
	diags := SSMSecretsDiagnostics(s)
	got := map[string]string{}
	for _, d := range diags {
		if d.Severity() != tfdiags.Warning {
			t.Errorf("%q is %v, want a warning", d.Description().Summary, d.Severity())
		}
		got[d.Description().Summary] = d.Description().Detail
	}
	if !strings.Contains(got[SummarySecretsAdvancedTier], "$0.05 per parameter-month") || !strings.Contains(got[SummarySecretsAdvancedTier], "tls_private_key.big") {
		t.Errorf("advanced-tier warning = %q", got[SummarySecretsAdvancedTier])
	}
	if !strings.Contains(got[SummarySecretsContractWaived], "parameter_access") {
		t.Errorf("waiver warning = %q", got[SummarySecretsContractWaived])
	}
	if len(diags) != 2 {
		t.Errorf("%d diagnostics, want 2: %v", len(diags), got)
	}
	if again := s.TakeDiagnostics(); len(again) != 0 {
		t.Errorf("taken twice: %d diagnostics the second time", len(again))
	}
}

func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	rd, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = old }()
	f()
	w.Close()
	b, _ := io.ReadAll(rd)
	return string(b)
}

// TestListOrphansDeletesNothing is follow-up 2's dry run: the same split
// SweepOrphans makes, and every parameter still there.
func TestListOrphansDeletesNothing(t *testing.T) {
	ctx := context.Background()
	r := newSSMRig(t)
	s := r.open()
	seedPassword(t, NewRecordEnvelopeStore(s, r.prefix), "random_password.db", "kept", nil)
	// An orphan: a parameter under the namespace nothing references.
	orphan := s.Namespace() + "/lost-race"
	r.ssm.params[orphan] = &fakeParam{value: "v", modified: r.clock.Now()}
	r.clock.Add(DefaultSSMSweepGrace + time.Hour)
	young := s.Namespace() + "/in-flight"
	r.ssm.params[young] = &fakeParam{value: "v", modified: r.clock.Now()}

	before := r.ssm.names()
	res, err := r.open().ListOrphans(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(res.Orphans) != fmt.Sprint([]string{orphan}) || fmt.Sprint(res.Young) != fmt.Sprint([]string{young}) || res.Referenced != 1 || len(res.Deleted) != 0 {
		t.Errorf("ListOrphans = %+v", res)
	}
	if fmt.Sprint(r.ssm.names()) != fmt.Sprint(before) {
		t.Errorf("a dry run changed SSM: %v -> %v", before, r.ssm.names())
	}
	res, err = r.open().SweepOrphans(ctx)
	if err != nil || fmt.Sprint(res.Deleted) != fmt.Sprint([]string{orphan}) || !r.ssm.has(young) {
		t.Errorf("SweepOrphans = %+v, %v", res, err)
	}
	if d := r.danglingRefs(); len(d) != 0 {
		t.Errorf("the sweep deleted a referenced parameter: %v", d)
	}
}
