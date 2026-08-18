// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// This file is the leg that makes internal/live/check's identity golden
// non-silenceable.
//
// TestIdentityGolden pins the rendered identity of every managed resource
// instance the in-repo fixtures resolve. It is the only instrument in this
// repository that measures the value a marker will carry rather than whether
// something refused - every other one counts refusals, and a marker can be
// wrong without anything refusing. Six defects shipped green through that gap.
//
// But the golden regenerates with -update, which is one word, and the diff it
// produces is a changed testdata file among thirteen hundred lines. Nobody
// reads that as an alarm. So the rule "explain a moved line, do not silence
// it" lived only in prose, in two files, and prose is what this project keeps
// discovering was stale.
//
// The golden is therefore pinned again here, in Go, where moving it is a
// one-line diff in a file named for the purpose. -update alone no longer
// makes the tree green: it makes THIS test fail, and the only way past is to
// edit a line next to a comment asking why.
//
// Two legs, and it took an audit to find out that one of them was not enough.
//
// The counts are the first. They catch a regression that adds or drops an
// instance or moves one between classes: reverting #251's conversion
// fabricated three identities and lost two correct ones, which is CONCRETE
// 658 -> 659. An exact pin catches that; a floor does not, and neither does
// any aggregate this repository was recording at the time, because the
// instance count went UP.
//
// The digest is the second, and it exists because the counts alone were
// defeated on 2026-08-16 by injecting a defect that rewrote 35 rendered
// ImportIDs and running -update. Every count was byte-identical afterwards and
// both tests went green over 35 changed markers - which is the very defect
// shape the golden's own doc says it catches eight times out of eleven. A
// count cannot see a value move.

// identityGoldenPin is the shape of internal/live/check/testdata/identity-golden.txt.
//
// TO CHANGE A NUMBER HERE, say why in the commit message, naming what moved
// and in which direction. A rising CONCRETE is usually the campaign working.
// A falling one, or a NEEDS_DISCOVERY that became CONCRETE in a fixture
// nobody touched, is the shape of a fabricated identity - which is worse than
// a refusal, because it is a marker this tool writes into a real cloud tag.
//
// Recompute with:
//
//	env -u PWD go test ./internal/live/check -run TestIdentityGolden -update
//
// then read the "# shape:" block at the top of the regenerated file.
var identityGoldenPin = map[string]int{
	// 734, up from 726 (issue #286, three more ratified rows missing an
	// optional component the provider's own import syntax includes):
	// eight ADDED rows across three fresh fixtures,
	// internal/live/identity/testdata/route53-record-set-identifier,
	// internal/live/identity/testdata/route53-zone-association-vpc-region and
	// internal/live/identity/testdata/target-group-attachment-optional,
	// exercising [identity.Component.OmitIfAbsent] on
	// aws_route53_record's set_identifier, aws_route53_zone_association's
	// vpc_region, and aws_lb_target_group_attachment /
	// aws_alb_target_group_attachment's availability_zone and
	// quic_server_id, against the provider's own documented import forms.
	// Not a moved row - no in-repo fixture used any of these four optional
	// arguments before this, so every pre-existing CONCRETE row in the
	// golden is byte-identical; see the digest below.
	//
	// 726, up from 723 (aws_lambda_permission's qualifier defect):
	// three ADDED rows in a fresh fixture,
	// internal/live/identity/testdata/omit-if-absent - unqualified,
	// qualified and present_but_null, exercising [identity.Component.OmitIfAbsent]
	// against the two shapes the provider's own Import section documents
	// for aws_lambda_permission plus the corpus regression a for_each
	// module's `qualifier = try(each.value.qualifier, null)` surfaced. Not
	// a moved row - the fix corrects the RATIFIED row's own components, and
	// every other CONCRETE row in the golden is byte-identical; see the
	// digest below.
	//
	// 742, up from 737 (issue #245's composite-bucket ratification batch,
	// merged 2026-08-18): five ADDED rows in
	// internal/live/identity/testdata/identity-object-distinct, all
	// aws_autoscaling_schedule - duplicate_a, duplicate_b and three
	// this[...] instances. That type used to have no identity.DefaultTable
	// row at all; the batch ratified it as a real "/"-joined composite
	// (autoscaling_group_name/scheduled_action_name) straight from the
	// provider's own documented Import section. Not a moved row - no
	// pre-existing CONCRETE row used this type before; see the digest
	// below.
	"CONCRETE": 742,
	// 601, up from 589 (issue #289's marker fallback): 12 ADDED rows across
	// nine fixtures - internal/live/identity/testdata/concrete-parent-attr
	// (aws_ecs_service.web, aws_eks_access_entry.assumed, resolved with no
	// schemas, which is what this sweep does),
	// internal/live/identity/testdata/module-provider-remap (both provider
	// aliases' aws_cloudwatch_log_group.this),
	// internal/live/identity/testdata/name-prefix-conditional-null
	// (aws_iam_role.broken_no_prefix), internal/live/identity/testdata/
	// naming-signal (aws_cloudwatch_log_group.split[1], aws_s3_bucket.nulled),
	// internal/live/identity/testdata/shapeb-trydata
	// (module.u.aws_iam_user.this["alice"]),
	// internal/live/lint/testdata/receipt-leaf (two sites) and
	// receipt-leaf-dynamic-name (one site), and
	// live/e2e/estates/ecs-eks's own aws_eks_access_entry.app. Every one of
	// these types is [identity.DiscoverableFallbackTypes]: taggable and
	// enumerable, so a migrated estate's marker now answers what
	// configuration alone could not, the same way ServerAssigned already
	// does for the other 589. No pre-existing row changed class - not a
	// moved row anywhere in this delta; see the digest below.
	//
	// Four rows also swapped class-preserving addresses this same commit:
	// internal/live/identity/testdata/foreach-value-impure and
	// foreach-value-sensitive renamed their CONCRETE aws_iam_user.team
	// blocks to aws_iam_group.team, so those two tests keep pinning the
	// general (ungated) refusal shape rather than #289's new answer for a
	// taggable, enumerable type - four rows removed, four added, same
	// class, same rendered values, net zero. See
	// internal/live/identity/markerfallback.go's own doc comment for why
	// aws_iam_user could not stay: it is taggable and enumerable too.
	"NEEDS_DISCOVERY": 613,
	// 96, up from 95 (issue #271):
	// internal/live/identity/testdata/managed-read-direct-arg's
	// aws_cloudwatch_log_group.app, whose name is
	// aws_acm_certificate.cert.arn. Resolved with no managed results - which
	// is what this sweep does - that is the ordinary symbolic-reference path
	// and it renders the formula ${aws_acm_certificate.cert.arn}. The
	// fixture exists for what happens when a run DOES hold managed results,
	// which this instrument never does.
	"PARENT_DERIVED": 96,
	"RECORD_BACKED":  17,
}

