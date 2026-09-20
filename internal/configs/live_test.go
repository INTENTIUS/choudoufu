// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package configs

import (
	"path/filepath"
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

// TestModule_liveSidecar is GitHub issue #72's happy path: a directory whose
// .tf files carry nothing choudoufu-specific, with the live configuration in
// the estate.chdf.hcl sidecar file instead. The sidecar's body is the live
// block's content, decoded by the same decoder, so nested blocks come
// through identically.
func TestModule_liveSidecar(t *testing.T) {
	mod, diags := testModuleFromDir("testdata/valid-modules/live-sidecar")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	if mod.Live == nil {
		t.Fatal("no live configuration was decoded from the sidecar")
	}
	if !mod.Live.Sidecar {
		t.Error("Sidecar is false for a live configuration read from the sidecar file")
	}
	if got, want := mod.Live.Estate, "my-estate"; got != want || !mod.Live.EstateSet {
		t.Errorf("estate is %q (set=%v), want %q (set=true)", got, mod.Live.EstateSet, want)
	}
	rs := mod.Live.RecordStore
	if rs == nil {
		t.Fatal("the sidecar's record_store block was not decoded")
	}
	if rs.Type != "local" || rs.Path != ".tofu-records" {
		t.Errorf("record_store decoded as %+v, want local/.tofu-records", rs)
	}
	if got, want := mod.Live.DeclRange.Filename, "testdata/valid-modules/live-sidecar/"+LiveSidecarFilename; filepath.ToSlash(got) != want {
		t.Errorf("DeclRange filename is %q, want %q", got, want)
	}
	// Issue #732's toggle rides the same shared decoder, so the sidecar
	// form pins it too (review nit on #737: only the block form was
	// tested).
	if got, want := mod.Live.Reads, "full"; got != want {
		t.Errorf("reads is %q from the sidecar, want %q", got, want)
	}
}

// TestModule_liveSidecarSelectiveBackendWall is the hazard the maintainer
// named on issue #72: SelectiveLoadBackend deliberately carries Lives
// alongside Backends and CloudConfigs so the backend-refusal wall in
// Module.appendFile can see all three in one load - the load every command
// performs before it would reach for a state manager. The sidecar must be
// visible under that same selective load, or a sidecar user's backend block
// would sail past the wall and a command would touch state while believing
// it is stateless.
func TestModule_liveSidecarSelectiveBackendWall(t *testing.T) {
	parser := NewParser(nil)
	_, diags := parser.LoadConfigDirSelective("testdata/invalid-modules/live-sidecar-and-backend", RootModuleCallForTesting(), SelectiveLoadBackend)
	if !diags.HasErrors() {
		t.Fatal("a sidecar live configuration beside a backend block loaded with no errors under SelectiveLoadBackend")
	}
	if !strings.Contains(diags.Error(), "Both a backend and a live configuration are present") {
		t.Errorf("the backend wall did not fire for a sidecar live configuration:\n%s", diags.Error())
	}
}

// TestModule_liveSidecarSelectiveBackendVisible is the positive half of the
// wall test: a selective backend load of a sidecar-only configuration
// surfaces the Live, which is what puts plain plan and apply into stateless
// mode before any state manager is built.
func TestModule_liveSidecarSelectiveBackendVisible(t *testing.T) {
	for name, load := range map[string]SelectiveLoader{
		"SelectiveLoadBackend": SelectiveLoadBackend,
		"SelectiveLoadAll":     SelectiveLoadAll,
	} {
		t.Run(name, func(t *testing.T) {
			parser := NewParser(nil)
			mod, diags := parser.LoadConfigDirSelective("testdata/valid-modules/live-sidecar", RootModuleCallForTesting(), load)
			if diags.HasErrors() {
				t.Fatalf("unexpected diagnostics: %s", diags.Error())
			}
			if mod.Live == nil || !mod.Live.Sidecar {
				t.Fatalf("the sidecar live configuration is not visible under %s: %+v", name, mod.Live)
			}
		})
	}

	t.Run("LoadConfigDirUneval", func(t *testing.T) {
		parser := NewParser(nil)
		mod, diags := parser.LoadConfigDirUneval("testdata/valid-modules/live-sidecar", SelectiveLoadAll)
		if diags.HasErrors() {
			t.Fatalf("unexpected diagnostics: %s", diags.Error())
		}
		if mod.Live == nil || !mod.Live.Sidecar {
			t.Fatalf("the sidecar live configuration is not visible under LoadConfigDirUneval: %+v", mod.Live)
		}
	})

	t.Run("LoadConfigDirWithTests", func(t *testing.T) {
		parser := NewParser(nil)
		mod, diags := parser.LoadConfigDirWithTests("testdata/valid-modules/live-sidecar", DefaultTestDirectory, RootModuleCallForTesting())
		if diags.HasErrors() {
			t.Fatalf("unexpected diagnostics: %s", diags.Error())
		}
		if mod.Live == nil || !mod.Live.Sidecar {
			t.Fatalf("the sidecar live configuration is not visible under LoadConfigDirWithTests: %+v", mod.Live)
		}
	})
}

// TestModule_liveSidecarAndBlockConflict: the sidecar and the in-terraform{}
// form are two spellings of one configuration, so a module carrying both has
// two sources of truth and is refused with an error naming both places -
// not the duplicate-block error meant for two live blocks in .tf files.
func TestModule_liveSidecarAndBlockConflict(t *testing.T) {
	_, diags := testModuleFromDir("testdata/invalid-modules/live-sidecar-and-block")
	if !diags.HasErrors() {
		t.Fatal("a module with both a sidecar and a live block loaded with no errors")
	}
	for _, want := range []string{
		"Both a live sidecar file and a live block are present",
		LiveSidecarFilename,
		"main.tf",
	} {
		if !strings.Contains(diags.Error(), want) {
			t.Errorf("the conflict error does not mention %q:\n%s", want, diags.Error())
		}
	}
}

// TestModule_liveSidecarSnapshotTombstone: the sidecar goes through the same
// decodeLiveBody as the in-terraform{} form, so issue #109's tombstones for
// the removed snapshot arguments fire there too, with the authored removal
// error rather than HCL's generic unsupported-argument one.
func TestModule_liveSidecarSnapshotTombstone(t *testing.T) {
	_, diags := testModuleFromDir("testdata/invalid-modules/live-sidecar-snapshots-removed")
	if !diags.HasErrors() {
		t.Fatal("a sidecar carrying the removed snapshots argument loaded with no errors")
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
		if rs.BucketSet || rs.KeyPrefixSet || rs.RegionSet || rs.BucketOwnerSet {
			t.Errorf("local record_store carries bucket/key_prefix/region/bucket_owner: %+v", rs)
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
		// Carried over from the Parameter Store fixture GitHub issue #1346
		// retired, which was the only one that set these two.
		if got, want := rs.KeyPrefix, "custom/prefix"; got != want {
			t.Errorf("KeyPrefix = %q, want %q", got, want)
		}
		if got, want := rs.Region, "us-west-2"; got != want {
			t.Errorf("Region = %q, want %q", got, want)
		}
		if rs.PathSet {
			t.Errorf("s3 record_store carries a path: %+v", rs)
		}
		// Optional, and unset here, so a build that defaulted it to something
		// would be caught: an ExpectedBucketOwner nobody wrote would refuse
		// every request against a bucket in another account, including the
		// ordinary case of one shared on purpose.
		if rs.BucketOwnerSet || rs.BucketOwner != "" {
			t.Errorf("bucket_owner is set on a block that does not name one: %+v", rs)
		}
	})

	// GitHub issue #1392. The third backend: Secrets in a cluster namespace,
	// with the connection block spelled the way the stock kubernetes backend
	// and the hashicorp/kubernetes provider spell it.
	t.Run("kubernetes", func(t *testing.T) {
		mod, diags := testModuleFromDir("testdata/valid-modules/live-record-store-kubernetes")
		if diags.HasErrors() {
			t.Fatalf("unexpected diagnostics: %s", diags.Error())
		}
		rs := mod.Live.RecordStore
		if rs == nil {
			t.Fatal("no record_store block was decoded")
		}
		if rs.Type != "kubernetes" {
			t.Errorf("Type = %q, want kubernetes", rs.Type)
		}
		if got, want := rs.Namespace, "tofu-records-my-estate"; got != want {
			t.Errorf("Namespace = %q, want %q", got, want)
		}
		if !rs.NamespaceSet {
			t.Error("NamespaceSet is false for a block that names a namespace")
		}
		if got, want := rs.Kubernetes.ConfigPath, "/home/ci/.kube/config"; got != want {
			t.Errorf("ConfigPath = %q, want %q", got, want)
		}
		if got, want := rs.Kubernetes.ConfigContext, "prod"; got != want {
			t.Errorf("ConfigContext = %q, want %q", got, want)
		}
		if got, want := rs.Kubernetes.Host, "https://cluster.example:6443"; got != want {
			t.Errorf("Host = %q, want %q", got, want)
		}
		if rs.Kubernetes.Insecure {
			t.Error("Insecure = true for a block that set it to false")
		}
		if rs.Kubernetes.Exec == nil {
			t.Fatal("the exec block was not decoded")
		}
		if got, want := rs.Kubernetes.Exec.Command, "aws"; got != want {
			t.Errorf("Exec.Command = %q, want %q", got, want)
		}
		if got, want := len(rs.Kubernetes.Exec.Args), 4; got != want {
			t.Errorf("Exec.Args has %d elements, want %d", got, want)
		}
		if got, want := rs.Kubernetes.Exec.Env["AWS_PROFILE"], "ci"; got != want {
			t.Errorf("Exec.Env[AWS_PROFILE] = %q, want %q", got, want)
		}
		if rs.BucketSet || rs.RegionSet || rs.BucketOwnerSet || rs.PathSet {
			t.Errorf("kubernetes record_store carries bucket/region/bucket_owner/path: %+v", rs)
		}
	})

	// GitHub issue #1381. A bucket name is global: a name that is free can be
	// taken by anyone, in any account, so the name alone does not say whose
	// bucket this is.
	t.Run("s3 with bucket_owner", func(t *testing.T) {
		mod, diags := testModuleFromDir("testdata/valid-modules/live-record-store-s3-bucket-owner")
		if diags.HasErrors() {
			t.Fatalf("unexpected diagnostics: %s", diags.Error())
		}
		rs := mod.Live.RecordStore
		if rs == nil {
			t.Fatal("no record_store block was decoded")
		}
		if got, want := rs.BucketOwner, "111122223333"; got != want {
			t.Errorf("BucketOwner = %q, want %q", got, want)
		}
		if !rs.BucketOwnerSet {
			t.Error("BucketOwnerSet is false for a block that names an owner")
		}
		if rs.BucketOwnerRange.Empty() {
			t.Error("BucketOwnerRange is empty, so a diagnostic about it would point nowhere")
		}
	})
}

// TestModule_liveRecordStoreRetired is GitHub issue #1346: Parameter Store
// is refused as a record store, by name, and the message carries the four
// things an operator who still declares it needs - that it is retired, why,
// what to declare instead, and that nothing migrates. It also holds the two
// things the message must NOT say: that SSM as such is removed, and that SSM
// is available for secrets today.
func TestModule_liveRecordStoreRetired(t *testing.T) {
	for _, file := range []string{
		"testdata/invalid-files/live-record-store-ssm-retired.tf",
		"testdata/invalid-files/live-record-store-ssm-retired-with-tier.tf",
	} {
		t.Run(file, func(t *testing.T) {
			_, diags := NewParser(nil).LoadConfigFile(file)
			if !diags.HasErrors() {
				t.Fatal("record_store \"ssm\" loaded with no errors")
			}
			if len(diags) != 1 {
				t.Errorf("want exactly one diagnostic, the refusal; a retired backend's arguments are not worth a second one. Got %d:\n%s", len(diags), diags.Error())
			}
			got := diags.Error()
			if !strings.Contains(got, SummaryRecordStoreRetired) {
				t.Errorf("the refusal is not the retirement one:\n%s", got)
			}
			for _, want := range []string{
				`record_store "ssm" is retired`,
				"as a record store",
				"cap at 10,000 per account and region",
				"no general conditional write",
				`record_store "s3"`,
				"examples/record-store-bucket",
				"not migrated",
				"no estate was on this backend",
				"planned (#1244) and not available yet",
				`strict { secrets = "refuse" }`,
			} {
				if !strings.Contains(got, want) {
					t.Errorf("the refusal does not say %q:\n%s", want, got)
				}
			}
			// The quota sentence used to end "past that every parameter is
			// billed monthly on the advanced tier", which reads as every
			// parameter in the account being billed. Only the ones put on
			// the advanced tier are; standard parameters are free at any
			// number up to the cap. GitHub issue #1383.
			for _, never := range []string{"SSM is removed", "SSM is retired", "names a backend this fork does not know", "every parameter is billed"} {
				if strings.Contains(got, never) {
					t.Errorf("the refusal says %q:\n%s", never, got)
				}
			}
		})
	}
}

// TestModule_liveRecordStoreImplied is GitHub issue #364's config surface,
// and the whole of its mechanism: a live block with no record_store block
// gets the implied local one, so that HANDOFF.md's "a configuration that
// works on stock OpenTofu works here with a live block added and nothing
// else" is true by construction rather than by review.
//
// Every field is asserted BY VALUE rather than "it is non-nil", because the
// values are what internal/live/projection.NewRecordStore then acts on: an
// empty Path is what resolves to ".tofu-records" beside the module, and a
// Type of anything but "local" would send this to the S3 branch and
// try to open an AWS client for a configuration that named no cloud store
// at all.
//
// This test replaced TestModule_liveRecordStoreAbsent, which pinned the
// opposite ("RecordStore is nil for a live block with no record_store").
// That was the behavior #364 exists to change; the nil case that survives
// is a module with no live block, pinned by
// TestModule_noLiveBlockImpliesNoRecordStore below.
func TestModule_liveRecordStoreImplied(t *testing.T) {
	mod, diags := testModuleFromDir("testdata/valid-modules/live")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	if mod.Live == nil {
		t.Fatal("no live block was decoded")
	}
	rs := mod.Live.RecordStore
	if rs == nil {
		t.Fatal("RecordStore is nil for a live block with no record_store; issue #364 says the local store is implied")
	}
	if got, want := rs.Type, "local"; got != want {
		t.Errorf("Type = %q, want %q", got, want)
	}
	if !rs.Implied {
		t.Errorf("Implied = false for a store nobody declared: %+v", rs)
	}
	if rs.Path != "" || rs.PathSet {
		t.Errorf("Path = %q (set=%v), want the empty default so projection.NewRecordStore resolves it to .tofu-records beside the module", rs.Path, rs.PathSet)
	}
	if rs.BucketSet || rs.KeyPrefixSet || rs.RegionSet {
		t.Errorf("the implied store carries bucket/key_prefix/region, which only the cloud backends have: %+v", rs)
	}
	// The implied store points at the live block, which is the nearest
	// thing the author actually wrote, so a diagnostic about it lands
	// somewhere real instead of on a zero range.
	if rs.DeclRange != mod.Live.DeclRange {
		t.Errorf("DeclRange = %s, want the live block's own header %s", rs.DeclRange, mod.Live.DeclRange)
	}
}

// TestModule_liveRecordStoreDeclaredIsNeverImplied is the other half of
// [LiveRecordStore.Implied]: a store the author wrote out carries
// Implied=false, whatever its backend. internal/live/lint's strict-markers
// check is the one reader that tells the two apart, and it refuses on
// Implied being true - so a declared store mis-flagged as implied would
// refuse a configuration that is correct.
func TestModule_liveRecordStoreDeclaredIsNeverImplied(t *testing.T) {
	for _, dir := range []string{
		"testdata/valid-modules/live-record-store-local",
		"testdata/valid-modules/live-record-store-s3",
	} {
		mod, diags := testModuleFromDir(dir)
		if diags.HasErrors() {
			t.Fatalf("%s: unexpected diagnostics: %s", dir, diags.Error())
		}
		if mod.Live.RecordStore.Implied {
			t.Errorf("%s: a declared record_store block carries Implied=true", dir)
		}
	}
}

// TestModule_noLiveBlockImpliesNoRecordStore is the one nil case left. A
// module with no live block has no estate, so there is nothing to imply a
// store for, and every reader that treats a nil RecordStore as "no store"
// (internal/live/lint's recordStoreConfiguredIn, internal/live/identity's
// copy of it, internal/command's three NewRecordStore call sites) keeps
// getting exactly that answer for exactly this configuration. `live-check`
// analysing an unadopted stock configuration is the case that reaches it.
func TestModule_noLiveBlockImpliesNoRecordStore(t *testing.T) {
	mod, diags := testModuleFromDir("testdata/valid-modules/empty")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	if mod.Live != nil {
		t.Fatalf("this fixture is supposed to have no live block, but decoded %+v; the assertion below would be vacuous", mod.Live)
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
		{"testdata/invalid-files/live-record-store-key-prefix-located.tf", `must not begin with the "tofu-located" segment`},
		{"testdata/invalid-files/live-record-store-key-prefix-residue.tf", `must not begin with the "tofu-residue" segment`},
		{"testdata/invalid-files/live-record-store-key-prefix-provisioned.tf", `must not begin with the "tofu-provisioned" segment`},
		{"testdata/invalid-files/live-record-store-key-prefix-outputs.tf", `must not begin with the "tofu-outputs" segment`},
		// GitHub issue #1381. These three go through the real decoder, not
		// just validateRecordStoreKeyPrefix, because the estate name has to
		// reach the validator from the surrounding live block for any of
		// them to be decidable at all.
		{"testdata/invalid-files/live-record-store-key-prefix-another-estate.tf", `is the namespace estate "other" writes its own records to`},
		{"testdata/invalid-files/live-record-store-key-prefix-records-root.tf", `was set to the "tofu-records" root itself`},
		{"testdata/invalid-files/live-record-store-key-prefix-no-estate.tf", `does not say which estate it owns`},
		// GitHub issue #1383. A leading slash used to pass here and fail
		// every run afterwards, with a message about a record key and never
		// about key_prefix.
		{"testdata/invalid-files/live-record-store-key-prefix-leading-slash.tf", `must not begin with "/"`},
		// "ssm-tier" was never a backend label at any release; "tier" was an
		// argument of record_store "ssm". It gets the unknown-backend
		// refusal, not the retirement one, which would tell a reader it
		// existed once (#1383).
		{"testdata/invalid-files/live-record-store-ssm-tier-is-not-a-backend.tf", `names a backend this fork does not know`},
		{"testdata/invalid-files/live-record-store-duplicate.tf", "Duplicate record_store block"},
		// "tier" selected a Parameter Store tier and went with that backend
		// (GitHub issue #1346). It is now simply not an argument.
		{"testdata/invalid-files/live-record-store-tier-is-gone.tf", `An argument named "tier" is not expected here`},
		// GitHub issue #1340. A typo must not silently waive nothing while
		// reading as a waiver, and the override is a list, never a boolean.
		{"testdata/invalid-files/live-record-store-allow-insecure-unknown.tf", `names "versionning", which is not something record_store "s3" asserts`},
		{"testdata/invalid-files/live-record-store-allow-insecure-boolean.tf", `must be a literal list of strings`},
		{"testdata/invalid-files/live-record-store-allow-insecure-twice.tf", `names "versioning" more than once`},
		{"testdata/invalid-files/live-record-store-allow-insecure-on-local.tf", `record_store "local" asserts nothing`},
		// GitHub issue #1393. Both remote backends take allow_insecure, with
		// their own names, so a bucket setting named on a cluster store is
		// refused rather than read as waiving something.
		{"testdata/invalid-files/live-record-store-allow-insecure-bucket-name-on-kubernetes.tf", `Valid names are "namespace_access", "read_isolation", "encryption_at_rest", "estate_boundary"`},
		// GitHub issue #1381. An account ID that is not twelve digits would
		// go on the wire as ExpectedBucketOwner and be refused by S3 on
		// every request, with nothing saying the configuration is why.
		{"testdata/invalid-files/live-record-store-bucket-owner-not-an-account.tf", `It must be an AWS account ID: exactly twelve digits`},
		{"testdata/invalid-files/live-record-store-bucket-owner-too-short.tf", `It must be an AWS account ID: exactly twelve digits`},
		{"testdata/invalid-files/live-record-store-bucket-owner-on-local.tf", `has no meaning for record_store "local"`},
		// GitHub issue #1392. The "kubernetes" backend's arguments and the
		// bucket's are refused on each other's backend, both directions, so a
		// block that names both is told which one this store does not have
		// rather than silently ignoring half of what was written.
		{"testdata/invalid-files/live-record-store-kubernetes-bucket.tf", `has no meaning for record_store "kubernetes"`},
		{"testdata/invalid-files/live-record-store-kubernetes-region.tf", `has no meaning for record_store "kubernetes"`},
		{"testdata/invalid-files/live-record-store-namespace-on-s3.tf", `has no meaning for record_store "s3"`},
		{"testdata/invalid-files/live-record-store-exec-on-local.tf", `has no meaning for record_store "local"`},
		{"testdata/invalid-files/live-record-store-kubernetes-bad-namespace.tf", `is not a Kubernetes namespace name`},
		{"testdata/invalid-files/live-record-store-kubernetes-exec-no-command.tf", `An "exec" block requires an "command" argument`},
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
//
// The records' own root is the seventh and is not reserved, because
// "tofu-records/<this estate>" IS the default. What it may not name is another
// estate's records namespace, which GitHub issue #1381 measured as accepted.
func TestValidateRecordStoreKeyPrefix(t *testing.T) {
	for _, tc := range []struct {
		prefix string
		// estate is what the live block names, "" meaning it names none and
		// the name comes from the tofu-estate tags at run time. Every row
		// that leaves it blank is read as "my-estate", which is what the
		// fixtures use; the rows about the estate say so.
		estate string
		want   string // a fragment of the refusal, or "" for accepted
	}{
		{prefix: "my-estate"},
		{prefix: "tofu-records/my-estate"},
		{prefix: "tofu-records/my-estate/"},
		// A deeper namespace under this estate's own records is still this
		// estate's: nothing else writes there.
		{prefix: "tofu-records/my-estate/inner"},
		// GitHub issue #1381. recordStoreKeyPrefix uses the override
		// verbatim, so this is the exact string estate "other" writes its
		// own records under, and each estate would read the other's
		// inventory as its own.
		{prefix: "tofu-records/other", want: `the estate this configuration owns is "my-estate"`},
		{prefix: "tofu-records/other/", want: `namespace estate "other" writes its own records to`},
		{prefix: "tofu-records/other/deeper", want: `namespace estate "other" writes its own records to`},
		// "my-estate-eu" is a different estate, and a prefix match is not an
		// estate match: NamespacePrefix gives both a trailing delimiter, so
		// these are two namespaces, and this one is not ours.
		{prefix: "tofu-records/my-estate-eu", want: `namespace estate "my-estate-eu" writes its own records to`},
		// The root itself holds every estate's records.
		{prefix: "tofu-records", want: "the \"tofu-records\" root itself"},
		{prefix: "tofu-records/", want: "the \"tofu-records\" root itself"},
		// With no estate argument the name is derived from the tags at run
		// time, so nothing at decode time can say whether this is our own
		// namespace or a neighbour's. Refused rather than guessed.
		{prefix: "tofu-records/my-estate", estate: "-", want: "does not say which estate it owns"},
		{prefix: "tofu-records/other", estate: "-", want: "does not say which estate it owns"},
		// A prefix outside the records root needs no estate to be judged.
		{prefix: "somewhere/else", estate: "-"},
		// A leading slash is refused in its own right (#1383), and the six
		// reserved namespaces below still get their own reason when they
		// carry one, because that is the more dangerous of the two.
		{prefix: "/tofu-records/my-estate/", want: `must not begin with "/"`},
		{prefix: "/", want: "empty"},
		// A prefix that merely starts with the same letters is not a
		// segment match and must not be refused.
		{prefix: "tofu-receipts-archive"},
		{prefix: "nested/tofu-receipts"},
		{prefix: "tofu-hints-archive"},
		{prefix: "nested/tofu-hints"},
		{prefix: "tofu-located-archive"},
		{prefix: "nested/tofu-located"},
		{prefix: "tofu-residue-archive"},
		{prefix: "nested/tofu-residue"},
		{prefix: "tofu-provisioned-archive"},
		{prefix: "nested/tofu-provisioned"},
		{prefix: "tofu-outputs-archive"},
		{prefix: "nested/tofu-outputs"},

		{prefix: "tofu-receipts", want: "must not begin with the \"tofu-receipts\" segment"},
		{prefix: "tofu-receipts/my-estate", want: "must not begin with the \"tofu-receipts\" segment"},
		{prefix: "/tofu-receipts/my-estate", want: "must not begin with the \"tofu-receipts\" segment"},

		{prefix: "tofu-hints", want: "must not begin with the \"tofu-hints\" segment"},
		{prefix: "tofu-hints/my-estate", want: "must not begin with the \"tofu-hints\" segment"},
		{prefix: "/tofu-hints/my-estate", want: "must not begin with the \"tofu-hints\" segment"},

		{prefix: "tofu-located", want: "must not begin with the \"tofu-located\" segment"},
		{prefix: "tofu-located/my-estate", want: "must not begin with the \"tofu-located\" segment"},
		{prefix: "/tofu-located/my-estate", want: "must not begin with the \"tofu-located\" segment"},

		{prefix: "tofu-residue", want: "must not begin with the \"tofu-residue\" segment"},
		{prefix: "tofu-residue/my-estate", want: "must not begin with the \"tofu-residue\" segment"},
		{prefix: "/tofu-residue/my-estate", want: "must not begin with the \"tofu-residue\" segment"},

		{prefix: "tofu-provisioned", want: "must not begin with the \"tofu-provisioned\" segment"},
		{prefix: "tofu-provisioned/my-estate", want: "must not begin with the \"tofu-provisioned\" segment"},
		{prefix: "/tofu-provisioned/my-estate", want: "must not begin with the \"tofu-provisioned\" segment"},

		{prefix: "tofu-outputs", want: "must not begin with the \"tofu-outputs\" segment"},
		{prefix: "tofu-outputs/my-estate", want: "must not begin with the \"tofu-outputs\" segment"},
		{prefix: "/tofu-outputs/my-estate", want: "must not begin with the \"tofu-outputs\" segment"},

		{prefix: "", want: "empty"},
		{prefix: "///", want: "empty"},
	} {
		t.Run(tc.prefix+"/estate="+tc.estate, func(t *testing.T) {
			// "" is the ordinary case and means the fixtures' estate name;
			// "-" is the row that says the live block names no estate at
			// all, which is a real configuration (the name then comes from
			// the tofu-estate tags) and is not the same thing as "unset in
			// this table".
			estate := tc.estate
			switch estate {
			case "":
				estate = "my-estate"
			case "-":
				estate = ""
			}
			got := validateRecordStoreKeyPrefix(tc.prefix, estate)
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

// TestModule_liveStrict covers GitHub issue #365's config surface: a "strict"
// block nested inside "live", carrying the profile toggles as literal
// strings. The decoder records what was written and judges none of it -
// which strings mean something is internal/live/strict's vocabulary, checked
// at lint time, the same layering LivePolicy's quadrant verbs already have.
func TestModule_liveStrict(t *testing.T) {
	mod, diags := testModuleFromDir("testdata/valid-modules/live-strict")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	st := mod.Live.Strict
	if st == nil {
		t.Fatal("no strict block was decoded")
	}
	if !st.MarkerRepairSet {
		t.Fatal("MarkerRepairSet is false for a block that writes marker_repair")
	}
	if got, want := st.MarkerRepair, "never"; got != want {
		t.Errorf("MarkerRepair = %q, want %q", got, want)
	}
	if st.MarkerRepairRange.Filename == "" {
		t.Error("MarkerRepairRange is the zero value, so a diagnostic cannot point at the argument")
	}
}

// TestModule_liveStrictSecrets is GitHub issue #365 slice 3's config
// surface: the same decoder, the same literal-string treatment, alongside
// the argument that was there first. Both are asserted in one fixture on
// purpose - the two arguments share a decode loop, and a loop that reads the
// second into the first's field would pass two separate single-argument
// tests.
func TestModule_liveStrictSecrets(t *testing.T) {
	mod, diags := testModuleFromDir("testdata/valid-modules/live-strict-secrets")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	st := mod.Live.Strict
	if st == nil {
		t.Fatal("no strict block was decoded")
	}
	if !st.SecretsSet {
		t.Fatal("SecretsSet is false for a block that writes secrets")
	}
	if got, want := st.Secrets, "refuse"; got != want {
		t.Errorf("Secrets = %q, want %q", got, want)
	}
	if st.SecretsRange.Filename == "" {
		t.Error("SecretsRange is the zero value, so a diagnostic cannot point at the argument")
	}
	if got, want := st.MarkerRepair, "never"; got != want {
		t.Errorf("MarkerRepair = %q, want %q - the two arguments share a decode loop and must not be reading each other's attribute", got, want)
	}
	if st.MarkerRepairRange == st.SecretsRange {
		t.Error("the two arguments decoded to one range, so a diagnostic about either would point at the same source")
	}
}

// TestModule_liveStrictEmpty: a strict block that sets nothing decodes as a
// non-nil block with every *Set flag false. The distinction matters because
// "the block is there and sets nothing" and "the block is absent" must both
// mean today's behavior, and neither may be spellable as a value.
func TestModule_liveStrictEmpty(t *testing.T) {
	mod, diags := testModuleFromDir("testdata/valid-modules/live-strict-empty")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	st := mod.Live.Strict
	if st == nil {
		t.Fatal("an empty strict block decoded as nil")
	}
	if st.MarkerRepairSet {
		t.Errorf("MarkerRepairSet is true for a strict block that writes no marker_repair (value %q)", st.MarkerRepair)
	}
	if st.SecretsSet {
		t.Errorf("SecretsSet is true for a strict block that writes no secrets (value %q)", st.Secrets)
	}
}

// TestModule_liveStrictAbsent: no strict block leaves Live.Strict nil, which
// every reader must take as "today's behavior". This is the config-layer half
// of HANDOFF.md's "compatible out of the box" - the same "absent means
// absent" contract Policy and RecordStore already follow.
func TestModule_liveStrictAbsent(t *testing.T) {
	mod, diags := testModuleFromDir("testdata/valid-modules/live")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	if mod.Live == nil {
		t.Fatal("no live block was decoded")
	}
	if mod.Live.Strict != nil {
		t.Errorf("Strict is %+v for a live block with no strict block, want nil", mod.Live.Strict)
	}
}

// TestModule_liveStrictRefused: everything that can be wrong with a strict
// block's SHAPE is lexical and caught here. What is wrong with a VALUE is
// not - "sometimes" decodes perfectly well and is refused by
// internal/live/lint, the same division policy verbs have.
func TestModule_liveStrictRefused(t *testing.T) {
	for _, tc := range []struct {
		file string
		want string
	}{
		{"testdata/invalid-files/live-strict-duplicate.tf", "Duplicate strict block"},
		{"testdata/invalid-files/live-strict-non-literal.tf", "Variables not allowed"},
		{"testdata/invalid-files/live-strict-secrets-non-literal.tf", "Variables not allowed"},
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

// TestModule_liveStrictMarkers covers GitHub issue #365 slice 2's config
// surface: the `markers "record"` block nested inside strict, carrying two
// literal lists.
//
// The decoder's whole job here is to record what was written, ranges
// included, and to judge none of it. Whether "aws_ebs_volume" is a real type
// needs the provider's schemas; whether "module.server.aws_instance.instance"
// is a usable address needs internal/addrs' target grammar and the module
// tree. Both are internal/live/lint's, the same division [LivePolicyScope]'s
// three lists already have.
func TestModule_liveStrictMarkers(t *testing.T) {
	mod, diags := testModuleFromDir("testdata/valid-modules/live-strict-markers")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	st := mod.Live.Strict
	if st == nil {
		t.Fatal("no strict block was decoded")
	}
	m := st.MarkersRecord
	if m == nil {
		t.Fatal("no markers block was decoded")
	}
	if got, want := m.Kind, "record"; got != want {
		t.Errorf("Kind = %q, want %q", got, want)
	}
	if got, want := strings.Join(m.Types, ","), "aws_ebs_volume"; got != want {
		t.Errorf("Types = %q, want %q", got, want)
	}
	if got, want := strings.Join(m.Addresses, ","), "aws_instance.worker,module.server.aws_instance.instance"; got != want {
		t.Errorf("Addresses = %q, want %q", got, want)
	}
	if !m.TypesSet || !m.AddressesSet {
		t.Errorf("TypesSet=%v AddressesSet=%v; both lists were written", m.TypesSet, m.AddressesSet)
	}
	// The ranges are what let a lint refusal point at the list an operator
	// wrote rather than at the whole block.
	if m.TypesRange.Filename == "" || m.AddressesRange.Filename == "" || m.DeclRange.Filename == "" {
		t.Error("a markers block range is the zero value, so a diagnostic cannot point at what was written")
	}
}

// TestModule_liveStrictMarkersAbsent: a strict block with no markers block
// leaves MarkersRecord nil, which every reader takes as "every taggable
// resource keeps its marker". The same "absent means absent" contract the
// rest of the live block has.
func TestModule_liveStrictMarkersAbsent(t *testing.T) {
	mod, diags := testModuleFromDir("testdata/valid-modules/live-strict")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	if mod.Live.Strict.MarkersRecord != nil {
		t.Errorf("MarkersRecord is %+v for a strict block with no markers block, want nil", mod.Live.Strict.MarkersRecord)
	}
}

// TestModule_liveStrictMarkersRefused: the three lexical shapes.
//
// The label is refused here rather than at lint time because it is a
// spelling, not a judgement - the same reason record_store's three backend
// names are checked in the decoder. That it is a LABEL at all is what leaves
// room for `markers "tag"`, the inverse selection, without a grammar change;
// today it is not a carrier this fork knows, so it is refused by name.
func TestModule_liveStrictMarkersRefused(t *testing.T) {
	for _, tc := range []struct {
		file string
		want string
	}{
		{"testdata/invalid-files/live-strict-markers-bad-label.tf", "Invalid markers selection"},
		{"testdata/invalid-files/live-strict-markers-duplicate.tf", "Duplicate markers block"},
		{"testdata/invalid-files/live-strict-markers-non-literal.tf", "Variables not allowed"},
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

// TestModule_liveReads is issue #732's toggle: "reads" accepts the two
// policy literals, defaults to unset (which the command layer reads as
// "selective" - live-backend behaviors default on), and refuses anything
// else with an error that names both accepted values.
func TestModule_liveReads(t *testing.T) {
	mod, diags := testModuleFromDir("testdata/valid-modules/live-reads")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	if got, want := mod.Live.Reads, "full"; got != want {
		t.Errorf("Reads is %q, want %q", got, want)
	}

	// A block that does not set the argument leaves it empty - "not set",
	// which downstream resolves to the selective default.
	mod, diags = testModuleFromDir("testdata/valid-modules/live")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	if got := mod.Live.Reads; got != "" {
		t.Errorf("Reads is %q for a block that never set it, want empty", got)
	}

	parser := NewParser(nil)
	_, diags = parser.LoadConfigFile("testdata/invalid-files/live-reads-invalid.tf")
	if !diags.HasErrors() {
		t.Fatal("reads = \"sometimes\" loaded with no errors")
	}
	for _, want := range []string{"Invalid reads setting", `"selective"`, `"full"`} {
		if !strings.Contains(diags.Error(), want) {
			t.Errorf("the error does not mention %q:\n%s", want, diags.Error())
		}
	}
}

// TestModule_liveRetry covers GitHub issues #1196 and #1148's config surface:
// a "retry" block nested inside "live", carrying an attempt budget and a mode.
// The decoder records what was written and judges none of it - which mode
// spellings mean anything, and what bounds an attempt count has, is
// internal/live/retry's vocabulary checked at lint time, the same layering
// LiveStrict and LivePolicy already have.
//
// Both arguments are asserted from one fixture on purpose: they are read by
// the same decoder, and a decoder that put mode into the attempt field would
// pass two separate single-argument tests.
func TestModule_liveRetry(t *testing.T) {
	mod, diags := testModuleFromDir("testdata/valid-modules/live-retry")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	rt := mod.Live.Retry
	if rt == nil {
		t.Fatal("no retry block was decoded")
	}
	if !rt.MaxAttemptsSet {
		t.Fatal("MaxAttemptsSet is false for a block that writes max_attempts")
	}
	if got, want := rt.MaxAttempts, 10; got != want {
		t.Errorf("MaxAttempts = %d, want %d", got, want)
	}
	if rt.MaxAttemptsRange.Filename == "" {
		t.Error("MaxAttemptsRange is the zero value, so a diagnostic cannot point at the argument")
	}
	if !rt.ModeSet {
		t.Fatal("ModeSet is false for a block that writes mode")
	}
	if got, want := rt.Mode, "adaptive"; got != want {
		t.Errorf("Mode = %q, want %q", got, want)
	}
	if rt.ModeRange.Filename == "" {
		t.Error("ModeRange is the zero value, so a diagnostic cannot point at the argument")
	}
}

// TestModule_liveRetryAbsent pins the contract every nested live block shares:
// absent means absent. A live block with no retry block leaves Retry nil,
// which internal/live/retry.Build reads as the aws-sdk-go-v2 defaults - what
// every configuration written before this block existed gets.
func TestModule_liveRetryAbsent(t *testing.T) {
	mod, diags := testModuleFromDir("testdata/valid-modules/live-strict")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	if mod.Live.Retry != nil {
		t.Errorf("Retry = %#v for a live block that declares no retry block, want nil", mod.Live.Retry)
	}
}
