// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/approval"
)

// GitHub issue #878's tests. The property under test is a pipeline's:
// "plan on the pull request, a human approves, apply exactly what was
// approved", with the world still authoritative at apply time.
//
// Every assertion here is on rendered text - the plan file that appears, the
// refusal's summary, the rows it prints, the exit status - because a boolean
// "they matched" would pass over a comparison that always matched, which is
// the one failure mode this feature cannot survive.

// approvalFixture stands a live-block configuration up in its own directory
// and returns it. estate overrides the fixture's own estate name when it is
// non-empty, which is how the wrong-estate case gets a second directory
// without a second fixture.
func approvalFixture(t *testing.T, estate string) string {
	t.Helper()
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block"), td)
	if estate != "" {
		path := filepath.Join(td, "main.tf")
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading the copied fixture: %s", err)
		}
		out := strings.Replace(string(src), `estate = "stateless-unit"`, `estate = "`+estate+`"`, 1)
		if out == string(src) {
			t.Fatalf("the fixture no longer names estate stateless-unit; this test's rewrite is stale")
		}
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			t.Fatalf("rewriting the copied fixture: %s", err)
		}
	}
	return td
}

// planOut runs "choudoufu plan -out=<name>" in dir and returns the file's
// path, failing loudly if the run did not produce one.
func planOut(t *testing.T, dir string, cloud *statelessTestCloud, name string) string {
	t.Helper()
	t.Chdir(dir)

	c, done := newLiveBlockPlanCommand(t, cloud)
	code := c.Run([]string{"-no-color", "-out=" + name})
	out := done(t)
	if code != 0 {
		t.Fatalf("plan -out exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out.Stdout(), out.Stderr())
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("plan -out wrote no file at %s: %s\nstdout:\n%s", path, err, out.Stdout())
	}
	return path
}

// applyPlanFile runs "choudoufu apply <file>" in dir and returns the exit
// code with everything the run printed.
func applyPlanFile(t *testing.T, dir string, cloud *statelessTestCloud, file string) (int, string) {
	t.Helper()
	t.Chdir(dir)

	view, done := testView(t)
	c := &ApplyCommand{Meta: liveBlockMeta(view, cloud)}
	code := c.Run([]string{"-no-color", file})
	out := done(t)
	return code, out.Stdout() + out.Stderr()
}

// TestApproval_planOutWritesAnArtifact is the half of the ruling that
// admits the stock form: "-out" under a live block writes stock's own plan
// file rather than being refused.
func TestApproval_planOutWritesAnArtifact(t *testing.T) {
	dir := approvalFixture(t, "")
	path := planOut(t, dir, newStatelessTestCloud(), "approved.tfplan")

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %s", err)
	}
	if info.Size() == 0 {
		t.Errorf("the plan file is empty")
	}
	// The refusal this replaces must be gone, not merely bypassed.
	assertNoStateArtifacts(t, dir)
}

// TestApproval_matchApplies: the world did not move, so the file applies.
// This is the case that must NOT refuse, and it is what makes every refusal
// below a real check rather than a constant.
func TestApproval_matchApplies(t *testing.T) {
	dir := approvalFixture(t, "")
	cloud := newStatelessTestCloud()
	path := planOut(t, dir, cloud, "approved.tfplan")

	code, output := applyPlanFile(t, dir, cloud, filepath.Base(path))
	if code != 0 {
		t.Fatalf("apply of an unmoved world exit code %d, want 0\n%s", code, output)
	}
	if strings.Contains(output, summaryApprovalMismatch) {
		t.Errorf("the apply refused an artifact that matches its own fresh plan:\n%s", output)
	}
	if !strings.Contains(output, "Apply complete!") {
		t.Errorf("the apply did not complete:\n%s", output)
	}
}

