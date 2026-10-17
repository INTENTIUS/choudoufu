// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/live/mv"
	"github.com/intentius/choudoufu/internal/providers"
)

// -progress-json (GitHub issue #2063): one stderr line per resource as its
// marker write returns, refusals and skips included, with stdout unchanged.

// progressLines returns every -progress-json line on stderr, in order. The
// diagnostics share stderr, and none of their lines starts with "{".
func progressLines(t *testing.T, stderr string) []views.LiveProgressLine {
	t.Helper()
	var out []views.LiveProgressLine
	for _, raw := range strings.Split(stderr, "\n") {
		if !strings.HasPrefix(raw, "{") {
			continue
		}
		var l views.LiveProgressLine
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&l); err != nil {
			t.Fatalf("a -progress-json line does not parse: %s\n%s", err, raw)
		}
		if _, err := time.Parse(time.RFC3339Nano, l.At); err != nil {
			t.Errorf("at = %q is not RFC 3339: %s", l.At, err)
		}
		out = append(out, l)
	}
	return out
}

// liveMvThreeMoves runs three live-mv commands against one fresh copy of
// the renamed fixture, all writing to the same streams, the way a carve
// runs one live-mv per resource: the security group moves, the VPC is
// refused (the provider cannot list it), and the bucket moves.
func liveMvThreeMoves(t *testing.T, extra ...string) (stdout, stderr string, cloud *mvCloud) {
	t.Helper()
	cloud = mvRenamedFixture(t)
	view, done := testView(t)
	runs := []struct {
		args []string
		code int
	}{
		{[]string{"aws_security_group.main", "aws_security_group.renamed"}, 0},
		{[]string{"-allow-missing-config", "aws_vpc.main", "aws_vpc.core"}, 1},
		{[]string{"aws_s3_bucket.data", "aws_s3_bucket.archive"}, 0},
	}
	for _, r := range runs {
		c := &LiveMvCommand{Meta: Meta{
			WorkingDir: workdir.NewDir("."),
			View:       view,
			testingOverrides: &testingOverrides{Providers: map[addrs.Provider]providers.Factory{
				addrs.NewDefaultProvider("aws"): providers.FactoryFixed(cloud.provider()),
			}},
		}}
		args := append(append([]string{"-no-color", "-json"}, extra...), r.args...)
		if code := c.Run(args); code != r.code {
			out := done(t)
			t.Fatalf("live-mv %v exited %d, want %d\nstdout:\n%s\nstderr:\n%s", args, code, r.code, out.Stdout(), out.Stderr())
		}
	}
	out := done(t)
	return out.Stdout(), out.Stderr(), cloud
}

func TestLiveMv_progressJSON(t *testing.T) {
	stdout, stderr, cloud := liveMvThreeMoves(t, "-progress-json")

	lines := progressLines(t, stderr)
	want := []views.LiveProgressLine{
		{EstateFrom: "live-unit", EstateTo: "live-unit", Address: "aws_security_group.renamed", LiveID: "sg-owned", Outcome: "moved"},
		{EstateFrom: "live-unit", EstateTo: "live-unit", Address: "aws_vpc.core", Outcome: "refused", Reason: "No marker search path for this resource type"},
		{EstateFrom: "live-unit", EstateTo: "live-unit", Address: "aws_s3_bucket.archive", LiveID: "tofu-mv-unit-data", Outcome: "moved"},
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d -progress-json lines, want %d:\n%s", len(lines), len(want), stderr)
	}
	for i := range want {
		got := lines[i]
		got.At = ""
		if got != want[i] {
			t.Errorf("line %d = %+v\nwant      %+v", i, got, want[i])
		}
	}
	if len(cloud.applied) != 2 {
		t.Errorf("applied = %v, want the two moves", cloud.applied)
	}

	// Stdout is the same three documents a run without the flag prints.
	plain, _, _ := liveMvThreeMoves(t)
	if stdout != plain {
		t.Errorf("-progress-json changed stdout.\nwith:\n%s\nwithout:\n%s", stdout, plain)
	}
	dec := json.NewDecoder(strings.NewReader(stdout))
	for i := 0; i < 3; i++ {
		var rep views.LiveMvJSONReport
		if err := dec.Decode(&rep); err != nil {
			t.Fatalf("stdout document %d does not parse: %s\n%s", i, err, stdout)
		}
	}
	if strings.Contains(stdout, `"outcome"`) {
		t.Errorf("a progress line reached stdout:\n%s", stdout)
	}
}

// TestLiveMv_progressJSONDryRun: a dry run writes nothing and prints no line.
func TestLiveMv_progressJSONDryRun(t *testing.T) {
	cloud := mvRenamedFixture(t)
	c, done := newLiveMvCommand(t, cloud)
	code := c.Run([]string{"-no-color", "-progress-json", "-dry-run", "aws_security_group.main", "aws_security_group.renamed"})
	out := done(t)
	if code != 0 {
		t.Fatalf("exit code %d\n%s", code, out.Stderr())
	}
	if lines := progressLines(t, out.Stderr()); len(lines) != 0 {
		t.Errorf("a dry run printed progress lines: %+v", lines)
	}
}

