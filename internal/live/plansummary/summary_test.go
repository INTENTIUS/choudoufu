// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package plansummary

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// moduleBump is the change every root of a module bump makes: an instance
// type moves, and its tags are rewritten with the estate's own name.
func moduleBump(t testing.TB, estate string) []ResourceChange {
	return []ResourceChange{
		change(t, "module.app.aws_instance.web", update,
			map[string]any{"id": "i-" + estate, "instance_type": "t3.small", "tags": map[string]any{"Name": estate + "-web"}},
			map[string]any{"id": "i-" + estate, "instance_type": "t3.medium", "tags": map[string]any{"Name": estate + "-web", "Tier": "app"}}),
		change(t, "module.app.aws_s3_bucket.assets", create, nil, map[string]any{"bucket": estate + "-assets"}),
	}
}

// setOf synthesizes n roots making the module bump, then plants: root 3
// adds a destroy, root 5 replaces instead of updating, root 7 fails, and
// root 9 picks a different instance size.
func setOf(t testing.TB, n int) *SetDocument {
	doc := &SetDocument{}
	for i := 1; i <= n; i++ {
		estate := fmt.Sprintf("estate-%02d", i)
		r := root("roots/"+estate, estate, moduleBump(t, estate)...)
		switch i {
		case 3:
			r.Plan.ResourceChanges = append(r.Plan.ResourceChanges,
				change(t, "module.app.aws_sqs_queue.old", destroy, map[string]any{"id": "q-" + estate}, nil))
		case 5:
			r.Plan.ResourceChanges[0].Change.Actions = replace
		case 7:
			r = SetRoot{Root: "roots/" + estate, Estate: estate, Status: "failed", Error: "Error: configuring provider: no valid credential sources"}
		case 9:
			var after map[string]any
			_ = json.Unmarshal(r.Plan.ResourceChanges[0].Change.After, &after)
			after["instance_type"] = "t3.large"
			r.Plan.ResourceChanges[0].Change.After = raw(t, after)
		}
		doc.Roots = append(doc.Roots, r)
	}
	return doc
}

func TestSetSummaryGroupsTheBumpAndNamesEachOutlier(t *testing.T) {
	s := Summarize(Input{Set: setOf(t, 10)})
	if s.Units != 10 || len(s.Failed) != 1 {
		t.Fatalf("units=%d failed=%d, want 10 and 1", s.Units, len(s.Failed))
	}
	if len(s.Groups) != 4 {
		t.Fatalf("%d groups, want 4 (the bump, the destroy, the replace, the size)\n%s", len(s.Groups), s.Text())
	}
	if g := s.Groups[0]; len(g.Members) != 6 || g.Outlier {
		t.Errorf("group 1 has %d members (outlier %v), want the 6 unplanted roots", len(g.Members), g.Outlier)
	}
	outliers := map[string]Group{}
	for _, g := range s.Groups[1:] {
		if !g.Outlier || len(g.Members) != 1 {
			t.Errorf("group %d: %v outlier=%v, want an outlier of one", g.ID, g.Members, g.Outlier)
			continue
		}
		outliers[g.Members[0]] = g
	}
	for _, want := range []string{"roots/estate-03", "roots/estate-05", "roots/estate-09"} {
		if _, ok := outliers[want]; !ok {
			t.Errorf("%s is not named as an outlier", want)
		}
	}
	if g := outliers["roots/estate-03"]; g.Extends != 1 || len(g.Plus) != 1 || g.Plus[0].Line != "- module.app.aws_sqs_queue.old" {
		t.Errorf("the destroy root should read as group 1's change plus the delete; got extends=%d plus=%+v", g.Extends, g.Plus)
	}
	if g := outliers["roots/estate-09"]; !strings.Contains(s.Text(), "[differs from group 1 in: instance_type]") || g.Extends != 0 {
		t.Errorf("the size outlier should name instance_type as its difference:\n%s", s.Text())
	}
	text := s.Text()
	for _, want := range []string{
		"10 roots: 4 groups, 1 failed, 2 destroys or replaces.",
		"Group 1: 6 roots, identical change:",
		"~ module.app.aws_instance.web: instance_type, tags",
		"+ module.app.aws_s3_bucket.assets",
		"Group 2: 1 root (outlier), group 1's change plus:",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text summary lacks %q:\n%s", want, text)
		}
	}
}

