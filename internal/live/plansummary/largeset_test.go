// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package plansummary

import (
	"os"
	"strings"
	"testing"
)

// TestLargeSetFixtureBump is #1753's fixture result. The input is real:
// the five plans of #1750's module bump at N=5 against floci, each from
// `live-plan -out` and `show -json`, wrapped into the set document by
// internal/live/largeset's TestLargeSetBaselineAgainstFloci with
// LARGESET_SUMMARY_DOC set, in the run that wrote
// live/large-set/baseline-n5.json's summary. e04 is the fixture's planted
// outlier (with_queue = true: one more change than the rest).
func TestLargeSetFixtureBump(t *testing.T) {
	data, err := os.ReadFile("testdata/largeset-n5-bump.json")
	if err != nil {
		t.Fatal(err)
	}
	in, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	s := Summarize(in)
	text := s.Text()
	if len(s.Failed) != 0 || len(s.Groups) != 2 {
		t.Fatalf("want one group plus the outlier, got %d groups and %d failed:\n%s", len(s.Groups), len(s.Failed), text)
	}
	if got := strings.Join(s.Groups[0].Members, ","); got != "estates/e01,estates/e02,estates/e03,estates/e05" {
		t.Errorf("group 1 is %s, want e01, e02, e03 and e05", got)
	}
	out := s.Groups[1]
	if !out.Outlier || len(out.Members) != 1 || out.Members[0] != "estates/e04" {
		t.Errorf("the outlier group is %v (outlier=%v), want e04 alone", out.Members, out.Outlier)
	}
	if out.Extends != 1 || len(out.Plus) != 1 || out.Plus[0].Line != "~ module.shared.aws_sqs_queue.extra: tags, tags_all" {
		t.Errorf("e04 should read as group 1's change plus its extra queue; got extends=%d plus=%+v", out.Extends, out.Plus)
	}
	if s.Destructive != 0 {
		t.Errorf("the bump destroys nothing, and the summary counts %d", s.Destructive)
	}
}
