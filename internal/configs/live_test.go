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

// TestModule_livePolicy pins the raw decode of the maintainer's exact
// example from GitHub issue #67's Design section: all four quadrant verbs
// set, nothing else (no tag_key/tag_value, no scope, no threshold).
func TestModule_livePolicy(t *testing.T) {
	mod, diags := testModuleFromDir("testdata/valid-modules/live-policy")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	if mod.Live == nil {
		t.Fatal("no live block was decoded")
	}
	p := mod.Live.Policy
	if p == nil {
		t.Fatal("no policy block was decoded")
	}

	for _, tc := range []struct {
		name string
		got  string
		set  bool
		want string
	}{
		{"declared_tagged", p.DeclaredTagged, p.DeclaredTaggedSet, "untag"},
		{"declared_untagged", p.DeclaredUntagged, p.DeclaredUntaggedSet, "converge"},
		{"undeclared_tagged", p.UndeclaredTagged, p.UndeclaredTaggedSet, "keep"},
		{"undeclared_untagged", p.UndeclaredUntagged, p.UndeclaredUntaggedSet, "delete"},
	} {
		if !tc.set {
			t.Errorf("%s: Set is false, want true", tc.name)
		}
		if tc.got != tc.want {
			t.Errorf("%s is %q, want %q", tc.name, tc.got, tc.want)
		}
	}

	if p.TagKeySet {
		t.Error("TagKeySet is true for a policy block that set no tag_key")
	}
	if p.TagValueSet {
		t.Error("TagValueSet is true for a policy block that set no tag_value")
	}
	if p.Scope != nil {
		t.Errorf("Scope is %#v for a policy block that set no scope block, want nil", p.Scope)
	}
	if p.ThresholdSet {
		t.Error("ThresholdSet is true for a policy block that set no threshold")
	}
}

// TestModule_livePolicyFull exercises every optional argument the policy
// block accepts: tag_key/tag_value distinct from the estate marker, a
// scope block, and a threshold.
func TestModule_livePolicyFull(t *testing.T) {
	mod, diags := testModuleFromDir("testdata/valid-modules/live-policy-full")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	p := mod.Live.Policy
	if p == nil {
		t.Fatal("no policy block was decoded")
	}

	if got, want := p.TagKey, "preserve"; got != want || !p.TagKeySet {
		t.Errorf("TagKey is %q (set=%v), want %q (set=true)", got, p.TagKeySet, want)
	}
	if got, want := p.TagValue, "yes"; got != want || !p.TagValueSet {
		t.Errorf("TagValue is %q (set=%v), want %q (set=true)", got, p.TagValueSet, want)
	}
	if got, want := p.Threshold, 25; got != want || !p.ThresholdSet {
		t.Errorf("Threshold is %d (set=%v), want %d (set=true)", got, p.ThresholdSet, want)
	}
	if p.Scope == nil {
		t.Fatal("Scope is nil for a policy block that set a scope block")
	}
	if got, want := p.Scope.Services, []string{"ec2", "s3"}; !slicesEqual(got, want) {
		t.Errorf("Scope.Services is %v, want %v", got, want)
	}
	if got, want := p.Scope.Types, []string{"aws_instance"}; !slicesEqual(got, want) {
		t.Errorf("Scope.Types is %v, want %v", got, want)
	}
	if got, want := p.Scope.Regions, []string{"us-east-1", "us-west-2"}; !slicesEqual(got, want) {
		t.Errorf("Scope.Regions is %v, want %v", got, want)
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestModule_livePolicyPartial: a policy block that sets one quadrant and
// nothing else decodes with the other three quadrants unset (Set false),
// not defaulted here - defaulting to today's fixed behavior is
// internal/live/policy.Build's job, not the decoder's.
func TestModule_livePolicyPartial(t *testing.T) {
	mod, diags := testModuleFromDir("testdata/valid-modules/live-policy-partial")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	p := mod.Live.Policy
	if p == nil {
		t.Fatal("no policy block was decoded")
	}
	if !p.DeclaredTaggedSet || p.DeclaredTagged != "keep" {
		t.Errorf("DeclaredTagged is %q (set=%v), want \"keep\" (set=true)", p.DeclaredTagged, p.DeclaredTaggedSet)
	}
	if p.DeclaredUntaggedSet || p.UndeclaredTaggedSet || p.UndeclaredUntaggedSet {
		t.Error("an omitted quadrant decoded as Set true")
	}
}

// TestModule_liveNoPolicy: a live block with no policy block at all decodes
// with a nil Policy - the same "absent means absent" rule Snapshots and
// SnapshotPath already follow.
func TestModule_liveNoPolicy(t *testing.T) {
	mod, diags := testModuleFromDir("testdata/valid-modules/live")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	if mod.Live.Policy != nil {
		t.Errorf("Policy is %#v for a live block with no policy block, want nil", mod.Live.Policy)
	}
}

// TestModule_livePolicyRefused: everything that can be wrong with a policy
// block's arguments is lexical, the same rule estate and snapshot_path
// follow, so the decoder catches all of it.
func TestModule_livePolicyRefused(t *testing.T) {
	for _, tc := range []struct {
		file string
		want string
	}{
		{"testdata/invalid-files/live-policy-non-literal-verb.tf", "Variables not allowed"},
		{"testdata/invalid-files/live-policy-bad-threshold.tf", "non-negative whole number"},
		{"testdata/invalid-files/live-policy-duplicate.tf", "Duplicate policy block"},
		{"testdata/invalid-files/live-policy-duplicate-scope.tf", "Duplicate scope block"},
		{"testdata/invalid-files/live-policy-non-list-scope.tf", "literal list of strings"},
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