// identityGoldenPinBodyDigest is sha256 over the golden's rows, and it is the
// leg that covers the VALUES rather than the counts.
//
// The counts above cannot see an identity move. An audit proved it by
// injecting a one-line defect into identity resolution that rewrote 35
// rendered ImportIDs, running -update, and watching both this test and
// TestIdentityGolden go green with a byte-identical "# shape:" count block.
// That is exactly the defect shape the golden's own doc says it catches eight
// times out of eleven - a MODIFIED line - and it was the one shape the pin
// could not see, because a changed value moves no count.
//
// So this number changes on any change to any rendered identity, which is
// deliberate and is the whole guarantee: -update alone leaves the tree red,
// and the only way past is to edit this line next to a comment asking why.
//
// TO CHANGE IT, read the diff first. `git diff` on the golden separates
// modified rows from added ones and they mean opposite things: an added row is
// the campaign working, a modified row on a fixture nobody touched is a marker
// this tool would write into a real cloud tag having silently changed.
//
// Recompute with:
//
//	env -u PWD go test ./internal/live/check -run TestIdentityGolden -update
//
// then copy the body-sha256 from the regenerated file's header.
// 2026-08-17 (issue #271): nine ADDED rows across eight new fixture
// directories, and zero MODIFIED ones. TestIdentityGolden's own diff, read
// before this line was edited, reported "0 identities changed, 9 added, 0
// removed". That zero is the load-bearing half: the sibling-apply
// classification #271 added is gated on a run holding managed results, this
// sweep supplies none, and so not one pre-existing marker moved.
// 2026-08-17 (issue #286): eight ADDED rows across three new fixture
// directories, and zero MODIFIED ones. TestIdentityGolden's own diff, read
// before this line was edited, reported "0 identities changed, 8 added, 0
// removed". That zero is the load-bearing half: no committed fixture under
// internal/live or live/ used set_identifier, vpc_region, availability_zone
// or quic_server_id before this fix, so no pre-existing marker's rendered
// string moved - only the three fresh fixtures exercising the newly
// admitted optional components contributed rows.
// 2026-08-17 (issue #289): TestIdentityGolden's own diff, read before this
// line was edited, reported "0 identities changed, 16 added, 4 removed".
// Both halves are load-bearing. Zero CHANGED means no pre-existing marker's
// rendered string moved - the marker fallback only ever fires on the
// FAILURE path, after configuration has already failed to yield a value,
// so a row that resolved before this change resolves identically after it.
// 16 ADDED is the class breakdown identityGoldenPin's own comment details.
// The 4 REMOVED are not a loss: they are the other half of 4 of the 16
// ADDED rows, a class-preserving rename (aws_iam_user.team ->
// aws_iam_group.team in two fixtures) made so those two tests keep
// exercising the general refusal shape rather than #289's new answer.
const identityGoldenPinBodyDigest = "0c7bc85a0ab6b37ffa7a281fc9244525d06ee7e4ca7aeb2a918499d1903628a4"

