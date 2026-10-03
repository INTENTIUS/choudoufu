// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package largeset

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/pins"
)

// pinnedDigests pins the rendering byte for byte (#1750: "the same inputs
// give byte-identical output, pinned by a test"). A deliberate change to the
// fixture moves these; regenerate with
//
//	env -u PWD go test ./internal/live/largeset/ -run TestRenderingIsPinned -v
//
// and read the logged digests, after reading the diff the change makes to
// a generated fixture. A digest that moved with no change to this package is
// the nondeterminism this test exists to catch.
var pinnedDigests = []struct {
	name string
	opts Options
	want string
}{
	{"local-A", Options{Estates: 5, ModuleVersion: VersionA}, "7792ce10ccbfef680b1090a7e871f65e3b23e2f4a4b2b0aa6c6bceb1ec80d369"},
	{"local-B", Options{Estates: 5, ModuleVersion: VersionB}, "6dce39ca76c8c6497524dba5c02849625ad320cac95cd976b056a3c96ddfdf5d"},
	{"oci-A-pinb", Options{Estates: 5, ModuleVersion: VersionA, Source: SourceOCI, Registry: "localhost:4890", PinB: []string{"e04", "e02"}}, "e8c00e5ddfa5ab0606d8467ad159913de90f9e085d67721d9c4a0aa797798e92"},
}

func TestRenderingIsPinned(t *testing.T) {
	for _, tc := range pinnedDigests {
		files, _, err := Render(tc.opts)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		got := Digest(files)
		t.Logf("%s: %s", tc.name, got)
		if got != tc.want {
			t.Errorf("%s rendered digest %s, pinned %s", tc.name, got, tc.want)
		}
	}
}

// TestWriteIsByteIdentical writes the same inputs to two directories and
// compares them file by file - the property, independent of the pins above.
func TestWriteIsByteIdentical(t *testing.T) {
	for _, tc := range pinnedDigests {
		a, b := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
		if _, err := Write(a, tc.opts); err != nil {
			t.Fatal(err)
		}
		if _, err := Write(b, tc.opts); err != nil {
			t.Fatal(err)
		}
		ta, tb := readTree(t, a), readTree(t, b)
		if len(ta) != len(tb) {
			t.Fatalf("%s: %d files against %d", tc.name, len(ta), len(tb))
		}
		for p, body := range ta {
			if !bytes.Equal(body, tb[p]) {
				t.Errorf("%s: %s differs between two writes of the same inputs", tc.name, p)
			}
		}
	}
}

func readTree(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p) //nolint:gosec // test temp dir
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = b
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestShapeAtFive is #1750's shape criterion, read off the manifest the
// harness itself reads: a cross-estate chain of depth 2, and an outlier whose
// module call differs from the group's.
func TestShapeAtFive(t *testing.T) {
	files, m, err := Render(Options{Estates: 5, ModuleVersion: VersionA})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Estate{}
	for _, e := range m.Estates {
		byName[e.Name] = e
	}
	var depth func(string) int
	depth = func(n string) int {
		d := 0
		for _, r := range byName[n].Reads {
			if x := 1 + depth(r); x > d {
				d = x
			}
		}
		return d
	}
	deepest := 0
	for n := range byName {
		if d := depth(n); d > deepest {
			deepest = d
		}
	}
	if deepest < 2 {
		t.Errorf("deepest cross-estate chain is %d; #1750 needs at least 2", deepest)
	}

	// Every read is a marker-filtered data source naming the producer's
	// estate, not a name convention.
	for _, e := range m.Estates {
		for _, r := range e.Reads {
			body := string(files[e.Dir+"/main.tf"])
			if !strings.Contains(body, `name   = "tag:tofu-estate"`) || !strings.Contains(body, `values = ["ls-`+r+`"]`) {
				t.Errorf("%s reads %s but its main.tf has no tofu-estate filter naming ls-%s", e.Name, r, r)
			}
		}
	}

	// The apply order puts every producer first.
	pos := map[string]int{}
	for i, d := range m.ApplyOrder {
		pos[d] = i
	}
	for _, e := range m.Estates {
		for _, r := range e.Reads {
			if pos[byName[r].Dir] > pos[e.Dir] {
				t.Errorf("apply order puts %s before %s, which it reads", e.Name, r)
			}
		}
	}

	outliers := 0
	for _, e := range m.Estates {
		if e.Role == RoleOutlier {
			outliers++
			if !strings.Contains(string(files[e.Dir+"/main.tf"]), "with_queue = true") {
				t.Errorf("outlier %s does not pass with_queue", e.Name)
			}
		}
	}
	if outliers == 0 {
		t.Error("no outlier estate at N=5")
	}

	a := string(ModulePackage(VersionA)["main.tf"])
	b := string(ModulePackage(VersionB)["main.tf"])
	if a == b {
		t.Fatal("module versions A and B render identically; the bump would move nothing")
	}
	if !strings.Contains(b, `resource "aws_sqs_queue" "events"`) || strings.Contains(a, `resource "aws_sqs_queue" "events"`) {
		t.Error("version B should add the events queue and version A should not")
	}
}

