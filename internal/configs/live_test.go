// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package configs

import (
	"strings"
	"testing"
)

func TestModule_live(t *testing.T) {
	mod, diags := testModuleFromDir("testdata/valid-modules/live")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	if mod.Live == nil {
		t.Fatal("no live block was decoded")
	}
	if got, want := mod.Live.Estate, "my-estate"; got != want {
		t.Errorf("estate is %q, want %q", got, want)
	}
	if !mod.Live.EstateSet {
		t.Error("EstateSet is false for a block that set the estate")
	}
}

// TestModule_liveSnapshotPath: the optional observational snapshot
// (P4.2) is opt-in through its own literal-string argument, decoded
// alongside estate but independent of it.
func TestModule_liveSnapshotPath(t *testing.T) {
	mod, diags := testModuleFromDir("testdata/valid-modules/live-snapshot")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	if mod.Live == nil {
		t.Fatal("no live block was decoded")
	}
	if got, want := mod.Live.SnapshotPath, "snapshots/my-estate.json"; got != want {
		t.Errorf("SnapshotPath is %q, want %q", got, want)
	}
}

// TestModule_liveSnapshots: the branch carrier is opt-in through its own
// literal-bool argument. Alone it means "orphan branch in the enclosing
// repository"; combined with snapshot_path it means "branch first, file as
// the fallback", and both decode side by side rather than excluding each
// other.
func TestModule_liveSnapshots(t *testing.T) {
	mod, diags := testModuleFromDir("testdata/valid-modules/live-snapshot-branch")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	if mod.Live == nil {
		t.Fatal("no live block was decoded")
	}
	if !mod.Live.Snapshots {
		t.Error("Snapshots is false for a block that set snapshots = true")
	}
	if mod.Live.SnapshotPath != "" {
		t.Errorf("SnapshotPath is %q for a block that set none, want empty", mod.Live.SnapshotPath)
	}

	both, diags := testModuleFromDir("testdata/valid-modules/live-snapshot-both")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics for the combined block: %s", diags.Error())
	}
	if !both.Live.Snapshots {
		t.Error("Snapshots is false for the combined block")
	}
	if got, want := both.Live.SnapshotPath, "snapshots/my-estate.json"; got != want {
		t.Errorf("SnapshotPath is %q, want %q", got, want)
	}
}

// TestModule_liveSnapshotsAbsent: no attribute means false, which must mean
// the branch carrier is never touched - the same "no attribute -> nothing
// written, ever" rule snapshot_path follows.
func TestModule_liveSnapshotsAbsent(t *testing.T) {
	mod, diags := testModuleFromDir("testdata/valid-modules/live")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	if mod.Live == nil {
		t.Fatal("no live block was decoded")
	}
	if mod.Live.Snapshots {
		t.Error("Snapshots is true for a block that set no snapshots argument")
	}
}

// TestModule_liveSnapshotsRefused: like estate and snapshot_path, the
// argument must be a literal, and it must be a bool. Both refusals arrive
// from the decoder, and neither leaves Snapshots set.
func TestModule_liveSnapshotsRefused(t *testing.T) {
	for _, tc := range []struct {
		file string
		want string
	}{
		{"testdata/invalid-files/live-non-literal-snapshots.tf", "Variables not allowed"},
		{"testdata/invalid-files/live-invalid-snapshots.tf", "literal true or false"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			parser := NewParser(nil)
			_, diags := parser.LoadConfigFile(tc.file)
			if !diags.HasErrors() {
				t.Fatal("the configuration loaded with no errors")
			}
			if !strings.Contains(diags.Error(), tc.want) {
				t.Errorf("wrong diagnostic:\n%s", diags.Error())
			}
		})
	}
}

// TestModule_liveSnapshotPathAbsent: no attribute means an empty
// SnapshotPath, which is the switch that must mean no snapshot is ever
// written - the same "no attribute -> no file, ever" rule as the block
// itself relative to state.
func TestModule_liveSnapshotPathAbsent(t *testing.T) {
	mod, diags := testModuleFromDir("testdata/valid-modules/live")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	if mod.Live == nil {
		t.Fatal("no live block was decoded")
	}
	if mod.Live.SnapshotPath != "" {
		t.Errorf("SnapshotPath is %q for a block that set no snapshot_path, want empty", mod.Live.SnapshotPath)
	}
}