// 2026-08-17 (issue #270): dirs 412 -> 413, instances unchanged at 1385 and
// the body digest unchanged. The new directory is
// internal/live/check/testdata/stamp-untaggable-record-located, the
// onboarded half of the stamp-gate split: a markerless type under a
// record_store, which resolves RECORD_LOCATED. It contributes no row
// because this sweep runs SCHEMA-LESS by design, and
// identity.LocatedType fails closed with no schema - the credential
// exclusion is readable only from a schema and a predicate that cannot run
// must refuse. So the located class is a class this instrument cannot see,
// which is stated here rather than discovered later from a suspiciously
// stable digest.
// 2026-08-17 (issue #271): dirs 424 -> 432 and instances 1397 -> 1406. Eight
// new fixture directories for the sibling-apply discriminator, contributing
// eight certificates and one log group. Both numbers rise by exactly what the
// eight directories hold.
// 2026-08-17 (issue #286): dirs 435 -> 438 and instances 1419 -> 1427. Three
// new fixture directories - route53-record-set-identifier,
// route53-zone-association-vpc-region and target-group-attachment-optional -
// contributing two, two and four instances respectively. Both numbers rise
// by exactly what the three directories hold.
// 2026-08-17 (issue #289): dirs unchanged at 443, instances 1436 -> 1448.
// No fixture directory was added or removed - every one of the 16 ADDED
// rows and 4 REMOVED rows (net +12) came from editing existing fixtures,
// most of them to swap a testing-generic resource type. See
// identityGoldenPin's own comment for the class breakdown and
// internal/live/identity/markerfallback.go for the mechanism.
// 2026-08-17 (merge of #253 and #289): dirs 443 -> 445, instances 1448 ->
// 1454, NEEDS_DISCOVERY 601 -> 607. The two branches were developed from a
// common ancestor and merged independently - #253 added two fixture
// directories (internal/live/dataread/testdata/zz-audit-arity and
// managed-projection-arity-expanded, six NEEDS_DISCOVERY aws_instance
// instances between them) that #289's branch never saw, and #289 added the
// marker-fallback reclassification that #253's branch never saw. Merging
// produced a real conflict in the golden data file itself (both branches
// independently regenerated it from a different ancestor), resolved per
// this repository's standing rule by regenerating fresh against the fully
// merged code rather than hand-merging the two diffs
// (contributing/LIVE-TABLES.md, "never hand-edit ... any artifact under
// live/"). The arithmetic checks: 1436 (pre-#253, pre-#289) + 6 (#253's own
// delta) + 12 (#289's own delta) = 1454, exactly the regenerated total -
// the two changes compose with no interaction, which is what independent,
// disjoint fixtures and a failure-path-only gate predict.
// 2026-08-17 (merge of #294): dirs unchanged at 445, instances 1454 -> 1456,
// CONCRETE 734 -> 736. Another independent-branch golden conflict, resolved
// the same way as the #253/#289 merge above: regenerate fresh rather than
// hand-merge. live/e2e/estates/lambda gained two rows for the two types
// lambdaTypes had grown to (aws_lambda_function_event_invoke_config,
// aws_lambda_layer_version_permission - both CONCRETE, both ADDED) and one
// existing row changed in place - aws_lambda_permission.app's identity now
// carries "qualifier", from #175's already-ratified component that this
// cohort's committed lambda.tf predated regenerating against. All three are
// named in #294's own commit message; nothing here is unexplained.
// 2026-08-17 (#258): dirs 445 -> 447, instances unchanged at 1456. Two new
// fixture directories (typedvar-emptyset and its child module) pin the
// currently-unreachable empty-set-target chase; zero instance lines added,
// changed or removed - fixture bookkeeping, not a behavior change.
// 2026-08-17 (issue #256 item 6, merged independently of #258 above): dirs
// 447 -> 448, instances and digest unchanged. A new fixture,
// internal/live/check/testdata/backend-only-real, proves OnboardingBackendOnly's
// coupling to lint.RuleStateBackend's severity through the real analyze.go
// pipeline rather than a fabricated Findings slice. It declares only a
// backend block and no resources, so it adds one directory and zero
// instances.
// 2026-08-17 (issue #238, merged independently of #256/#258 above): dirs
// 448 -> 449, instances and digest unchanged. A new fixture,
// live/e2e/limits/local-sensitive-file, exercises local_sensitive_file's
// new SECRET_REFUSED verdict. Its resource has no identity.DefaultTable
// row (local_sensitive_file is a logical type, not an AWS resource), so it
// contributes a directory and zero golden instance lines - same shape as
// local_file's own fixture.
// 2026-08-18 (merge of #272): dirs 449 -> 451, instances 1456 -> 1463,
// CONCRETE 736 -> 737, NEEDS_DISCOVERY 607 -> 613. #272's branch was based
// on a commit roughly 190 commits behind main and its own golden diff could
// not be hand-merged with everything landed since, so the file was
// regenerated fresh against the fully-merged code per this repository's
// standing rule. Two new fixture directories -
// internal/live/discovery/testdata/contentmatch-e2e and
// contentmatch-static - exercise the new content-match discovery leg: six
// NEEDS_DISCOVERY instances (the policy itself, unresolvable from
// configuration alone) and one CONCRETE instance (an unrelated
// aws_s3_bucket fixture neighbour). Every other line in the golden is
// byte-identical to the pre-merge file; the diff is a pure addition of
// these seven rows, matching dirs +2 and instances +7 exactly.
// 2026-08-18 (same merge, discovered fixing the merge's own test breakage):
// dirs and instances unchanged; digest only. Two independently-evolved
// mechanisms - the earlier unique-name binding (uniquename.go) and #272's
// own content-match - both qualified the same four CloudFront/Route53
// types from the same two-source uniqueness evidence, so scanType's
// dispatch now defers to unique-name (the admission-backed leg) whenever
// both apply; see discovery.go's own doc comment. That moved
// contentmatch-e2e's fixture off aws_cloudfront_cache_policy, which cleared
// unique-name's bar and so stopped reaching content-match through the real
// dispatch this test exists to exercise, onto
// aws_cloudfront_realtime_log_config, which does not. Same directory, same
// class, one address swapped for another - net zero on every count above.
// 2026-08-18 (merge of #245 slice 3, ratify the composite bucket): dirs
// 451 -> 452, instances 1463 -> 1468, CONCRETE 737 -> 742. One new fixture,
// internal/live/identity/testdata/identity-object-distinct - five ADDED
// rows, all aws_autoscaling_schedule (duplicate_a, duplicate_b and three
// this[...] instances) - exercising the batch's ratification of that type
// as a real "/"-joined composite (autoscaling_group_name/
// scheduled_action_name) read straight from the provider's documented
// Import section. That type previously had no identity.DefaultTable row at
// all; every pre-existing CONCRETE row in the golden is byte-identical.
const (
	identityGoldenPinInstances = 1468
	identityGoldenPinDirs      = 452

	// identityGoldenSweepFloor is the anti-tamper leg, in the same spirit as
	// universeFloor in admission_coverage_test.go.
	//
	// Every pin above can be satisfied by making the sweep smaller: drop a
	// root, tighten the file filter, and the numbers fall to something you
	// then re-pin, with each individual edit looking reasonable. This is the
	// number that must never go down. It is deliberately well below the
	// current 400 so that removing fixtures is not an event, and far enough
	// above zero that narrowing the walk to nothing is.
	identityGoldenSweepFloor = 300
)

