// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tracing

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/engine/forkport"
	"github.com/intentius/choudoufu/internal/tracing"
)

func TestSwitchTurnsThePortOff(t *testing.T) {
	spec := tracing.DetailSpec{Kind: tracing.DetailResourceInstance, Name: "Plan resource instance changes", Group: "aws_instance", Label: "aws_instance.foo"}
	run := func() int {
		recorder := tracing.NewTestTracer(t)
		tracing.SetDetailForTest(t, tracing.DetailFull, 0)
		ctx, end := Walk(context.Background(), "Graph walk", "opentofu.walk.operation", "walkPlan")
		EndInstance(StartInstance(ctx, spec, "aws_instance.foo", "aws_instance", ""), "Create", nil)
		end(nil)
		return len(recorder.Ended())
	}

	t.Setenv(forkport.AllOff, "")
	t.Setenv(forkport.Switch(Name), "")
	if n := run(); n != 2 {
		t.Fatalf("with no switch set: %d spans ended, want 2 (the walk and the instance)", n)
	}
	for name, env := range map[string]string{"all ports off": forkport.AllOff, "this port off": forkport.Switch(Name)} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(forkport.AllOff, "")
			t.Setenv(forkport.Switch(Name), "")
			t.Setenv(env, "1")
			if n := run(); n != 0 {
				t.Fatalf("with %s=1: %d spans ended, want none", env, n)
			}
		})
	}
}
