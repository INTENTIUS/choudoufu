// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// fixedClock returns a func() time.Time that always answers t, for
// reproducible "writtenAt" fields in these tests.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// ---------------------------------------------------------------------------
// No attribute -> no file, ever
// ---------------------------------------------------------------------------

// TestManager_snapshotDisabledByDefault: a Manager nobody called
// EnableSnapshot on writes no snapshot no matter how many times PersistState
// runs. This is the manager-level half of "no snapshot_path attribute -> no
// file, ever"; the configuration-level half is
// TestModule_statelessSnapshotPathAbsent in internal/configs.
func TestManager_snapshotDisabledByDefault(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	m := NewManager()
	if err := m.WriteState(testProjectionState()); err != nil {
		t.Fatalf("WriteState: %s", err)
	}
	for i := 0; i < 3; i++ {
		if err := m.PersistState(context.Background(), nil); err != nil {
			t.Fatalf("PersistState: %s", err)
		}
	}

	assertNoFilesUnder(t, dir)
	if diags := m.SnapshotWarning(); diags.HasErrors() || len(diags) != 0 {
		t.Errorf("SnapshotWarning() is %v, want none (the snapshot was never enabled)", diags)
	}
}

// ---------------------------------------------------------------------------
// Attribute set -> file written on persist, with the right shape
// ---------------------------------------------------------------------------

// TestManager_snapshotWritten is the golden-ish shape test: once
// EnableSnapshot names a path, a PersistState call writes a [snapshot] there
// whose fields match the state that was written to the manager, using an
// injected clock so "writtenAt" is reproducible.
func TestManager_snapshotWritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out", "snapshot.json")

	m := NewManager()
	when := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	m.EnableSnapshot(path, "my-estate", fixedClock(when))

	if err := m.WriteState(testProjectionState()); err != nil {
		t.Fatalf("WriteState: %s", err)
	}
	if err := m.PersistState(context.Background(), nil); err != nil {
		t.Fatalf("PersistState: %s", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the snapshot: %s", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %s", err)
	}
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("snapshot mode is %o, want 0600", got)
		}
	}

	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("decoding the snapshot: %s\nraw: %s", err, data)
	}

	if snap.FormatVersion != snapshotFormatVersion {
		t.Errorf("formatVersion is %q, want %q", snap.FormatVersion, snapshotFormatVersion)
	}
	if snap.WrittenAt != when.Format(time.RFC3339) {
		t.Errorf("writtenAt is %q, want %q", snap.WrittenAt, when.Format(time.RFC3339))
	}
	if snap.Estate != "my-estate" {
		t.Errorf("estate is %q, want %q", snap.Estate, "my-estate")
	}
	if len(snap.Resources) != 1 {
		t.Fatalf("resources has %d entries, want 1: %#v", len(snap.Resources), snap.Resources)
	}

	res := snap.Resources[0]
	if res.Address != "aws_s3_bucket.data" {
		t.Errorf("address is %q, want %q", res.Address, "aws_s3_bucket.data")
	}
	if res.Type != "aws_s3_bucket" {
		t.Errorf("type is %q, want %q", res.Type, "aws_s3_bucket")
	}
	if res.AttributesHash == "" {
		t.Error("attributesHash is empty")
	}

	// SnapshotWarning is nil after a successful write.
	if diags := m.SnapshotWarning(); len(diags) != 0 {
		t.Errorf("SnapshotWarning() after a successful write is %v, want none", diags)
	}
}

// TestManager_snapshotOverwrittenOnEachPersist: PersistState writes on every
// call (the documented periodic-vs-final decision), so a second call with
// different state produces a snapshot reflecting the newer state, not a
// stale first write.
func TestManager_snapshotOverwrittenOnEachPersist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")

	m := NewManager()
	m.EnableSnapshot(path, "my-estate", fixedClock(time.Unix(0, 0)))

	if err := m.WriteState(states.NewState()); err != nil {
		t.Fatalf("WriteState: %s", err)
	}
	if err := m.PersistState(context.Background(), nil); err != nil {
		t.Fatalf("PersistState: %s", err)
	}
	first := readSnapshot(t, path)
	if len(first.Resources) != 0 {
		t.Fatalf("first snapshot has resources, want none: %#v", first.Resources)
	}

	if err := m.WriteState(testProjectionState()); err != nil {
		t.Fatalf("WriteState: %s", err)
	}
	if err := m.PersistState(context.Background(), nil); err != nil {
		t.Fatalf("PersistState: %s", err)
	}
	second := readSnapshot(t, path)
	if len(second.Resources) != 1 {
		t.Fatalf("second snapshot has %d resources, want 1", len(second.Resources))
	}

	if got := m.Persists(); got != 2 {
		t.Errorf("Persists() is %d, want 2", got)
	}
}

