// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"bytes"
	"os/exec"
	"testing"
)

// This file is issue #1143.
//
// live/live-cert/terralith-scale.sh's index_wait gives the Resource Groups
// Tagging API's search index a bounded chance to catch up with the tag
// writes migrate just made, so that test_plan measures choudoufu's plan
// against a settled index rather than recording lag it never measured
// (#1046, #1049). It polled for VERIFIED = 33*SCALE+5 - every object migrate
// stamps - which it cannot reach: 11*SCALE of those are aws_iam_role, which
// GetResources returns for in NO region, and 20*SCALE+1 are global-service
// objects the index holds only in us-east-1 (#1134, #1144). Three real-AWS
// runs, at three scales, each burned the full bound and plateaued at exactly
// the reachable ceiling, and each plateau was read as an index settling
// slowly.
//
// index_partition() now splits VERIFIED by type and index reachability, and
// index_wait() polls to that. The proof is
// live/live-cert/selftest-index-wait.sh: it extracts the three functions
// verbatim from the production script, drives them against a stubbed `aws`,
// and checks the split against all three recorded real-AWS ceilings. That
// self-test needs no AWS, no docker, no terraform and no go build - which is
// exactly why it belongs in the ordinary Go tier rather than in a list of
// scripts someone is supposed to remember to run by hand.

// TestIndexWaitSelftestPasses runs live/live-cert/selftest-index-wait.sh and
// fails with its whole output. The self-test prints one line per confirmed
// property and one FAIL line per broken one, so the Go-side failure message
// is the self-test's own reasoning rather than an exit code.
func TestIndexWaitSelftestPasses(t *testing.T) {
	cmd := exec.Command("bash", "live-cert/selftest-index-wait.sh")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("live/live-cert/selftest-index-wait.sh failed (%v). Its own output:\n%s", err, out.String())
	}
}

// indexWaitGlobalHalfTracker is the issue to re-read when the assertion
// below flips. Named as a constant so the failure message and the reason
// cannot drift apart.
const indexWaitGlobalHalfTracker = "#1152 (lex00/floci#205)"

// indexWaitGlobalHalfTypes are the two types that carry index_partition's
// "global" bucket, alongside the one aws_route53_zone. Real AWS serves both
// through GetResources - 500 each at scale 50, us-east-1 only, IAM being
// global (#1134). The pinned emulator serves neither, which is
// lex00/floci#205 and is tracked here as #1152.
var indexWaitGlobalHalfTypes = []string{"aws_iam_policy", "aws_iam_instance_profile"}

// TestIndexWaitGlobalHalfStillUnprovableOnTheEmulator is a SELF-RETIRING
// exception, the shape internal/command/tagging_sweep_premise_test.go's
// taggingSweepEmulatorDefects already uses one level up.
//
// What it records: index_partition's global bucket - 20*SCALE+1 objects,
// aws_iam_policy and aws_iam_instance_profile per scale plus the route53
// zone - is the half of the #1143 fix that cannot be exercised end to end.
// terralith-scale.sh skips index_wait entirely for TARGET=floci, and if it
// did not, the emulator would serve an empty ResourceTagMappingList for
// every IAM type, so the wait would time out for a reason that says nothing
// about the product. An empty result from the emulator is identical for a
// correct implementation and a broken one, which is exactly the situation
// #1152 exists to name.
//
// What it is NOT: a claim that the split is unproven. The split is proven
// against the three recorded real-AWS measurements by
// selftest-index-wait.sh's case A, and index_wait's own behaviour on it is
// proven by cases C through F. What is unproven is the END-TO-END path: a
// real run of this estate whose global objects actually appear in the index
// being polled. Only real AWS or a fixed emulator can do that.
//
// Why it is here rather than in a comment: a comment stating "floci cannot
// serve this" rots the day floci can. This assertion fails in the reverse
// direction - the day the pinned emulator starts serving IAM through
// GetResources, it goes red and names the issue to re-read, because at that
// point terralith-scale.sh's TARGET=floci skip is worth reconsidering and
// the global half becomes exercisable without spending a cent.
func TestIndexWaitGlobalHalfStillUnprovableOnTheEmulator(t *testing.T) {
	for _, tfType := range indexWaitGlobalHalfTypes {
		cap, ok := FlociTypeCapability(pinnedDigest, tfType, "tagging-sweep")
		if !ok {
			t.Errorf("live/floci-capabilities.json has no tagging-sweep row for %s at the pinned digest %s.\n"+
				"That row is this exception's whole input: without it nothing can tell whether the emulator "+
				"now serves the type. Re-run tools/floci-capability-gen's tagging mode against the pin, or, "+
				"if the row was deliberately dropped, re-read %s and decide what replaces this guard.",
				tfType, pinnedDigest, indexWaitGlobalHalfTracker)
			continue
		}
		if cap.Status == FlociUnimplemented {
			continue
		}
		t.Errorf("the pinned emulator now reports %s tagging-sweep as %q, not %q.\n"+
			"Evidence on record: %s\n\n"+
			"This exception has expired. It said index_partition's global bucket (20*SCALE+1 objects: "+
			"aws_iam_policy + aws_iam_instance_profile per scale, plus the route53 zone) could not be "+
			"exercised end to end because the emulator served no IAM through GetResources - %s. If that "+
			"is fixed, re-read %s and decide two things: whether live/live-cert/terralith-scale.sh should "+
			"still skip index_wait for TARGET=floci (its skip message cites this same issue), and whether "+
			"#1144's per-type, per-region routing can now be exercised against the emulator. Then delete "+
			"this test or narrow it to whatever is still unserved.",
			tfType, cap.Status, FlociUnimplemented, cap.Evidence, indexWaitGlobalHalfTracker, indexWaitGlobalHalfTracker)
	}
}
