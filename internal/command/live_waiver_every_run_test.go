// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/pkg/staterecord"
)

// TestAWaiverWarnsOnEveryRunOfAnEstate is the half of claim 29's waiver
// proof that is choudoufu's own (GitHub issues #1340 and #2288). A waiver in
// allow_insecure must say what it waives on every run for as long as it is
// configured: on an estate's first apply, on a plan once the state cache
// exists, and on the apply after that. The defect it is shaped against is a
// warning that goes quiet once the estate has run before, which no single
// run can show, so the test runs the estate three times and requires the
// cache to be on disk before the second.
//
// The smoke scenario a-waiver-names-what-it-waives held this until #2288,
// with a BREAK arm that rebuilt choudoufu so the warning was emitted only
// while .terraform/choudoufu-cache.tfstate was absent. That same patch to
// live_mode.go's `if !r.waiverWarned {` fails this test on run two.
//
// Which settings a waiver reaches is pkg/staterecord's
// (TestAWaiverReachesOnlyWhatItNames) and first contact's
// (internal/live/projection's TestFirstContactHonoursTheWaiver).
func TestAWaiverWarnsOnEveryRunOfAnEstate(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block-record-store"), td)
	t.Chdir(td)

	main := filepath.Join(td, "main.tf")
	raw, err := os.ReadFile(main)
	if err != nil {
		t.Fatal(err)
	}
	const local = `record_store "local" {}`
	if !strings.Contains(string(raw), local) {
		t.Fatalf("the fixture no longer carries %s, so this test would not configure the waiver it reads", local)
	}
	waived := `record_store "s3" {
      bucket         = "` + testBucket + `"
      allow_insecure = ["versioning"]
    }`
	if err := os.WriteFile(main, []byte(strings.Replace(string(raw), local, waived, 1)), 0o644); err != nil {
		t.Fatal(err)
	}

	// One store for the three runs, as one bucket would be. It answers the
	// bucket contract with versioning failing, the scenario's bucket, so the
	// apply's own contract check has something real to say beside the
	// waiver.
	store := &contractCheckingStore{Store: localStoreForTest(t), findings: withFinding(staterecord.Finding{
		Setting: staterecord.BucketVersioning, Found: "versioning has never been enabled",
	})}
	opened := 0
	prev := liveRecordStoreOpener
	liveRecordStoreOpener = func(_ context.Context, rs *configs.LiveRecordStore, _ *configs.LiveRetry, _, _ string, _ ...projection.RecordStoreOption) (staterecord.Store, error) {
		opened++
		if rs.Type != "s3" || strings.Join(rs.AllowInsecure, ",") != "versioning" {
			t.Errorf("the run opened record_store %q with allow_insecure %v, want s3 with [versioning]", rs.Type, rs.AllowInsecure)
		}
		return store, nil
	}
	t.Cleanup(func() { liveRecordStoreOpener = prev })

	const warning = "The record store bucket's versioning assertion is waived"
	cachePath := filepath.Join(td, ".terraform", "choudoufu-cache.tfstate")
	cloud := newLiveTestCloud()

	for i, args := range [][]string{
		{"apply", "-no-color", "-auto-approve"},
		{"plan", "-no-color"},
		{"apply", "-no-color", "-auto-approve"},
	} {
		run := i + 1
		if run > 1 {
			if _, err := os.Stat(cachePath); err != nil {
				t.Fatalf("run %d: the state cache is not on disk (%v), so this run is not the one a warning tied to the first run would go quiet on", run, err)
			}
		}
		view, done := testView(t)
		meta := liveBlockMeta(view, cloud)
		var code int
		if args[0] == "plan" {
			code = (&PlanCommand{Meta: meta}).Run(args[1:])
		} else {
			code = (&ApplyCommand{Meta: meta}).Run(args[1:])
		}
		out := done(t)
		all := out.Stdout() + out.Stderr()
		if code != 0 {
			t.Fatalf("run %d (%s) exited %d; the one failing assertion is the one waived, so it goes ahead\nstdout:\n%s\nstderr:\n%s", run, args[0], code, out.Stdout(), out.Stderr())
		}
		if got := strings.Count(all, warning); got != 1 {
			t.Errorf("run %d (%s) printed %q %d time(s), want once on every run for as long as the waiver is configured:\n%s", run, args[0], warning, got, all)
		}
	}
	if opened != 3 {
		t.Errorf("the record store was opened %d time(s) over three runs; the waiver warning is made where it is opened", opened)
	}
}
