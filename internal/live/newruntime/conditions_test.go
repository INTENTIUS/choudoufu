// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package newruntime

import (
	"testing"

	"github.com/intentius/choudoufu/internal/engine/forkport"
	"github.com/intentius/choudoufu/internal/engine/forkport/conditions"
	"github.com/intentius/choudoufu/internal/states"
)

// choudoufu fork port (#2379): remove when the variable-validation row
// closes.

// The conditions fork port lets validation blocks through the
// variable-validation preflight while it is on, and either switch (all
// ports off, or this one) refuses them again.
func TestPreflightConditionsPortSwitch(t *testing.T) {
	r := row(t, "variable-validation")
	if r.ForkPort != "internal/engine/forkport/conditions" || r.Switch != forkport.Switch(conditions.Name)+"=1" {
		t.Fatalf("the variable-validation row names port %q and switch %q, not the package and its switch", r.ForkPort, r.Switch)
	}
	src := preflightFixtures["variable-validation"]
	unset := func(t *testing.T) {
		t.Setenv(forkport.AllOff, "")
		t.Setenv(forkport.Switch(conditions.Name), "")
	}

	t.Run("on", func(t *testing.T) {
		unset(t)
		if diags := Preflight(loadConfig(t, src), states.NewState(), false); diags.HasErrors() {
			t.Fatalf("the port is on and a validation block was refused:\n%s", diags.ErrWithWarnings())
		}
	})
	for _, env := range []string{forkport.AllOff, forkport.Switch(conditions.Name)} {
		t.Run(env, func(t *testing.T) {
			unset(t)
			t.Setenv(env, "1")
			if diags := Preflight(loadConfig(t, src), states.NewState(), false); !refusedFor(diags, r) {
				t.Fatalf("with %s=1, a validation block was not refused by the variable-validation row:\n%s", env, diags.ErrWithWarnings())
			}
		})
	}
}