// TestDestroysAndReplacesAreNamedInEveryFormat: a destroy and a replace
// made by every root of a 50-root group are each listed, by real address,
// in text, JSON and markdown.
func TestDestroysAndReplacesAreNamedInEveryFormat(t *testing.T) {
	doc := &SetDocument{}
	for i := 1; i <= 50; i++ {
		estate := fmt.Sprintf("e%02d", i)
		doc.Roots = append(doc.Roots, root("roots/"+estate, estate,
			change(t, "aws_sqs_queue."+estate+"_jobs", destroy, map[string]any{"id": "q"}, nil),
			change(t, `aws_instance.web["`+estate+`"]`, replace,
				map[string]any{"ami": "ami-1"}, map[string]any{"ami": "ami-2"}),
		))
	}
	s := Summarize(Input{Set: doc})
	if len(s.Groups) != 1 || len(s.Groups[0].Members) != 50 {
		t.Fatalf("want one group of 50, got:\n%s", s.Text())
	}
	js, err := s.JSON()
	if err != nil {
		t.Fatal(err)
	}
	formats := map[string]string{"text": s.Text(), "json": js, "markdown": s.Markdown(GitLabNoteLimit)}
	for i := 1; i <= 50; i++ {
		estate := fmt.Sprintf("e%02d", i)
		for _, addr := range []string{"aws_sqs_queue." + estate + "_jobs", `aws_instance.web["` + estate + `"]`} {
			for name, out := range formats {
				want := addr
				if name == "json" {
					b, _ := json.Marshal(addr)
					want = strings.Trim(string(b), `"`)
				}
				if !strings.Contains(out, want) {
					t.Errorf("%s does not name %s's %s", name, estate, addr)
				}
			}
		}
	}
	if s.Destructive != 100 {
		t.Errorf("counted %d destroys and replaces, want 100", s.Destructive)
	}
}

// TestFailedRootsAreTheirOwnLine: a failed root and a root with no plan
// are never group members, and each is a line with its reason in every
// format.
func TestFailedRootsAreTheirOwnLine(t *testing.T) {
	doc := setOf(t, 4)
	doc.Roots = append(doc.Roots,
		SetRoot{Root: "roots/empty", Estate: "empty", Status: "planned"},
		SetRoot{Root: "roots/odd", Estate: "odd", Status: "refused", Error: "refused: unadmitted-type"},
	)
	s := Summarize(Input{Set: doc})
	if len(s.Failed) != 2 {
		t.Fatalf("%d failed, want 2: %+v", len(s.Failed), s.Failed)
	}
	for _, g := range s.Groups {
		for _, m := range g.Members {
			if m == "roots/empty" || m == "roots/odd" {
				t.Errorf("%s is a member of group %d", m, g.ID)
			}
		}
	}
	js, _ := s.JSON()
	for name, out := range map[string]string{"text": s.Text(), "json": js, "markdown": s.Markdown(GitLabNoteLimit)} {
		for _, want := range []string{"refused: unadmitted-type", "the document holds no plan for it", "roots/odd", "roots/empty"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s lacks %q", name, want)
			}
		}
	}
	if !strings.Contains(s.Text(), "\n  roots/odd (estate odd): refused: unadmitted-type\n") {
		t.Errorf("the refused root is not a line of its own:\n%s", s.Text())
	}
}

// TestMarkdownForTwentyRootsFitsOneNote is the N=20 case, synthesized: it
// fits a GitLab note untruncated.
func TestMarkdownForTwentyRootsFitsOneNote(t *testing.T) {
	md := Summarize(Input{Set: setOf(t, 20)}).Markdown(GitLabNoteLimit)
	if n := utf8.RuneCountInString(md); n > GitLabNoteLimit {
		t.Fatalf("N=20 markdown is %d characters, over the %d bound", n, GitLabNoteLimit)
	}
	if strings.Contains(md, "**Truncated:**") {
		t.Errorf("N=20 markdown was truncated:\n%s", md)
	}
	if GitLabNoteLimit != 1_000_000 {
		t.Errorf("GitLabNoteLimit is %d; GitLab documents 1,000,000 characters for a note body", GitLabNoteLimit)
	}
}

