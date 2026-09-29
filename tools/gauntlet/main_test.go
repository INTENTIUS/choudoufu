// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"testing"
)

// callerEnvKnobs are the environment variables this package's production
// code reads from its own process (flociPortBase reads FLOCI_PORT; the
// live-cert runner reads the other three). A test binary inherits them from
// whoever ran `go test`, so a worker with FLOCI_PORT=4780 exported in its
// shell turned TestRunEstatesParallelAssignsDistinctPortsPerSlot red, and a
// malformed value turned eighteen RunEstates tests red (#1511). The gate's
// verdict must depend on the tree only, so the binary starts with every one
// of them unset. A test that wants one sets it with t.Setenv, or passes
// FLOCI_PORT through RunOptions.Env, as the #1040 guards already do.
var callerEnvKnobs = []string{
	"FLOCI_PORT",
	"LIVECERT_SCRIPT_OVERRIDE",
	LiveCertSignalGraceEnv,
	LiveCertHeartbeatEnv,
}

func TestMain(m *testing.M) {
	for _, key := range callerEnvKnobs {
		os.Unsetenv(key)
	}
	os.Exit(m.Run())
}
