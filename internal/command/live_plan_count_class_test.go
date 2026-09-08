// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// GitHub issue #969. live/MARKERS.md's tag table said tofu-slot is present
// on "count instances only", which reads as "on all of them", and an
// operator applied two count-expanded aws_cloudwatch_log_groups against
// floci and found neither carried one. They were right about what they saw
// and the spec was wrong about what is written: a slot is minted for a
// count block whose instances are a FUNGIBLE set, and a block whose members
// the configuration itself names is not one.
//
// This is the by-value pin on the difference, at the seam that writes the
// tags rather than at the seam that computes them. Both blocks are created
// from nothing in the same plan, so the two tag sets come out of one run of
// one stamping pass, and each is compared as a whole map rather than by
// asking whether some substring appears: an assertion that only looked for
// "tofu-slot" would pass just as well if the address had gone missing.
//
// PROVING IT RED. Put "tofu-slot": "0" into wantConcreteTags below (the
// spec's old claim, asserted literally) and this fails with
//
//	aws_s3_bucket.shard[0]: planned tags
//	  map[tofu-address:aws_s3_bucket.shard:0 tofu-estate:stateless-count-classes]
//	want
//	  map[tofu-address:aws_s3_bucket.shard:0 tofu-estate:stateless-count-classes tofu-slot:0]
//
// which is the shape of #969's report, printed by a check rather than
// found by hand against an emulator.
func TestLivePlan_slotIsWrittenForAFungibleSetAndNotForANamedOne(t *testing.T) {
	const estate = "stateless-count-classes"

	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-count-classes"), td)
	t.Chdir(td)

	// Nothing live: every instance of both blocks is a create, which is the
	// case #969 reported ("choudoufu apply reported them created") and the
	// one the spec's "assigned when the instance is created" is about.
	cloud := newStatelessTestCloud()

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=" + estate, "-detailed-exitcode"})
	output := done(t)
	if code != 2 {
		t.Fatalf("exit code %d, want 2 (four creates are proposed)\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	stdout := output.Stdout()

	if !strings.Contains(stdout, "4 to add, 0 to change, 0 to destroy") {
		t.Fatalf("the plan is not four creates:\n%s", stdout)
	}

	// The fungible set: a slot from birth, one per member, ascending with
	// the index because nothing live holds a slot yet.
	for i := 0; i < 2; i++ {
		addr := fmt.Sprintf("aws_eip.pool[%d]", i)
		want := map[string]string{
			"tofu-estate":  estate,
			"tofu-address": fmt.Sprintf("aws_eip.pool:%d", i),
			"tofu-slot":    fmt.Sprintf("%d", i),
		}
		if got := statelessPlannedTags(t, stdout, addr); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: planned tags\n  %v\nwant\n  %v", addr, got, want)
		}
	}

	// The named set: the two marker tags and nothing else. The configuration
	// says which live bucket is which instance, so a slot would be a claim
	// that the k-th lowest one is free to become index k, which a bucket
	// name is not.
	for i := 0; i < 2; i++ {
		addr := fmt.Sprintf("aws_s3_bucket.shard[%d]", i)
		wantConcreteTags := map[string]string{
			"tofu-estate":  estate,
			"tofu-address": fmt.Sprintf("aws_s3_bucket.shard:%d", i),
		}
		if got := statelessPlannedTags(t, stdout, addr); !reflect.DeepEqual(got, wantConcreteTags) {
			t.Errorf("%s: planned tags\n  %v\nwant\n  %v", addr, got, wantConcreteTags)
		}
	}
}

// statelessPlannedTags is the tags map the rendered plan proposes for one
// resource instance, read out of the diff the operator actually sees.
//
// It reads the renderer's output rather than the plan file on purpose: the
// tag set is a claim made to whoever approves the plan, and #969 was
// reported from exactly this text. The scan is bounded to the one
// resource's `tags = {` block, so a tag belonging to a sibling instance
// cannot be mistaken for this one's.
func statelessPlannedTags(t *testing.T, output, addr string) map[string]string {
	t.Helper()

	lines := strings.Split(output, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "# "+addr+" ") {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("%s does not appear in the plan at all:\n%s", addr, output)
	}

	inTags := false
	tags := map[string]string{}
	for _, line := range lines[start+1:] {
		trimmed := strings.TrimSpace(line)
		if !inTags {
			if strings.HasPrefix(trimmed, "# ") {
				// The next resource's header: this one had no tags block.
				break
			}
			if statelessTagsOpen.MatchString(trimmed) {
				inTags = true
			}
			continue
		}
		if strings.HasPrefix(trimmed, "}") {
			break
		}
		if m := statelessTagEntry.FindStringSubmatch(trimmed); m != nil {
			tags[m[1]] = m[2]
		}
	}
	if !inTags {
		t.Fatalf("%s is planned with no tags block at all:\n%s", addr, output)
	}
	return tags
}

var (
	statelessTagsOpen = regexp.MustCompile(`^\+?\s*(?:~\s*)?tags(?:_all)?\s*=\s*\{`)
	statelessTagEntry = regexp.MustCompile(`^\+?\s*"([^"]+)"\s*=\s*"([^"]*)"`)
)
