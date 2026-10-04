// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"strings"
	"testing"
)

// The aws/scale=50 record's test_plan detail, verbatim as it was committed
// (#1083). This exact line is what a downstream ingest published.
const scale50ScratchDetail = "the post-migrate plan is not empty - see /private/tmp/claude-501/-Users-alex-Documents-checkouts-intentius-choudoufu/aefed153-1ee1-4e68-9a5a-49f6e3e1cf34/scratchpad/scale50b/work/test_plan.out"

func TestScrubLocalPaths(t *testing.T) {
	cases := []struct{ in, want string }{
		{scale50ScratchDetail, "the post-migrate plan is not empty - see <local temp path>/test_plan.out"},
		{"see /tmp/x/plan.out.", "see <local temp path>/plan.out."},
		{"log at /var/folders/ab/cd/T/run-1/apply.log, then exit", "log at <local temp path>/apply.log, then exit"},
		{"two: /tmp/a/one.out and /var/tmp/b/two.out", "two: <local temp path>/one.out and <local temp path>/two.out"},
		{"(see /home/runner/work/_temp/abc/plan.txt)", "(see <local temp path>/plan.txt)"},
		{"bare /tmp/ dir", "bare <local temp path> dir"},
		// Left alone: nothing temporary, or a path a reader can follow.
		{"68 added, 41 stamped, 27 skipped", "68 added, 41 stamped, 27 skipped"},
		{"see live/gauntlet/logs/terralith-scale.log", "see live/gauntlet/logs/terralith-scale.log"},
		{"Non-static identity argument: x=y", "Non-static identity argument: x=y"},
		{"/tmpfoo/bar is not a temp dir", "/tmpfoo/bar is not a temp dir"},
		{"see live/e2e/x/tmp/y.out", "see live/e2e/x/tmp/y.out"},
		{"/tmp/at/start.log", "<local temp path>/start.log"},
		{"out=/tmp/w/plan.out", "out=<local temp path>/plan.out"},
	}
	for _, c := range cases {
		if got := scrubLocalPaths(c.in); got != c.want {
			t.Errorf("scrubLocalPaths(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

// TestProtocolNeverRecordsALocalTempPath (#1083): what the parser hands the
// recorder - a stage detail or a refusal reason - never names a local
// temporary path, whichever script printed it. The red arm is the raw line
// itself: it does contain the path, so a parser that passed details through
// untouched would fail here.
func TestProtocolNeverRecordsALocalTempPath(t *testing.T) {
	in := strings.Join([]string{
		"GAUNTLET protocol=1",
		"GAUNTLET stage=test_plan verdict=fail duration_s=3 detail=" + scale50ScratchDetail,
		"GAUNTLET refused=1 scale=136 detail=the work dir /tmp/lc/work/ is full",
	}, "\n")
	if !strings.Contains(in, "/private/tmp/") {
		t.Fatal("the fixture no longer carries a temp path, so this test proves nothing")
	}
	res, err := ParseProtocol(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if d := res.Detail["test_plan"]; localTempPath.MatchString(d) || !strings.HasSuffix(d, "<local temp path>/test_plan.out") {
		t.Errorf("test_plan detail = %q, want the temp path scrubbed to its file name", d)
	}
	if res.Refusal == nil {
		t.Fatal("the refusal line was not parsed")
	}
	if r := res.Refusal.Reason; localTempPath.MatchString(r) || !strings.Contains(r, "<local temp path>/work") {
		t.Errorf("refusal reason = %q, want the temp path scrubbed", r)
	}
}
