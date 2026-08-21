// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os/exec"
)

// runBash runs a script body under bash and returns combined output.
func runBash(script string) ([]byte, error) {
	cmd := exec.Command("bash", "-c", script)
	return cmd.CombinedOutput()
}
