//go:build !unix

package e2e

import "os/exec"

func joinTestProcessGroup(cmd *exec.Cmd) {}
