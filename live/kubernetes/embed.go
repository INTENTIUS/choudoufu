// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package kubernetes hands the cluster templates in this directory to Go.
//
// It is one file in a directory of YAML and Markdown, and it is here rather
// than beside its one caller for two reasons. An embed directive can only
// name files under its own package's directory, so this is the only place a
// directive can reach live/kubernetes/estate-boundary.yaml at all; and the
// package that could otherwise have held it, github.com/intentius/choudoufu/live,
// has test files that reach internal/live/projection, which reaches
// internal/live/staterecord, which is the caller - an import cycle Go
// refuses even when only a test closes it. This package imports nothing.
//
// internal/live/staterecord's cluster contract reads the boundary to say how
// the policy INSTALLED on a cluster differs from the one this repository
// ships (GitHub issue #1448, section B4). Embedding the file rather than
// transcribing its CEL into Go is the point: an edit to the policy - #1449's
// matchConditions, say - changes what the contract expects the moment it is
// committed, with no second copy to re-sync.
package kubernetes

import _ "embed"

//go:embed estate-boundary.yaml
var estateBoundaryYAML []byte

// EstateBoundaryYAML is estate-boundary.yaml as shipped: one multi-document
// YAML file holding the ValidatingAdmissionPolicy a cluster admin installs
// and the binding that puts it in force.
//
// The copy is the caller's; the embedded bytes are not handed out, because a
// caller that wrote through them would change every later reader's answer.
func EstateBoundaryYAML() []byte {
	return append([]byte(nil), estateBoundaryYAML...)
}
