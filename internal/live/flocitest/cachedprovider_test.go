// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package flocitest

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestCachedProviderWaitsOutAWriter is #1699, with no emulator and no
// provider download.
//
// The floci tier went red with "fork/exec .../terraform-provider-aws_v6.59.0_x5:
// text file busy" in all three cohort renders at once, each of which had
// logged "hashicorp/aws 6.59.0 is there; no registry lookup". Linux refuses to
// exec a file some process holds open for writing. None of the three renders
// writes the binary: estate-gen's init symlinks it from the shared cache. The
// writer was another package's init filling that cache entry under the
// cache's lock, and the renders took an executable file in a half-extracted
// entry for a complete one because the warm check ran without the lock.
//
// A writer holds the lock for its whole init, so the only honest warm check
// is one made under it. This stands in for the writer: it takes the lock and
// leaves an executable, partly written provider in the cache, then asserts
// CachedProvider does not call the entry warm until the writer lets go.
func TestCachedProviderWaitsOutAWriter(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("TF_PLUGIN_CACHE_DIR", cache)
	const host, ns, typ, version = "registry.terraform.io", "hashicorp", "aws", "6.59.0"

	writerUnlock := lockPluginCache(t)
	platform := filepath.Join(cache, host, ns, typ, version, runtime.GOOS+"_"+runtime.GOARCH)
	if err := os.MkdirAll(platform, 0o755); err != nil {
		t.Fatal(err)
	}
	// Mid-extraction: the file exists, is executable, and is still open
	// for writing, which is the state that makes exec fail with ETXTBSY.
	bin, err := os.OpenFile(filepath.Join(platform, "terraform-provider-aws_v6.59.0_x5"), os.O_CREATE|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bin.WriteString("half"); err != nil {
		t.Fatal(err)
	}

	type result struct {
		dir    string
		unlock func()
	}
	got := make(chan result, 1)
	go func() {
		dir, unlock := CachedProvider(t, host, ns, typ, version)
		got <- result{dir, unlock}
	}()

	select {
	case r := <-got:
		r.unlock()
		t.Fatalf("CachedProvider returned (dir %q) while a writer held the cache lock over a half-written provider; "+
			"a render execing that file is the text-file-busy of #1699", r.dir)
	case <-time.After(750 * time.Millisecond):
	}

	if err := bin.Close(); err != nil {
		t.Fatal(err)
	}
	writerUnlock()

	select {
	case r := <-got:
		defer r.unlock()
		if r.dir != cache {
			t.Fatalf("after the writer finished, CachedProvider = %q, want the cache %q (warm)", r.dir, cache)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("CachedProvider never returned after the writer released the lock")
	}
}

// TestCachedProviderColdKeepsTheLock: a cold cache means the caller is the
// writer, so it must come back holding the lock.
func TestCachedProviderColdKeepsTheLock(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("TF_PLUGIN_CACHE_DIR", cache)

	dir, unlock := CachedProvider(t, "registry.terraform.io", "hashicorp", "aws", "6.59.0")
	if dir != "" {
		unlock()
		t.Fatalf("an empty cache was called warm: %q", dir)
	}
	if _, err := os.Stat(filepath.Join(cache, ".choudoufu-init.lock")); err != nil {
		t.Fatalf("a cold CachedProvider returned without holding the lock: %v", err)
	}
	unlock()
	if _, err := os.Stat(filepath.Join(cache, ".choudoufu-init.lock")); !os.IsNotExist(err) {
		t.Fatalf("unlock left the lock behind: %v", err)
	}
}
