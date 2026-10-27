// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/livegraph"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/pkg/staterecord"
)

// GitHub issue #2285's acceptance tests at the command: the default path
// reads no network and starts no provider, no run writes a file under the
// estate or into the record store, and no sensitive value reaches stdout
// or stderr.

// lgSmall is the package fixture the livegraph tests use.
var lgSmall = filepath.Join("..", "live", "livegraph", "testdata", "small")

// lgCopy copies src into a fresh directory and applies the overrides
// (name -> body; "" removes the file).
func lgCopy(t *testing.T, src string, overrides map[string]string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range overrides {
		p := filepath.Join(dst, name)
		if body == "" {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

// lgNoProviders is a provider set whose every factory fails the test: a
// run that starts any provider is caught here, whatever its source.
func lgNoProviders(t *testing.T) *testingOverrides {
	fail := func(name string) providers.Factory {
		return func() (providers.Interface, error) {
			t.Errorf("live-graph started provider %s", name)
			return nil, errors.New("no provider may start")
		}
	}
	return &testingOverrides{Providers: map[addrs.Provider]providers.Factory{
		addrs.NewDefaultProvider("aws"):       fail("aws"),
		addrs.NewBuiltInProvider("terraform"): fail("terraform"),
	}}
}

// lgRefuseTransport fails the test on any HTTP request.
type lgRefuseTransport struct {
	t     *testing.T
	calls atomic.Int32
}

func (r *lgRefuseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.calls.Add(1)
	r.t.Errorf("live-graph made a network request: %s %s", req.Method, req.URL)
	return nil, errors.New("no network in this test")
}

func lgRun(t *testing.T, open recordStoreOpener, args ...string) (int, string, string) {
	t.Helper()
	view, done := testView(t)
	c := &LiveGraphCommand{Meta: Meta{WorkingDir: workdir.NewDir("."), View: view, testingOverrides: lgNoProviders(t)}, open: open}
	code := c.Run(args)
	out := done(t)
	return code, out.Stdout(), out.Stderr()
}

func lgDecode(t *testing.T, stdout string) livegraph.Document {
	t.Helper()
	var doc livegraph.Document
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	return doc
}

func TestLiveGraphRequiresJSON(t *testing.T) {
	dir := lgCopy(t, lgSmall, nil)
	code, stdout, stderr := lgRun(t, nil, dir)
	if code == 0 || stdout != "" || !strings.Contains(stderr, "-json") {
		t.Errorf("live-graph without -json: exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
}

// TestLiveGraphDefaultPathIsOffline: without -from-plan, live-graph makes
// no network call and starts no provider. Every HTTP request through the
// default transport fails the test, and so does every provider factory.
func TestLiveGraphDefaultPathIsOffline(t *testing.T) {
	refuse := &lgRefuseTransport{t: t}
	old := http.DefaultTransport
	http.DefaultTransport = refuse
	t.Cleanup(func() { http.DefaultTransport = old })
	// A proxy or endpoint variable is no way out either.
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("AWS_ENDPOINT_URL", "http://127.0.0.1:1")

	dir := lgCopy(t, lgSmall, nil)
	code, stdout, stderr := lgRun(t, nil, "-json", dir)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	doc := lgDecode(t, stdout)
	if len(doc.Nodes) == 0 || len(doc.Edges) == 0 {
		t.Errorf("the graph is empty: %s", stdout)
	}
	if doc.Meta.EngineGraph.Scope.Estate != "web" || doc.Meta.EngineGraph.Engine.Name != "choudoufu" {
		t.Errorf("meta.engineGraph = %+v", doc.Meta.EngineGraph)
	}
	if ms := doc.Meta.EngineGraph.MarkerScheme; ms == nil || ms.OwnerKey != "tofu-estate" || ms.AddressKey != "tofu-address" || len(ms.ContinuationKeys) != 3 {
		t.Errorf("markerScheme = %+v, want choudoufu's configured keys", ms)
	}
	if refuse.calls.Load() != 0 {
		t.Errorf("%d network requests", refuse.calls.Load())
	}
}

// lgTree is every file under dir with its size, mode and modification
// time.
func lgTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out[p] = fmt.Sprintf("%v %d %s", info.Mode(), info.Size(), info.ModTime().Format(time.RFC3339Nano))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// lgReadOnlyStore is a record store whose every write fails the test.
type lgReadOnlyStore struct {
	staterecord.Store
	t        *testing.T
	versions map[string][]staterecord.RecordVersion
	listed   int
}

func (s *lgReadOnlyStore) PutIfVersion(context.Context, string, []byte, string) (string, error) {
	s.t.Error("live-graph wrote a record (PutIfVersion)")
	return "", errors.New("read-only")
}

func (s *lgReadOnlyStore) PutIfAbsent(context.Context, string, []byte) (string, error) {
	s.t.Error("live-graph wrote a record (PutIfAbsent)")
	return "", errors.New("read-only")
}

func (s *lgReadOnlyStore) Delete(context.Context, string, string) error {
	s.t.Error("live-graph deleted a record")
	return errors.New("read-only")
}

func (s *lgReadOnlyStore) ListVersions(_ context.Context, key string) ([]staterecord.RecordVersion, error) {
	s.listed++
	return s.versions[key], nil
}

const lgLiveBlock = `terraform {
  live {
    estate = "web"

    record_store "s3" {
      bucket = "web-records"
    }
  }
}
`

func lgOpener(t *testing.T, store *lgReadOnlyStore) recordStoreOpener {
	return func(_ context.Context, rs *configs.LiveRecordStore, _ *configs.LiveRetry, estate, _ string, _ ...projection.RecordStoreOption) (staterecord.Store, error) {
		if rs.Type != "s3" || estate != "web" {
			t.Errorf("opened a %q store for estate %q, want s3 for web", rs.Type, estate)
		}
		return store, nil
	}
}

// TestLiveGraphWritesNothing: no file under the estate directory is made,
// changed or removed, by the default path, by -from-plan, or by -history,
// and the record store sees no write.
func TestLiveGraphWritesNothing(t *testing.T) {
	dir := lgCopy(t, lgSmall, map[string]string{"estate.chdf.hcl": "", "live.tf": lgLiveBlock})
	plan := filepath.Join(dir, "web.live-plan.json")
	b, err := os.ReadFile(filepath.Join("..", "live", "livegraph", "testdata", "small.live-plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plan, b, 0o600); err != nil {
		t.Fatal(err)
	}
	local, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	addr, diags := addrs.ParseAbsResourceInstanceStr("aws_s3_bucket.logs")
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	key := projection.RecordKey(projection.RecordKeyPrefix("web"), addr)
	store := &lgReadOnlyStore{Store: local, t: t, versions: map[string][]staterecord.RecordVersion{key: {
		{VersionID: "v2", LastModified: time.Date(2026, 10, 26, 11, 58, 2, 0, time.UTC), Current: true},
		{VersionID: "v1", LastModified: time.Date(2026, 10, 20, 9, 14, 40, 0, time.UTC)},
	}}}

	before := lgTree(t, dir)
	for _, args := range [][]string{
		{"-json", dir},
		{"-json", "-from-plan=" + plan, dir},
		{"-json", "-from-plan=" + plan, "-history", dir},
	} {
		code, stdout, stderr := lgRun(t, lgOpener(t, store), args...)
		if code != 0 {
			t.Fatalf("%v: exit %d\n%s", args, code, stderr)
		}
		lgDecode(t, stdout)
		after := lgTree(t, dir)
		var changed []string
		for p, v := range after {
			if before[p] != v {
				changed = append(changed, p)
			}
		}
		for p := range before {
			if _, ok := after[p]; !ok {
				changed = append(changed, p+" (removed)")
			}
		}
		sort.Strings(changed)
		if len(changed) > 0 {
			t.Errorf("%v changed files under the estate directory: %v", args, changed)
		}
	}
	if store.listed == 0 {
		t.Error("-history listed no record's versions; the record store fake was never asked")
	}
}

// TestLiveGraphHistory: -history carries version ids and times, and a
// non-s3 store says no versions are kept without being opened.
func TestLiveGraphHistory(t *testing.T) {
	dir := lgCopy(t, lgSmall, map[string]string{"estate.chdf.hcl": "", "live.tf": lgLiveBlock})
	local, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	addr, _ := addrs.ParseAbsResourceInstanceStr("aws_s3_bucket.logs")
	key := projection.RecordKey(projection.RecordKeyPrefix("web"), addr)
	const payload = "a-record-payload-live-graph-never-prints"
	if _, err := local.PutIfAbsent(context.Background(), key, []byte(payload)); err != nil {
		t.Fatal(err)
	}
	store := &lgReadOnlyStore{Store: local, t: t, versions: map[string][]staterecord.RecordVersion{key: {
		{VersionID: "v2", LastModified: time.Date(2026, 10, 26, 11, 58, 2, 0, time.UTC), Current: true},
	}}}
	code, stdout, stderr := lgRun(t, lgOpener(t, store), "-json", "-history", dir)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if strings.Contains(stdout, payload) {
		t.Error("the graph printed a record's payload")
	}
	doc := lgDecode(t, stdout)
	for _, n := range doc.Nodes {
		if n.ID != "aws_s3_bucket.logs" {
			continue
		}
		rv := n.Facts.RecordVersions
		if rv == nil || !rv.Kept || len(rv.Versions) != 1 || rv.Versions[0].ID != "v2" || rv.Versions[0].WrittenAt != "2026-10-26T11:58:02Z" || !rv.Versions[0].Current {
			t.Errorf("aws_s3_bucket.logs recordVersions = %+v", rv)
		}
	}

	local2 := lgCopy(t, lgSmall, map[string]string{"estate.chdf.hcl": "", "live.tf": strings.Replace(lgLiveBlock, `record_store "s3" {
      bucket = "web-records"
    }`, `record_store "local" {}`, 1)})
	opened := false
	code, stdout, stderr = lgRun(t, func(context.Context, *configs.LiveRecordStore, *configs.LiveRetry, string, string, ...projection.RecordStoreOption) (staterecord.Store, error) {
		opened = true
		return nil, errors.New("not opened")
	}, "-json", "-history", local2)
	if code != 0 {
		t.Fatalf("local store: exit %d\n%s", code, stderr)
	}
	if opened {
		t.Error("a local record store was opened for -history")
	}
	if !strings.Contains(stdout, `"kept": false`) {
		t.Errorf("a local store's records do not say no versions are kept:\n%s", stdout)
	}
}

func TestLiveGraphRefusesAnotherEstatesPlan(t *testing.T) {
	dir := lgCopy(t, lgSmall, nil)
	plan := filepath.Join(t.TempDir(), "other.json")
	if err := os.WriteFile(plan, []byte(`{"estate": "billing", "bound": []}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := lgRun(t, nil, "-json", "-from-plan="+plan, dir)
	if code == 0 || stdout != "" || !strings.Contains(stderr, "another estate") {
		t.Errorf("a plan of another estate: exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
}

// ---------------------------------------------------------------------------
// Secrets
// ---------------------------------------------------------------------------

const (
	lgVarSecret  = "var-s3cr3t-7731-never-printed"
	lgAttrSecret = "attr-s3cr3t-9924-never-printed"
)

// lgSecretMain declares a sensitive variable and a provider-sensitive
// argument (aws_db_instance.password), and reads both into other blocks.
const lgSecretMain = `
variable "db_password" {
  type      = string
  sensitive = true
  default   = "` + lgVarSecret + `"
}

resource "aws_db_instance" "main" {
  identifier = "web-db"
  password   = var.db_password
}

resource "aws_db_instance" "replica" {
  identifier = "web-db-replica"
  password   = "` + lgAttrSecret + `"
}

resource "aws_ssm_parameter" "dsn" {
  name  = "/web/dsn"
  type  = "SecureString"
  value = "${aws_db_instance.main.endpoint}:${var.db_password}"
}
`

// lgSecretPlan is a fake live read of that estate: a live-plan -json
// document whose rows a sweep would produce, with both secrets in every
// place a live-plan document can hold a value the graph must not copy.
var lgSecretPlan = `{
  "estate": "web",
  "observed_at": "2026-10-26T12:00:00Z",
  "bound": [
    {"addr": "aws_db_instance.main", "type": "aws_db_instance", "identity": "web-db", "source": "marker",
     "identity_values": {"password": "` + lgVarSecret + `"}}
  ],
  "omissions": [
    {"addr": "aws_db_instance.replica", "reason": "ABSENT", "detail": "password ` + lgAttrSecret + `"}
  ],
  "unowned": [],
  "foreign": [{"type": "aws_ssm_parameter", "identity": "/other", "why": "` + lgVarSecret + `"}],
  "adoptable": [
    {"addr": "aws_ssm_parameter.dsn", "type": "aws_ssm_parameter", "identity": "/web/dsn",
     "matched": [{"attribute": "value", "value": "` + lgVarSecret + `"}, {"attribute": "password", "value": "` + lgAttrSecret + `"}],
     "adopt_tofu_estate": "web", "adopt_tofu_address": "aws_ssm_parameter.dsn"}
  ],
  "swept": ["aws_ssm_parameter"],
  "diagnostics": [{"severity": "warning", "summary": "x", "detail": "` + lgAttrSecret + `"}]
}`

// lgNoSecrets reports each secret found in out. It takes a testing.TB so
// the test can show it failing on a deliberate leak.
func lgNoSecrets(tb testing.TB, where, out string) {
	tb.Helper()
	for _, s := range []string{lgVarSecret, lgAttrSecret} {
		if strings.Contains(out, s) {
			tb.Errorf("%s holds the sensitive value %q", where, s)
		}
	}
}

// lgRecorder is a testing.TB that records Errorf instead of failing.
type lgRecorder struct {
	testing.TB
	failed bool
}

func (r *lgRecorder) Errorf(string, ...any) { r.failed = true }
func (r *lgRecorder) Helper()               {}

// TestLiveGraphPrintsNoSensitiveValue: a sensitive variable and a
// provider-sensitive argument, emitted with live facts from a fake live
// read that carries both values in every field it can, and with a state
// cache on disk that holds them, never reach stdout or stderr.
//
// Red first: the check is run on a deliberate leak and must fail there.
func TestLiveGraphPrintsNoSensitiveValue(t *testing.T) {
	t.Run("the check catches a deliberate leak", func(t *testing.T) {
		leak := livegraph.Document{Nodes: []livegraph.Node{{ID: "aws_db_instance.main", Kind: "aws_db_instance", Lexicon: "terraform",
			Attrs: livegraph.NodeAttrs{Address: lgVarSecret}}}}
		b, _ := json.Marshal(leak)
		rec := &lgRecorder{TB: t}
		lgNoSecrets(rec, "a deliberately leaking document", string(b))
		if !rec.failed {
			t.Fatal("the secret check passed a document that holds the secret; it cannot fail")
		}
	})

	dir := lgCopy(t, filepath.Join(lgSmall), map[string]string{
		"main.tf": lgSecretMain, "modules/app/main.tf": "",
		"estate.chdf.hcl": "", "live.tf": lgLiveBlock,
		// The state cache, and a stock state file: neither is ever read.
		"terraform.tfstate":            `{"version": 4, "resources": [{"instances": [{"attributes": {"password": "` + lgVarSecret + `"}}]}]}`,
		".terraform/terraform.tfstate": `{"version": 4, "outputs": {"p": {"value": "` + lgAttrSecret + `"}}}`,
	})
	plan := filepath.Join(t.TempDir(), "web.live-plan.json")
	if err := os.WriteFile(plan, []byte(lgSecretPlan), 0o600); err != nil {
		t.Fatal(err)
	}
	local, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	addr, _ := addrs.ParseAbsResourceInstanceStr("aws_db_instance.main")
	key := projection.RecordKey(projection.RecordKeyPrefix("web"), addr)
	if _, err := local.PutIfAbsent(context.Background(), key, []byte(`{"password":"`+lgVarSecret+`"}`)); err != nil {
		t.Fatal(err)
	}
	store := &lgReadOnlyStore{Store: local, t: t, versions: map[string][]staterecord.RecordVersion{key: {
		{VersionID: "v1", LastModified: time.Date(2026, 10, 26, 11, 0, 0, 0, time.UTC), Current: true},
	}}}

	for _, args := range [][]string{
		{"-json", dir},
		{"-json", "-from-plan=" + plan, dir},
		{"-json", "-from-plan=" + plan, "-history", dir},
	} {
		code, stdout, stderr := lgRun(t, lgOpener(t, store), args...)
		if code != 0 {
			t.Fatalf("%v: exit %d\n%s", args, code, stderr)
		}
		lgNoSecrets(t, fmt.Sprintf("stdout of %v", args), stdout)
		lgNoSecrets(t, fmt.Sprintf("stderr of %v", args), stderr)
		doc := lgDecode(t, stdout)
		if len(args) > 2 {
			// The facts did arrive, so the test is not passing on an empty
			// document.
			found := false
			for _, n := range doc.Nodes {
				if n.ID == "aws_db_instance.main" && n.Facts != nil && n.Facts.Ownership == "bound" {
					found = true
				}
			}
			if !found {
				t.Errorf("%v: aws_db_instance.main has no bound facts:\n%s", args, stdout)
			}
		}
	}
}