// TestIdentityGoldenShapeIsPinned fails in both directions.
//
// Forwards: the golden's shape moved and nobody recorded why, which is what a
// bare -update produces.
//
// Backwards: this pin names a class the golden no longer emits, or the header
// disagrees with a recount of the body beneath it. Both mean the pin has
// stopped describing the thing it pins, and a pin that describes nothing
// passes forever.
func TestIdentityGoldenShapeIsPinned(t *testing.T) {
	path := filepath.Join(repoRoot(t), "internal", "live", "check", "testdata", "identity-golden.txt")

	header, bodyCounts, bodyInstances, bodyDigest, headerDigest := readIdentityGolden(t, path)

	// The value leg, checked first because it is the one that covers what a
	// marker will actually say. The counts below cover how many there are.
	if headerDigest == "" {
		t.Error("the golden's header carries no \"# shape: body-sha256=\" line.\n" +
			"Without it the pin covers counts only, and a defect that rewrites a rendered identity without\n" +
			"adding or removing one is invisible to every check in this repository. Regenerate with -update.")
	} else if headerDigest != bodyDigest {
		t.Errorf("the golden's header says body-sha256=%s, a rehash of its rows gives %s: the header was edited without regenerating.",
			headerDigest, bodyDigest)
	}
	if bodyDigest != identityGoldenPinBodyDigest {
		t.Errorf("the golden's rows hash to %s, pinned at %s.\n"+
			"Some rendered identity changed. That is not settled by re-running -update: the counts can be identical\n"+
			"while every marker in the file is different, which is how this leg came to exist.\n"+
			"Read `git diff internal/live/check/testdata/identity-golden.txt`. A MODIFIED row on a fixture nobody\n"+
			"touched is a marker this tool would write into a real cloud tag having silently moved; an ADDED row is\n"+
			"the campaign working. Say which, then re-pin.",
			bodyDigest, identityGoldenPinBodyDigest)
	}

	// The header is written by the same walk that writes the body, so these
	// agreeing proves only that the file was not hand-edited afterwards -
	// which is exactly the edit this test would otherwise miss, since the
	// header is where a reader looks and the body is where the truth is.
	if got, want := header["instances"], bodyInstances; got != want {
		t.Errorf("header says instances=%d, body holds %d rows: the header was edited without regenerating", got, want)
	}
	for class, n := range bodyCounts {
		if got := header["class "+class]; got != n {
			t.Errorf("header says class %s=%d, body holds %d: the header was edited without regenerating", class, got, n)
		}
	}

	if got := header["dirs"]; got < identityGoldenSweepFloor {
		t.Errorf("the golden swept %d configuration directories, below the floor of %d.\n"+
			"Every count in identityGoldenPin can be satisfied by sweeping less, so this is the leg that is not allowed to move.\n"+
			"If the tree genuinely shrank, lower the floor in its own commit that says so - not in the commit that shrank it.",
			got, identityGoldenSweepFloor)
	}
	if got := header["dirs"]; got != identityGoldenPinDirs {
		t.Errorf("the golden sweeps %d configuration directories, pinned at %d.\n"+
			"Adding fixtures moves this and is fine; say so and re-pin.",
			got, identityGoldenPinDirs)
	}
	if bodyInstances != identityGoldenPinInstances {
		t.Errorf("the golden holds %d instances, pinned at %d.\n"+
			"An instance is a marker this tool writes into a cloud tag. Rising is usually the campaign working; falling means something that resolved no longer does.\n"+
			"Neither is settled by re-running -update.",
			bodyInstances, identityGoldenPinInstances)
	}

	for _, class := range sortedKeys(identityGoldenPin) {
		want := identityGoldenPin[class]
		got, present := bodyCounts[class]
		if !present {
			t.Errorf("identityGoldenPin pins class %s at %d, but the golden no longer emits that class at all.\n"+
				"A pin on something that does not exist passes forever. Remove it, or find out why the class vanished.",
				class, want)
			continue
		}
		if got != want {
			t.Errorf("class %s: golden has %d, pinned at %d.\n"+
				"%s", class, got, want, identityGoldenClassAdvice(class, got, want))
		}
	}
	for _, class := range sortedKeys(bodyCounts) {
		if _, pinned := identityGoldenPin[class]; !pinned {
			t.Errorf("the golden emits class %s (%d instances) and identityGoldenPin does not mention it.\n"+
				"A new class is a new way for a marker to be rendered, and it is unpinned until it is listed here.",
				class, bodyCounts[class])
		}
	}
}

