// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package inrunestates

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

func TestSwitchTurnsThePortOff(t *testing.T) {
	r := &configs.Resource{Mode: addrs.DataResourceMode, Type: TypeName, Name: "net", Provider: addrs.NewBuiltInProvider("terraform")}
	other := &configs.Resource{Mode: addrs.DataResourceMode, Type: TypeName, Name: "net", Provider: addrs.NewDefaultProvider("terraform")}
	remote := &configs.Resource{Mode: addrs.DataResourceMode, Type: "terraform_remote_state", Name: "net", Provider: addrs.NewBuiltInProvider("terraform")}

	t.Setenv(SwitchVariable, "")
	if !Enabled() || New(nil) == nil || !Covers(r) {
		t.Fatal("with the switch unset the port must be on and cover terraform_estate_outputs")
	}
	if Covers(other) || Covers(remote) {
		t.Fatal("the port covers only the builtin provider's terraform_estate_outputs")
	}

	t.Setenv(SwitchVariable, "1")
	if Enabled() || New(nil) != nil || Covers(r) {
		t.Fatalf("with %s=1 the port must be off and cover nothing", SwitchVariable)
	}
}

func TestPlanDefersWhileTheModuleIsPending(t *testing.T) {
	t.Setenv(SwitchVariable, "")
	port := New(map[string]addrs.Module{"net": {"net"}})
	addr := addrs.Resource{Mode: addrs.DataResourceMode, Type: TypeName, Name: "net"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance.Child("app", addrs.NoKey))
	config := cty.ObjectVal(map[string]cty.Value{
		"estate": cty.StringVal("net"),
		"names":  cty.SetVal([]cty.Value{cty.StringVal("a"), cty.StringVal("b")}),
		"values": cty.NullVal(cty.DynamicPseudoType),
	})
	outputs := cty.ObjectVal(map[string]cty.Value{"a": cty.StringVal("a-id"), "b": cty.UnknownVal(cty.String)})
	for name, tc := range map[string]struct {
		pending, deferred bool
		outputs           cty.Value
	}{
		"pending":       {pending: true, deferred: true, outputs: cty.ObjectVal(map[string]cty.Value{"a": cty.StringVal("a-id"), "b": cty.StringVal("b-id")})},
		"unknown":       {deferred: true, outputs: outputs},
		"nothing to do": {outputs: cty.ObjectVal(map[string]cty.Value{"a": cty.StringVal("a-id"), "b": cty.StringVal("b-id")})},
	} {
		t.Run(name, func(t *testing.T) {
			planned, handled, diags := port.Plan(addr, config, cty.DynamicPseudoType, hostFunc(func(addrs.ModuleInstance) Module {
				return Module{Outputs: tc.outputs, Pending: tc.pending}
			}))
			if !handled || diags.HasErrors() {
				t.Fatalf("handled %t, diags %v", handled, diags.Err())
			}
			if planned.Deferred != tc.deferred {
				t.Fatalf("deferred %t, want %t", planned.Deferred, tc.deferred)
			}
			if got := planned.Value.GetAttr("values").GetAttr("a"); !got.RawEquals(cty.StringVal("a-id")) {
				t.Fatalf("a known output must keep its value, got %#v", got)
			}
		})
	}
}

type hostFunc func(addrs.ModuleInstance) Module

func (f hostFunc) InRunModule(m addrs.ModuleInstance) Module { return f(m) }
