// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/terminal"
)

// TestStatelessPlan_progressGoesToStderr pins the property the "sweep
// progress" feature depends on: a heartbeat is stderr-only, never stdout,
// so it can never end up in anything a script reads from this command's
// output - today that is everything live-plan prints on success, since it
// has no -json mode. It also checks the line names the type just scanned
// and both running counts, since those are what makes it a heartbeat rather
// than decoration.
func TestStatelessPlan_progressGoesToStderr(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	v := NewStatelessPlan(NewView(streams).SetRunningInAutomation(true))

	v.Progress(StatelessProgress{
		TypeName:       "aws_cloudwatch_log_group",
		TypesScanned:   12,
		ResourcesFound: 37,
	})

	out := done(t)
	if got := out.Stdout(); got != "" {
		t.Errorf("a progress heartbeat wrote to stdout: %q", got)
	}
	stderr := out.Stderr()
	for _, want := range []string{"12", "37", "aws_cloudwatch_log_group"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not contain %q:\n%s", want, stderr)
		}
	}
}

// TestStatelessPlan_unownedSection renders one of each disposition and pins
// the shape: the heading with its at-a-glance split, the copyable adoption
// line with the exact tag values, and the in-the-way entries naming what
// blocks them. The wording of the intro is the section's own business; the
// lines an operator acts on are not.
func TestStatelessPlan_unownedSection(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	v := NewStatelessPlan(NewView(streams).SetRunningInAutomation(true))

	v.Unowned([]StatelessUnowned{
		{
			Addr:          "aws_iam_role.app",
			TypeName:      "aws_iam_role",
			LiveID:        "app-role",
			MarkerEstate:  "dev",
			MarkerAddress: "aws_iam_role.app",
		},
		{
			Addr:     "aws_s3_bucket.data",
			TypeName: "aws_s3_bucket",
			LiveID:   "shared-data",
			HeldBy:   "prod",
		},
		{
			Addr:     "aws_s3_bucket.logs",
			TypeName: "aws_s3_bucket",
			LiveID:   "orphan-logs",
		},
	})

	got := done(t).Stdout()

	for _, want := range []string{
		"Unowned: 3 live resources hold identities this configuration declares (1 adoptable, 2 in the way)",
		"aws_iam_role.app [ADOPTABLE] <- aws_iam_role app-role",
		"      adopt by writing: tofu-estate=dev tofu-address=aws_iam_role.app",
		"aws_s3_bucket.data [IN_THE_WAY] <- aws_s3_bucket shared-data",
		`held by estate "prod"`,
		"aws_s3_bucket.logs [IN_THE_WAY] <- aws_s3_bucket orphan-logs",
		"Pass -estate=<name>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the section does not contain %q:\n%s", want, got)
		}
	}

	// No line of this section may parse as an omission entry: the test
	// harnesses read "<address> [REASON]" lines out of the plan output, and a
	// section entry masquerading as one would be counted as an omission.
	for _, line := range strings.Split(got, "\n") {
		trimmed := strings.TrimRight(line, " ")
		if strings.HasSuffix(trimmed, "[UNOWNED]") {
			t.Errorf("a section line ends in [UNOWNED], which only an omission entry may: %q", line)
		}
	}
}

// TestStatelessPlan_unownedSectionEmpty: nothing refused, nothing rendered.
// The ownership check runs on every instance the projection reads, so an
// empty list carries no coverage question the way an empty sweep does, and a
// standing empty section would bury the sections that do say something.
func TestStatelessPlan_unownedSectionEmpty(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	v := NewStatelessPlan(NewView(streams).SetRunningInAutomation(true))

	v.Unowned(nil)

	if got := done(t).Stdout(); got != "" {
		t.Errorf("an empty unowned list rendered output:\n%s", got)
	}
}

// TestStatelessPlan_guidedFallback pins the one-sentence note a configured
// but fallen-back guided-discovery pass renders: the heading names what
// happened, and discovery's own reason - passed through unedited - is the
// body.
func TestStatelessPlan_guidedFallback(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	v := NewStatelessPlan(NewView(streams).SetRunningInAutomation(true))

	v.GuidedFallback(`the record store's hint for estate "unit" is stale (72h0m0s old, over the 168h0m0s limit); falling back to full enumeration`)

	got := done(t).Stdout()
	for _, want := range []string{
		"Guided discovery: fell back to a full sweep",
		`the record store's hint for estate "unit" is stale`,
		"falling back to full enumeration",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the note does not contain %q:\n%s", want, got)
		}
	}
}

// TestStatelessPlan_lookalikesSection pins the lookalike guard's exact
// wording: a matchTable-confirmed warning names what it matched on, a
// generic warning (no matchTable entry) says only that a live resource
// exists, and both name the live ID and print the adoption remedy.
func TestStatelessPlan_lookalikesSection(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	v := NewStatelessPlan(NewView(streams).SetRunningInAutomation(true))

	v.Lookalikes([]StatelessLookalike{
		{
			Addr:          "aws_security_group.main",
			TypeName:      "aws_security_group",
			LiveID:        "sg-0abc",
			Matched:       []StatelessTag{{Key: "name", Value: "stateless-e2e-main"}},
			MarkerEstate:  "stateless-e2e",
			MarkerAddress: "aws_security_group.main",
			Hint:          "aws ec2 create-tags --resources 'sg-0abc' --tags 'Key=tofu-estate,Value=stateless-e2e' 'Key=tofu-address,Value=aws_security_group.main'",
		},
		{
			Addr:          "aws_route_table.main",
			TypeName:      "aws_route_table",
			LiveID:        "rtb-stripped",
			MarkerEstate:  "stateless-e2e",
			MarkerAddress: "aws_route_table.main",
		},
	})

	got := done(t).Stdout()

	for _, want := range []string{
		"Possible duplicates: 2 planned creates may duplicate a live resource this estate does not own",
		"aws_security_group.main [POSSIBLE DUPLICATE] ~ aws_security_group sg-0abc",
		"matched on: name=stateless-e2e-main",
		"a live aws_security_group this estate does not own matches this create",
		"exactly (sg-0abc); if this create duplicates it, adopt instead:",
		"adopt with: aws ec2 create-tags --resources 'sg-0abc'",
		"aws_route_table.main [POSSIBLE DUPLICATE] ~ aws_route_table rtb-stripped",
		"a live aws_route_table this estate does not own exists (rtb-stripped);",
		"if this create duplicates it, adopt instead:",
		"adopt by writing: tofu-estate=stateless-e2e tofu-address=aws_route_table.main",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the section does not contain %q:\n%s", want, got)
		}
	}
}

// TestStatelessPlan_guidedFallbackEmpty: an empty reason renders nothing,
// which is the case for every pass that never configured guided discovery at
// all and for every pass where it engaged successfully - neither is
// something an operator needs told about on every run.
func TestStatelessPlan_guidedFallbackEmpty(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	v := NewStatelessPlan(NewView(streams).SetRunningInAutomation(true))

	v.GuidedFallback("")

	if got := done(t).Stdout(); got != "" {
		t.Errorf("an empty fallback reason rendered output:\n%s", got)
	}
}

// TestStatelessPlan_lookalikesSectionEmpty: no warnings, nothing rendered -
// the ordinary case, since a plan with nothing to warn about should print
// nothing about it.
func TestStatelessPlan_lookalikesSectionEmpty(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	v := NewStatelessPlan(NewView(streams).SetRunningInAutomation(true))

	v.Lookalikes(nil)

	if got := done(t).Stdout(); got != "" {
		t.Errorf("an empty lookalike list rendered output:\n%s", got)
	}
}
