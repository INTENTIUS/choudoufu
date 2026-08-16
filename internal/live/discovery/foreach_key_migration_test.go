// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"testing"
)

// TestDiscoverLegacyForEachKeyStillBinds is issue #178's migration proof: a
// live resource stamped before "." and ":" were admitted into a for_each
// key still binds to its declared instance under the new escaping, for the
// one key shape both grammars admit but escape differently: a key
// containing "@".
//
// aws_subnet.this["at@sign"] escapes, under the current grammar, to
// "aws_subnet.this:at@@sign" ([EscapeKey] doubles "@"). A marker a run
// wrote before issue #178 carries the un-doubled form,
// "aws_subnet.this:at@sign" ([LegacyEscapeAddress], and the pre-#178
// [EscapeAddress] before it - "@" was never escaped at all). If discovery
// only ever computed the current escaping and compared it byte-for-byte
// against what is on the resource, this live resource would read as an
// orphan today: unowned, with a plan proposing to destroy it and recreate
// aws_subnet.this["at@sign"] in its place - a silent ownership break on
// real infrastructure. It must still bind.
func TestDiscoverLegacyForEachKeyStillBinds(t *testing.T) {
	cfg := loadConfig(t, "testdata/foreach-legacy-key")
	req := Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolveOrFail(t, cfg).All(),
	}

	legacy := LegacyEscapeAddress(`aws_subnet.this["at@sign"]`)
	current := EscapeAddress(`aws_subnet.this["at@sign"]`)
	if legacy == current {
		t.Fatalf("test premise is wrong: legacy and current escaping agree for this key (%q); this test proves nothing", legacy)
	}
	if legacy != `aws_subnet.this:at@sign` {
		t.Fatalf("test premise is wrong: legacy escaping is %q, want aws_subnet.this:at@sign", legacy)
	}
	if current != `aws_subnet.this:at@@sign` {
		t.Fatalf("test premise is wrong: current escaping is %q, want aws_subnet.this:at@@sign", current)
	}

	cloud := newFakeCloud()
	// The live resource carries exactly the marker a pre-issue-#178 run
	// would have stamped for this key - the legacy escaping, not the
	// current one.
	cloud.own("aws_subnet", "subnet-legacy", legacy)
	req.Provider = cloud

	res, diags := Discover(context.Background(), req)
	assertNoErrors(t, diags)

	b, ok := res.BindingFor(mustAddr(t, `aws_subnet.this["at@sign"]`))
	if !ok {
		t.Fatalf("a resource carrying the pre-issue-#178 marker %q did not bind to its declared instance:\n%s", legacy, res)
	}
	if b.ImportID != "subnet-legacy" {
		t.Errorf("bound to import ID %q, want subnet-legacy", b.ImportID)
	}
	if len(res.Orphans) != 0 {
		t.Errorf("the legacy-marked resource was also filed as an orphan: %v", res.Orphans)
	}
	if len(res.Unbound) != 0 {
		t.Errorf("the declared instance was reported unbound even though a live resource claims it: %v", res.Unbound)
	}
}

// TestDiscoverLegacyForEachKeyDoesNotCollideAcrossInstances checks the other
// direction the same alias mechanism has to get right: a SECOND instance
// whose CURRENT escaping happens to equal the first instance's legacy
// escaping must never be silently misattributed to the first instance's
// live resource. It cannot actually arise for this fixture (no other
// declared instance shares a legacy alias), so this pins the narrower,
// always-true property instead: a legacy alias never overrides an entry
// already claimed by its own canonical key, by construction of the
// declaredInstances builder in discovery.go.
func TestDiscoverLegacyForEachKeyDoesNotCollideAcrossInstances(t *testing.T) {
	cfg := loadConfig(t, "testdata/foreach-legacy-key")
	req := Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolveOrFail(t, cfg).All(),
	}

	cloud := newFakeCloud()
	// A second live aws_subnet, correctly marked under the CURRENT grammar
	// for the same instance. Only one live resource may claim this address;
	// filing the same declared entry under both its canonical key and a
	// legacy alias must not create a second slot for it to collide into.
	current := EscapeAddress(`aws_subnet.this["at@sign"]`)
	cloud.own("aws_subnet", "subnet-current", current)
	req.Provider = cloud

	res, diags := Discover(context.Background(), req)
	assertNoErrors(t, diags)

	b, ok := res.BindingFor(mustAddr(t, `aws_subnet.this["at@sign"]`))
	if !ok {
		t.Fatalf("a resource carrying the current marker %q did not bind:\n%s", current, res)
	}
	if b.ImportID != "subnet-current" {
		t.Errorf("bound to import ID %q, want subnet-current", b.ImportID)
	}
	if len(res.Bindings) != 1 {
		t.Errorf("want exactly one binding, got %d:\n%s", len(res.Bindings), res)
	}
}