// TestMarkdownTruncatesByGroup pins the truncation wording and that it
// cuts whole groups: no group is ever half in the note.
func TestMarkdownTruncatesByGroup(t *testing.T) {
	s := Summarize(Input{Set: setOf(t, 20)})
	full := s.Markdown(GitLabNoteLimit)
	limit := utf8.RuneCountInString(full) - 10
	md := s.Markdown(limit)
	if n := utf8.RuneCountInString(md); n > limit {
		t.Fatalf("truncated markdown is %d characters, over its %d limit", n, limit)
	}
	// The last groups are the three single-root outliers. Ten characters
	// short of the full note, cutting the last (about 170 characters) is not
	// enough once the notice itself (about 200) is added, so the last two
	// go: the replace root and the size outlier.
	last := s.Groups[len(s.Groups)-1]
	title := "#### " + s.groupTitle(last)
	if strings.Contains(md, title) {
		t.Fatalf("the last group survived a limit 10 characters short of the full note:\n%s", md)
	}
	want := "**Truncated:** this note leaves out 2 groups (2 roots, 1 destroy or replace), to stay within the " +
		fmt.Sprint(limit) + "-character limit of a merge-request note. `choudoufu live-summary` without `-markdown` prints all of it."
	if !strings.HasSuffix(strings.TrimSpace(md), want) {
		t.Errorf("truncation notice is not the pinned wording.\nwant suffix: %s\ngot:\n%s", want, md)
	}
	// Every group the note does hold, it holds whole.
	for _, g := range s.Groups {
		title := "#### " + s.groupTitle(g) + "\n"
		if !strings.Contains(md, title) {
			continue
		}
		var whole string
		for _, piece := range strings.SplitAfter(full, "\n#### ") {
			if strings.HasPrefix("#### "+piece, title) {
				whole = strings.TrimSuffix(piece, "\n#### ")
			}
		}
		if whole == "" || !strings.Contains(md, whole) {
			t.Errorf("group %d is in the note but not whole", g.ID)
		}
	}

	// A limit that holds only the header cuts everything and counts every
	// destroy it cut.
	tiny := s.Markdown(400)
	if !strings.Contains(tiny, "leaves out 1 failed root and 4 groups (19 roots, 2 destroys or replaces)") {
		t.Errorf("a header-only note does not count what it cut:\n%s", tiny)
	}
}

// TestSinglePlanGroupsInstancesOfOneExpansion is the for_each/count case:
// one plan, instances grouped within their expansion.
func TestSinglePlanGroupsInstancesOfOneExpansion(t *testing.T) {
	var rcs []ResourceChange
	for i := 0; i < 8; i++ {
		size := "t3.small"
		if i == 6 {
			size = "t3.xlarge"
		}
		rcs = append(rcs, change(t, fmt.Sprintf("aws_instance.web[%d]", i), update,
			map[string]any{"id": fmt.Sprintf("i-%d", i), "instance_type": "t3.nano", "tags": map[string]any{"Name": fmt.Sprintf("web-%d", i)}},
			map[string]any{"id": fmt.Sprintf("i-%d", i), "instance_type": size, "tags": map[string]any{"Name": fmt.Sprintf("web-%d", i)}}))
	}
	rcs = append(rcs, change(t, "aws_instance.web[7]", replace, map[string]any{"ami": "a"}, map[string]any{"ami": "b"}))
	rcs[7] = change(t, `aws_s3_bucket.b["logs"]`, destroy, map[string]any{"id": "logs"}, nil)
	rcs = append(rcs, change(t, "aws_vpc.main", []string{"no-op"}, map[string]any{"id": "v"}, map[string]any{"id": "v"}))
	s := Summarize(Input{Plan: &Plan{FormatVersion: "1.2", ResourceChanges: rcs}})
	text := s.Text()
	for _, want := range []string{
		"9 instances: 4 groups, 2 destroys or replaces.",
		"Group 1: 6 instances of aws_instance.web, identical change:",
		"(outlier)",
		"[differs from group 1 in: instance_type]",
		"aws_instance.web[7] (replace)",
		`aws_s3_bucket.b["logs"] (delete)`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("single-plan summary lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "aws_vpc.main") {
		t.Errorf("a no-op is in the summary:\n%s", text)
	}
}

func TestParseTellsTheTwoDocumentsApart(t *testing.T) {
	set, err := Parse([]byte(`{"roots":[{"root":"r","estate":"e","status":"planned","plan":{"resource_changes":[]}}],"other":1}`))
	if err != nil || set.Set == nil || len(set.Set.Roots) != 1 {
		t.Errorf("set document: %+v %v", set, err)
	}
	plan, err := Parse([]byte(`{"format_version":"1.2","resource_changes":[{"address":"a.b","change":{"actions":["create"]}}]}`))
	if err != nil || plan.Plan == nil || len(plan.Plan.ResourceChanges) != 1 {
		t.Errorf("plan: %+v %v", plan, err)
	}
	if _, err := Parse([]byte(`{"values":{}}`)); err == nil {
		t.Errorf("a document that is neither was accepted")
	}
}
