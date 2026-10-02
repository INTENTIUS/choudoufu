// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package absent

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

func TestRemovedWithoutIdentity(t *testing.T) {
	ty := cty.Object(map[string]cty.Type{"id": cty.String})
	idTy := cty.Object(map[string]cty.Type{"name": cty.String})
	complaint := tfdiags.Diagnostics{}.Append(tfdiags.Sourceless(tfdiags.Error, FrameworkMissingIdentityAfterRead, "x"))
	other := complaint.Append(tfdiags.Sourceless(tfdiags.Error, "Failed to read namespace", "x"))
	warnOnly := tfdiags.Diagnostics{}.Append(tfdiags.Sourceless(tfdiags.Warning, FrameworkMissingIdentityAfterRead, "x"))
	for name, tc := range map[string]struct {
		prior, state cty.Value
		diags        tfdiags.Diagnostics
		want         bool
	}{
		"removed, no prior identity":   {cty.NilVal, cty.NullVal(ty), complaint, true},
		"removed, null prior identity": {cty.NullVal(idTy), cty.NullVal(ty), complaint, true},
		"prior identity carried":       {cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("a")}), cty.NullVal(ty), complaint, false},
		"live object":                  {cty.NilVal, cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("a")}), complaint, false},
		"unknown state":                {cty.NilVal, cty.UnknownVal(ty), complaint, false},
		"no state at all":              {cty.NilVal, cty.NilVal, complaint, false},
		"another error too":            {cty.NilVal, cty.NullVal(ty), other, false},
		"no error":                     {cty.NilVal, cty.NullVal(ty), warnOnly, false},
	} {
		if got := RemovedWithoutIdentity(tc.prior, tc.state, tc.diags); got != tc.want {
			t.Errorf("%s: RemovedWithoutIdentity = %v, want %v", name, got, tc.want)
		}
	}
	if got := WithoutErrors(other.Append(tfdiags.Sourceless(tfdiags.Warning, "w", "x"))); len(got) != 1 || got[0].Severity() != tfdiags.Warning {
		t.Errorf("WithoutErrors kept %d diagnostics, want the one warning", len(got))
	}
}