// TestOCIPinsASubset: -pin-b moves exactly the named roots to B's tag.
func TestOCIPinsASubset(t *testing.T) {
	files, m, err := Render(Options{Estates: 5, ModuleVersion: VersionA, Source: SourceOCI, Registry: "localhost:4890", PinB: []string{"e04", "e02", "e02"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["modules/shared/main.tf"]; ok {
		t.Error("the OCI variant wrote a local module directory")
	}
	for _, e := range m.Estates {
		wantTag := "1.0.0"
		if e.Name == "e02" || e.Name == "e04" {
			wantTag = "1.1.0"
		}
		want := `source = "oci://localhost:4890/largeset/shared?tag=` + wantTag + `"`
		if !strings.Contains(string(files[e.Dir+"/main.tf"]), want) {
			t.Errorf("%s: main.tf lacks %s", e.Name, want)
		}
	}
	if strings.Join(m.PinB, ",") != "e02,e04" {
		t.Errorf("pin_b recorded as %v, want sorted and deduplicated [e02 e04]", m.PinB)
	}
}

func TestOptionsAreRefused(t *testing.T) {
	for name, o := range map[string]Options{
		"zero estates":       {Estates: 0, ModuleVersion: VersionA},
		"bad version":        {Estates: 5, ModuleVersion: "C"},
		"oci no registry":    {Estates: 5, ModuleVersion: VersionA, Source: SourceOCI},
		"pin-b out of range": {Estates: 5, ModuleVersion: VersionA, Source: SourceOCI, Registry: "r:1", PinB: []string{"e06"}},
		"pin-b on local":     {Estates: 5, ModuleVersion: VersionA, PinB: []string{"e02"}},
		"bad prefix":         {Estates: 5, ModuleVersion: VersionA, Prefix: "Bad_Prefix"},
	} {
		if _, _, err := Render(o); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// TestRegenerationKeepsWhatItDoesNotOwn: a bump regenerates in place over
// applied estates, so .terraform and record stores must survive it, and a
// smaller N must remove the estates it no longer renders.
func TestRegenerationKeepsWhatItDoesNotOwn(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fx")
	if _, err := Write(dir, Options{Estates: 5, ModuleVersion: VersionA}); err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(dir, "estates", "e01", ".terraform", "marker")
	if err := os.MkdirAll(filepath.Dir(kept), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kept, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(dir, Options{Estates: 4, ModuleVersion: VersionB}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Errorf("regeneration removed a file it does not own: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "estates", "e05", "main.tf")); err == nil {
		t.Error("regenerating at N=4 left e05's main.tf behind")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "modules", "shared", "main.tf")) //nolint:gosec // test temp dir
	if !strings.Contains(string(b), "version B") {
		t.Error("the module was not rewritten at B")
	}

	foreign := t.TempDir()
	if err := os.WriteFile(filepath.Join(foreign, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(foreign, Options{Estates: 5, ModuleVersion: VersionA}); err == nil {
		t.Error("wrote a fixture over a non-empty directory with no fixture.json")
	}
}

func TestProviderVersionMatchesThePin(t *testing.T) {
	if ProviderVersion != pins.AWSProviderVersion {
		t.Errorf("fixture pins hashicorp/aws %s, internal/live/pins pins %s", ProviderVersion, pins.AWSProviderVersion)
	}
}

// TestModuleZipIsAStockModulePackage: one main.tf in a deterministic zip.
func TestModuleZipIsAStockModulePackage(t *testing.T) {
	a1, err := ModuleZip(VersionA)
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := ModuleZip(VersionA)
	if !bytes.Equal(a1, a2) {
		t.Error("two zips of version A differ; the published digest would move on every publish")
	}
	r, err := zip.NewReader(bytes.NewReader(a1), int64(len(a1)))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.File) != 1 || r.File[0].Name != "main.tf" {
		t.Errorf("zip holds %d files, first %q; want exactly main.tf", len(r.File), r.File[0].Name)
	}
}
