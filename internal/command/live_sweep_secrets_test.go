// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/pkg/staterecord"
)

// fakeSecretsSweeper stands in for projection.SSMSecretsStore, whose sweep is
// tested against a fake SSM in internal/live/projection
// (TestListOrphansDeletesNothing, TestSSMSecretsSweepNeverDeletesAReferencedParameter).
// This test is the command's half: which of the two it calls, and what it
// prints.
type fakeSecretsSweeper struct {
	listed, swept int
	res           projection.SweepResult
}

func (f *fakeSecretsSweeper) Namespace() string { return "/tofu-secrets/prod/abcd" }
func (f *fakeSecretsSweeper) ListOrphans(context.Context) (projection.SweepResult, error) {
	f.listed++
	return f.res, nil
}
func (f *fakeSecretsSweeper) SweepOrphans(context.Context) (projection.SweepResult, error) {
	f.swept++
	r := f.res
	r.Deleted = r.Orphans
	return r, nil
}
func (f *fakeSecretsSweeper) TakeDiagnostics() tfdiags.Diagnostics { return nil }

const sweepSSMConfig = `record_store "s3" { bucket = "prod-records" }
    strict {
      secrets = "ssm"
      ssm { kms_key_id = "arn:aws:kms:eu-west-1:111122223333:key/1234abcd" }
    }`

// TestLiveSweepSecrets is GitHub issue #2393's follow-up 2 at the command:
// the default lists the orphans and deletes nothing, -delete deletes, and a
// configuration that keeps no secrets in SSM is refused before any store is
// opened.
func TestLiveSweepSecrets(t *testing.T) {
	opener := func(context.Context, *configs.LiveRecordStore, *configs.LiveRetry, string, string, ...projection.RecordStoreOption) (staterecord.Store, error) {
		return staterecord.NewLocalStore(t.TempDir())
	}
	run := func(t *testing.T, f *fakeSecretsSweeper, args ...string) (int, string, string) {
		t.Helper()
		view, done := testView(t)
		c := &LiveSweepSecretsCommand{Meta: Meta{WorkingDir: workdir.NewDir("."), View: view}, open: opener,
			sweeper: func(staterecord.Store) (secretsSweeper, bool) { return f, true }}
		code := c.Run(args)
		out := done(t)
		return code, out.Stdout(), out.Stderr()
	}
	res := projection.SweepResult{Referenced: 4, Young: []string{"/tofu-secrets/prod/abcd/young"}, Orphans: []string{"/tofu-secrets/prod/abcd/lost-race"}}

	t.Run("the default lists and deletes nothing", func(t *testing.T) {
		writeLiveHistoryConfig(t, sweepSSMConfig)
		f := &fakeSecretsSweeper{res: res}
		code, stdout, stderr := run(t, f)
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, stderr)
		}
		if f.swept != 0 || f.listed != 1 {
			t.Errorf("listed %d, swept %d; want a list and no sweep", f.listed, f.swept)
		}
		for _, want := range []string{"4 referenced by a record, kept", "1 orphans", "lost-race", "Nothing was deleted. Run again with -delete"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("output lacks %q:\n%s", want, stdout)
			}
		}
	})

	t.Run("-delete -json", func(t *testing.T) {
		writeLiveHistoryConfig(t, sweepSSMConfig)
		f := &fakeSecretsSweeper{res: res}
		code, stdout, stderr := run(t, f, "-delete", "-json")
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, stderr)
		}
		var r liveSweepSecretsReport
		if err := json.Unmarshal([]byte(stdout), &r); err != nil {
			t.Fatalf("%v\n%s", err, stdout)
		}
		if f.swept != 1 || f.listed != 0 || r.DryRun || len(r.Deleted) != 1 || r.Estate != "prod" || r.Referenced != 4 {
			t.Errorf("swept %d listed %d, document %+v", f.swept, f.listed, r)
		}
	})

	t.Run("no secrets in SSM", func(t *testing.T) {
		writeLiveHistoryConfig(t, `record_store "s3" { bucket = "prod-records" }`)
		f := &fakeSecretsSweeper{res: res}
		code, _, stderr := run(t, f)
		if code != 1 || !strings.Contains(stderr, "No secrets in SSM") || f.listed+f.swept != 0 {
			t.Errorf("exit %d, list %d sweep %d:\n%s", code, f.listed, f.swept, stderr)
		}
	})
}
