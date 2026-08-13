// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package flocitest

import (
	"strings"
	"testing"

	flocicap "github.com/intentius/choudoufu/live"
)

// imageDigest extracts the bare "sha256:<hex>" digest out of image() -
// defaultImage unless FLOCI_IMAGE overrides it. ok is false when the ref in
// play is not pinned by digest at all (a floating tag, or an override with
// no digest suffix): a floating tag has no fixed entry in
// live/floci-capabilities.json to look up, so every *CapabilityGate call
// below becomes a no-op in that case rather than failing a test over a
// manifest gap that is not this run's image to have.
func imageDigest() (string, bool) {
	ref := image()
	i := strings.Index(ref, "@sha256:")
	if i < 0 {
		return "", false
	}
	return ref[i+1:], true
}

// CapabilityGate skips t, loudly and citing live/floci-capabilities.json,
// when tfType is recorded unimplemented or broken against the exact floci
// image digest this run uses. It does nothing - the caller's test runs
// normally - when the manifest has no entry for tfType (an unrecorded gap;
// let the test run and, if floci genuinely cannot do this, fail rather than
// being silently waved through) or records it implemented or partial.
//
// Call this before driving floci through tfType's ordinary create/read
// path. For a type that can work on that path while still being invisible
// to one specific discovery mechanism, use that mechanism's own wrapper
// below instead - TaggingSweepCapabilityGate or CloudControlListCapabilityGate
// - so a real gap in one mechanism is never masked by, or confused with, the
// type working fine everywhere else.
func CapabilityGate(t *testing.T, tfType string) {
	t.Helper()
	capabilityGate(t, tfType, "")
}

// TaggingSweepCapabilityGate is [CapabilityGate] scoped to the Resource
// Groups Tagging API sweep discovery mechanism (internal/live/discovery's
// SourceTagging path).
func TaggingSweepCapabilityGate(t *testing.T, tfType string) {
	t.Helper()
	capabilityGate(t, tfType, "tagging-sweep")
}

// CloudControlListCapabilityGate is [CapabilityGate] scoped to Cloud
// Control's ListResources discovery mechanism (internal/live/discovery's
// SourceCloudControl path).
func CloudControlListCapabilityGate(t *testing.T, tfType string) {
	t.Helper()
	capabilityGate(t, tfType, "cloudcontrol-list")
}

func capabilityGate(t *testing.T, tfType, mechanism string) {
	t.Helper()

	digest, ok := imageDigest()
	if !ok {
		return
	}
	entry, ok := flocicap.FlociTypeCapability(digest, tfType, mechanism)
	if !ok {
		return
	}
	switch entry.Status {
	case flocicap.FlociUnimplemented, flocicap.FlociBroken:
		t.Skipf("skipped: %s not implemented by floci image %s per capability manifest (%s): %s",
			tfType, digest, entry.Source, entry.Evidence)
	}
}

// ServiceCapabilityGate is [CapabilityGate]'s service-level twin: it skips
// t, citing live/floci-capabilities.json, when service (a botocore/AWS CLI
// service id, e.g. "networkmanager") is recorded unimplemented against the
// exact floci image digest this run uses. Same no-op behavior as
// CapabilityGate when the manifest has no matching entry or records the
// service implemented.
func ServiceCapabilityGate(t *testing.T, service string) {
	t.Helper()

	digest, ok := imageDigest()
	if !ok {
		return
	}
	entry, ok := flocicap.FlociServiceCapability(digest, service)
	if !ok {
		return
	}
	if entry.Status == flocicap.FlociUnimplemented {
		t.Skipf("skipped: service %s not implemented by floci image %s per capability manifest (%s): %s",
			service, digest, entry.Source, entry.Evidence)
	}
}
