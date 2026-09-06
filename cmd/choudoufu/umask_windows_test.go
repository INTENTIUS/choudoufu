// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

//go:build windows

package main

// currentUmask always returns 0 on Windows: there is no POSIX umask there,
// and os.Mkdir's mode argument is ignored (see TestMkConfigDir_new, which
// hardcodes its own expectation for this platform).
func currentUmask() int {
	return 0
}
