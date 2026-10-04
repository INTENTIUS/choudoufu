// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

//go:build !windows

package command

import (
	"fmt"
	"os"
	"syscall"

	"github.com/intentius/choudoufu/internal/live/kubesweep"
)

// liveMvInterruptEnv names the object a live-mv run kills itself on, as
// NAMESPACE/NAME (NAME alone for a cluster-scoped object), the instant the
// marker merge patch for it has landed and before the request handing the
// markers' ownership to the block's field manager (GitHub issue #1883).
const liveMvInterruptEnv = "TOFU_E2E_LIVE_MV_INTERRUPT"

// installLiveMvInterrupt arms kubesweep.AfterMarkerPatch from
// TOFU_E2E_LIVE_MV_INTERRUPT. It is called only from an e2eTestingFeatures
// build (cmd/choudoufu/testing.go), the same gate
// TOFU_E2E_APPLY_RESOURCE_INTERRUPT sits behind; an ordinary build never
// reaches it, and with the variable unset it installs nothing.
//
// The kill is SIGKILL, delivered to this process by itself, synchronously
// inside PatchMarkers: no deferred function, no signal handler and no
// further request runs after it, which is what a run killed between the
// two requests leaves (GitHub issue #1858). The line on stderr is written
// first so the caller can tell the window was reached from a kill that
// landed anywhere else.
//
// reference-k8s-platform-app's day2_crash runs a cross-estate move of a
// kubernetes_manifest object under this, then reruns it with an ordinary
// build and requires the hand-off finished.
func installLiveMvInterrupt() {
	target := os.Getenv(liveMvInterruptEnv)
	if target == "" {
		return
	}
	kubesweep.AfterMarkerPatch = func(ref kubesweep.ObjectRef) {
		if kubesweep.NaturalKey(ref.Namespace, ref.Name) != target {
			return
		}
		fmt.Fprintf(os.Stderr, "%s: killing live-mv after the marker patch on %s %s and before the ownership hand-off\n", liveMvInterruptEnv, ref.Kind, target)
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		select {} // parked until the kernel ends the process; the hand-off is never sent
	}
}
