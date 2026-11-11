// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/engine/forkport/inrunestates"
)

// choudoufu fork port (#2370): remove when upstream #4058 lands. The port
// copies the default runtime's type name and refusals; a copy that drifts
// would make the two runtimes disagree about what an in-run read is.
func TestInRunEstatesPortMatchesTheDefaultRuntime(t *testing.T) {
	if inrunestates.TypeName != inRunEstateOutputsType {
		t.Fatalf("the port reads %q, the default runtime %q", inrunestates.TypeName, inRunEstateOutputsType)
	}

	config := cty.ObjectVal(map[string]cty.Value{
		"estate": cty.StringVal("net"),
		"names":  cty.SetVal([]cty.Value{cty.StringVal("vpc_id"), cty.StringVal("gone")}),
		"values": cty.NullVal(cty.DynamicPseudoType),
	})
	target := addrs.Module{"net"}
	readerAddr := mustResourceInstanceAddr("module.app.data.terraform_estate_outputs.net")
	evalCtx := &MockEvalContext{InRunEstatesValue: map[string]addrs.Module{"net": target}}
	node := &NodeAbstractResourceInstance{Addr: readerAddr}

	port := inrunestates.New(evalCtx.InRunEstatesValue)
	if port == nil {
		t.Fatalf("the port is off; unset %s to run this test", inrunestates.SwitchVariable)
	}
	wantTarget, wantInRun, wantSelf := node.inRunEstateTarget(evalCtx, config)
	gotTarget, gotInRun, gotSelf := port.Target(readerAddr, config)
	if !gotTarget.Equal(wantTarget) || gotInRun != wantInRun || gotSelf != wantSelf {
		t.Fatalf("port target (%s, %t, %t), default runtime (%s, %t, %t)", gotTarget, gotInRun, gotSelf, wantTarget, wantInRun, wantSelf)
	}

	// The default runtime refuses an output the module does not declare;
	// so must the port, in the same words.
	_, _, gotDiags := inrunestates.Value(target, false, config, cty.ObjectVal(map[string]cty.Value{"vpc_id": cty.StringVal("vpc-1")}))
	if len(gotDiags) != 1 {
		t.Fatalf("want one refusal of the undeclared output, got %d", len(gotDiags))
	}
	want := `data "terraform_estate_outputs" reads output "gone" of estate "net", which this run applies as module module.net, and that module declares no such output.`
	if got := gotDiags[0].Description().Detail; got != want {
		t.Fatalf("refusal drifted from the default runtime's:\n got: %s\nwant: %s", got, want)
	}
	_, _, selfDiags := inrunestates.Value(target, true, config, cty.NilVal)
	if len(selfDiags) != 1 || selfDiags[0].Description().Summary != "An estate cannot read its own outputs this way" {
		t.Fatalf("self-read refusal drifted: %v", selfDiags.Err())
	}
}