// identityGoldenClassAdvice says which direction of movement is the alarming
// one, per class, because they are not symmetric.
func identityGoldenClassAdvice(class string, got, want int) string {
	switch class {
	case "CONCRETE":
		if got > want {
			return "CONCRETE rose. That is usually the campaign working - but it is also exactly what a fabricated identity looks like,\n" +
				"and a fabricated marker is worse than a refusal. Read the added lines' rendered values before re-pinning."
		}
		return "CONCRETE fell. Something that rendered a real identity no longer does, which is a regression unless a fixture was deleted."
	case "NEEDS_DISCOVERY":
		if got < want {
			return "NEEDS_DISCOVERY fell. If CONCRETE rose by the same amount, resources became identifiable offline, which is the goal.\n" +
				"If it fell on its own, instances went missing."
		}
		return "NEEDS_DISCOVERY rose. Something that used to resolve offline now defers to a live read."
	default:
		return "Read the diff and say which fixtures moved."
	}
}

// readIdentityGolden parses the "# shape:" header and independently recounts
// the body, so the two can be compared against each other.
func readIdentityGolden(t *testing.T, path string) (header map[string]int, bodyCounts map[string]int, bodyInstances int, bodyDigest, headerDigest string) {
	t.Helper()

	// Rehashed here rather than read from the header, so a hand-edited header
	// and a hand-edited body are two different failures.
	digest := sha256.New()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("reading the identity golden: %s\n"+
			"This pin exists to guard that file; with the file absent it guards nothing, so this is a failure rather than a skip.", err)
	}
	defer f.Close()

	header = map[string]int{}
	bodyCounts = map[string]int{}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") {
			for _, kv := range parseShapeLine(line) {
				header[kv.key] = kv.n
			}
			if d, ok := parseShapeDigest(line); ok {
				headerDigest = d
			}
			continue
		}
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			t.Fatalf("malformed golden row (%d tab-separated fields, want at least 3): %q", len(fields), line)
		}
		bodyCounts[fields[2]]++
		bodyInstances++
		// The same bytes the writer hashed: each row plus its newline, and
		// nothing else.
		digest.Write([]byte(line))
		digest.Write([]byte("\n"))
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scanning the identity golden: %s", err)
	}
	if len(header) == 0 {
		t.Fatal("the identity golden carries no \"# shape:\" header.\n" +
			"That block is what makes a silenced regression visible in the first fifteen lines of a diff; regenerate with -update.")
	}
	return header, bodyCounts, bodyInstances, hex.EncodeToString(digest.Sum(nil)), headerDigest
}

