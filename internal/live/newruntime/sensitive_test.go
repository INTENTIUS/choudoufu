// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package newruntime

import (
	"testing"

	"github.com/intentius/choudoufu/internal/engine/forkport"
	"github.com/intentius/choudoufu/internal/engine/forkport/sensitive"
	"github.com/intentius/choudoufu/internal/states"
)

// choudoufu fork port (#2410): remove when upstream #4360 lands.

// The sensitive fork port lets sensitive variables, sensitive outputs and
// the sensitive() function through the sensitive preflight while it is on,
// and either switch (all ports off, or this one) refuses them again.
func TestPreflightSensitivePortSwitch(t *testing.T) {
	r := row(t, "sensitive")
	if r.ForkPort != "internal/engine/forkport/sensitive" || r.Switch != forkport.Switch(sensitive.Name)+"=1" {
		t.Fatalf("the sensitive row names port %q and switch %q, not the package and its switch", r.ForkPort, r.Switch)
	}
	unset := func(t *testing.T) {
		t.Setenv(forkport.AllOff, "")
		t.Setenv(forkport.Switch(sensitive.Name), "")
	}
	for name, src := range map[string]string{
		"sensitive variable": preflightFixtures["sensitive"],
		"sensitive()":        "locals {\n  x = sensitive(\"a\")\n}\n",
		"sensitive output":   "output \"o\" {\n  value     = \"a\"\n  sensitive = true\n}\n",
	} {
		t.Run(name+"/on", func(t *testing.T) {
			unset(t)
			if diags := Preflight(loadConfig(t, src), states.NewState(), false); diags.HasErrors() {
				t.Fatalf("the port is on and %s was refused:\n%s", name, diags.ErrWithWarnings())
			}
		})
		for _, env := range []string{forkport.AllOff, forkport.Switch(sensitive.Name)} {
			t.Run(name+"/"+env, func(t *testing.T) {
				unset(t)
				t.Setenv(env, "1")
				if diags := Preflight(loadConfig(t, src), states.NewState(), false); !refusedFor(diags, r) {
					t.Fatalf("with %s=1, %s was not refused by the sensitive row:\n%s", env, name, diags.ErrWithWarnings())
				}
			})
		}
	}
}
