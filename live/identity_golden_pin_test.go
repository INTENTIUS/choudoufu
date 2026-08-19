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
	//
	// 755, up from 742 (worstCaseChildKey's count'd module call): thirteen
	// ADDED rows, all one fixture's aws_s3_bucket with a fixed literal
	// bucket argument. internal/live/lint/testdata/overlong-address gained
	// a count = 12 module call whose child holds a single bucket, so the
	// call's twelve instances contribute
	// module.counted[0..11].aws_s3_bucket.q...  to that directory and the
	// child directory contributes the bare aws_s3_bucket.q... when the
	// sweep reaches it on its own. All thirteen render the same literal,
	// "counted-child", which is the whole reason they are CONCRETE and not
	// a class this rule could have got wrong. Not a moved row: the fixture
	// is new and no pre-existing row's rendered value changed.
	//
	// 759, up from 755 (issue #308's fix): four ADDED rows across two
	// fresh fixtures exercising the same shape -
	// internal/live/identity/testdata/module-foreach-comprehension-chase
	// and internal/live/lint/testdata/child-module-foreach-comprehension -
	// each contributing
	// module.wrapper.module.task["app"].aws_iam_user.this and
	// module.wrapper.module.task["fluent-bit"].aws_iam_user.this. Both
	// mirror the corpus shape: a child module's own module call for_each
	// ranges over a for-comprehension whose SOURCE is a bare var.X
	// reference chased across a module-call boundary, filtering on one
	// attribute (v.create) while an unrelated sibling attribute (image)
	// reaches a data source; fluent-bit's own "create" comes from the
	// variable's declared `optional(bool, true)` default, never from its
	// own literal. Not a moved row - no pre-existing fixture used this
	// shape before, so every other CONCRETE row in the golden is
	// byte-identical; see the digest below.
	//
	// 761, up from 759 (issue #315's fix): two ADDED rows in a fresh
	// fixture, internal/live/identity/testdata/module-foreach-comprehension-each-value -
	// module.wrapper.module.task["app"].aws_iam_user.this and
	// ["fluent-bit"].aws_iam_user.this, rendering app-core-unset and
	// fluent-bit-default-team-unset. #308 proved a child module's for_each
	// KEY set even when one entry's value has an unprovable sibling
	// attribute (fluent-bit's own "image", an SSM-sourced data source);
	// this fixture goes one step further, into the module call's OWN
	// argument list, which reads each.value.<attr> off the same entries -
	// label (an explicit typeexpr default) and owner (a bare
	// optional(string) with none at all, needing the declared
	// ConstraintType directly rather than typeexpr.Defaults, which never
	// records an entry for that shape). Not a moved row - no pre-existing
	// fixture used this shape before, so every other CONCRETE row in the
	// golden is byte-identical; see the digest below.
	"CONCRETE": 761,
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
	//
	// 614, up from 613 (issue #301): one ADDED row,
	// internal/live/identity/testdata/module-foreach-var-typed-sibling-value's
	// aws_iam_policy.imagebuilder itself - the sibling whose arn the new
	// fixture's bare each.value now resolves through. This sweep supplies
	// no managed results, so the policy's own server-assigned arn stays
	// NEEDS_DISCOVERY; the fixture's OTHER new row
	// (module.attach.aws_iam_role_policy_attachment.this["ImageBuilder"])
	// is PARENT_DERIVED instead, see below.
	//
	// 616, up from 614: live/e2e/estates/apigateway gained two supporting
	// resources, aws_subnet.apigateway and aws_vpc.apigateway
	// (tools/estate-gen's aws_lb override needed a real subnet for
	// aws_lb.apigateway's own subnets argument - "one of `subnet_mapping,
	// subnets` must be specified" - and a subnet needs a VPC for its own
	// vpc_id). Both are server-assigned AWS types with no client-supplied
	// identity, so both render NEEDS_DISCOVERY, the same class every other
	// supporting aws_subnet/aws_vpc pair in this golden already carries
	// (e.g. live/e2e/estates/ec2-networking's aws_subnet.ec2-networking,
	// aws_vpc.ec2-networking).
	//
	// 618, up from 616 (markers.UnescapeAddress's module-step key): one new
	// fixture, internal/live/discovery/testdata/counted-module-orphan, whose
	// root holds a count = 1 module call and whose child/ holds the one
	// aws_vpc it wraps. The two rows are that one resource seen twice - once
	// as module.counted[0].aws_vpc.kept from the root, once as
	// aws_vpc.kept with the child directory swept as a root of its own - and
	// aws_vpc is server-assigned with no client-supplied identity, so both
	// render NEEDS_DISCOVERY like every other bare aws_vpc in this golden.
	// The module.counted[0] spelling in the first row is worth reading: it
	// is identity resolution's own rendering of the count'd call, and it is
	// the address the fix makes UnescapeAddress recover from that marker.
	//
	// 622, up from 618 (issue #316, the rename-withholding guard): one new
	// fixture, internal/live/discovery/testdata/module-rename-withhold,
	// which declares the same for_each'd aws_subnet.this three times over -
	// at the root, inside a static module call, and inside a count = 1
	// module call - so that the guard can be driven down all three module
	// paths and asserted to answer identically. Four rows, because the
	// child/ directory is also swept as a root of its own: the root's own
	// aws_subnet.this["b"], module.net's, module.counted[0]'s, and the
	// child taken alone. aws_subnet is server-assigned with no
	// client-supplied identity, so all four render NEEDS_DISCOVERY, the
	// same class every other bare aws_subnet in this golden carries.
	// 630, up from 622 (issue #321): six new server-assigned resources
	// across element-splat-count-index (aws_route_table.private x3,
	// aws_subnet.private x3) and element-splat-wraparound (aws_subnet.small
	// x2) - eight rows total, all NEEDS_DISCOVERY like every other bare
	// aws_route_table/aws_subnet in this golden. See
	// identityGoldenPinInstances' own comment for the fixtures.
	"NEEDS_DISCOVERY": 630,
	// 96, up from 95 (issue #271):
	// internal/live/identity/testdata/managed-read-direct-arg's
	// aws_cloudwatch_log_group.app, whose name is
	// aws_acm_certificate.cert.arn. Resolved with no managed results - which
	// is what this sweep does - that is the ordinary symbolic-reference path
	// and it renders the formula ${aws_acm_certificate.cert.arn}. The
	// fixture exists for what happens when a run DOES hold managed results,
	// which this instrument never does.
	//
	// 97, up from 96 (issue #301): one ADDED row,
	// module.attach.aws_iam_role_policy_attachment.this["ImageBuilder"] in
	// the same new fixture - a bare each.value forwarding a sibling
	// resource's arn across a module-call boundary, now resolving to
	// "gh-image-builder/${aws_iam_policy.imagebuilder.arn}" through the
	// same [resolver.parentPart] machinery issue #284 built for a direct
	// reference. See internal/live/identity/typedvar.go's preservedExpr.
	// 105, up from 97 (issue #321): eight new aws_route_table_association
	// rows resolving element(<resource>[*].attr, idx) through
	// resolveElementCall - three in element-splat-count-index (subnet_id
	// AND route_table_id both through element(), formula reading both
	// parents at the matching index) and five in element-splat-wraparound
	// (element()'s own modulo wraparound over a 5-instance block and a
	// 2-instance source). See identityGoldenPinInstances' own comment for
	// the fixtures.
	"PARENT_DERIVED": 105,
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
// 2026-08-18 (issue #301): TestIdentityGolden's own diff, read before this
// line was edited, reported "0 identities changed, 2 added, 0 removed".
// Zero CHANGED means no pre-existing marker's rendered string moved -
// #301's fix (internal/live/identity/typedvar.go's preservedExpr) only
// changes what a module-variable hop does with an UNPROVEN value that
// previously had nowhere to go, so any row that already rendered something
// keeps rendering it. The 2 ADDED rows are both from the one new fixture,
// internal/live/identity/testdata/module-foreach-var-typed-sibling-value:
// aws_iam_policy.imagebuilder (NEEDS_DISCOVERY, no managed results in this
// sweep) and module.attach.aws_iam_role_policy_attachment.this["ImageBuilder"]
// (PARENT_DERIVED, the bare each.value -> sibling arn formula #301 exists
// for).
//
// 2026-08-18: two ADDED rows, no dirs change (both land in the existing
// live/e2e/estates/apigateway directory) - aws_subnet.apigateway and
// aws_vpc.apigateway, both NEEDS_DISCOVERY. See identityGoldenPin's own
// comment above.
// 2026-08-18 (worstCaseChildKey's count'd module call): TestIdentityGolden's
// own diff, read before this line was edited, reported "0 identities
// changed, 13 added, 0 removed". The zero is the load-bearing half, and here
// it is close to a tautology worth stating anyway: the fix is confined to
// internal/live/lint's address-BUDGET measurement, which renders no
// identity and is not on any path this sweep runs. Every one of the 13
// ADDED rows comes from the fixture the fix needed - one count = 12 module
// call in internal/live/lint/testdata/overlong-address and its child - and
// all 13 render the same literal bucket name, "counted-child". Read them in
// the diff: a fabricated identity would have shown up as a rendered value
// that is not that literal.
//
// 2026-08-18 (issue #308's fix): four ADDED rows, dirs 455 -> 461 (two new
// fixture roots, each with two child-module subdirectories of its own -
// see identityGoldenPin's own comment above for the four rows themselves).
//
// 2026-08-18 (markers.UnescapeAddress's module-step key): TestIdentityGolden's
// own diff, read before this line was edited, reported "0 identities changed,
// 2 added, 0 removed". The zero is again the load-bearing half, and here it
// is a real result rather than a tautology: the fix changes how an escaped
// marker DECODES, and this sweep renders identities from configuration
// without ever decoding a marker, so a change visible here would have meant
// the fix reached somewhere it has no business being. The two ADDED rows are
// the fixture the reachability test needed,
// internal/live/discovery/testdata/counted-module-orphan and its child/, both
// rendering NEEDS_DISCOVERY for the same aws_vpc.kept.
//
// 2026-08-18 (issue #316, the rename-withholding guard): TestIdentityGolden's
// own diff, read before this line was edited, reported "0 identities changed,
// 4 added, 0 removed". The zero is the load-bearing half, and it is a real
// result rather than a tautology: the fix changes which orphans discovery
// withholds from removal and which module a "is this block still declared"
// lookup descends into, and this sweep renders identities from configuration
// without classifying an orphan or reading a marker at all, so a changed row
// would have meant the fix reached somewhere it has no business being. The
// four ADDED rows are the fixture the reproduction needed,
// internal/live/discovery/testdata/module-rename-withhold and its child/ -
// one for_each'd aws_subnet.this declared at three module paths, plus the
// child directory swept as a root of its own.
// 2026-08-18 (issue #315's fix): body digest moved because two rows were
// ADDED (see the CONCRETE class comment above for the fixture and shape);
// no pre-existing row's rendered value changed.
// 2026-08-19 (issue #321's fix): body digest moved because sixteen rows
// were ADDED (see identityGoldenPinInstances' own comment above for the
// three fixtures and the class breakdown); no pre-existing row's rendered
// value changed - TestIdentityGolden's own diff read "0 identities
// changed, 16 added, 0 removed" before this line was edited.
const identityGoldenPinBodyDigest = "c2f7935157b3f178b39f9f27a1a90d73c2e03db31e92ca89f368923ba21ff357"

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
// 2026-08-18 (issue #301): dirs 452 -> 454, instances 1468 -> 1470. One new
// fixture directory, internal/live/identity/testdata/
// module-foreach-var-typed-sibling-value, plus its child module directory
// (.../attach) - two directories, two instances (the sibling policy and
// the role-policy-attachment whose bare each.value now resolves through
// it). See identityGoldenPin's own comment above for the class breakdown.
// 2026-08-18: instances 1470 -> 1472, dirs unchanged at 454 (both new rows
// land in the existing live/e2e/estates/apigateway directory).
// 2026-08-18 (worstCaseChildKey's count'd module call): dirs 454 -> 455,
// instances 1472 -> 1485. One new directory,
// internal/live/lint/testdata/overlong-address/counted, holding one
// resource; the +13 is that one row plus the twelve instances the parent
// directory's new count = 12 module call expands it into. dirs rises by one
// and instances by thirteen because a count'd module call multiplies rows
// without adding directories - the same arithmetic a for_each'd call has
// always produced here.
// 2026-08-18 (issue #308's fix): instances 1485 -> 1489, dirs 455 -> 461.
// Two new fixture roots (module-foreach-comprehension-chase,
// child-module-foreach-comprehension), each with a wrapper/ and a
// wrapper/task/ child module directory - six new directories, four new
// CONCRETE instances (two per fixture; see identityGoldenPin's own comment
// above).
// 2026-08-18 (markers.UnescapeAddress's module-step key): instances
// 1489 -> 1491, dirs 461 -> 463. One new fixture root,
// internal/live/discovery/testdata/counted-module-orphan, plus its child/
// module directory - two directories, and two instances because the child's
// single aws_vpc is swept once under the root's count = 1 module call and
// once with child/ taken as a root of its own.
// 2026-08-18 (issue #316, the rename-withholding guard): instances
// 1491 -> 1495, dirs 463 -> 465. One new fixture root,
// internal/live/discovery/testdata/module-rename-withhold, plus its child/
// module directory - two directories, and four instances because the root
// sweep sees the same for_each'd aws_subnet.this three times (the root's own
// block, the static module call's, and the count = 1 module call's, the last
// two being the child's one block seen through two calls), with a fourth
// coming from child/ swept as a root of its own.
// 2026-08-18 (issue #313, the data-read value crossing a plain module call):
// instances 1495 -> 1495, dirs 465 -> 467. One new fixture root,
// internal/live/identity/testdata/data-read-across-module-call, plus its
// child/ module directory - two directories, and NO new instances, which is
// the whole point of this entry. The fixture is a root-module data source
// feeding an unrepeated module call's argument, and the golden renders every
// fixture without DataResults, so it resolves nothing here by construction.
// That is #313's offline guarantee written down as a number: the widening in
// internal/live/identity's resolver.frozenClosureIsStale is reachable only
// when read results exist, so live-check, which never reads, cannot see it.
// An instance appearing here later would mean the widening had escaped that
// condition.
// 2026-08-18 (issue #315's fix): instances 1495 -> 1497, dirs 467 -> 470.
// One new fixture root, internal/live/identity/testdata/module-foreach-
// comprehension-each-value, plus its wrapper/ and wrapper/task/ module
// directories - three directories, two new CONCRETE instances (see the
// CONCRETE class comment above).
// 2026-08-19 (issue #321's fix, splat.go's resolveElementCall): instances
// 1497 -> 1513, dirs 470 -> 473, NEEDS_DISCOVERY 622 -> 630, PARENT_DERIVED
// 97 -> 105. Three new fixture roots -
// internal/live/identity/testdata/element-splat-count-index,
// element-splat-wraparound and element-splat-empty-source - pinning
// element(<resource>[*].attr, idx) resolving structurally to the
// same-indexed sibling instance. element-splat-count-index contributes
// three aws_route_table.private and three aws_subnet.private
// (NEEDS_DISCOVERY, both server-assigned) plus three
// aws_route_table_association.private (PARENT_DERIVED, formula reading
// both parents at the matching index); element-splat-wraparound
// contributes two aws_subnet.small (NEEDS_DISCOVERY) plus five
// aws_route_table_association.wrap (PARENT_DERIVED), the fifth exercising
// element()'s own modulo wraparound (a 5-instance block over a 2-instance
// source); element-splat-empty-source contributes zero rows - its one
// resource refuses (the source resource expands to no instances), which
// is the point of that fixture. Totals: +16 instances (8 NEEDS_DISCOVERY +
// 8 PARENT_DERIVED), +3 dirs. Every pre-existing row is byte-identical;
// this is a pure addition, matching TestIdentityGolden's own "0 changed,
// 16 added, 0 removed".
// 2026-08-19 (the #304 crash fix, normalizeRefValue's nil cty.Type): dirs
// 473 -> 475, instances unchanged at 1513, every class count unchanged and
// the body digest unchanged. One new fixture root,
// internal/live/lint/testdata/count-index-undeclared-var, plus its child/
// module directory - two directories and NOT ONE instance, which is this
// entry's whole point. The fixture exists to reproduce a crash, so its one
// resource sits under a count nobody can compute (the caller's module
// argument is a binary operation over an undeclared variable), and a block
// whose expansion is unknown resolves no instance by construction. A row
// appearing here later would mean the count had become computable, which
// would mean the fixture had stopped reproducing what it was built for.
//
// The zero on the instances line is the load-bearing half of this entry for
// a second reason: the fix changes a value that the static evaluator hands
// into every hcl.EvalContext it builds, which is as central as this fork
// gets. It moving no rendered identity anywhere in the tree is the evidence
// that it changed only the crashing path.
const (
	identityGoldenPinInstances = 1513
	identityGoldenPinDirs      = 475

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