// parseShapeDigest reads "# shape: body-sha256=<hex>". It is separate from
// parseShapeLine because that one Atoi's every value and silently drops what
// does not parse, which is how a hex digest would have gone unnoticed.
func parseShapeDigest(line string) (string, bool) {
	const marker = "# shape:"
	if !strings.HasPrefix(line, marker) {
		return "", false
	}
	for _, field := range strings.Fields(strings.TrimPrefix(line, marker)) {
		if v, ok := strings.CutPrefix(field, "body-sha256="); ok {
			return v, true
		}
	}
	return "", false
}

type shapeKV struct {
	key string
	n   int
}

// parseShapeLine reads "# shape: dirs=375 instances=1320" and
// "# shape: class CONCRETE=658".
func parseShapeLine(line string) []shapeKV {
	const marker = "# shape:"
	if !strings.HasPrefix(line, marker) {
		return nil
	}
	rest := strings.TrimSpace(strings.TrimPrefix(line, marker))

	var prefix string
	if after, ok := strings.CutPrefix(rest, "class "); ok {
		prefix, rest = "class ", after
	}

	var out []shapeKV
	for _, field := range strings.Fields(rest) {
		key, val, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(val)
		if err != nil {
			continue
		}
		out = append(out, shapeKV{key: prefix + key, n: n})
	}
	return out
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
