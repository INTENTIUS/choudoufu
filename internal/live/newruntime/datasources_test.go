// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package newruntime

import (
	"testing"

	"github.com/intentius/choudoufu/internal/engine/forkport"
	"github.com/intentius/choudoufu/internal/engine/forkport/datasources"
	"github.com/intentius/choudoufu/internal/engine/forkport/inrunestates"
	"github.com/intentius/choudoufu/internal/states"
)

// choudoufu fork port (#2414): remove when upstream #4058 and #4740 land.

// The datasources fork port lets data blocks, a check block's scoped one
// included, through the data-sources preflight while it is on, and either
// switch (all ports off, or this one) refuses them again. A
// terraform_estate_outputs block stays the in-run-estates port's.
func TestPreflightDatasourcesPortSwitch(t *testing.T) {
	r := row(t, "data-sources")
	if r.ForkPort != "internal/engine/forkport/datasources" || r.Switch != forkport.Switch(datasources.Name)+"=1" {
		t.Fatalf("the data-sources row names port %q and switch %q, not the package and its switch", r.ForkPort, r.Switch)
	}
	srcs := map[string]string{
		"data block": preflightFixtures["data-sources"],
		"check-scoped": `
check "c" {
  data "test_object" "d" {}
  assert {
    condition     = data.test_object.d.id != ""
    error_message = "x"
  }
}
`,
	}
	unset := func(t *testing.T) {
		t.Setenv(forkport.AllOff, "")
		t.Setenv(forkport.Switch(datasources.Name), "")
		t.Setenv(forkport.Switch(inrunestates.Name), "")
	}
	for name, src := range srcs {
		t.Run(name, func(t *testing.T) {
			t.Run("on", func(t *testing.T) {
				unset(t)
				if diags := Preflight(loadConfig(t, src), states.NewState(), false); diags.HasErrors() {
					t.Fatalf("the port is on and the %s was refused:\n%s", name, diags.ErrWithWarnings())
				}
			})
			for _, env := range []string{forkport.AllOff, forkport.Switch(datasources.Name)} {
				t.Run(env, func(t *testing.T) {
					unset(t)
					t.Setenv(env, "1")
					if diags := Preflight(loadConfig(t, src), states.NewState(), false); !refusedFor(diags, r) {
						t.Fatalf("with %s=1, the %s was not refused by the data-sources row:\n%s", env, name, diags.ErrWithWarnings())
					}
				})
			}
		})
	}
	t.Run("estate outputs stay the in-run-estates port's", func(t *testing.T) {
		unset(t)
		t.Setenv(forkport.Switch(inrunestates.Name), "1")
		if diags := Preflight(loadConfig(t, estateOutputsFixture), states.NewState(), false); !refusedFor(diags, r) {
			t.Fatalf("with the in-run-estates port off, terraform_estate_outputs passed on the datasources port:\n%s", diags.ErrWithWarnings())
		}
	})
}
