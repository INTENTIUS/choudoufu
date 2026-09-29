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

// ── issue #1152's exception, narrowed ────────────────────────────────────
//
// What stood here was TestIndexWaitGlobalHalfStillUnprovableOnTheEmulator,
// a self-retiring exception recording that index_partition's global bucket
// could not be exercised end to end because the pinned emulator served an
// empty ResourceTagMappingList for every IAM type. It expired on
// 2026-09-18, exactly as designed: live/floci-image moved to
// sha256:74ffd40e..., the capability manifest's tagging-sweep rows for
// aws_iam_policy and aws_iam_instance_profile turned implemented, and the
// assertion went red naming #1152 and the two decisions to make.
//
// Both decisions, made and recorded here because the skip message and this
// file are the two places a reader looks:
//
//  1. live/live-cert/terralith-scale.sh KEEPS skipping index_wait for
//     TARGET=floci, on a narrower reason than before. The IAM half of the
//     old reason is gone. Two reasons survive it. The first is that the
//     emulator's index is written synchronously - a probe tagged an object
//     and GetResources returned it on the next call, with no settling at
//     all - so a wait against floci measures no lag, and lag is the only
//     thing the wait exists to absorb (#1046, #1049). The second is the one
//     this file can still check, and does, below: index_partition's target
//     is regional + global, nine types between them, and SEVEN of the nine
//     have no tagging-sweep row at this digest at all. Silence in the
//     manifest is "not yet probed", never a clean bill of health
//     (live/flocicap.go), so the target a floci index_wait would poll to is
//     not derivable from evidence. index_partition itself is UNCHANGED: it
//     is TARGET-blind and stays that way.
//
//  2. #1144's per-type, per-region routing can now be exercised against the
//     emulator, and is. internal/live/discovery's
//     TestPerRegionTaggingRoutingAgainstFloci runs a correct narrowing and
//     two broken ones against one container and records that they produce
//     different observable results - the comparison #1152 said was
//     impossible, because an empty answer had been identical for a correct
//     implementation and a broken one.
//
// What replaces the exception is below, and it is deliberately not nothing.
// An exception list with nothing challenging it rots, which is why the
// original was written to fail in both directions; the two arms keep that
// property over the part that is still unserved.

// indexWaitTracker is the issue to re-read when either assertion below
// flips. Named as a constant so the failure messages and the reason cannot
// drift apart.
const indexWaitTracker = "#1152 (lex00/floci#205), and #1143 for the split itself"

// indexWaitUnindexedTypes are the types index_partition puts in its
// `unindexed` bucket - 11*SCALE objects it subtracts from the target
// outright, on the grounds that GetResources returns nothing for them in
// ANY region.
//
// That is a claim about real AWS (#1134: 0 returned, while iam:ListRoleTags
// showed 550 of 550 tagged, stable over 35 minutes) and the emulator is
// faithful to it. The arm below is the reverse direction: an emulator that
// STARTED serving iam:role would diverge from AWS the other way, and the
// bucket that subtracts these objects would be wrong on the emulator while
// staying right on AWS - which is the shape hardest to notice, because the
// split would keep reproducing all three recorded real-AWS plateaus.
var indexWaitUnindexedTypes = []string{"aws_iam_role"}

// indexWaitReachableTypes are the types index_partition's other two buckets
// name: the ones a target is built FROM. All nine must be probed and
// implemented before a floci index_wait could poll to a target derived from
// evidence rather than from assumption.
//
// Kept in bucket order, matching terralith-scale.sh's own comment, so the
// two lists can be read side by side.
var indexWaitReachableTypes = []string{
	// regional, 2*SCALE + 4
	"aws_ecs_task_definition", "aws_ecs_service", "aws_ecs_cluster",
	"aws_vpc", "aws_subnet", "aws_security_group",
	// global, 20*SCALE + 1
	"aws_iam_policy", "aws_iam_instance_profile", "aws_route53_zone",
}

