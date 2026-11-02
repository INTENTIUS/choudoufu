// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/investigate"
	"github.com/intentius/choudoufu/internal/live/largeset"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/pkg/cloudcontrol"
	"github.com/intentius/choudoufu/pkg/staterecord"
)

// runInvestigate runs "choudoufu investigate" with no chant and no behold,
// so only the native lookups are in reach, and decodes the -json answer.
func runInvestigate(t *testing.T, c *InvestigateCommand, args ...string) (int, investigate.Answer, string) {
	t.Helper()
	view, done := testView(t)
	c.Meta = Meta{WorkingDir: workdir.NewDir("."), View: view}
	code := c.Run(append([]string{"-json", "-chant=none", "-behold=none"}, args...))
	out := done(t)
	var a investigate.Answer
	if strings.TrimSpace(out.Stdout()) != "" {
		if err := json.Unmarshal([]byte(out.Stdout()), &a); err != nil {
			t.Fatalf("%v\nstdout:\n%s\nstderr:\n%s", err, out.Stdout(), out.Stderr())
		}
	}
	return code, a, out.Stderr()
}

// TestInvestigateOwner: the owner of a cloud ID, from its markers. The
// fixture is one bucket's tags; the BREAK moves it to another estate, and
// the answer changes.
func TestInvestigateOwner(t *testing.T) {
	const arn = "arn:aws:s3:::web-logs"
	fixture := func(estate string) ownerTagReader {
		return func(_ context.Context, a cloudcontrol.ARN, id string) (map[string]string, bool, string, error) {
			if id != arn || a.Service != "s3" {
				t.Errorf("read %s (service %s), want %s", id, a.Service, arn)
			}
			return map[string]string{
				markers.TagEstate:  estate,
				markers.TagAddress: markers.EscapeAddress("aws_s3_bucket.logs"),
				"team":             "a-tag-the-answer-never-prints",
			}, true, "tagging", nil
		}
	}
	owner := func(t *testing.T, read ownerTagReader, id string) (int, investigate.Answer, map[string]any) {
		code, a, stderr := runInvestigate(t, &InvestigateCommand{ownerTags: read}, "owner", id)
		var res map[string]any
		if len(a.Result) > 0 {
			if err := json.Unmarshal(a.Result, &res); err != nil {
				t.Fatalf("%v: %s (%s)", err, a.Result, stderr)
			}
		}
		return code, a, res
	}

	code, a, res := owner(t, fixture("web"), arn)
	if code != 0 || a.Kind != investigate.KindNative || a.Method != investigate.MethodDeterministic || a.ObservedAt == "" || a.Reads != "live markers" {
		t.Fatalf("exit %d, answer %+v", code, a)
	}
	want := map[string]any{"estate": "web", "address": "aws_s3_bucket.logs", "owned": true}
	for k, v := range want {
		if res[k] != v {
			t.Errorf("%s = %v, want %v (%v)", k, res[k], v, res)
		}
	}
	if strings.Contains(string(a.Result), "a-tag-the-answer-never-prints") {
		t.Errorf("the answer carries a tag that is not a marker: %s", a.Result)
	}

	_, _, broken := owner(t, fixture("api"), arn)
	if broken["estate"] == want["estate"] {
		t.Errorf("BREAK: the bucket moved to estate api and the answer still reads %v", broken["estate"])
	}

	t.Run("no marker", func(t *testing.T) {
		none := func(context.Context, cloudcontrol.ARN, string) (map[string]string, bool, string, error) {
			return nil, false, "tagging", nil
		}
		code, _, res := owner(t, none, arn)
		if code != 0 || res["owned"] != false {
			t.Errorf("exit %d, %v", code, res)
		}
	})

	t.Run("not an ARN", func(t *testing.T) {
		called := false
		read := func(context.Context, cloudcontrol.ARN, string) (map[string]string, bool, string, error) {
			called = true
			return nil, false, "", nil
		}
		code, a, _ := owner(t, read, "/subscriptions/x/resourceGroups/rg")
		if code != 1 || !strings.Contains(a.Refused, "is not an AWS ARN") || called {
			t.Errorf("exit %d, %+v, read called %v", code, a, called)
		}
	})
}

