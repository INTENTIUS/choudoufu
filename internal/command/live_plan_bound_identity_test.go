// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/terminal"
)

// TestLivePlan_jsonBoundCarriesTheIdentityItMatchedOn is GitHub issue #967:
// every bound[] row states the live id its binding actually matched on,
// whichever admission path supplied it, because that id is the join key a
// reader (behold, chant #2104) uses to pair a live-plan row with the
// live-ls item for the same object.
//
// Before this test, the id was read off the pre-projection
// [identity.Resolution] ([statelessBoundReport]'s own merged list), which
// holds one only for the paths that settle an identity BEFORE the
// projection runs. Two of the three shapes the issue observed do not:
//
//   - A record-located instance (identity.ClassRecordLocated, the "record"
//     source) never gets an ImportID written back onto its resolution at
//     all; the id lives in the record the projection reads.
//   - A needs-discovery instance the estate-wide sweep did NOT bind - the
//     tag index has not caught up, which the issue observes changing the
//     answer between two runs over one estate - keeps its
//     ClassNeedsDiscovery resolution, with no ImportID, and is
//     materialized by GitHub issue #364's record-first read instead. It
//     is still reported with source "marker", because
//     [statelessNeedsDiscoverySet] is what classifies it and the sweep is
//     what it was waiting on.
//
// Both are asserted here BY VALUE against literal ids the fixtures seed, so
// that a row losing its identity again - or gaining a fabricated one -
// fails rather than degrading to a join on addr.
//
// The fourth [views.LivePlanBoundSource], "cache", has no subtest: this
// pipeline never sets projection.Options.StateCache, so it cannot produce
// one - see [statelessBoundReport]'s own doc comment.
func TestLivePlan_jsonBoundCarriesTheIdentityItMatchedOn(t *testing.T) {
	t.Run("derived and marker", func(t *testing.T) {
		td := t.TempDir()
		testCopyDir(t, testFixturePath("live-block-record-store"), td)
		t.Chdir(td)

		const estate = "stateless-unit"
		cloud := newStatelessTestCloud()
		// The bucket: client-named, so its identity is derivable from
		// configuration and the projection reads it back directly.
		cloud.putMarked("aws_s3_bucket", "tofu-stateless-unit-data", estate, "aws_s3_bucket.data", map[string]string{
			"id": "tofu-stateless-unit-data", "bucket": "tofu-stateless-unit-data",
		})
		// The VPC: marked and readable, but deliberately NOT listed, so the
		// estate-wide sweep finds nothing to bind it with - the tag-index
		// lag the issue names. Its resolution therefore stays
		// needs-discovery and the record below is what materializes it,
		// while the row is still classified "marker".
		cloud.putMarked("aws_vpc", "vpc-owned", estate, "aws_vpc.main", map[string]string{
			"id": "vpc-owned", "cidr_block": "10.42.0.0/16",
		})
		seedIdentityRecord(t, td, estate, "aws_vpc.main", "vpc-owned")

		c, done := newLiveBlockPlanCommand(t, cloud)
		code := c.Run([]string{"-no-color", "-json"})
		out := done(t)
		if code != 0 {
			t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out.Stdout(), out.Stderr())
		}

		want := []views.LivePlanBound{
			{
				Addr:           "aws_s3_bucket.data",
				TypeName:       "aws_s3_bucket",
				Identity:       "tofu-stateless-unit-data",
				IdentityValues: map[string]string{"bucket": "tofu-stateless-unit-data"},
				Source:         views.LivePlanBoundDerived,
			},
			{
				Addr:     "aws_vpc.main",
				TypeName: "aws_vpc",
				Identity: "vpc-owned",
				Source:   views.LivePlanBoundMarker,
			},
		}
		assertBoundRows(t, out, want)
	})

	t.Run("record", func(t *testing.T) {
		td := t.TempDir()
		testCopyDir(t, testFixturePath("live-plan-markers-record"), td)
		t.Chdir(td)

		const estate = "markers-record-unit"
		cloud := newStatelessTestCloud()
		cloud.putMarked("aws_vpc", "vpc-existing", estate, "aws_vpc.main", map[string]string{
			"id": "vpc-existing", "cidr_block": "10.42.0.0/16",
		})
		seedIdentityRecord(t, td, estate, "aws_vpc.main", "vpc-existing")

		// No -estate flag: the fixture's live block names it, and
		// strict { markers "record" } is what routes aws_vpc.main through
		// identity.ClassRecordLocated.
		c, done := newLivePlanCommand(t, cloud)
		code := c.Run([]string{"-no-color", "-json"})
		out := done(t)
		if code != 0 {
			t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out.Stdout(), out.Stderr())
		}

		assertBoundRows(t, out, []views.LivePlanBound{
			{
				Addr:     "aws_vpc.main",
				TypeName: "aws_vpc",
				Identity: "vpc-existing",
				Source:   views.LivePlanBoundRecord,
			},
		})
	})
}

// seedIdentityRecord writes the v2 kind=identity envelope an apply would
// have written for addr, into the local record store the fixture's live
// block declares. Written by hand for the same reason
// TestLivePlan_markersRecordPreservesExistingMarker's own record is: this
// package cannot reach internal/live/projection's unexported write path.
func seedIdentityRecord(t *testing.T, dir, estate, addrStr, importID string) {
	t.Helper()
	store, err := staterecord.NewLocalStore(filepath.Join(dir, ".tofu-records"))
	if err != nil {
		t.Fatalf("opening the record store: %s", err)
	}
	addr, addrDiags := addrs.ParseAbsResourceInstanceStr(addrStr)
	if addrDiags.HasErrors() {
		t.Fatalf("parsing %q: %s", addrStr, addrDiags.Err())
	}
	rec := []byte(`{"format_version":2,"address":"` + addr.String() + `","kind":"identity","identity":{"import_id":"` + importID + `"}}`)
	if _, err := store.PutIfAbsent(t.Context(), projection.RecordKey(projection.RecordKeyPrefix(estate), addr), rec); err != nil {
		t.Fatalf("seeding the record for %s: %s", addrStr, err)
	}
}

// assertBoundRows parses the one document on stdout and compares its
// bound[] against want by value.
func assertBoundRows(t *testing.T, out *terminal.TestOutput, want []views.LivePlanBound) {
	t.Helper()
	var doc views.LivePlanDocument
	if err := json.Unmarshal([]byte(out.Stdout()), &doc); err != nil {
		t.Fatalf("stdout does not parse as one JSON document: %s\nstdout:\n%s", err, out.Stdout())
	}
	if !reflect.DeepEqual(doc.Bound, want) {
		t.Errorf("bound[] does not carry the identities the bindings matched on.\n--- got ---\n%#v\n--- want ---\n%#v", doc.Bound, want)
	}
}
