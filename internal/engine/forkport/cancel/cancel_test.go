// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package cancel

import (
	"context"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/engine/forkport"
)

func TestWatch(t *testing.T) {
	watch := func(releaseFirst bool) bool {
		run, end := context.WithCancel(context.Background())
		stopped := make(chan struct{})
		release := Watch(run, func() { close(stopped) })
		if releaseFirst {
			release()
		}
		end()
		defer release()
		select {
		case <-stopped:
			return true
		case <-time.After(time.Second):
			return false
		}
	}

	t.Setenv(forkport.AllOff, "")
	t.Setenv(forkport.Switch(Name), "")
	if !watch(false) {
		t.Fatal("a stop during the run did not stop the plugins")
	}
	if watch(true) {
		t.Fatal("the run ending after release stopped the plugins")
	}
	for name, env := range map[string]string{"all ports off": forkport.AllOff, "this port off": forkport.Switch(Name)} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(forkport.AllOff, "")
			t.Setenv(forkport.Switch(Name), "")
			t.Setenv(env, "1")
			if watch(false) {
				t.Fatalf("with %s=1 a stop still stopped the plugins", env)
			}
		})
	}
}

func TestHalted(t *testing.T) {
	t.Setenv(forkport.AllOff, "")
	t.Setenv(forkport.Switch(Name), "")
	if Halted(false) != nil {
		t.Fatal("a run that was not stopped failed")
	}
	if err := Halted(true); err == nil || err.Error() != "execution halted" {
		t.Fatalf("a stopped run: %v, want execution halted", err)
	}
	t.Setenv(forkport.Switch(Name), "1")
	if Halted(true) != nil {
		t.Fatal("with the port off a stopped run failed")
	}
}