// ---------------------------------------------------------------------------
// Attribute values never appear; markers and hashes do
// ---------------------------------------------------------------------------

// TestManager_snapshotScrubsAttributeValues: the attribute blob never
// reaches the file. A value that lives only in an attribute the snapshot has
// no field for is covered by the hash and nothing else.
//
// This test alone is not the secret-leak regression, and the audit was right
// to call the earlier version of it hollow: "secret_string" is a field
// buildSnapshot never touches, so it would pass against a writer with no
// redaction in it at all - as it did. The fields the writer DOES write
// (ImportID, Identity, Markers, and the address) are covered by
// TestManager_snapshotRedactsWrittenFields and the two below it, which plant
// their secrets there.
func TestManager_snapshotScrubsAttributeValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")

	const secret = "correct-horse-battery-staple-do-not-leak-me"

	state := states.NewState()
	state.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{
			Mode: addrs.ManagedResourceMode,
			Type: "aws_secretsmanager_secret_version",
			Name: "cred",
		}.Instance(addrs.NoKey),
		&states.ResourceInstanceObjectSrc{
			AttrsJSON: []byte(`{
				"id": "secret-1",
				"secret_string": "` + secret + `",
				"tags": {"tofu-estate": "my-estate", "tofu-address": "aws_secretsmanager_secret_version.cred"}
			}`),
			Status: states.ObjectReady,
		},
		addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")},
		addrs.NoKey,
	)

	m := NewManager()
	m.EnableSnapshot(path, "my-estate", fixedClock(time.Unix(0, 0)))
	if err := m.WriteState(state); err != nil {
		t.Fatalf("WriteState: %s", err)
	}
	if err := m.PersistState(context.Background(), nil); err != nil {
		t.Fatalf("PersistState: %s", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the snapshot: %s", err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("the snapshot contains the secret attribute value:\n%s", raw)
	}

	snap := readSnapshot(t, path)
	if len(snap.Resources) != 1 {
		t.Fatalf("want 1 resource, got %d", len(snap.Resources))
	}
	res := snap.Resources[0]
	if res.AttributesHash == "" {
		t.Error("attributesHash is empty; a changed-or-not signal needs something")
	}
	if res.Markers["tofu-estate"] != "my-estate" || res.Markers["tofu-address"] != "aws_secretsmanager_secret_version.cred" {
		t.Errorf("markers did not carry the ownership tags: %#v", res.Markers)
	}
}

