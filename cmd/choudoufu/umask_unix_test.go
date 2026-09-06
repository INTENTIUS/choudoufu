// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

//go:build !windows

package main

import "syscall"

// currentUmask returns the calling process's umask without permanently
// changing it. syscall.Umask sets a new umask and returns the previous
// value, so this sets it right back to what it was immediately after
// reading it.
func currentUmask() int {
	old := syscall.Umask(0)
	syscall.Umask(old)
	return old
}
