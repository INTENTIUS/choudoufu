// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import _ "embed"

// estate-boundary.yaml is the cluster admission policy this store's
// cluster contract reads to say how the policy INSTALLED on a cluster
// differs from the one shipped (GitHub issue #1448, section B4). It sits
// beside the store because an embed directive can only name files under
// its own package's directory, and the store is public (GitHub issue
// #2281): another module that imports it gets the policy with it.
// live/kubernetes/estate-boundary.yaml, the path the cluster docs tell an
// admin to `kubectl apply -f`, is a symlink to this file.
//
// Embedding the file rather than transcribing its CEL into Go is the
// point: an edit to the policy (#1449's matchConditions, say) changes what
// the contract expects the moment it is committed, with no second copy to
// re-sync.
//
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