// TestIndexWaitUnindexedBucketStaysUnservedOnTheEmulator is the half of the
// old exception that is still meaningful, and it is meaningful precisely
// because it is unserved BY DESIGN rather than by omission.
//
// It fails if the pinned emulator ever starts serving aws_iam_role through
// GetResources. That would not be an improvement: real AWS does not, and
// index_partition subtracts 11*SCALE objects from its target on the
// strength of that. A divergent emulator would make the subtraction wrong
// here while leaving it right there, and nothing else in the tree would
// notice.
func TestIndexWaitUnindexedBucketStaysUnservedOnTheEmulator(t *testing.T) {
	for _, tfType := range indexWaitUnindexedTypes {
		cap, ok := FlociTypeCapability(pinnedDigest, tfType, "tagging-sweep")
		if !ok {
			t.Errorf("live/floci-capabilities.json has no tagging-sweep row for %s at the pinned digest %s.\n"+
				"That row is this guard's whole input: without it nothing can tell whether the emulator has "+
				"started serving a type real AWS never serves. Re-run tools/floci-capability-gen's tagging mode "+
				"against the pin, or, if the recipe was deliberately dropped, re-read %s and decide what replaces "+
				"this guard.", tfType, pinnedDigest, indexWaitTracker)
			continue
		}
		if cap.Status == FlociUnimplemented {
			continue
		}
		t.Errorf("the pinned emulator reports %s tagging-sweep as %q, not %q.\n"+
			"Evidence on record: %s\n\n"+
			"This is the emulator diverging from real AWS, not catching up to it. #1134 measured a live account "+
			"at scale 50: GetResources returns 0 for iam:role in EVERY region while iam:ListRoleTags shows every "+
			"one of them tagged. live/live-cert/terralith-scale.sh's index_partition subtracts 11*SCALE objects "+
			"from index_wait's target on exactly that, and internal/live/discovery's taggingAPITypeCoverage routes "+
			"the type away from the tagging leg on it too. If floci now indexes roles, both are right about AWS "+
			"and wrong about this emulator - re-read %s before changing either.",
			tfType, cap.Status, FlociUnimplemented, cap.Evidence, indexWaitTracker)
	}
}

// TestIndexWaitTargetIsNotYetDerivableOnTheEmulator is decision 1's evidence,
// and it is self-retiring in the same way the exception it replaces was.
//
// It records which of index_partition's nine target-bearing types the
// pinned emulator has no implemented tagging-sweep row for. While any
// remain, terralith-scale.sh's TARGET=floci skip stands on something
// checkable rather than on a sentence. When none remain, this goes red and
// says so, and the skip is worth re-deciding - though not automatically
// withdrawing: the other half of the reason, that the emulator's index does
// not lag and so a wait measures nothing, is not a thing this file can
// check.
func TestIndexWaitTargetIsNotYetDerivableOnTheEmulator(t *testing.T) {
	var unprobed, unimplemented, implemented []string
	for _, tfType := range indexWaitReachableTypes {
		cap, ok := FlociTypeCapability(pinnedDigest, tfType, "tagging-sweep")
		switch {
		case !ok:
			unprobed = append(unprobed, tfType)
		case cap.Status == FlociImplemented:
			implemented = append(implemented, tfType)
		default:
			unimplemented = append(unimplemented, tfType+" ("+string(cap.Status)+")")
		}
	}

	t.Logf("index_partition's %d target-bearing types at %s: %d implemented %v, %d unprobed %v, %d not implemented %v",
		len(indexWaitReachableTypes), pinnedDigest,
		len(implemented), implemented, len(unprobed), unprobed, len(unimplemented), unimplemented)

	if len(unprobed) == 0 && len(unimplemented) == 0 {
		t.Errorf("every one of index_partition's %d target-bearing types now has an implemented tagging-sweep row at "+
			"the pinned digest %s: %v.\n\n"+
			"This guard has expired. It was decision 1's evidence - that a floci index_wait has no target derivable "+
			"from anything, because seven of the nine types were unprobed. That is no longer so. Re-read %s and "+
			"re-decide whether live/live-cert/terralith-scale.sh should still skip index_wait for TARGET=floci, "+
			"remembering the half this file cannot check: the emulator's index is written synchronously, so a wait "+
			"against it measures no lag even when the target is sound. Then delete this test or narrow it again.",
			len(indexWaitReachableTypes), pinnedDigest, implemented, indexWaitTracker)
	}
}