// TestManager_snapshotRedactsWrittenFields is C7's regression, and it plants
// its secrets only in fields buildSnapshot actually writes: the "id"
// attribute (which becomes ImportID), a tag value (which becomes a marker),
// and a for_each key (which becomes part of the address). All three are
// sensitive by the marks the state itself carries, so this half needs no
// schema - the state is the source, exactly as it is for a value that became
// sensitive by passing through a sensitive input variable.
//
// The for_each key is the case the substring sweep exists for, and it also
// shows the sweep's shape: the key is redacted out of the address and out of
// the tofu-address marker because the same string is a sensitive attribute
// value ("name") on the object. A key that appeared ONLY inside the marker,
// with no sensitive attribute holding it, is not something this pass can
// recognize - nothing in the object says it was ever a secret.
func TestManager_snapshotRedactsWrittenFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")

	const (
		secretID  = "arn:aws:secret:the-id-is-the-secret-here"
		secretTag = "tag-value-that-must-not-be-written-down"
		secretKey = "for-each-key-derived-from-a-secret"
	)

	state := states.NewState()
	state.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{
			Mode: addrs.ManagedResourceMode,
			Type: "aws_ssm_parameter",
			Name: "cred",
		}.Instance(addrs.StringKey(secretKey)),
		&states.ResourceInstanceObjectSrc{
			AttrsJSON: []byte(`{
				"id": "` + secretID + `",
				"name": "` + secretKey + `",
				"tags": {
					"tofu-estate": "my-estate",
					"tofu-address": "aws_ssm_parameter.cred[\"` + secretKey + `\"]",
					"tofu-slot": "` + secretTag + `"
				}
			}`),
			AttrSensitivePaths: []cty.PathValueMarks{
				{
					Path:  cty.GetAttrPath("id"),
					Marks: cty.NewValueMarks(marks.Sensitive),
				},
				{
					Path:  cty.GetAttrPath("name"),
					Marks: cty.NewValueMarks(marks.Sensitive),
				},
				{
					Path:  cty.GetAttrPath("tags").IndexString("tofu-slot"),
					Marks: cty.NewValueMarks(marks.Sensitive),
				},
			},
			Status: states.ObjectReady,
		},
		addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")},
		addrs.NoKey,
	)

	m := NewManager()
	m.EnableSnapshot(path, "my-estate", fixedClock(time.Unix(0, 0)))
	if err := m.WriteState(state); err != nil {
		t.Fatalf("WriteState: %s", err)
	}
	if err := m.PersistState(context.Background(), nil); err != nil {
		t.Fatalf("PersistState: %s", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the snapshot: %s", err)
	}
	for _, secret := range []string{secretID, secretTag, secretKey} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("the snapshot contains the sensitive value %q:\n%s", secret, raw)
		}
	}

	snap := readSnapshot(t, path)
	if len(snap.Resources) != 1 {
		t.Fatalf("want 1 resource, got %d", len(snap.Resources))
	}
	res := snap.Resources[0]
	if res.ImportID != redacted {
		t.Errorf("importID is %q, want %q", res.ImportID, redacted)
	}
	if res.Markers["tofu-slot"] != redacted {
		t.Errorf("the sensitive tofu-slot marker is %q, want %q", res.Markers["tofu-slot"], redacted)
	}
	// The estate marker was never sensitive and is the whole point of the
	// file, so redaction must not have swept it up along the way.
	if res.Markers["tofu-estate"] != "my-estate" {
		t.Errorf("the tofu-estate marker is %q, want it left alone", res.Markers["tofu-estate"])
	}
	// The address and the tofu-address marker both embedded the sensitive
	// for_each key; the sweep is what removes it from those, since no path
	// in either of them is itself marked.
	if !strings.Contains(res.Address, redacted) {
		t.Errorf("the address is %q, want the sensitive for_each key redacted out of it", res.Address)
	}
	if !strings.Contains(res.Markers["tofu-address"], redacted) {
		t.Errorf("the tofu-address marker is %q, want the sensitive for_each key redacted out of it", res.Markers["tofu-address"])
	}
	if res.AttributesHash == "" {
		t.Error("attributesHash is empty; redaction must not cost the changed-or-not signal")
	}
}

// TestManager_snapshotRedactsFromSchema is the other source of sensitivity,
// and the one that matters most in practice: an object the projection read
// back from the live system carries no marks at all, so only the provider's
// schema knows that its "id" is a secret. Without the schemas argument
// PersistState used to discard, none of this is reachable.
func TestManager_snapshotRedactsFromSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")

	const secret = "id-that-the-provider-declares-sensitive"

	state := states.NewState()
	state.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{
			Mode: addrs.ManagedResourceMode,
			Type: "aws_ssm_parameter",
			Name: "cred",
		}.Instance(addrs.NoKey),
		&states.ResourceInstanceObjectSrc{
			// No AttrSensitivePaths: an imported object has none.
			AttrsJSON: []byte(`{
				"id": "` + secret + `",
				"tags": {"tofu-estate": "my-estate", "tofu-address": "aws_ssm_parameter.cred"}
			}`),
			Status: states.ObjectReady,
		},
		addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")},
		addrs.NoKey,
	)

	m := NewManager()
	m.EnableSnapshot(path, "my-estate", fixedClock(time.Unix(0, 0)))
	if err := m.WriteState(state); err != nil {
		t.Fatalf("WriteState: %s", err)
	}
	if err := m.PersistState(context.Background(), testSensitiveIDSchemas()); err != nil {
		t.Fatalf("PersistState: %s", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the snapshot: %s", err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("the snapshot contains the schema-sensitive id:\n%s", raw)
	}
	snap := readSnapshot(t, path)
	if len(snap.Resources) != 1 {
		t.Fatalf("want 1 resource, got %d", len(snap.Resources))
	}
	if got := snap.Resources[0].ImportID; got != redacted {
		t.Errorf("importID is %q, want %q", got, redacted)
	}
}

