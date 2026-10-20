// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package gcp hands the GCP template in this directory to Go, for the same
// reason live/kubernetes is a package: an embed directive can only name
// files under its own package's directory, and this package imports
// nothing, so any caller can take it without an import cycle.
//
// The template is the project-scoped grant of one estate (GitHub issue
// #1121's fence ruling). live/gcp_gate_test.go reads it through here and
// holds it to the copy live/MARKERS.md inlines.
package gcp

import _ "embed"

//go:embed estate-grant.sh
var estateGrant []byte

// EstateGrant is estate-grant.sh as shipped: the gcloud lines that enable
// the Cloud Asset API in one estate's project and bind its principal there.
//
// The copy is the caller's; the embedded bytes are not handed out, because a
// caller that wrote through them would change every later reader's answer.
func EstateGrant() []byte {
	return append([]byte(nil), estateGrant...)
}
