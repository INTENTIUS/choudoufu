// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/engine/forkport"
	"github.com/intentius/choudoufu/internal/engine/forkport/conditions"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// choudoufu fork port (#2379): remove when the variable-validation row
// closes. TestContext2Plan_variableCustomValidationsSensitive's fixture on
// the new runtime: a child module's validation reads a value its caller
// marks sensitive. The old runtime checks it like any other value; with the
// conditions port on the new runtime does too, and with the port's switch
// set it refuses the condition as derived from a sensitive value.
//
// It plans through newEnginePlan directly, past the gap table's preflight,
// because the preflight's "sensitive" row refuses the root module's
// sensitive = true before the engine runs.
func TestNewRuntimeConditionsPortSensitiveValidation(t *testing.T) {
	m := testModule(t, "validate-variable-custom-validations-child-sensitive")
	plan := func(t *testing.T, value string) []string {
		t.Helper()
		ctx := testContext2(t, &ContextOpts{
			Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
				addrs.NewDefaultProvider("test"): testProviderFuncFixed(testProvider("test")),
			}, nil),
		})
		_, diags := ctx.newEnginePlan(context.Background(), m, states.NewState(), &PlanOpts{
			Mode:         plans.NormalMode,
			SetVariables: InputValues{"test": &InputValue{Value: cty.StringVal(value), SourceType: ValueFromCLIArg}},
		})
		var errs []string
		for _, d := range diags {
			if d.Severity() == tfdiags.Error {
				errs = append(errs, d.Description().Summary+": "+d.Description().Detail)
			}
		}
		return errs
	}
	unset := func(t *testing.T) {
		t.Setenv(forkport.AllOff, "")
		t.Setenv(forkport.Switch(conditions.Name), "")
	}

	t.Run("on", func(t *testing.T) {
		unset(t)
		if errs := plan(t, "fine"); len(errs) != 0 {
			t.Fatalf("a valid sensitive value was refused:\n%s", strings.Join(errs, "\n"))
		}
		errs := plan(t, "nope")
		if len(errs) != 1 || !strings.Contains(errs[0], `Value must not be "nope".`) {
			t.Fatalf("want the validation's own error, got:\n%s", strings.Join(errs, "\n"))
		}
	})
	t.Run(forkport.Switch(conditions.Name), func(t *testing.T) {
		unset(t)
		t.Setenv(forkport.Switch(conditions.Name), "1")
		errs := plan(t, "fine")
		if len(errs) != 1 || !strings.Contains(errs[0], "must not be derived from a sensitive value") {
			t.Fatalf("with the port off, want the condition refused as sensitive, got:\n%s", strings.Join(errs, "\n"))
		}
	})
}
