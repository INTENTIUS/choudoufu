// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestIdentityValuesClaimed is INTENTIUS/choudoufu#2145 item 4's half of
// GitHub issue #404: an undeclared record imported by its identity values
// alone, with no import ID, is superseded when a declared instance of the
// same type materialized in this plan holds the same object, the way an
// import ID match already is. Without it the orphan would plan a destroy
// of an object the declared instance keeps.
func TestIdentityValuesClaimed(t *testing.T) {
	addr := func(s string) addrs.AbsResourceInstance {
		a, diags := addrs.ParseAbsResourceInstanceStr(s)
		if diags.HasErrors() {
			t.Fatalf("parsing %s: %s", s, diags.Err())
		}
		return a
	}
	declared := addr("google_scc_v2_folder_mute_config.new")
	b := &builder{
		live: map[string]cty.Value{declared.String(): cty.ObjectVal(map[string]cty.Value{
			"folder": cty.StringVal("123"), "mute_config_id": cty.StringVal("quiet"), "location": cty.StringVal("global"), "description": cty.StringVal("x"),
		})},
		materialized: []addrs.AbsResourceInstance{declared},
	}
	orphan := identity.Resolution{
		Addr:           addr("google_scc_v2_folder_mute_config.old"),
		Undeclared:     true,
		IdentityValues: map[string]string{"folder": "123", "mute_config_id": "quiet", "location": "global"},
	}
	if !b.identityValuesClaimed(orphan) {
		t.Error("an orphan holding the declared instance's identity values was not superseded")
	}
	other := orphan
	other.IdentityValues = map[string]string{"folder": "123", "mute_config_id": "loud", "location": "global"}
	if b.identityValuesClaimed(other) {
		t.Error("an orphan naming a different object was superseded")
	}
	withID := orphan
	withID.ImportID = "folders/123/locations/global/muteConfigs/quiet"
	if b.identityValuesClaimed(withID) {
		t.Error("an orphan with an import ID was checked by values; the import ID check owns it")
	}
	otherType := orphan
	otherType.Addr = addr("google_scc_v2_organization_mute_config.old")
	if b.identityValuesClaimed(otherType) {
		t.Error("an orphan of another type was superseded")
	}
}
