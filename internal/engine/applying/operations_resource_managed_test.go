// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package applying

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/engine/internal/exec"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/states"
)

// This package had no test file at all until GitHub issue #353's follow-up
// pass, which is worth saying out loud rather than quietly fixing: the two
// pieces of apply behaviour tested here were both landed for #353, and an
// audit reverted each of them to its pre-fix form and ran the whole of
// internal/tofu under TOFU_X_EXPERIMENTAL_RUNTIME=1 without a single test
// failing. A fix nothing can catch regressing is a fix with a half-life.
//
// Both tests below fail against the pre-fix code. That is checked by
// mutation, not by intention, and if either is ever weakened into passing
// both ways it is worth less than the time it takes to run.

func testBlockSchema() *configschema.Block {
	return &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":       {Type: cty.String, Computed: true},
			"password": {Type: cty.String, Optional: true, Sensitive: true},
			"plain":    {Type: cty.String, Optional: true},
		},
	}
}

func testAppliedValue(id, password, plain string) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal(id),
		"password": cty.StringVal(password),
		"plain":    cty.StringVal(plain),
	})
}

func hasSensitiveAt(t *testing.T, v cty.Value, attr string) bool {
	t.Helper()
	if v == cty.NilVal || v.IsNull() {
		return false
	}
	unmarked, pvms := v.UnmarkDeepWithPaths()
	_ = unmarked
	want := cty.GetAttrPath(attr)
	for _, pvm := range pvms {
		if !pvm.Path.Equals(want) {
			continue
		}
		if _, ok := pvm.Marks[marks.Sensitive]; ok {
			return true
		}
	}
	return false
}

// TestMarkedAppliedValue_selfSeesSensitivity is the create-time
// provisioner's `self` value, which comes back off the plugin wire where
// marks cannot travel.
//
// [execOperations.runProvisioner] unmarks the provisioner configuration it
// builds and hands the marks to the ProvisionOutput hook so a sensitive
// value is not echoed into the log. It has nothing to hand over when the
// value it built `self` from was never marked, so a provisioner whose
// command interpolates self.password would print the password. That is the
// FIXME this function replaced.
//
// The mutation this is written against: `return applied`, which is what the
// function did before issue #353's pass. Every subtest below fails on it
// except the last, which is the control that says the function is not just
// marking everything it sees.
func TestMarkedAppliedValue_selfSeesSensitivity(t *testing.T) {
	schema := testBlockSchema()

	t.Run("the schema's own Sensitive flag reaches self", func(t *testing.T) {
		applied := testAppliedValue("i-1", "hunter2", "ok")
		got := markedAppliedValue(applied, cty.NilVal, schema)
		if !hasSensitiveAt(t, got, "password") {
			t.Errorf("self.password came back unmarked: %#v\n"+
				"A provisioner interpolating it would echo the value into the log, because runProvisioner has no mark to hand the ProvisionOutput hook.", got)
		}
		if hasSensitiveAt(t, got, "plain") {
			t.Errorf("a non-sensitive attribute was marked sensitive: %#v", got)
		}
	})

	t.Run("a mark the PLANNED value carried reaches self", func(t *testing.T) {
		// Where a sensitive input variable or a sensitive upstream
		// attribute leaves its mark. The schema knows nothing about it,
		// which is why the plan is the second source and not an
		// alternative to the first.
		planned := cty.ObjectVal(map[string]cty.Value{
			"id":       cty.StringVal("i-1"),
			"password": cty.StringVal("hunter2"),
			"plain":    cty.StringVal("ok").Mark(marks.Sensitive),
		})
		got := markedAppliedValue(testAppliedValue("i-1", "hunter2", "ok"), planned, schema)
		if !hasSensitiveAt(t, got, "plain") {
			t.Errorf("a mark the planned value carried was dropped: %#v\n"+
				"The schema does not know a value came from a sensitive variable, so nothing else can put this mark back.", got)
		}
		if !hasSensitiveAt(t, got, "password") {
			t.Errorf("composing the two sources lost the schema's own mark: %#v", got)
		}
	})

	t.Run("a planned mark at a path the applied object lacks is dropped", func(t *testing.T) {
		// A create can legitimately return an object shaped differently
		// from the plan. Marking a path that is not there would fail the
		// whole apply over a log-redaction detail.
		planned := cty.ObjectVal(map[string]cty.Value{
			"id":    cty.StringVal("i-1"),
			"gone":  cty.StringVal("x").Mark(marks.Sensitive),
			"plain": cty.StringVal("ok"),
		})
		got := markedAppliedValue(testAppliedValue("i-1", "hunter2", "ok"), planned, schema)
		if got.IsNull() {
			t.Fatal("the value was destroyed rather than marked")
		}
		if !hasSensitiveAt(t, got, "password") {
			t.Errorf("the schema's mark was lost while handling an absent planned path: %#v", got)
		}
	})

	t.Run("a null or absent object is returned untouched", func(t *testing.T) {
		// The control. Without it a function that marked unconditionally
		// would pass every case above.
		if got := markedAppliedValue(cty.NilVal, cty.NilVal, schema); got != cty.NilVal {
			t.Errorf("markedAppliedValue(NilVal) = %#v, want NilVal", got)
		}
		null := cty.NullVal(schema.ImpliedType())
		if got := markedAppliedValue(null, cty.NilVal, schema); !got.RawEquals(null) {
			t.Errorf("markedAppliedValue(null) = %#v, want the null back unchanged", got)
		}
		plainSchema := &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id": {Type: cty.String, Computed: true},
			},
		}
		plain := cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("i-1")})
		if got := markedAppliedValue(plain, cty.NilVal, plainSchema); got.ContainsMarked() {
			t.Errorf("an object with nothing sensitive in it came back marked: %#v", got)
		}
	})
}

// TestAppliedObjectStatus_taintsOnlyACreate is parity with internal/tofu's
// maybeTainted, which taints on an error during a CREATE and leaves every
// other action alone.
//
// It matters more here than the same rule matters upstream. A tainted
// object drives internal/live/projection's issue #353 taint record, so
// tainting a failed UPDATE would persist a record saying a create-time
// provisioner failed when none did, and every plan after that would propose
// destroying and re-creating a live resource whose update merely needed
// retrying.
//
// The mutation this is written against: taint whenever diags.HasErrors(),
// regardless of the prior state, which is what this code did before.
func TestAppliedObjectStatus_taintsOnlyACreate(t *testing.T) {
	prior := testAppliedValue("i-1", "hunter2", "ok")
	null := cty.NullVal(testBlockSchema().ImpliedType())

	for _, tc := range []struct {
		name   string
		prior  cty.Value
		failed bool
		want   states.ObjectStatus
	}{
		{"a create that failed is tainted", null, true, states.ObjectTainted},
		{"a create with no prior value at all is tainted", cty.NilVal, true, states.ObjectTainted},
		{"a create that succeeded is ready", null, false, states.ObjectReady},
		{"an UPDATE that failed is left alone", prior, true, states.ObjectReady},
		{"an update that succeeded is ready", prior, false, states.ObjectReady},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := &exec.ManagedResourceObjectFinalPlan{PriorStateVal: tc.prior}
			if got := appliedObjectStatus(plan, tc.failed); got != tc.want {
				t.Errorf("appliedObjectStatus = %s, want %s.\n"+
					"internal/tofu's maybeTainted is the answer this has to match: an error during a create leaves the object undefined, and an error during an update usually leaves the remote object untouched.",
					got, tc.want)
			}
		})
	}
}
