// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package adoption

// GitHub issue #2286: the adoption-file reader. Written for
// epic:engine-consolidation and not run in the unit that added it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	fixtureIndex   = "testdata/chant-index-v1.json"
	fixtureAdopt   = "testdata/adopt-v1.json"
	fixtureNode    = "aws/000000000000/us-east-1/AWS::S3::Bucket/web-logs"
	fixtureVersion = "20261026T120000Z-e2f92b265a4d.json"
	fixtureKey     = "cloud/aws/000000000000/9bd41033bb42"
)

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func wantErr(t *testing.T, err error, contains string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want an error containing %q, got none", contains)
	}
	if !strings.Contains(err.Error(), contains) {
		t.Fatalf("want an error containing %q, got %q", contains, err)
	}
}

// chant's own fixture, digest and scope key written by chant, reads clean: the
// Go canonical JSON and scope key match chant's byte for byte.
func TestChantIndexFixtureReads(t *testing.T) {
	t.Parallel()
	v, err := ParseIndexVersion(readFixture(t, fixtureIndex))
	if err != nil {
		t.Fatalf("chant's index fixture: %s", err)
	}
	if v.Stamp.ScopeKey != fixtureKey {
		t.Errorf("scopeKey %s, want %s", v.Stamp.ScopeKey, fixtureKey)
	}
	name, err := v.Name()
	if err != nil || name != fixtureVersion {
		t.Errorf("version name %q (%v), want %s", name, err, fixtureVersion)
	}
	n, ok := v.Node(fixtureNode)
	if !ok {
		t.Fatalf("node %s not found", fixtureNode)
	}
	if n.Kind != "AWS::S3::Bucket" || n.PhysicalID != "web-logs" {
		t.Errorf("node read as %+v", n)
	}
}

// A changed graph no longer hashes to the stamped digest; a changed scope no
// longer maps to the stamped key.
func TestIndexVersionRefusesATamperedDocument(t *testing.T) {
	t.Parallel()
	data := string(readFixture(t, fixtureIndex))

	graph := strings.Replace(data, `"BucketName": "web-logs"`, `"BucketName": "web-logs-2"`, 1)
	if graph == data {
		t.Fatal("the fixture no longer has the attribute this test edits")
	}
	_, err := ParseIndexVersion([]byte(graph))
	wantErr(t, err, "not the stamped digest")

	scope := strings.Replace(data, `"scopeId": "000000000000"`, `"scopeId": "111111111111"`, 1)
	_, err = ParseIndexVersion([]byte(scope))
	wantErr(t, err, "is not its scope's key")

	kind := strings.Replace(data, `"kind": "cloud",
      "name"`, `"kind": "engine",
      "name"`, 1)
	if kind == data {
		t.Fatal("the fixture's source kind is not where this test edits it")
	}
	_, err = ParseIndexVersion([]byte(kind))
	wantErr(t, err, "does not match its scope kind")

	_, err = ParseIndexVersion([]byte(strings.Replace(data, `"index": "v1"`, `"index": "v2"`, 1)))
	wantErr(t, err, "is not v1")
}

