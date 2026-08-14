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

// TestModule_liveSnapshotArgumentsRemoved: issue #109 removed observational
// snapshots, and a configuration still carrying either of the two arguments
// that configured them gets the authored removal error - naming what the
// argument did, why the subsystem is gone, and where the surviving piece
// (guided discovery's hint) went - rather than HCL's generic "Unsupported
// argument".
func TestModule_liveSnapshotArgumentsRemoved(t *testing.T) {
	for _, tc := range []struct {
		file string
	}{
		{"testdata/invalid-files/live-snapshots-removed.tf"},
		{"testdata/invalid-files/live-snapshot-path-removed.tf"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			parser := NewParser(nil)
			_, diags := parser.LoadConfigFile(tc.file)
			if !diags.HasErrors() {
				t.Fatal("the configuration loaded with no errors")
			}
			for _, want := range []string{
				"Observational snapshots were removed",
				"Remove the argument",
				"record_store",
			} {
				if !strings.Contains(diags.Error(), want) {
					t.Errorf("the removal error does not mention %q:\n%s", want, diags.Error())
				}
			}
		})
	}
}

// TestValidateRecordStorePath is C6's regression, inherited from the
// removed snapshot_path argument (issue #109): the unchecked version of
// this rule set was used by an audit to destroy a real terraform.tfstate
// and to write through "../../" into a sibling project. The local record
// store may write inside one operator-named directory in the module
// directory and nowhere else.
func TestValidateRecordStorePath(t *testing.T) {
	for _, tc := range []struct {
		path string
		want string // a fragment of the refusal, or "" for accepted
	}{
		// Accepted: a relative path inside the module directory.
		{"records", ""},
		{".tofu-records", ""},
		{"records/my-estate", ""},
		{"./cache/records", ""},
		{"a/../b/records", ""},

		// Escaping the module directory, in every spelling.
		{"../victim/terraform.tfstate", "stay inside the module directory"},
		{"../../victim/records", "stay inside the module directory"},
		{"records/../../victim", "stay inside the module directory"},
		{"..\\victim\\records", "stay inside the module directory"},

		// Absolute, so "inside the module directory" is not even claimed.
		{"/etc/passwd", "relative path"},
		{"/tmp/records", "relative path"},
		{"C:\\windows\\system32\\records", "relative path"},

		// Named like a state file.
		{"terraform.tfstate", "must not name a state file"},
		{"terraform.tfstate.backup", "must not name a state file"},
		{"TERRAFORM.TFSTATE", "must not name a state file"},
		{"sub/dir/terraform.tfstate", "must not name a state file"},
		{"anything.tfstate", "must not name a state file"},

		// OpenTofu's own working directory.
		{".terraform/terraform.tfstate", "must not name a state file"},
		{".terraform/records", "inside the .terraform directory"},

		// The module directory itself is not a directory to hand over.
		{".", "names the module directory itself"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			got := validateRecordStorePath(tc.path)
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
// with a nil Policy - the same "absent means absent" rule the record_store
// block follows.
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
// block's arguments is lexical, the same rule estate follows, so the
// decoder catches all of it.
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

// TestModule_liveRecordStore covers GitHub issue #73's config surface: a
// "record_store" block nested inside "live", labeled by backend, phrased in
// the same labeled-block-names-the-implementation shape a stock "backend"
// block uses.
func TestModule_liveRecordStore(t *testing.T) {
	t.Run("local", func(t *testing.T) {
		mod, diags := testModuleFromDir("testdata/valid-modules/live-record-store-local")
		if diags.HasErrors() {
			t.Fatalf("unexpected diagnostics: %s", diags.Error())
		}
		rs := mod.Live.RecordStore
		if rs == nil {
			t.Fatal("no record_store block was decoded")
		}
		if rs.Type != "local" {
			t.Errorf("Type = %q, want local", rs.Type)
		}
		if got, want := rs.Path, ".tofu-records"; got != want {
			t.Errorf("Path = %q, want %q", got, want)
		}
		if rs.BucketSet || rs.KeyPrefixSet || rs.RegionSet {
			t.Errorf("local record_store carries bucket/key_prefix/region: %+v", rs)
		}
	})

	t.Run("ssm", func(t *testing.T) {
		mod, diags := testModuleFromDir("testdata/valid-modules/live-record-store-ssm")
		if diags.HasErrors() {
			t.Fatalf("unexpected diagnostics: %s", diags.Error())
		}
		rs := mod.Live.RecordStore
		if rs == nil {
			t.Fatal("no record_store block was decoded")
		}
		if rs.Type != "ssm" {
			t.Errorf("Type = %q, want ssm", rs.Type)
		}
		if got, want := rs.KeyPrefix, "custom/prefix"; got != want {
			t.Errorf("KeyPrefix = %q, want %q", got, want)
		}
		if got, want := rs.Region, "us-west-2"; got != want {
			t.Errorf("Region = %q, want %q", got, want)
		}
		if rs.PathSet || rs.BucketSet {
			t.Errorf("ssm record_store carries path/bucket: %+v", rs)
		}
	})

	t.Run("s3", func(t *testing.T) {
		mod, diags := testModuleFromDir("testdata/valid-modules/live-record-store-s3")
		if diags.HasErrors() {
			t.Fatalf("unexpected diagnostics: %s", diags.Error())
		}
		rs := mod.Live.RecordStore
		if rs == nil {
			t.Fatal("no record_store block was decoded")
		}
		if rs.Type != "s3" {
			t.Errorf("Type = %q, want s3", rs.Type)
		}
		if got, want := rs.Bucket, "my-records-bucket"; got != want {
			t.Errorf("Bucket = %q, want %q", got, want)
		}
	})
}

// TestModule_liveRecordStoreAbsent: no record_store block leaves
// Live.RecordStore nil, which is what internal/live/lint reads as "GitHub
// issue #73's RECORD_ADMITTED logical types stay refused" - the same
// "no attribute -> nothing" contract every other optional live-block
// feature follows.
func TestModule_liveRecordStoreAbsent(t *testing.T) {
	mod, diags := testModuleFromDir("testdata/valid-modules/live")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	if mod.Live == nil {
		t.Fatal("no live block was decoded")
	}
	if mod.Live.RecordStore != nil {
		t.Errorf("RecordStore is %+v for a live block with no record_store, want nil", mod.Live.RecordStore)
	}
}

// TestModule_liveRecordStoreRefused covers every decode-time refusal: an
// unknown backend label, "s3" with no bucket, and a key_prefix that would
// collide with the receipts namespace (live/RECEIPTS.md).
func TestModule_liveRecordStoreRefused(t *testing.T) {
	for _, tc := range []struct {
		file string
		want string
	}{
		{"testdata/invalid-files/live-record-store-unknown-backend.tf", `names a backend this fork does not know`},
		{"testdata/invalid-files/live-record-store-s3-no-bucket.tf", `requires a "bucket" argument`},
		{"testdata/invalid-files/live-record-store-key-prefix-receipts.tf", `must not begin with the "tofu-receipts" segment`},
		{"testdata/invalid-files/live-record-store-key-prefix-hints.tf", `must not begin with the "tofu-hints" segment`},
		{"testdata/invalid-files/live-record-store-duplicate.tf", "Duplicate record_store block"},
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

// TestValidateRecordStoreKeyPrefix is the disjointness rule GitHub issue
// #73's namespace-safety requirement rests on at the config layer: a
// key_prefix override can never land inside live/RECEIPTS.md's
// "/tofu-receipts/" namespace, nor inside guided discovery's "tofu-hints/"
// namespace (issue #109), checked at the "/"-delimited segment level so a
// merely-similar-looking prefix ("tofu-receipts-archive") is not falsely
// refused.
func TestValidateRecordStoreKeyPrefix(t *testing.T) {
	for _, tc := range []struct {
		prefix string
		want   string // a fragment of the refusal, or "" for accepted
	}{
		{"my-estate", ""},
		{"tofu-records/my-estate", ""},
		{"/tofu-records/my-estate/", ""},
		// A prefix that merely starts with the same letters is not a
		// segment match and must not be refused.
		{"tofu-receipts-archive", ""},
		{"nested/tofu-receipts", ""},
		{"tofu-hints-archive", ""},
		{"nested/tofu-hints", ""},

		{"tofu-receipts", "must not begin with the \"tofu-receipts\" segment"},
		{"tofu-receipts/my-estate", "must not begin with the \"tofu-receipts\" segment"},
		{"/tofu-receipts/my-estate", "must not begin with the \"tofu-receipts\" segment"},

		{"tofu-hints", "must not begin with the \"tofu-hints\" segment"},
		{"tofu-hints/my-estate", "must not begin with the \"tofu-hints\" segment"},
		{"/tofu-hints/my-estate", "must not begin with the \"tofu-hints\" segment"},

		{"", "empty"},
		{"///", "empty"},
	} {
		t.Run(tc.prefix, func(t *testing.T) {
			got := validateRecordStoreKeyPrefix(tc.prefix)
			switch {
			case tc.want == "" && got != "":
				t.Errorf("%q was refused: %s", tc.prefix, got)
			case tc.want != "" && got == "":
				t.Errorf("%q was accepted, want a refusal mentioning %q", tc.prefix, tc.want)
			case tc.want != "" && !strings.Contains(got, tc.want):
				t.Errorf("%q was refused with the wrong reason:\ngot:  %s\nwant it to mention: %s", tc.prefix, got, tc.want)
			}
		})
	}
}
