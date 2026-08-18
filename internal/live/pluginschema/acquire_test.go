// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package pluginschema

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAcquireRefusesEmptyWorkDir guards issue #273: a zero-value Request has
// an empty WorkDir, and before this fix AcquireSession (and therefore
// Acquire, which is a thin wrapper over it) treated that as license to write
// main.tf into the process's current working directory and run init there -
// no error, no temp dir, just a silent scribble on whatever directory the
// process happened to be running in. That directory can be the repository
// checkout itself.
//
// The fix is a refusal, not a smarter default: this test asserts Acquire
// returns an error immediately for an empty WorkDir, and that nothing is
// written anywhere as a result - neither into this test's own working
// directory nor into a fresh temp directory the test controls but never
// hands to Acquire.
func TestAcquireRefusesEmptyWorkDir(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	cwdMainTF := filepath.Join(cwd, "main.tf")
	if _, err := os.Stat(cwdMainTF); err == nil {
		t.Fatalf("this test's own working directory %s already has a main.tf before the test ran - "+
			"cannot tell a pre-existing file from one this test would catch", cwd)
	}

	// A directory the test can inspect afterward, distinct from both the
	// test's cwd and whatever Acquire might otherwise have guessed. Acquire
	// is never given this path; it exists only so the test can assert
	// nothing landed here either.
	sentinelDir := t.TempDir()

	_, err = Acquire(context.Background(), Request{
		InitBin: "tofu",
		Source:  "example.com/example/example",
		Version: "1.0.0",
		// WorkDir deliberately left empty: the zero value under test.
	})
	if err == nil {
		t.Fatal("Acquire with an empty WorkDir returned no error; it must refuse rather than guess a directory to write into")
	}
	if !strings.Contains(err.Error(), "WorkDir") {
		t.Fatalf("Acquire's error for an empty WorkDir does not mention WorkDir, so it would not tell a caller what to fix: %v", err)
	}

	if _, statErr := os.Stat(cwdMainTF); statErr == nil {
		t.Fatalf("Acquire with an empty WorkDir wrote %s - it must refuse before any file write", cwdMainTF)
	} else if !os.IsNotExist(statErr) {
		t.Fatalf("checking %s: %v", cwdMainTF, statErr)
	}

	entries, err := os.ReadDir(sentinelDir)
	if err != nil {
		t.Fatalf("reading sentinel dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("sentinel directory %s is not empty after a refused Acquire call: %v", sentinelDir, entries)
	}
}

// TestAcquireSessionRefusesEmptyWorkDir is the same guard at the lower-level
// entry point. Acquire is a wrapper over AcquireSession
// ("Acquire reads a provider's schemas and closes the plugin before
// returning. It is [AcquireSession] for the caller that wants nothing but
// the schemas."), and the guard lives in AcquireSession so both entry
// points - and any future direct caller of AcquireSession - are covered by
// one check rather than two copies that could drift apart.
func TestAcquireSessionRefusesEmptyWorkDir(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	cwdMainTF := filepath.Join(cwd, "main.tf")
	if _, err := os.Stat(cwdMainTF); err == nil {
		t.Fatalf("this test's own working directory %s already has a main.tf before the test ran", cwd)
	}

	sess, err := AcquireSession(context.Background(), Request{
		InitBin: "tofu",
		Source:  "example.com/example/example",
		Version: "1.0.0",
	})
	if err == nil {
		if sess != nil {
			_ = sess.Close(context.Background())
		}
		t.Fatal("AcquireSession with an empty WorkDir returned no error; it must refuse rather than guess a directory to write into")
	}
	if sess != nil {
		t.Fatalf("AcquireSession returned a non-nil Session alongside an error: %+v", sess)
	}
	if !strings.Contains(err.Error(), "WorkDir") {
		t.Fatalf("AcquireSession's error for an empty WorkDir does not mention WorkDir: %v", err)
	}

	if _, statErr := os.Stat(cwdMainTF); statErr == nil {
		t.Fatalf("AcquireSession with an empty WorkDir wrote %s - it must refuse before any file write", cwdMainTF)
	} else if !os.IsNotExist(statErr) {
		t.Fatalf("checking %s: %v", cwdMainTF, statErr)
	}
}
