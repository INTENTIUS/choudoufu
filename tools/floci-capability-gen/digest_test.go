// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

// TestResolveDigestAlreadyPinned covers the no-docker-call fast path: a ref
// already pinned by digest is split, not resolved. This is the only branch
// a sandbox with no docker daemon can exercise; the `docker inspect`
// fallback needs a real docker CLI and is left to a human running this tool
// against a real endpoint, per this tool's own doc comment.
func TestResolveDigestAlreadyPinned(t *testing.T) {
	got, err := resolveDigest("ghcr.io/lex00/floci@sha256:4753246c0260a22af1056c65993f4d73b0a907729a9580b9baba5d628b6dad34")
	if err != nil {
		t.Fatalf("resolveDigest: %v", err)
	}
	want := "sha256:4753246c0260a22af1056c65993f4d73b0a907729a9580b9baba5d628b6dad34"
	if got != want {
		t.Errorf("resolveDigest = %q, want %q", got, want)
	}
}
