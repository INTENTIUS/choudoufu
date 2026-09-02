// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestFindRefusesARecordLocatedRename is GitHub issue #270's consequence for
// this package, and the reason it is worth a test of its own is that the
// hole it closes was opened by that issue rather than found in it.
//
// Before #270 a markerless type never reached here: identity resolution
// refused it, so live-mv stopped before this function ran. Now it resolves,
// and both of find's paths end in a marker rewrite that a record-located
// resource has nowhere to receive. What actually says which object it owns
// is a key in the estate's record store, keyed by the OLD address, and
// moving that key is an operation this package does not have.
//
// The provider is deliberately NIL. Everything below the guard needs one -
// listclient.ListSchemas is the very next call - so a guard that ran late,
// or not at all, panics here rather than passing quietly. That is the whole
// design of this test: it proves the refusal comes BEFORE the provider is
// touched, which a test with a working stub provider could not distinguish
// from a refusal raised three calls later for a different reason.
func TestFindRefusesARecordLocatedRename(t *testing.T) {
	old := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_eip_association", Name: "bastion"}.
		Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	newAddr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_eip_association", Name: "jump"}.
		Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)

	m := &mover{
		req: Request{
			Estate:      "test-estate",
			Old:         old,
			New:         newAddr,
			Resolutions: []identity.Resolution{{Addr: old, Class: identity.ClassRecordLocated}},
		},
		res: &Result{
			Old:      old,
			New:      newAddr,
			TypeName: old.Resource.Resource.Type,
			Anchor:   old,
		},
		// provider intentionally nil: see the doc comment.
	}

	obj, diags := m.find(t.Context())
	if obj != nil {
		t.Errorf("find returned an object for a record-located resource; nothing may be rewritten")
	}
	if !diags.HasErrors() {
		t.Fatal("find accepted a record-located rename. Renaming without moving the store key leaves the record naming the old address, the next plan reads the new address unbound, and it proposes creating a second object.")
	}
	var found bool
	for _, d := range diags {
		if d.Description().Summary == SummaryLocatedRenameUnsupported {
			found = true
			detail := d.Description().Detail
			for _, want := range []string{"record store", old.String(), newAddr.String()} {
				if !strings.Contains(detail, want) {
					t.Errorf("the refusal does not mention %q, so an operator cannot act on it:\n%s", want, detail)
				}
			}
		}
	}
	if !found {
		t.Errorf("no %q diagnostic. The run stopping is not enough: without this one, the operator is told the projection has an internal inconsistency, which is false - their configuration does declare a record_store and live-mv simply never opened it.", SummaryLocatedRenameUnsupported)
	}
}
