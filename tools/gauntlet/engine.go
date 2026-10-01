// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

// EngineVersionPin is the engine's upstream base, repo-relative (#1778
// ruling 6).
const EngineVersionPin = "version/VERSION"

// LegacyEngineBase is the base every row written before LastRun recorded
// UpstreamVersion was measured on.
const LegacyEngineBase = "1.13.0-dev"

// engineVersion reads EngineVersionPin.
func engineVersion(root string) string { return "" }

// IsEngineStale reports whether r was measured on a different engine base
// than current.
func IsEngineStale(r EstateResult, current string) bool { return false }

// engineVersionProbe is overridden in tests.
var engineVersionProbe = probeEngine

// probeEngine asks the choudoufu binary a run uses for its upstream base.
func probeEngine(root string, env []string) string { return "" }

// reconcileEngineVersion folds what the binary reported with the tree's pin.
func reconcileEngineVersion(reported, tree string) string { return reported }