// TestBuildSnapshot_redactsIdentity covers the third written field: a
// provider-native identity (#2854) whose schema declares one of its
// attributes sensitive. BuildSnapshot writes the whole identity object, so a
// sensitive attribute inside it went straight to disk before C7.
func TestBuildSnapshot_redactsIdentity(t *testing.T) {
	const secret = "identity-attribute-that-is-a-credential"

	state := states.NewState()
	state.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{
			Mode: addrs.ManagedResourceMode,
			Type: "aws_ssm_parameter",
			Name: "cred",
		}.Instance(addrs.NoKey),
		&states.ResourceInstanceObjectSrc{
			AttrsJSON:    []byte(`{"id":"p-1"}`),
			IdentityJSON: []byte(`{"name":"/app/db","token":"` + secret + `"}`),
			Status:       states.ObjectReady,
		},
		addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")},
		addrs.NoKey,
	)

	snap := buildSnapshot(state, "my-estate", time.Unix(0, 0), testSensitiveIDSchemas())
	encoded, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("encoding the snapshot: %s", err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("the snapshot contains the sensitive identity attribute:\n%s", encoded)
	}
	if len(snap.Resources) != 1 {
		t.Fatalf("want 1 resource, got %d", len(snap.Resources))
	}
	identity := snap.Resources[0].Identity
	if identity["token"] != redacted {
		t.Errorf("identity token is %v, want %q", identity["token"], redacted)
	}
	if identity["name"] != "/app/db" {
		t.Errorf("identity name is %v, want it left alone", identity["name"])
	}
}

// testSensitiveIDSchemas is a provider schema set declaring
// aws_ssm_parameter's "id" attribute and its identity's "token" attribute
// sensitive - the shape a real provider uses to say "do not print this".
func testSensitiveIDSchemas() *tofu.Schemas {
	return &tofu.Schemas{
		Providers: map[addrs.Provider]providers.ProviderSchema{
			addrs.NewDefaultProvider("aws"): {
				ResourceTypes: map[string]providers.Schema{
					"aws_ssm_parameter": {
						Block: &configschema.Block{
							Attributes: map[string]*configschema.Attribute{
								"id":   {Type: cty.String, Computed: true, Sensitive: true},
								"tags": {Type: cty.Map(cty.String), Optional: true},
							},
						},
						IdentitySchema: &configschema.Object{
							Nesting: configschema.NestingSingle,
							Attributes: map[string]*configschema.Attribute{
								"name":  {Type: cty.String, Required: true},
								"token": {Type: cty.String, Required: true, Sensitive: true},
							},
						},
					},
				},
			},
		},
	}
}

// ---------------------------------------------------------------------------
// A snapshot never overwrites a state file (C6)
// ---------------------------------------------------------------------------

