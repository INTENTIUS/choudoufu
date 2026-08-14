// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The ownership half of issue #108 criterion 4, ungated: pure filesystem
// behavior, no schema acquisition.

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckForeignTF(t *testing.T) {
	t.Run("missing directory is a fine first run", func(t *testing.T) {
		if err := checkForeignTF(filepath.Join(t.TempDir(), "absent"), "s3"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("owned files pass", func(t *testing.T) {
		dir := t.TempDir()
		for _, f := range []string{"versions.tf", "locals.tf", "s3.tf", "supporting.tf"} {
			touch(t, filepath.Join(dir, f))
		}
		touch(t, filepath.Join(dir, "README.md"))
		if err := checkForeignTF(dir, "s3"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("a hand-written tf file is refused by name", func(t *testing.T) {
		dir := t.TempDir()
		touch(t, filepath.Join(dir, "versions.tf"))
		touch(t, filepath.Join(dir, "iam.tf"))
		touch(t, filepath.Join(dir, "wrapped", "extra.tf"))
		err := checkForeignTF(dir, "s3")
		if err == nil {
			t.Fatal("foreign iam.tf was not refused")
		}
		for _, want := range []string{"iam.tf", "wrapped/extra.tf"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error does not name %s: %v", want, err)
			}
		}
	})

	t.Run("non-tf files are not policed", func(t *testing.T) {
		dir := t.TempDir()
		touch(t, filepath.Join(dir, "NOTES.md"))
		if err := checkForeignTF(dir, "s3"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestRemoveStaleOwned(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"versions.tf", "locals.tf", "s3.tf", "supporting.tf", "main.tf", "README.md"} {
		touch(t, filepath.Join(dir, f))
	}
	touch(t, filepath.Join(dir, "wrapped", "locals.tf"))
	touch(t, filepath.Join(dir, "NOTES.md"))

	wrote := map[string]bool{"versions.tf": true, "locals.tf": true, "s3.tf": true, "README.md": true}
	if err := removeStaleOwned(dir, "s3", wrote); err != nil {
		t.Fatal(err)
	}

	for _, kept := range []string{"versions.tf", "locals.tf", "s3.tf", "README.md", "NOTES.md"} {
		if _, err := os.Stat(filepath.Join(dir, kept)); err != nil {
			t.Errorf("%s should have been kept: %v", kept, err)
		}
	}
	for _, gone := range []string{"supporting.tf", "main.tf", filepath.Join("wrapped", "locals.tf"), "wrapped"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s should have been removed (stale owned file)", gone)
		}
	}
}
