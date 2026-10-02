// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package absent

import (
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// FrameworkMissingIdentityAfterRead is the error summary
// terraform-plugin-framework's fwserver attaches to a ReadResource whose
// provider code finished without error but left the identity null.
const FrameworkMissingIdentityAfterRead = "Missing Resource Identity After Read"

// RemovedWithoutIdentity reports whether a failed ReadResource is really
// the provider answering "absent": the read was sent with no prior
// identity, it came back with a null new state, and its only error is the
// framework's identity complaint.
//
// The framework (v1.16.1, fwserver/server_readresource.go) runs that check
// only after the provider's own Read returned no error, and does not exempt
// a resource the Read removed. A read that starts with a null identity -
// an import by ID through resource.ImportStatePassthroughWithIdentity,
// which sets the state's id and leaves the identity null - therefore fails
// whenever the object does not exist. A read that carries a prior identity
// never reaches this, because the framework copies it into the response.
// hashicorp/kubernetes 3.3.0's kubernetes_namespace_v1 is the founding
// case (corpus-quickpizza's greenfield stage, 2026-10-02): stock tofu fails
// an import block for a missing namespace this way, and a live plan, which
// imports to find out whether an object exists, could not plan its create.
//
// The same complaint over a non-null state is a provider returning an
// object with no identity, which is a real defect, and stays a failure; so
// does any other error alongside it.
func RemovedWithoutIdentity(priorIdentity, newState cty.Value, diags tfdiags.Diagnostics) bool {
	if priorIdentity != cty.NilVal && !priorIdentity.IsNull() {
		return false
	}
	if newState == cty.NilVal || !newState.IsKnown() || !newState.IsNull() {
		return false
	}
	saw := false
	for _, d := range diags {
		if d.Severity() != tfdiags.Error {
			continue
		}
		if d.Description().Summary != FrameworkMissingIdentityAfterRead {
			return false
		}
		saw = true
	}
	return saw
}

// WithoutErrors is diags with every error-severity diagnostic removed, for
// a caller that has decided, by [RemovedWithoutIdentity], that the errors
// describe an absence.
func WithoutErrors(diags tfdiags.Diagnostics) tfdiags.Diagnostics {
	var out tfdiags.Diagnostics
	for _, d := range diags {
		if d.Severity() != tfdiags.Error {
			out = out.Append(d)
		}
	}
	return out
}