// TestWriteSnapshot_refusesToOverwriteStateFile: the configuration decoder
// rejects a snapshot_path NAMED like a state file, but a state file can be
// called anything. writeSnapshot therefore looks at what is already there and
// refuses rather than renaming over somebody's record of their
// infrastructure. The audit destroyed a real terraform.tfstate this way.
func TestWriteSnapshot_refusesToOverwriteStateFile(t *testing.T) {
	dir := t.TempDir()

	// A v4 state file under a name the decoder has no reason to object to.
	victim := filepath.Join(dir, "cache.json")
	const victimState = `{
  "version": 4,
  "terraform_version": "1.6.0",
  "serial": 3,
  "lineage": "9d3b4b2a-0000-0000-0000-000000000000",
  "outputs": {},
  "resources": []
}`
	if err := os.WriteFile(victim, []byte(victimState), 0o644); err != nil {
		t.Fatalf("writing the victim state file: %s", err)
	}

	snap := buildSnapshot(testProjectionState(), "my-estate", time.Unix(0, 0), nil)
	err := writeSnapshot(victim, snap)
	if err == nil {
		t.Fatal("writeSnapshot overwrote a state file, want a refusal")
	}
	if !strings.Contains(err.Error(), "state file") {
		t.Errorf("the refusal does not say what it refused: %s", err)
	}

	after, readErr := os.ReadFile(victim)
	if readErr != nil {
		t.Fatalf("reading the victim back: %s", readErr)
	}
	if string(after) != victimState {
		t.Errorf("the state file was modified:\n%s", after)
	}

	// The previous snapshot is not a state file, so a normal rewrite is
	// unaffected: the refusal must not brick the cache it is protecting.
	ok := filepath.Join(dir, "snapshot.json")
	if err := writeSnapshot(ok, snap); err != nil {
		t.Fatalf("first write: %s", err)
	}
	if err := writeSnapshot(ok, snap); err != nil {
		t.Fatalf("rewriting the snapshot over itself: %s", err)
	}
}

// TestManager_snapshotStateFileRefusalIsWarningOnly: the C6 refusal travels
// the same road as every other snapshot write failure - a warning, never an
// error out of PersistState. A cache that can fail an apply is a record.
func TestManager_snapshotStateFileRefusalIsWarningOnly(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "cache.json")
	if err := os.WriteFile(victim, []byte(`{"version":4,"lineage":"x","resources":[]}`), 0o644); err != nil {
		t.Fatalf("writing the victim state file: %s", err)
	}

	m := NewManager()
	m.EnableSnapshot(victim, "my-estate", fixedClock(time.Unix(0, 0)))
	if err := m.WriteState(testProjectionState()); err != nil {
		t.Fatalf("WriteState: %s", err)
	}
	if err := m.PersistState(context.Background(), nil); err != nil {
		t.Fatalf("PersistState returned an error (rule 5: it must not): %s", err)
	}

	diags := m.SnapshotWarning()
	if len(diags) == 0 {
		t.Fatal("SnapshotWarning() is empty, want a warning about the refused overwrite")
	}
	if diags.HasErrors() {
		t.Errorf("the refusal is error severity, want warning only: %s", diags.ErrWithWarnings())
	}
}