// The fixture adoption file resolves against chant's index fixture, read from
// a filesystem store at index/<scopeKey>/<version>.
func TestOpenResolvesAgainstTheStore(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	f, err := ReadFile(fixtureAdopt)
	if err != nil {
		t.Fatal(err)
	}
	path := VersionPath(store, f.Index)
	if want := filepath.Join(store, "index", "cloud", "aws", "000000000000", "9bd41033bb42", fixtureVersion); path != want {
		t.Fatalf("VersionPath %s, want %s", path, want)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, readFixture(t, fixtureIndex), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Open(fixtureAdopt, store)
	if err != nil {
		t.Fatalf("Open: %s", err)
	}
	if len(a.Entries) != 1 || a.Entries[0].Node.ID != fixtureNode || a.Entries[0].As != "aws_s3_bucket.web_logs" {
		t.Fatalf("entries %+v", a.Entries)
	}

	// A file under another name is not that version.
	other := filepath.Join(filepath.Dir(path), "20261026T120001Z-e2f92b265a4d.json")
	if err := os.WriteFile(other, readFixture(t, fixtureIndex), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = ReadIndexVersion(other)
	wantErr(t, err, "its stamp names version")
}

// Resolve refuses a version that is not the one the file names, and a node
// the version does not hold.
func TestResolveRefusals(t *testing.T) {
	t.Parallel()
	v, err := ParseIndexVersion(readFixture(t, fixtureIndex))
	if err != nil {
		t.Fatal(err)
	}
	base := func() *File {
		f, err := Parse(readFixture(t, fixtureAdopt))
		if err != nil {
			t.Fatal(err)
		}
		return f
	}

	f := base()
	f.Index.ScopeKey = "cloud/aws/000000000000/aaaaaaaaaaaa"
	_, err = Resolve(f, v)
	wantErr(t, err, "scopeKey")

	f = base()
	f.Index.Digest = "sha256:e2f92b265a4d" + strings.Repeat("0", 52)
	_, err = Resolve(f, v)
	wantErr(t, err, "digest is")

	f = base()
	f.Index.Version = "20261027T120000Z-e2f92b265a4d.json"
	_, err = Resolve(f, v)
	wantErr(t, err, "not the adoption file's 20261027T120000Z")

	f = base()
	f.Select = append(f.Select, Selection{Node: "aws/000000000000/us-east-1/AWS::S3::Bucket/gone", As: "aws_s3_bucket.gone"})
	_, err = Resolve(f, v)
	wantErr(t, err, "holds no node aws/000000000000/us-east-1/AWS::S3::Bucket/gone")

	// The digest is read with or without its prefix.
	f = base()
	f.Index.Digest = strings.TrimPrefix(f.Index.Digest, "sha256:")
	if _, err := Resolve(f, v); err != nil {
		t.Errorf("a digest without sha256: should resolve: %s", err)
	}
}

// The file is checked on its own before any index is read.
func TestParseRefusals(t *testing.T) {
	t.Parallel()
	good := string(readFixture(t, fixtureAdopt))
	for _, tc := range []struct {
		name, from, to, want string
	}{
		{"version", `"adopt": "v1"`, `"adopt": "v2"`, `version "v2" is not v1`},
		{"unknown field", `"adopt": "v1",`, `"adopt": "v1", "extra": true,`, "unknown field"},
		{"digest not hex", `"digest": "sha256:e2f9`, `"digest": "sha256:zzf9`, "is not sha256 hex"},
		{"version name", fixtureVersion, "latest.json", "is not <yyyymmddThhmmssZ>"},
		{"version not the digest's", fixtureVersion, "20261026T120000Z-000000000000.json", "first 12 hex"},
		{"no address", `"as": "aws_s3_bucket.web_logs",`, `"as": "",`, "names no address"},
		{"no node", `"node": "` + fixtureNode + `",`, `"node": "",`, "names no node"},
		{"scope key escapes the store", `"scopeKey": "` + fixtureKey + `"`, `"scopeKey": "cloud/../../etc"`, "is not a store path"},
	} {
		in := strings.Replace(good, tc.from, tc.to, 1)
		if in == good {
			t.Fatalf("%s: the fixture has no %q to replace", tc.name, tc.from)
		}
		_, err := Parse([]byte(in))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want an error containing %q, got %v", tc.name, tc.want, err)
		}
	}

	twice := strings.Replace(good, `"select": [`, `"select": [{"node": "`+fixtureNode+`", "as": "x.y"},`, 1)
	_, err := Parse([]byte(twice))
	wantErr(t, err, "twice")

	f, err := Parse([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	f.Select = nil
	wantErr(t, f.Validate(), "selects no node")
}

// The engine type: named, or the one candidate; an ambiguous join without a
// named type is refused with every candidate (pkg/registry maps
// AWS::ElasticLoadBalancingV2::LoadBalancer to aws_alb and aws_lb).
func TestSelectionEngineType(t *testing.T) {
	t.Parallel()
	lb := []string{"aws_alb", "aws_lb"}
	if got, err := (Selection{Node: "n", Type: "aws_lb"}).EngineType(lb); err != nil || got != "aws_lb" {
		t.Errorf("named type: %q, %v", got, err)
	}
	_, err := (Selection{Node: "n"}).EngineType(lb)
	wantErr(t, err, "aws_alb, aws_lb")
	_, err = (Selection{Node: "n", Type: "aws_s3_bucket"}).EngineType(lb)
	wantErr(t, err, "is not one the listed type joins to")
	if got, err := (Selection{Node: "n"}).EngineType([]string{"aws_s3_bucket"}); err != nil || got != "aws_s3_bucket" {
		t.Errorf("one candidate: %q, %v", got, err)
	}
	_, err = (Selection{Node: "n"}).EngineType(nil)
	wantErr(t, err, "joins to no engine type")
}

// The canonical form is JavaScript's: UTF-16 key order, JavaScript numbers, no
// HTML escaping.
func TestCanonicalJSONIsJavaScripts(t *testing.T) {
	t.Parallel()
	v, err := decodeJSON([]byte(`{"b":1.0,"a":[1e21,1.5e-7,0.000001,-0,100],"｡":"<&> ","😀":"\u0001\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := canonicalJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	// U+1F600 is the surrogate pair D83D DE00, which sorts before U+FF61 by
	// UTF-16 code unit although it is after it by code point.
	want := "{\"a\":[1e+21,1.5e-7,0.000001,0,100],\"b\":1,\"\U0001F600\":\"\\u0001\\n\",\"｡\":\"<&> \"}"
	if got != want {
		t.Errorf("canonical JSON\n got %s\nwant %s", got, want)
	}
}

// Index key segments are percent-encoded as chant's encodeIndexSegment does.
func TestEncodeSegment(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"000000000000": "000000000000", "my stack": "my%20stack", "a/b:c": "a%2Fb%3Ac",
		"it's(*)!": "it%27s%28%2A%29%21", ".": "%2E", "..": "%2E%2E", "a~b_c-d.e": "a~b_c-d.e",
	} {
		if got, err := encodeSegment(in); err != nil || got != want {
			t.Errorf("encodeSegment(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := encodeSegment(""); err == nil {
		t.Error("an empty segment is refused")
	}
}
