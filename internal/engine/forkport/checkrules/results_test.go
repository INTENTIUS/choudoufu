// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package checkrules

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/engine/forkport"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// TestRecordCheckDataRead: a check block's scoped data read that failed is
// found with the read's error as its message, one that succeeded is not a
// failure, and one never recorded (deferred to the apply) is not either
// (#2418).
func TestRecordCheckDataRead(t *testing.T) {
	t.Setenv(forkport.AllOff, "")
	t.Setenv(forkport.Switch(Name), "")
	ctx := WithResults(context.Background(), NewResults(false))

	failing := mustInst(t, "data.checks_object.failing")
	passing := mustInst(t, "data.checks_object.passing")
	unread := mustInst(t, "data.checks_object.unread")

	var readDiags tfdiags.Diagnostics
	readDiags = readDiags.Append(tfdiags.Sourceless(tfdiags.Error, "data source read failed", "the provider could not read it"))
	RecordCheckDataRead(ctx, failing, readDiags)
	RecordCheckDataRead(ctx, passing, nil)

	if msg, failed := CheckDataFailure(ctx, failing); !failed || msg != "data source read failed: the provider could not read it" {
		t.Errorf("failing read: got (%q, %t), want the read's error and true", msg, failed)
	}
	if msg, failed := CheckDataFailure(ctx, passing); failed || msg != "" {
		t.Errorf("passing read: got (%q, %t), want no failure", msg, failed)
	}
	if _, failed := CheckDataFailure(ctx, unread); failed {
		t.Errorf("unread instance reported as failed")
	}

	// A later read of the same instance, as the apply's, replaces the plan's.
	RecordCheckDataRead(ctx, failing, nil)
	if _, failed := CheckDataFailure(ctx, failing); failed {
		t.Errorf("a successful re-read left the earlier failure in place")
	}
}

// TestCheckDependencies: what check assertions read is collected for the
// planning engine, without duplicates.
func TestCheckDependencies(t *testing.T) {
	t.Setenv(forkport.AllOff, "")
	t.Setenv(forkport.Switch(Name), "")
	ctx := WithResults(context.Background(), NewResults(false))

	a := mustInst(t, "data.checks_object.a")
	b := mustInst(t, "checks_object.b")
	RecordCheckDependencies(ctx, a, b)
	RecordCheckDependencies(ctx, a)
	RecordCheckDependencies(ctx)

	got := CheckDependencies(ctx)
	if len(got) != 2 || !got.Has(a) || !got.Has(b) {
		t.Errorf("wrong dependencies: %v", got)
	}
}

// TestCheckDataPortOff: with the port off nothing is recorded, and nothing
// is found failed.
func TestCheckDataPortOff(t *testing.T) {
	t.Setenv(forkport.Switch(Name), "1")
	ctx := WithResults(context.Background(), NewResults(false))

	addr := mustInst(t, "data.checks_object.failing")
	var readDiags tfdiags.Diagnostics
	readDiags = readDiags.Append(tfdiags.Sourceless(tfdiags.Error, "data source read failed", "nope"))
	RecordCheckDataRead(ctx, addr, readDiags)
	RecordCheckDependencies(ctx, addr)

	if _, failed := CheckDataFailure(ctx, addr); failed {
		t.Errorf("recorded a failure with the port off")
	}
	if got := CheckDependencies(ctx); len(got) != 0 {
		t.Errorf("recorded dependencies with the port off: %v", got)
	}
}

func mustInst(t *testing.T, s string) addrs.AbsResourceInstance {
	t.Helper()
	addr, diags := addrs.ParseAbsResourceInstanceStr(s)
	if diags.HasErrors() {
		t.Fatalf("bad address %q: %s", s, diags.Err())
	}
	return addr
}
