// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// A configuration that declares its own tofu-address spells it the way HCL
// does - `module.wrapped["a"].aws_eip.app` - and the marker on the wire is
// the escaped `module.wrapped:a.aws_eip.app`. The two name one instance,
// and the conflict check used to call that a rename, which refused every
// keyed-module estate whose wrapped module declared its own address tag
// (live/e2e/estate-module-keyed, TestModuleKeyedForEachAgainstFloci, red
// on every night the floci tier ran). A value that still differs after the
// same normalization Discover applies is the rename the rule exists for.
func TestMarkerConflictAcceptsTheUnescapedSpellingOfTheSameAddress(t *testing.T) {
	addr, diags := addrs.ParseAbsResourceInstanceStr(`module.wrapped["a"].aws_eip.app`)
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	want := markers.EscapeAddress(addr.String())
	if want == addr.String() {
		t.Fatalf("the fixture address %s escapes to itself; this test needs a keyed module", addr)
	}

	declare := func(v string) map[string]cty.Value {
		return map[string]cty.Value{markers.TagAddress: cty.StringVal(v)}
	}

	if d := markerConflictDiag(addr, declare(addr.String()), markers.TagAddress, want); d.HasErrors() {
		t.Errorf("the unescaped spelling of the instance's own address was reported as a conflict: %s", d.Err())
	}
	if d := markerConflictDiag(addr, declare(want), markers.TagAddress, want); d.HasErrors() {
		t.Errorf("the escaped spelling of the instance's own address was reported as a conflict: %s", d.Err())
	}

	other := `module.wrapped["b"].aws_eip.app`
	d := markerConflictDiag(addr, declare(other), markers.TagAddress, want)
	if !d.HasErrors() {
		t.Fatalf("a tag naming another instance (%s) must still be a conflict; the normalization has swallowed the rename rule", other)
	}
	if msg := d.Err().Error(); !strings.Contains(msg, SummaryMarkerConflict) || !strings.Contains(msg, "live-mv") {
		t.Errorf("the conflict for another address lost its wording: %s", msg)
	}

	// The estate key is not an address and gets no such tolerance.
	if d := markerConflictDiag(addr, map[string]cty.Value{markers.TagEstate: cty.StringVal("other-estate")}, markers.TagEstate, "this-estate"); !d.HasErrors() {
		t.Error("a tofu-estate naming another estate must still be a conflict")
	}
}
