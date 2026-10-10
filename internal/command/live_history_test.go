// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// historyStore is a record store that keeps past versions, keyed by the
// exact record key, under a run cache the way NewRecordStore returns one.
type historyStore struct {
	staterecord.Store
	versions map[string][]staterecord.RecordVersion
}

func (h *historyStore) ListVersions(_ context.Context, key string) ([]staterecord.RecordVersion, error) {
	return h.versions[key], nil
}

func writeLiveHistoryConfig(t *testing.T, recordStore string) {
	t.Helper()
	td := t.TempDir()
	cfg := "terraform {\n  live {\n    estate = \"prod\"\n\n    " + recordStore + "\n  }\n}\n"
	if err := os.WriteFile(filepath.Join(td, "main.tf"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(td)
}

// TestLiveHistory is GitHub issue #1954's done-when at the command: a record
// changed by three applies lists three versions, newest first, with no
// values; Kubernetes and local stores say none are kept.
func TestLiveHistory(t *testing.T) {
	const address = `terraform_data.config["eu"]`
	addr, diags := addrs.ParseAbsResourceInstanceStr(address)
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	key := projection.RecordKey(projection.RecordKeyPrefix("prod"), addr)
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	const secret = "the-value-a-history-never-prints"

	s3Opener := func(t *testing.T) recordStoreOpener {
		return func(ctx context.Context, rs *configs.LiveRecordStore, _ *configs.LiveRetry, estate, _ string, _ ...projection.RecordStoreOption) (staterecord.Store, error) {
			if rs.Type != "s3" || estate != "prod" {
				t.Errorf("opened a %q store for estate %q, want s3 for prod", rs.Type, estate)
			}
			local, err := staterecord.NewLocalStore(t.TempDir())
			if err != nil {
				return nil, err
			}
			if _, err := local.PutIfAbsent(ctx, key, []byte(secret)); err != nil {
				return nil, err
			}
			inner := &historyStore{Store: local, versions: map[string][]staterecord.RecordVersion{key: {
				{VersionID: "v3", LastModified: base.Add(2 * time.Hour), Current: true},
				{VersionID: "v2", LastModified: base.Add(time.Hour)},
				{VersionID: "v1", LastModified: base},
			}}}
			return staterecord.NewRunCache(inner, projection.RecordKeyPrefix("prod")), nil
		}
	}

	run := func(t *testing.T, open recordStoreOpener, args ...string) (int, string, string) {
		t.Helper()
		view, done := testView(t)
		c := &LiveHistoryCommand{Meta: Meta{WorkingDir: workdir.NewDir("."), View: view}, open: open}
		code := c.Run(args)
		out := done(t)
		return code, out.Stdout(), out.Stderr()
	}

	t.Run("three applies list three versions, newest first, no values", func(t *testing.T) {
		writeLiveHistoryConfig(t, `record_store "s3" { bucket = "prod-records" }`)
		code, stdout, stderr := run(t, s3Opener(t), address)
		if code != 0 {
			t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
		}
		if !strings.Contains(stdout, "3 versions of its record, newest first") {
			t.Errorf("the headline does not count three versions:\n%s", stdout)
		}
		i3, i2, i1 := strings.Index(stdout, "v3  current"), strings.Index(stdout, "v2\n"), strings.Index(stdout, "v1\n")
		if i3 < 0 || i2 < 0 || i1 < 0 || !(i3 < i2 && i2 < i1) {
			t.Errorf("want v3 (current), then v2, then v1:\n%s", stdout)
		}
		if strings.Contains(stdout, secret) {
			t.Errorf("the history printed a record's value:\n%s", stdout)
		}
	})

	t.Run("-json", func(t *testing.T) {
		writeLiveHistoryConfig(t, `record_store "s3" { bucket = "prod-records" }`)
		code, stdout, stderr := run(t, s3Opener(t), "-json", address)
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, stderr)
		}
		var h liveHistory
		if err := json.Unmarshal([]byte(stdout), &h); err != nil {
			t.Fatalf("%v\n%s", err, stdout)
		}
		if !h.Kept || h.Store != "s3" || h.Address != address || len(h.Versions) != 3 || h.Versions[0].VersionID != "v3" || !h.Versions[0].Current {
			t.Errorf("unexpected document: %+v", h)
		}
	})

	t.Run("an address with no record says so", func(t *testing.T) {
		writeLiveHistoryConfig(t, `record_store "s3" { bucket = "prod-records" }`)
		code, stdout, _ := run(t, s3Opener(t), "terraform_data.other")
		if code != 0 || !strings.Contains(stdout, "has no record in estate prod's record store, current or past") {
			t.Errorf("exit %d:\n%s", code, stdout)
		}
	})

	for _, rs := range []string{`record_store "kubernetes" {}`, `record_store "local" {}`} {
		t.Run(rs+" keeps none", func(t *testing.T) {
			writeLiveHistoryConfig(t, rs)
			never := func(context.Context, *configs.LiveRecordStore, *configs.LiveRetry, string, string, ...projection.RecordStoreOption) (staterecord.Store, error) {
				t.Error("a store that keeps no versions was opened")
				return nil, nil
			}
			code, stdout, stderr := run(t, never, address)
			if code != 0 {
				t.Fatalf("exit %d\n%s", code, stderr)
			}
			if !strings.Contains(stdout, "record store keeps no past versions") {
				t.Errorf("does not say none are kept:\n%s", stdout)
			}
		})
	}
}
