// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// PinnedSpec is the accepted upstream CloudFormation Registry bundle.
//
// To move it: run generation, read AssertPinned's refusal (or, for a
// byte-only drift, its warning), confirm the delta is one you want, and
// paste the printed digest/resources/accepted block here - in its own
// commit, together with a regenerated pinned-types.json.
var PinnedSpec = SpecPin{
	Digest:    "sha256:2ab2db2351402f588b86795eb31d73290ca9910e11f13fdac9cb46c00eb8ece0",
	Resources: 1683,
	Accepted:  "2026-08-13",
}

//go:embed pinned-types.json
var pinnedTypesJSON []byte

// PinnedTypeNames is the full resource-type roster the pinned archive
// contained, committed beside the digest (pinned-types.json) so the PR that
// moves the pin shows exactly which types AWS added or removed, rather than
// one opaque hash replacing another.
var PinnedTypeNames = mustLoadPinnedTypeNames()

func mustLoadPinnedTypeNames() map[string]bool {
	var names []string
	if err := json.Unmarshal(pinnedTypesJSON, &names); err != nil {
		panic(fmt.Sprintf("registry-gen: pinned-types.json does not parse: %v", err))
	}
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}
