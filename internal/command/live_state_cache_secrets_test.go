// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/encryption"
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/states/statefile"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// TestRefuseTurnsTheStateCacheOff is GitHub issue #1375's product half
// (maintainer's ruling, 2026-09-19). The cache is a stock state file written
// unencrypted, holding every sensitive attribute and output. Under
// strict { secrets = "refuse" } an operator has said the tool keeps no secret
// material, so the tool does not write that file, as if
// CHOUDOUFU_STATE_CACHE=off. Naming a path is still an explicit opt-in, which
// is what keeps the cache-as-the-exit route open for such an estate.
func TestRefuseTurnsTheStateCacheOff(t *testing.T) {
	defaultPath := filepath.Join(".terraform", "choudoufu-cache.tfstate")
	for _, tc := range []struct {
		name          string
		env           string
		secrets       strict.Secrets
		wantPath      string
		offForSecrets bool
	}{
		{"store, nothing set: the default path", "", strict.Store, defaultPath, false},
		{"refuse, nothing set: off, and because of refuse", "", strict.Refuse, "", true},
		{"refuse, a path named on purpose: honoured", "/tmp/exit.tfstate", strict.Refuse, "/tmp/exit.tfstate", false},
		{"store, a path named: honoured", "/tmp/c.tfstate", strict.Store, "/tmp/c.tfstate", false},
		{"store, off: off, and not because of refuse", "off", strict.Store, "", false},
		{"refuse, off: off, and the operator's doing", "off", strict.Refuse, "", false},
		// GitHub issue #1515's ruling 4. "ssm" is on "refuse"'s side of
		// this question and on "store"'s side of every other one, which is
		// why it is spelled out here rather than left to the predicate:
		// the values it keeps go to Parameter Store under a customer
		// managed key, and a cache file would put the same values back in
		// clear in the working directory of every machine that applied.
		{"ssm, nothing set: off, and because of the setting", "", strict.SSM, "", true},
		{"ssm, a path named on purpose: honoured", "/tmp/exit.tfstate", strict.SSM, "/tmp/exit.tfstate", false},
		{"ssm, off: off, and the operator's doing", "off", strict.SSM, "", false},
		// The zero value is a caller that could not read a configuration.
		// It keeps the cache, which is what every run written before
		// either setting existed does; turning the cache off here would
		// be a behavior change nothing asked for.
		{"the zero value: the default path, unchanged", "", strict.Secrets(""), defaultPath, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvStateCache, tc.env)
			t.Setenv("TF_DATA_DIR", "")
			path, off := stateCachePathFor(tc.secrets)
			if path != tc.wantPath || off != tc.offForSecrets {
				t.Errorf("stateCachePathFor(%q) with %s=%q = (%q, %v), want (%q, %v)", tc.secrets, EnvStateCache, tc.env, path, off, tc.wantPath, tc.offForSecrets)
			}
		})
	}
}