// TestApproval_driftRefuses: the world moved between the plan and the apply,
// so the approval no longer covers what the apply would do.
//
// The move is the shape a pipeline actually meets - something else created
// the bucket this estate was about to create, and marked it - so the fresh
// plan has one change where the approved artifact had two.
func TestApproval_driftRefuses(t *testing.T) {
	dir := approvalFixture(t, "")
	cloud := newStatelessTestCloud()
	path := planOut(t, dir, cloud, "approved.tfplan")

	cloud.putMarked("aws_s3_bucket", "tofu-stateless-unit-data", "stateless-unit", "aws_s3_bucket.data", map[string]string{
		"id": "tofu-stateless-unit-data", "bucket": "tofu-stateless-unit-data",
	})

	code, output := applyPlanFile(t, dir, cloud, filepath.Base(path))
	if code != ExitApprovalRefused {
		t.Fatalf("apply after drift exit code %d, want %d\n%s", code, ExitApprovalRefused, output)
	}
	for _, want := range []string{
		summaryApprovalMismatch,
		"aws_s3_bucket.data  Create  -",
		"The approved plan includes, and this apply would not do",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("the refusal does not carry %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "Apply complete!") {
		t.Errorf("the apply ran anyway after refusing:\n%s", output)
	}
	assertNoStateArtifacts(t, dir)
}

// TestApproval_wrongEstateRefuses: an artifact produced for another estate
// is refused by name, before any comparison, because an approval for one
// estate says nothing about another.
func TestApproval_wrongEstateRefuses(t *testing.T) {
	other := approvalFixture(t, "some-other-estate")
	cloud := newStatelessTestCloud()
	foreign := planOut(t, other, cloud, "approved.tfplan")

	dir := approvalFixture(t, "")
	copied := filepath.Join(dir, "approved.tfplan")
	src, err := os.ReadFile(foreign)
	if err != nil {
		t.Fatalf("reading the foreign plan file: %s", err)
	}
	if err := os.WriteFile(copied, src, 0o644); err != nil {
		t.Fatalf("writing the foreign plan file into the estate's directory: %s", err)
	}

	code, output := applyPlanFile(t, dir, cloud, "approved.tfplan")
	if code != ExitApprovalRefused {
		t.Fatalf("apply of another estate's artifact exit code %d, want %d\n%s", code, ExitApprovalRefused, output)
	}
	// Compared against the unwrapped rendering: the diagnostic printer
	// folds at its width, and this assertion is about the sentence rather
	// than about where the fold landed.
	flat := unwrapped(output)
	for _, want := range []string{
		summaryApprovalWrongEstate,
		`estate "some-other-estate"`,
		`estate "stateless-unit"`,
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the refusal does not carry %q:\n%s", want, output)
		}
	}
	assertNoStateArtifacts(t, dir)
}

// TestApprovalMismatchDetail_saysWhatToDo pins the wording, which is the
// product here: a refusal that does not name a way forward reads as a bug in
// the tool. Testable with no plan, no backend and no provider, for
// [unmigrateRefusalDetail]'s reason.
func TestApprovalMismatchDetail_saysWhatToDo(t *testing.T) {
	diff := approval.Difference{
		Extra:   []approval.Row{{Address: "aws_subnet.crashed", Action: "Delete", Identity: "subnet-99"}},
		Missing: []approval.Row{{Address: "aws_vpc.main", Action: "Update", Identity: "vpc-owned"}},
	}
	got := approvalMismatchDetail("approved.tfplan", diff)
	for _, want := range []string{
		"aws_subnet.crashed  Delete  subnet-99",
		"aws_vpc.main  Update  vpc-owned",
		"The live system moved between the plan and the apply",
		"Another apply landed in between",
		"the configuration changed since the file was written",
		"choudoufu plan -out=approved.tfplan",
		"Exit status 3",
		"Nothing was applied",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the mismatch detail does not say %q:\n%s", want, got)
		}
	}
}

// TestApprovalRowList_capsAndCounts: the plan was already printed in full
// above the refusal, so the refusal names a few rows and counts the rest
// rather than burying its own last paragraph.
func TestApprovalRowList_capsAndCounts(t *testing.T) {
	var rows []approval.Row
	for i := 0; i < 14; i++ {
		rows = append(rows, approval.Row{Address: "aws_vpc.n" + string(rune('a'+i)), Action: "Create", Identity: approval.IdentityNone})
	}
	got := approvalRowList(rows)
	if strings.Count(got, "\n") != 11 {
		t.Errorf("expected ten rows and one count line, got:\n%s", got)
	}
	if !strings.Contains(got, "... and 4 more") {
		t.Errorf("the row list does not count what it left out:\n%s", got)
	}
}

// TestApprovalWrongEstateDetail_namesBothSides, including the case where one
// side has no live block at all - a different mistake that must not be
// described as an estate name.
func TestApprovalWrongEstateDetail_namesBothSides(t *testing.T) {
	got := approvalWrongEstateDetail("approved.tfplan", "billing", "platform")
	for _, want := range []string{`estate "billing"`, `estate "platform"`, "Nothing was applied"} {
		if !strings.Contains(got, want) {
			t.Errorf("the wrong-estate detail does not say %q:\n%s", want, got)
		}
	}

	stock := approvalWrongEstateDetail("approved.tfplan", "", "platform")
	if !strings.Contains(stock, "no live block, so it is a state-backed configuration") {
		t.Errorf("a plan file made with no live block is described as an estate:\n%s", stock)
	}
}
