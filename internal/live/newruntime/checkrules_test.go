// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package newruntime

import (
	"testing"

	"github.com/intentius/choudoufu/internal/engine/forkport"
	"github.com/intentius/choudoufu/internal/engine/forkport/checkrules"
	"github.com/intentius/choudoufu/internal/states"
)

// choudoufu fork port (#2409): remove when upstream #4390 lands.

// The checkrules fork port lets check blocks, resource preconditions and
// postconditions and output preconditions through the checks-conditions
// preflight while it is on, and either switch (all ports off, or this one)
// refuses them again.
func TestPreflightCheckrulesPortSwitch(t *testing.T) {
	r := row(t, "checks-conditions")
	if r.ForkPort != "internal/engine/forkport/checkrules" || r.Switch != forkport.Switch(checkrules.Name)+"=1" {
		t.Fatalf("the checks-conditions row names port %q and switch %q, not the package and its switch", r.ForkPort, r.Switch)
	}
	srcs := map[string]string{
		"check block":   preflightFixtures["checks-conditions"],
		"precondition":  "variable \"x\" {\n  default = \"a\"\n}\nresource \"test_object\" \"a\" {\n  lifecycle {\n    precondition {\n      condition     = var.x != \"\"\n      error_message = \"x\"\n    }\n  }\n}\n",
		"postcondition": "resource \"test_object\" \"a\" {\n  lifecycle {\n    postcondition {\n      condition     = self.id != \"\"\n      error_message = \"x\"\n    }\n  }\n}\n",
		"output":        "variable \"x\" {\n  default = \"a\"\n}\noutput \"o\" {\n  value = var.x\n  precondition {\n    condition     = var.x != \"\"\n    error_message = \"x\"\n  }\n}\n",
	}
	unset := func(t *testing.T) {
		t.Setenv(forkport.AllOff, "")
		t.Setenv(forkport.Switch(checkrules.Name), "")
	}
	for name, src := range srcs {
		t.Run(name, func(t *testing.T) {
			t.Run("on", func(t *testing.T) {
				unset(t)
				if diags := Preflight(loadConfig(t, src), states.NewState(), false); diags.HasErrors() {
					t.Fatalf("the port is on and the %s was refused:\n%s", name, diags.ErrWithWarnings())
				}
			})
			for _, env := range []string{forkport.AllOff, forkport.Switch(checkrules.Name)} {
				t.Run(env, func(t *testing.T) {
					unset(t)
					t.Setenv(env, "1")
					if diags := Preflight(loadConfig(t, src), states.NewState(), false); !refusedFor(diags, r) {
						t.Fatalf("with %s=1, the %s was not refused by the checks-conditions row:\n%s", env, name, diags.ErrWithWarnings())
					}
				})
			}
		})
	}
}
