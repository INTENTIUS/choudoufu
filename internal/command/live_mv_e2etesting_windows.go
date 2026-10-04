// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

//go:build windows

package command

// installLiveMvInterrupt is a no-op on Windows, which has no SIGKILL to
// deliver to itself; see live_mv_e2etesting.go.
func installLiveMvInterrupt() {}