// TestInvestigateForeign: the live resources nobody declares, from a
// live-plan document. The BREAK adds an undeclared queue to the plan.
func TestInvestigateForeign(t *testing.T) {
	plan := func(t *testing.T, extra string) string {
		doc := `{
  "estate": "web",
  "observed_at": "2026-10-26T10:00:00Z",
  "swept": ["aws_s3_bucket", "aws_sqs_queue"],
  "foreign": [
    {"type": "aws_sqs_queue", "identity": "https://sqs.us-east-1.amazonaws.com/000000000000/orphan", "why": "no marker"},
    {"type": "aws_s3_bucket", "identity": "old-logs", "tofu_estate": "api", "why": "another estate's marker"}` + extra + `
  ]
}`
		p := filepath.Join(t.TempDir(), "plan.json")
		if err := os.WriteFile(p, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	ids := func(a investigate.Answer) []string {
		var res struct {
			Count   int `json:"count"`
			Foreign []struct {
				ID string `json:"identity"`
			} `json:"foreign"`
		}
		if err := json.Unmarshal(a.Result, &res); err != nil {
			t.Fatalf("%v: %s", err, a.Result)
		}
		var out []string
		for _, f := range res.Foreign {
			out = append(out, f.ID)
		}
		if res.Count != len(out) {
			t.Errorf("count %d for %d rows", res.Count, len(out))
		}
		return out
	}

	code, a, stderr := runInvestigate(t, &InvestigateCommand{}, "foreign", plan(t, ""))
	if code != 0 {
		t.Fatalf("exit %d: %+v %s", code, a, stderr)
	}
	want := []string{"old-logs", "https://sqs.us-east-1.amazonaws.com/000000000000/orphan"}
	if got := ids(a); !reflect.DeepEqual(got, want) {
		t.Errorf("foreign %v, want %v", got, want)
	}
	if a.ObservedAt != "2026-10-26T10:00:00Z" {
		t.Errorf("observed_at %q, want the plan's", a.ObservedAt)
	}

	_, broken, _ := runInvestigate(t, &InvestigateCommand{}, "foreign", plan(t, `,
    {"type": "aws_sqs_queue", "identity": "https://sqs.us-east-1.amazonaws.com/000000000000/injected"}`))
	if got := ids(broken); reflect.DeepEqual(got, want) {
		t.Errorf("BREAK: an undeclared queue was added and the answer still reads %v", got)
	}

	t.Run("a type filter", func(t *testing.T) {
		_, a, _ := runInvestigate(t, &InvestigateCommand{}, "foreign", plan(t, ""), "type=aws_s3_bucket")
		if got := ids(a); !reflect.DeepEqual(got, []string{"old-logs"}) {
			t.Errorf("foreign %v", got)
		}
		_, a, _ = runInvestigate(t, &InvestigateCommand{}, "foreign", plan(t, ""), "type=aws_iam_role")
		if !strings.Contains(string(a.Result), "did not enumerate aws_iam_role") {
			t.Errorf("an unswept type is not called unswept: %s", a.Result)
		}
	})

	t.Run("not a plan", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "x.json")
		if err := os.WriteFile(p, []byte(`{"estate": "web"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		code, a, _ := runInvestigate(t, &InvestigateCommand{}, "foreign", p)
		if code != 1 || !strings.Contains(a.Refused, "no foreign section") {
			t.Errorf("exit %d, %+v", code, a)
		}
	})
}

// TestInvestigateAffected: which roots a git range touches, over the #1750
// fixture. The BREAK widens the range over a provider bump in another root.
func TestInvestigateAffected(t *testing.T) {
	dir := t.TempDir()
	if _, err := largeset.Write(dir, largeset.Options{Estates: 5, ModuleVersion: largeset.VersionA}); err != nil {
		t.Fatal(err)
	}
	liveAffectedGit(t, dir, "init", "-q", "-b", "main")
	liveAffectedGit(t, dir, "add", "-A")
	liveAffectedGit(t, dir, "commit", "-q", "-m", "base")
	base := liveAffectedGit(t, dir, "rev-parse", "HEAD")
	edit := func(rel, old, new string) string {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(strings.Replace(string(b), old, new, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
		liveAffectedGit(t, dir, "commit", "-q", "-am", rel)
		return liveAffectedGit(t, dir, "rev-parse", "HEAD")
	}
	oneRoot := edit("estates/e01/main.tf", "# Root e01", "# Root e01, edited")
	providerBump := edit("estates/e05/main.tf", `version = "= 6.59.0"`, `version = "= 6.60.0"`)

	roots := func(a investigate.Answer) []string {
		var res struct {
			Roots []struct {
				Dir string `json:"dir"`
			} `json:"roots"`
		}
		if err := json.Unmarshal(a.Result, &res); err != nil {
			t.Fatalf("%v: %s", err, a.Result)
		}
		var out []string
		for _, r := range res.Roots {
			out = append(out, r.Dir)
		}
		return out
	}

	code, a, stderr := runInvestigate(t, &InvestigateCommand{}, "affected", base+".."+oneRoot, "dir="+dir)
	if code != 0 {
		t.Fatalf("exit %d: %+v %s", code, a, stderr)
	}
	want := []string{"estates/e01", "estates/e02", "estates/e03"}
	if got := roots(a); !reflect.DeepEqual(got, want) {
		t.Errorf("roots %v, want %v", got, want)
	}

	_, broken, _ := runInvestigate(t, &InvestigateCommand{}, "affected", base+".."+providerBump, "dir="+dir)
	if got := roots(broken); reflect.DeepEqual(got, want) {
		t.Errorf("BREAK: the range took in e05's provider bump and the answer still reads %v", got)
	}
}

// TestInvestigateHistory: a record's past versions through investigate,
// with no record value in the answer. The BREAK is a fourth write.
func TestInvestigateHistory(t *testing.T) {
	const address = `terraform_data.config["eu"]`
	addr, diags := addrs.ParseAbsResourceInstanceStr(address)
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	key := projection.RecordKey(projection.RecordKeyPrefix("prod"), addr)
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	const secret = "the-value-an-investigation-never-prints"
	opener := func(versions []staterecord.RecordVersion) recordStoreOpener {
		return func(ctx context.Context, rs *configs.LiveRecordStore, _ *configs.LiveRetry, estate, _ string, _ ...projection.RecordStoreOption) (staterecord.Store, error) {
			local, err := staterecord.NewLocalStore(t.TempDir())
			if err != nil {
				return nil, err
			}
			if _, err := local.PutIfAbsent(ctx, key, []byte(secret)); err != nil {
				return nil, err
			}
			inner := &historyStore{Store: local, versions: map[string][]staterecord.RecordVersion{key: versions}}
			return staterecord.NewRunCache(inner, projection.RecordKeyPrefix("prod")), nil
		}
	}
	three := []staterecord.RecordVersion{
		{VersionID: "v3", LastModified: base.Add(2 * time.Hour), Current: true},
		{VersionID: "v2", LastModified: base.Add(time.Hour)},
		{VersionID: "v1", LastModified: base},
	}
	versionIDs := func(a investigate.Answer) []string {
		var h liveHistory
		if err := json.Unmarshal(a.Result, &h); err != nil {
			t.Fatalf("%v: %s", err, a.Result)
		}
		var out []string
		for _, v := range h.Versions {
			out = append(out, v.VersionID)
		}
		return out
	}

	writeLiveHistoryConfig(t, `record_store "s3" { bucket = "prod-records" }`)
	code, a, stderr := runInvestigate(t, &InvestigateCommand{open: opener(three)}, "history", address)
	if code != 0 {
		t.Fatalf("exit %d: %+v %s", code, a, stderr)
	}
	want := []string{"v3", "v2", "v1"}
	if got := versionIDs(a); !reflect.DeepEqual(got, want) {
		t.Errorf("versions %v, want %v", got, want)
	}
	if strings.Contains(string(a.Result), secret) {
		t.Errorf("the answer carries a record's value: %s", a.Result)
	}

	four := append([]staterecord.RecordVersion{{VersionID: "v4", LastModified: base.Add(3 * time.Hour), Current: true}}, three...)
	four[1].Current = false
	_, broken, _ := runInvestigate(t, &InvestigateCommand{open: opener(four)}, "history", address)
	if got := versionIDs(broken); reflect.DeepEqual(got, want) {
		t.Errorf("BREAK: a fourth write and the answer still reads %v", got)
	}
}

// TestInvestigateWithoutChant: with no chant, -list says the templates are
// unavailable and still lists the native lookups, and a template name is
// refused with the reason.
func TestInvestigateWithoutChant(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	view, done := testView(t)
	code := (&InvestigateCommand{Meta: Meta{View: view}}).Run([]string{"-list", "-json"})
	out := done(t)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out.Stderr())
	}
	var listing struct {
		Natives              []investigate.Native `json:"natives"`
		TemplatesUnavailable string               `json:"templates_unavailable"`
	}
	if err := json.Unmarshal([]byte(out.Stdout()), &listing); err != nil {
		t.Fatalf("%v: %s", err, out.Stdout())
	}
	var ids []string
	for _, n := range listing.Natives {
		ids = append(ids, n.ID)
	}
	if !reflect.DeepEqual(ids, []string{"owner", "history", "foreign", "affected"}) {
		t.Errorf("natives %v", ids)
	}
	if !strings.Contains(listing.TemplatesUnavailable, "no chant binary on PATH") {
		t.Errorf("templates_unavailable %q", listing.TemplatesUnavailable)
	}

	view, done = testView(t)
	code = (&InvestigateCommand{Meta: Meta{View: view}}).Run([]string{"dependents", "aws_s3_bucket.logs"})
	out = done(t)
	if code != 1 || !strings.Contains(out.Stderr(), "the templates are unavailable") {
		t.Errorf("exit %d: %s", code, out.Stderr())
	}
}

// TestMcpCommandWithoutNeighbours: "choudoufu mcp" with no behold and no
// chant lists the native lookups and no behold: or chant: tool.
func TestMcpCommandWithoutNeighbours(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n")
	var out strings.Builder
	view, done := testView(t)
	c := &McpCommand{Meta: Meta{View: view}, stdin: in, stdout: &out}
	code := c.Run(nil)
	output := done(t)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, output.Stderr())
	}
	var tools []string
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var msg struct {
			ID     int `json:"id"`
			Result struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
				Instructions string `json:"instructions"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("%v: %s", err, line)
		}
		if msg.ID == 1 && !strings.Contains(msg.Result.Instructions, "graph templates unavailable") {
			t.Errorf("instructions do not say graph templates are unavailable: %s", msg.Result.Instructions)
		}
		for _, tl := range msg.Result.Tools {
			tools = append(tools, tl.Name)
		}
	}
	for _, want := range []string{"owner", "history", "foreign", "affected", "templates", "investigate"} {
		found := false
		for _, n := range tools {
			found = found || n == want
		}
		if !found {
			t.Errorf("%s is not listed: %v", want, tools)
		}
	}
	for _, n := range tools {
		if strings.HasPrefix(n, "behold:") || strings.HasPrefix(n, "chant:") {
			t.Errorf("%s is listed with no behold or chant", n)
		}
	}
}
