// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// TestRecordIdentityMatchesNumberComponent pins the read half of issue
// #742 (review finding B1 on that PR): a record stores number identity
// components as their plain decimal digits, and a live identity object
// carries them typed by the provider's identity schema - a cty.Number.
// The matcher must compare through the same canonical rendering the
// writer used, or every record written for the ECS-revision shape is
// unmatchable by the superseded-claimant, tombstone and deposed
// machinery it exists to feed.
func TestRecordIdentityMatchesNumberComponent(t *testing.T) {
	live := cty.ObjectVal(map[string]cty.Value{
		"family":   cty.StringVal("probe"),
		"revision": cty.NumberIntVal(7),
	})
	if !recordIdentityMatches("", map[string]string{"family": "probe", "revision": "7"}, "", live) {
		t.Error("a number identity attribute does not match its record's decimal rendering - #742's records are unmatchable again")
	}
	if recordIdentityMatches("", map[string]string{"family": "probe", "revision": "8"}, "", live) {
		t.Error("a wrong revision matched")
	}
	frac := cty.ObjectVal(map[string]cty.Value{
		"family":   cty.StringVal("probe"),
		"revision": cty.NumberFloatVal(7.5),
	})
	if recordIdentityMatches("", map[string]string{"family": "probe", "revision": "7"}, "", frac) {
		t.Error("a fractional live value matched an integral record")
	}
}
