// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

// TestOverlayFSReturnsNilForNothing is the guard that keeps [Load] on the
// path it has always been on. configs.NewParser reads nil as afero.OsFs, so
// an empty overlay puts no wrapper between the loader and the disk at all -
// which is what makes "the published-form numbers cannot move" a structural
// claim rather than a hope.
func TestOverlayFSReturnsNilForNothing(t *testing.T) {
	if fs := overlayFS(nil); fs != nil {
		t.Errorf("overlayFS(nil) = %T, want nil", fs)
	}
	if fs := overlayFS(map[string][]byte{}); fs != nil {
		t.Errorf("overlayFS(empty) = %T, want nil", fs)
	}
	if fs := overlayFS(map[string][]byte{"a": {}}); fs == nil {
		t.Error("overlayFS with content returned nil, so the overlay would never reach the parser")
	}
}

// TestOverlayFSMergesTheDirectory pins the two properties the loader depends
// on. A listing that missed the added file would silently drop the live
// sidecar - the whole edit - and the measurement would report that onboarding
// changes nothing. A listing that returned the replaced file twice would make
// the loader read one module's backend block twice.
func TestOverlayFSMergesTheDirectory(t *testing.T) {
	dir := t.TempDir()
	for name, src := range map[string]string{
		"main.tf":     "original\n",
		"versions.tf": "versions\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	fs := afero.Afero{Fs: overlayFS(map[string][]byte{
		filepath.Join(dir, "main.tf"):         []byte("rewritten\n"),
		filepath.Join(dir, "estate.chdf.hcl"): []byte("added\n"),
	})}

	infos, err := fs.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, i := range infos {
		names = append(names, i.Name())
	}
	sort.Strings(names)
	if got, want := strings.Join(names, " "), "estate.chdf.hcl main.tf versions.tf"; got != want {
		t.Errorf("listing = %q, want %q", got, want)
	}

	for name, want := range map[string]string{
		"main.tf":         "rewritten\n",
		"versions.tf":     "versions\n",
		"estate.chdf.hcl": "added\n",
	} {
		src, err := fs.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("read %s: %v", name, err)
			continue
		}
		if string(src) != want {
			t.Errorf("%s = %q, want %q", name, src, want)
		}
	}

	if _, err := fs.Stat(filepath.Join(dir, "absent.tf")); !os.IsNotExist(err) {
		t.Errorf("a path in neither layer: err = %v, want a not-exist error", err)
	}
}

// TestLoadOverlayWithNothingIsLoad. The two entry points must agree on an
// empty overlay or every published number in this repository moves the day
// somebody routes a caller through the new one.
func TestLoadOverlayWithNothingIsLoad(t *testing.T) {
	dir := filepath.Join("..", "lint", "testdata", "count-index")
	ctx := t.Context()

	plain := Analyze(ctx, Load(ctx, dir).Config, Context{})
	overlaid := Analyze(ctx, LoadOverlay(ctx, dir, nil).Config, Context{})

	if plain.Blocked() != overlaid.Blocked() || plain.Sites() != overlaid.Sites() || len(plain.Findings) != len(overlaid.Findings) {
		t.Fatalf("Load and LoadOverlay(nil) disagree: blocked %v/%v sites %d/%d findings %d/%d",
			plain.Blocked(), overlaid.Blocked(), plain.Sites(), overlaid.Sites(), len(plain.Findings), len(overlaid.Findings))
	}
	for i := range plain.Findings {
		if plain.Findings[i].ID != overlaid.Findings[i].ID {
			t.Errorf("finding %d: %q vs %q", i, plain.Findings[i].ID, overlaid.Findings[i].ID)
		}
	}
}

// TestLoadOverlayReadsTheOverlay, in the smallest possible form: a directory
// with nothing in it and a whole module in the overlay. If the overlay were
// not reaching configs.Parser this would load nothing and the assertion below
// would be the only thing to notice.
func TestLoadOverlayReadsTheOverlay(t *testing.T) {
	dir := t.TempDir()
	res := LoadOverlay(t.Context(), dir, map[string][]byte{
		filepath.Join(dir, "main.tf"): []byte("resource \"aws_s3_bucket\" \"b\" {\n  bucket = \"x\"\n}\n"),
	})
	if res.Config == nil {
		t.Fatalf("nothing loaded: %s", res.Diags.Error())
	}
	if n := len(res.Config.Module.ManagedResources); n != 1 {
		t.Errorf("%d managed resource(s), want 1", n)
	}
}