// TestRefuseSaysSoWhenAnOldCacheIsStillOnDisk: turning the cache off stops
// the NEXT write. A file from before the setting was turned on still holds
// what it held, in clear, and the run is the only thing that knows both
// facts. It says so as a warning, names the file, and does not delete it:
// the file is the operator's.
func TestRefuseSaysSoWhenAnOldCacheIsStillOnDisk(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TF_DATA_DIR", dir)
	t.Setenv(EnvStateCache, "")
	leftover := filepath.Join(dir, "choudoufu-cache.tfstate")

	if diags := stateCacheOffForSecretsDiags(strict.Refuse); len(diags) != 0 {
		t.Fatalf("with no leftover file the run warned anyway: %v", diags.ErrWithWarnings())
	}

	if err := os.WriteFile(leftover, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	diags := stateCacheOffForSecretsDiags(strict.Refuse)
	if len(diags) != 1 || diags[0].Severity() != tfdiags.Warning {
		t.Fatalf("want exactly one warning, got %d: %v", len(diags), diags.ErrWithWarnings())
	}
	detail := diags[0].Description().Detail
	for _, want := range []string{leftover, "secrets = \"refuse\"", "in clear", "Delete it"} {
		if !strings.Contains(detail, want) {
			t.Errorf("the warning does not say %q:\n%s", want, detail)
		}
	}
	if _, err := os.Stat(leftover); err != nil {
		t.Errorf("the run removed the operator's file: %v", err)
	}

	// The warning names the setting in force, not a hard-coded "refuse".
	// An operator under #1515's "ssm" reading a warning about a setting
	// their configuration does not contain would go looking for a line
	// that is not there.
	ssmDiags := stateCacheOffForSecretsDiags(strict.SSM)
	if len(ssmDiags) != 1 {
		t.Fatalf("under %q, want exactly one warning, got %d", strict.SSM, len(ssmDiags))
	}
	ssmDetail := ssmDiags[0].Description().Detail
	if !strings.Contains(ssmDetail, `secrets = "ssm"`) {
		t.Errorf("the warning under %q does not name that setting:\n%s", strict.SSM, ssmDetail)
	}
	if strings.Contains(ssmDetail, `secrets = "refuse"`) {
		t.Errorf("the warning under %q names \"refuse\", a setting this configuration does not contain:\n%s", strict.SSM, ssmDetail)
	}
}

// TestRefuseWritesNoStateCacheEndToEnd is the wiring: a real apply under
// strict { secrets = "refuse" } reaches PersistState and leaves no cache file,
// where the same apply without the setting leaves one (the control, so that
// "no file" is not simply this fixture never writing a cache at all).
func TestRefuseWritesNoStateCacheEndToEnd(t *testing.T) {
	for _, tc := range []struct {
		name      string
		refuse    bool
		wantCache bool
	}{
		{"control: the default setting writes the cache", false, true},
		{"refuse writes none", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			td := t.TempDir()
			testCopyDir(t, testFixturePath("live-block"), td)
			if tc.refuse {
				mainTF := filepath.Join(td, "main.tf")
				raw, err := os.ReadFile(mainTF)
				if err != nil {
					t.Fatal(err)
				}
				const anchor = "    estate = \"stateless-unit\"\n"
				if !strings.Contains(string(raw), anchor) {
					t.Fatalf("the fixture no longer has the line this test edits: %q", anchor)
				}
				edited := strings.Replace(string(raw), anchor, anchor+"\n    strict {\n      secrets = \"refuse\"\n    }\n", 1)
				if err := os.WriteFile(mainTF, []byte(edited), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Chdir(td)
			dataDir := filepath.Join(t.TempDir(), "data")
			t.Setenv("TF_DATA_DIR", dataDir)
			t.Setenv(EnvStateCache, "")

			cloud := newStatelessTestCloud()
			view, done := testView(t)
			c := &ApplyCommand{Meta: liveBlockMeta(view, cloud)}
			var captured *statelessRunner
			defer statelessRunnerTestHook(func(r *statelessRunner) { captured = r })()

			code := c.Run([]string{"-no-color", "-auto-approve"})
			output := done(t)
			if code != 0 {
				t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
			}
			if captured == nil || captured.mgr.Persists() == 0 {
				t.Fatal("the apply never reached PersistState, so this test says nothing about the cache")
			}
			_, err := os.Stat(filepath.Join(dataDir, "choudoufu-cache.tfstate"))
			if got := err == nil; got != tc.wantCache {
				t.Errorf("cache file present = %v, want %v", got, tc.wantCache)
			}
		})
	}
}

// TestRefuseReadsNoStateCache: a run that writes no cache must not go on
// serving reads out of one an earlier run left behind. The control is the
// same file under the default setting, which does load.
func TestRefuseReadsNoStateCache(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TF_DATA_DIR", dir)
	t.Setenv(EnvStateCache, "")
	f, err := os.Create(filepath.Join(dir, "choudoufu-cache.tfstate"))
	if err != nil {
		t.Fatal(err)
	}
	if err := statefile.Write(statefile.New(states.NewState(), "", 0), f, encryption.StateEncryptionDisabled()); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if loadStateCache(strict.Store) == nil {
		t.Fatal("control: the cache file did not load under the default setting, so this test proves nothing")
	}
	if got := loadStateCache(strict.Refuse); got != nil {
		t.Error("a state cache was loaded under strict { secrets = \"refuse\" }")
	}
}