// TestIsStateFile covers the recognizer's two jobs directly: every state
// format version is protected, and the snapshot's own shape is not mistaken
// for one.
func TestIsStateFile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		want    bool
	}{
		{"v4 state", `{"version":4,"terraform_version":"1.6.0","serial":1,"resources":[]}`, true},
		{"v3 state", `{"version":3,"serial":1,"lineage":"abc","modules":[]}`, true},
		{"state with resources first", `{"resources":[],"version":4}`, true},
		{"state behind a long preamble", `{"version":4,` + strings.Repeat(`"pad":{"a":[1,2,3]},`, 20) + `"lineage":"x"}`, true},
		{"a live snapshot", `{"formatVersion":"` + snapshotFormatVersion + `","writtenAt":"","estate":"e","resources":[]}`, false},
		{"a version with nothing else", `{"version":4}`, false},
		{"not json", `hello`, false},
		{"a json array", `[1,2,3]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "f.json")
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatalf("writing the fixture: %s", err)
			}
			if got := isStateFile(path); got != tc.want {
				t.Errorf("isStateFile = %v, want %v", got, tc.want)
			}
		})
	}

	if isStateFile(filepath.Join(t.TempDir(), "absent.json")) {
		t.Error("isStateFile said yes about a file that does not exist")
	}
}

// ---------------------------------------------------------------------------
// Write failure is a warning, never an error
// ---------------------------------------------------------------------------

// TestManager_snapshotWriteFailureIsWarningOnly: an unwritable target
// directory makes the snapshot write fail, and that failure must not
// propagate as an error from PersistState - rule 5 of P4.2, a cache that can
// fail an apply is a record, not a cache. It shows up only as
// Manager.SnapshotWarning.
func TestManager_snapshotWriteFailureIsWarningOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permission bits do not block writes the same way on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permission bits")
	}

	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")
	if err := os.Mkdir(blocked, 0o500); err != nil { // read+execute, no write
		t.Fatalf("creating the unwritable directory: %s", err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o700) }) // so TempDir cleanup can remove it

	path := filepath.Join(blocked, "snapshot.json")

	m := NewManager()
	m.EnableSnapshot(path, "my-estate", fixedClock(time.Unix(0, 0)))
	if err := m.WriteState(testProjectionState()); err != nil {
		t.Fatalf("WriteState: %s", err)
	}

	err := m.PersistState(context.Background(), nil)
	if err != nil {
		t.Fatalf("PersistState returned an error (rule 5: it must not): %s", err)
	}

	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("a snapshot file exists despite the write failure: stat error %v", statErr)
	}

	diags := m.SnapshotWarning()
	if len(diags) == 0 {
		t.Fatal("SnapshotWarning() is empty after a failed write, want a warning diagnostic")
	}
	if diags.HasErrors() {
		t.Errorf("SnapshotWarning() contains an error-severity diagnostic, want warning only: %s", diags.ErrWithWarnings())
	}
	desc := diags[0].Description()
	if desc.Detail == "" || !strings.Contains(desc.Detail, path) {
		t.Errorf("the warning does not name the path %q: %+v", path, desc)
	}
}

// ---------------------------------------------------------------------------
// Atomicity: no partial file on a simulated mid-write failure
// ---------------------------------------------------------------------------

// TestWriteSnapshot_atomic drives writeSnapshot directly (rather than
// through the manager) so it can simulate a failure between "temp file
// written" and "renamed into place": pointing path at a location whose
// parent looks writable but whose final rename target cannot be created
// (path itself given as a directory) forces the rename to fail after the
// temp file already holds the full content, and the test asserts the temp
// file is cleaned up and the target was never created.
func TestWriteSnapshot_atomic(t *testing.T) {
	dir := t.TempDir()

	// path names a directory, so the final os.Rename onto it fails (POSIX
	// rename onto a non-empty target kind mismatch), after MarshalIndent and
	// the temp-file write have already succeeded - exactly the failure
	// window atomicity has to cover.
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("creating the directory that stands in for a busy target: %s", err)
	}

	snap := buildSnapshot(testProjectionState(), "my-estate", time.Unix(0, 0), nil)
	if err := writeSnapshot(target, snap); err == nil {
		t.Fatal("writeSnapshot succeeded against a directory target, want an error")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %s", dir, err)
	}
	for _, e := range entries {
		if e.Name() != "target" {
			t.Errorf("a leftover temp file survived the failed write: %s", e.Name())
		}
	}

	// The real success path: a normal write leaves exactly the target file
	// behind and nothing else, confirming the temp file does not linger
	// even on the happy path.
	ok := filepath.Join(dir, "ok.json")
	if err := writeSnapshot(ok, snap); err != nil {
		t.Fatalf("writeSnapshot: %s", err)
	}
	entries, err = os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %s", dir, err)
	}
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.Name()] = true
	}
	if len(entries) != 2 || !names["target"] || !names["ok.json"] {
		var got []string
		for _, e := range entries {
			got = append(got, e.Name())
		}
		t.Errorf("directory contains %v, want exactly [target ok.json]", got)
	}
}

// ---------------------------------------------------------------------------
// The no-reader honesty test
// ---------------------------------------------------------------------------

// TestSnapshot_noReader is the sweeping half of P4.2 rule 3's enforcement
// ("no code path may read this file"). The other half is the compiler:
// [snapshot], [snapshotResource] and [snapshotFormatVersion] are unexported,
// so no package outside this one can name the format constant or decode the
// file into the shape the writer declares. That is a real wall, and the audit
// was right that the previous version of this test was not one - it grepped
// for the constant while EXCLUDING the writer's own package, so a reader
// added next to the writer, or one that spelled the literal itself, walked
// straight past.
//
// What this test adds on top of the compiler, both checks walking the tree
// rather than trusting a list:
//
//  1. The format literal may appear in exactly one place in the repository,
//     snapshot.go, where it is declared. Every other .go file - this
//     package's own included, which is the hole that is now closed - is
//     checked. Spelling the literal instead of importing the constant is the
//     obvious way around an unexported identifier, so that is the way this
//     looks for.
//  2. No non-test .go file outside this package may redeclare the snapshot's
//     struct shape, recognized by its two distinctive JSON tags. Hand-rolling
//     the struct is the other way around an unexported type, and a reader
//     that does it has to write those tags to decode anything.
//
// Test files are exempt from (2) on purpose, and
// internal/live/lifecycle's snapshot integration test uses the
// exemption: reading the file to assert what was written IS the verification
// of this contract, and it deliberately hand-writes the shape so that a drift
// between writer and file is visible. The contract binds code paths that
// treat the snapshot as input to a decision, and a test asserting the
// writer's output is not one.
//
// What neither half catches: a reader that opens the operator's snapshot_path
// and decodes into map[string]any, naming nothing. That is not statically
// decidable, and it is the reason the fork's prose says the guarantee is
// enforced for typed readers and a convention beyond that, rather than
// claiming an absolute.
func TestSnapshot_noReader(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine this test file's own path")
	}
	thisDir := filepath.Dir(thisFile)
	writerFile := filepath.Join(thisDir, "snapshot.go")

	// The repository root, three levels up from
	// internal/live/projection.
	root := filepath.Join(thisDir, "..", "..", "..")
	root, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("resolving the repository root: %s", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("%s does not look like the repository root (no go.mod): %s", root, err)
	}

	// The struct shape is recognized by the pair of JSON tags that are
	// distinctive to it. Written by concatenation so that this test file does
	// not itself contain the very thing it forbids.
	formatVersionTag := "json:" + `"` + "formatVersion" + `"`
	attributesHashTag := "json:" + `"` + "attributesHash" + `"`

	var literalOffenders, shapeOffenders []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			// .claude holds agent worktrees: full checkouts of this repo
			// whose own snapshot.go copies are not readers of this one.
			case ".git", "vendor", "node_modules", ".claude":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		abs, absErr := filepath.Abs(path)
		if absErr != nil {
			return absErr
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		text := string(content)

		rel, relErr := filepath.Rel(root, abs)
		if relErr != nil {
			rel = abs
		}

		if abs != writerFile && strings.Contains(text, snapshotFormatVersion) {
			literalOffenders = append(literalOffenders, rel)
		}

		if filepath.Dir(abs) != thisDir && !strings.HasSuffix(abs, "_test.go") &&
			strings.Contains(text, formatVersionTag) && strings.Contains(text, attributesHashTag) {
			shapeOffenders = append(shapeOffenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %s", err)
	}

	if len(literalOffenders) > 0 {
		t.Errorf(
			"the live snapshot format literal %q appears outside its one declaration in snapshot.go, in: %v\n"+
				"the snapshot is observational and staleness is part of its contract (P4.2 rule 3); "+
				"no code path may read or parse it back. If this is intentional it needs a design review, not a bigger allow list.",
			snapshotFormatVersion, literalOffenders,
		)
	}
	if len(shapeOffenders) > 0 {
		t.Errorf(
			"the live snapshot's struct shape is redeclared outside internal/live/projection, in: %v\n"+
				"the type is unexported so that nothing can decode the snapshot into it; hand-writing the shape is the way around that, "+
				"and a non-test file doing so is a reader. See P4.2 rule 3.",
			shapeOffenders,
		)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func readSnapshot(t *testing.T, path string) *snapshot {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %s", path, err)
	}
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("decoding %s: %s\nraw: %s", path, err, data)
	}
	return &snap
}

func assertNoFilesUnder(t *testing.T, dir string) {
	t.Helper()
	var found []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != dir {
			rel, _ := filepath.Rel(dir, path)
			found = append(found, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("files exist under %s: %v", dir, found)
	}
}