// TestLiveMvProgressLines_followers: a child that follows its parent gets a
// line of its own right after the parent's, naming the parent.
func TestLiveMvProgressLines_followers(t *testing.T) {
	view, done := testView(t)
	res := &mv.Result{
		Estate:     "team",
		FromEstate: "monolith",
		New:        addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_iam_role", Name: "team"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
		LiveID:     "team-role",
		Followers: []mv.Follower{{
			Addr:     addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_iam_role_policy", Name: "inline"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
			TypeName: "aws_iam_role_policy",
		}},
	}
	liveMvProgressMoved(views.NewLiveWriteProgress(view), res)
	lines := progressLines(t, done(t).Stderr())
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %+v", len(lines), lines)
	}
	if l := lines[0]; l.Address != "aws_iam_role.team" || l.Outcome != "moved" || l.Follows != "" || l.EstateFrom != "monolith" || l.EstateTo != "team" || l.LiveID != "team-role" {
		t.Errorf("parent line = %+v", l)
	}
	if l := lines[1]; l.Address != "aws_iam_role_policy.inline" || l.Outcome != "moved" || l.Follows != "aws_iam_role.team" || l.LiveID != "" {
		t.Errorf("follower line = %+v", l)
	}
}

// liveImportThreeBuckets writes a state of three buckets into a fresh copy
// of live-import-basic: alpha is unmarked and stamps, beta carries another
// estate's marker and is refused, gamma is gone from the cloud and is
// skipped.
func liveImportThreeBuckets(t *testing.T, args ...string) (stdout, stderr string, cloud *importCloud) {
	t.Helper()
	cloud = importNewCloud()
	cloud.put("aws_s3_bucket", "alpha", map[string]string{"id": "alpha", "bucket": "alpha"}, map[string]string{})
	cloud.put("aws_s3_bucket", "beta", map[string]string{"id": "beta", "bucket": "beta"},
		map[string]string{"tofu-estate": "someone-else", "tofu-address": "aws_s3_bucket.beta"})

	c, done := newLiveImportCommand(t, cloud)
	var res []string
	for _, name := range []string{"alpha", "beta", "gamma"} {
		res = append(res, fmt.Sprintf(`{"mode":"managed","type":"aws_s3_bucket","name":%q,"provider":"provider[\"registry.opentofu.org/hashicorp/aws\"]","instances":[{"schema_version":0,"attributes":{"id":%q,"bucket":%q,"tags":{}},"private":"bnVsbA==","dependencies":[]}]}`, name, name, name))
	}
	state := `{"version":4,"serial":1,"lineage":"9b6a5f21-2b1e-4a29-8e2a-6c4a9d5b7f02","terraform_version":"1.7.0","outputs":{},"resources":[` + strings.Join(res, ",") + `]}`
	if err := os.WriteFile("import.tfstate", []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}

	c.Run(append([]string{"-no-color", "-state=import.tfstate", "-estate=live-unit"}, args...))
	out := done(t)
	return out.Stdout(), out.Stderr(), cloud
}

func TestLiveImport_progressJSON(t *testing.T) {
	// -parallelism=1 so the write order is the ratification order and the
	// test can name it.
	stdout, stderr, cloud := liveImportThreeBuckets(t, "-approve", "-parallelism=1", "-progress-json")

	lines := progressLines(t, stderr)
	if len(lines) != 3 {
		t.Fatalf("got %d -progress-json lines, want 3:\n%s\nstdout:\n%s", len(lines), stderr, stdout)
	}
	wantAddr := []string{"aws_s3_bucket.alpha", "aws_s3_bucket.beta", "aws_s3_bucket.gamma"}
	wantOutcome := []string{"stamped", "refused", "skipped"}
	for i, l := range lines {
		if l.Address != wantAddr[i] || l.Outcome != wantOutcome[i] {
			t.Errorf("line %d = %s %s, want %s %s", i, l.Address, l.Outcome, wantAddr[i], wantOutcome[i])
		}
		if l.EstateTo != "live-unit" || l.EstateFrom != "" {
			t.Errorf("line %d estates = %q -> %q, want \"\" -> live-unit", i, l.EstateFrom, l.EstateTo)
		}
		if (l.Outcome == "stamped") != (l.Reason == "") {
			t.Errorf("line %d: outcome %s with reason %q", i, l.Outcome, l.Reason)
		}
	}
	if lines[0].LiveID != "alpha" {
		t.Errorf("stamped line live_id = %q, want alpha", lines[0].LiveID)
	}
	if !strings.Contains(lines[1].Reason, "owned by another estate") {
		t.Errorf("refused line reason = %q", lines[1].Reason)
	}
	if got := cloud.tagsOf("aws_s3_bucket", "alpha")["tofu-estate"]; got != "live-unit" {
		t.Errorf("alpha was not stamped: tofu-estate = %q", got)
	}

	plain, _, _ := liveImportThreeBuckets(t, "-approve", "-parallelism=1")
	if stdout != plain {
		t.Errorf("-progress-json changed stdout.\nwith:\n%s\nwithout:\n%s", stdout, plain)
	}
}

// TestLiveImport_progressJSONWithoutApprove: a ratification writes nothing,
// so it prints no line.
func TestLiveImport_progressJSONWithoutApprove(t *testing.T) {
	_, stderr, cloud := liveImportThreeBuckets(t, "-progress-json")
	if lines := progressLines(t, stderr); len(lines) != 0 {
		t.Errorf("a run without -approve printed progress lines: %+v", lines)
	}
	if len(cloud.applied) != 0 {
		t.Errorf("a run without -approve wrote: %v", cloud.applied)
	}
}
