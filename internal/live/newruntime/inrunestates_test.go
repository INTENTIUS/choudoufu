// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package newruntime

import (
	"testing"

	"github.com/intentius/choudoufu/internal/engine/forkport/inrunestates"
	"github.com/intentius/choudoufu/internal/states"
)

// choudoufu fork port (#2370): remove when upstream #4058 lands, with the
// "in-run-estates" row.

const estateOutputsFixture = `
data "terraform_estate_outputs" "net" {
  estate = "net"
  names  = ["vpc_id"]
}
`

// The in-run-estates fork port lets terraform_estate_outputs through the
// data-sources preflight while it is on, refuses it again with the switch
// set, and never lets any other data source through.
func TestPreflightInRunEstatesPortSwitch(t *testing.T) {
	data := row(t, "data-sources")
	port := row(t, "in-run-estates")
	if port.ForkPort != "internal/engine/forkport/inrunestates" || port.Switch != inrunestates.SwitchVariable+"=1" {
		t.Fatalf("the in-run-estates row names port %q and switch %q, not the package and its switch", port.ForkPort, port.Switch)
	}

	t.Run("on", func(t *testing.T) {
		t.Setenv(inrunestates.SwitchVariable, "")
		if diags := Preflight(loadConfig(t, estateOutputsFixture), states.NewState()); diags.HasErrors() {
			t.Fatalf("the port is on and terraform_estate_outputs was refused:\n%s", diags.ErrWithWarnings())
		}
		if diags := Preflight(loadConfig(t, estateOutputsFixture+preflightFixtures["data-sources"]), states.NewState()); !refusedFor(diags, data) {
			t.Fatalf("another data source passed the preflight with the port on:\n%s", diags.ErrWithWarnings())
		} else if len(diags) != 1 {
			t.Fatalf("want only the other data source refused, got %d diagnostics:\n%s", len(diags), diags.ErrWithWarnings())
		}
	})
	t.Run("switched off", func(t *testing.T) {
		t.Setenv(inrunestates.SwitchVariable, "1")
		if diags := Preflight(loadConfig(t, estateOutputsFixture), states.NewState()); !refusedFor(diags, data) {
			t.Fatalf("with %s set, terraform_estate_outputs was not refused by the data-sources row:\n%s", port.Switch, diags.ErrWithWarnings())
		}
	})
}