// TestValidateSnapshotPath is C6's regression. The argument used to be
// accepted as any non-empty literal and then handed straight to os.MkdirAll
// and os.Rename, which the audit used to destroy a real terraform.tfstate
// and to write through "../../" into a sibling project. A cache may write
// one operator-named file inside the module directory and nothing else.
func TestValidateSnapshotPath(t *testing.T) {
	for _, tc := range []struct {
		path string
		want string // a fragment of the refusal, or "" for accepted
	}{
		// Accepted: a relative path inside the module directory.
		{"snapshot.json", ""},
		{"snapshots/my-estate.json", ""},
		{"./cache/estate.json", ""},
		{"a/../b/estate.json", ""},

		// Escaping the module directory, in every spelling.
		{"../victim/terraform.tfstate", "stay inside the module directory"},
		{"../../victim/terraform.tfstate", "stay inside the module directory"},
		{"snapshots/../../victim.json", "stay inside the module directory"},
		{"..\\victim\\snapshot.json", "stay inside the module directory"},

		// Absolute, so "inside the module directory" is not even claimed.
		{"/etc/passwd", "relative path"},
		{"/tmp/snapshot.json", "relative path"},
		{"C:\\windows\\system32\\snapshot.json", "relative path"},

		// Named like a state file.
		{"terraform.tfstate", "must not name a state file"},
		{"terraform.tfstate.backup", "must not name a state file"},
		{"TERRAFORM.TFSTATE", "must not name a state file"},
		{"sub/dir/terraform.tfstate", "must not name a state file"},
		{"anything.tfstate", "must not name a state file"},

		// OpenTofu's own working directory.
		{".terraform/terraform.tfstate", "must not name a state file"},
		{".terraform/snapshot.json", "inside the .terraform directory"},

		// A directory is not a file to write.
		{".", "names the module directory itself"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			got := validateSnapshotPath(tc.path)
			switch {
			case tc.want == "" && got != "":
				t.Errorf("%q was refused: %s", tc.path, got)
			case tc.want != "" && got == "":
				t.Errorf("%q was accepted, want a refusal mentioning %q", tc.path, tc.want)
			case tc.want != "" && !strings.Contains(got, tc.want):
				t.Errorf("%q was refused with the wrong reason:\ngot:  %s\nwant it to mention: %s", tc.path, got, tc.want)
			}
		})
	}
}

// TestModule_liveSnapshotPathRefused: the refusal is a configuration error
// reaching the operator through the decoder, not a runtime surprise, and the
// path never makes it onto the Live value for anything to act on.
func TestModule_liveSnapshotPathRefused(t *testing.T) {
	for _, tc := range []struct {
		file string
		want string
	}{
		{"testdata/invalid-files/live-snapshot-path-traversal.tf", "stay inside the module directory"},
		{"testdata/invalid-files/live-snapshot-path-statefile.tf", "must not name a state file"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			parser := NewParser(nil)
			_, diags := parser.LoadConfigFile(tc.file)
			if !diags.HasErrors() {
				t.Fatal("the configuration loaded with no errors")
			}
			if !strings.Contains(diags.Error(), tc.want) {
				t.Errorf("wrong diagnostic:\n%s", diags.Error())
			}
		})
	}
}

// The estate argument is optional: without it the name is derived from the
// tofu-estate tags the configuration stamps, and the block is still the thing
// that puts the run into stateless mode.
func TestModule_liveWithoutEstate(t *testing.T) {
	mod, diags := testModuleFromDir("testdata/valid-modules/live-no-estate")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	if mod.Live == nil {
		t.Fatal("no live block was decoded")
	}
	if mod.Live.EstateSet {
		t.Error("EstateSet is true for a block that set no estate")
	}
}

// An ordinary configuration is untouched: no block, no field, no diagnostics
// about a block that is not there.
func TestModule_liveAbsent(t *testing.T) {
	mod, diags := testModuleFromDir("testdata/valid-modules/override-backend")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	if mod.Live != nil {
		t.Errorf("a configuration with no live block decoded one: %#v", mod.Live)
	}
}

func TestModule_liveConflicts(t *testing.T) {
	for _, tc := range []struct {
		dir  string
		want string
	}{
		{"testdata/invalid-modules/live-and-backend", "Both a backend and a live configuration are present"},
		{"testdata/invalid-modules/live-and-cloud", "Both a cloud and a live configuration are present"},
		{"testdata/invalid-modules/live-duplicate", "Duplicate live configuration"},
	} {
		t.Run(tc.dir, func(t *testing.T) {
			_, diags := testModuleFromDir(tc.dir)
			if !diags.HasErrors() {
				t.Fatal("no diagnostics")
			}
			if !strings.Contains(diags.Error(), tc.want) {
				t.Errorf("wrong diagnostic:\n%s", diags.Error())
			}
		})
	}
}
