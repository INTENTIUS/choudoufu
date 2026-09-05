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
	//
	// 762, up from 761 (issue #324 item 2, splat.go's resolveConcatIndex):
	// one ADDED row, internal/live/identity/testdata/concat-splat-index-
	// literal-fallback's aws_security_group_rule.ingress, rendering
	// "sg-fallback_ingress_tcp_80_80_0.0.0.0/0". Both of the fixture's own
	// splats (aws_security_group.a, .b) provably expand to zero instances,
	// so concat(a.*.id, b.*.id, ["sg-fallback"])[0] provably lands on the
	// trailing literal rather than any resource's attribute - the case the
	// fix's own doc comment says is NOT identity-bearing via a marker, and
	// resolves through resolveExpr on that one literal element the same
	// way any other plain string does. Not a moved row - no pre-existing
	// fixture used this shape before, so every other CONCRETE row in the
	// golden is byte-identical; see the digest below.
	//
	// 763, up from 762 (issue #324 item 1, splat.go's
	// resolveElementCoalescelist): one ADDED row,
	// internal/live/identity/testdata/coalescelist-element-literal-fallback's
	// aws_route_table_association.database, rendering
	// "subnet-fake/rtb-fallback". Both of coalescelist()'s splat arguments
	// (aws_route_table.database, .private) provably expand to zero
	// instances, so coalescelist() provably falls through to its trailing
	// literal-list argument, and element()'s index [0] lands on that one
	// literal element rather than any resource's attribute - not
	// identity-bearing via a marker at all, resolved through resolveExpr
	// on that literal the same way resolveConcatIndex's own literal
	// fallback does. Not a moved row - no pre-existing fixture used this
	// shape before; see the digest below.
	// 765, up from 763 (issue #323, the identity-argument half of
	// partialargs.go's tolerant retry): two ADDED rows, both in the one
	// new fixture root
	// internal/live/identity/testdata/modulearg-partial-value -
	// aws_iam_role.r (the-role, the caller's own literal) and
	// module.u.aws_iam_user.literal[0] (platform-alpha, two literal leaves
	// of a module argument whose third leaf names a resource). No
	// pre-existing row moved.
	// 766, up from 765 (module output read inside a module-CALL argument,
	// moduleoutputvalue.go): one ADDED row, in the one new fixture root
	// internal/live/identity/testdata/module-output-in-call-arg -
	// module.sg.aws_security_group_rule.a[0], rendering
	// sg-fixed_ingress_tcp_5432_5432_10.77.0.0/16. The CIDR is written once
	// in that fixture's root locals and reaches the rule only by evaluating
	// module.vpc's own output expression, so a fabricated or defaulted value
	// could not spell it; it is asserted by value in
	// internal/live/identity's TestModuleOutputInsideModuleCallArgument. The
	// same fixture's five adversarial siblings (rules b..f) contribute no
	// row at all, which is the half that has to hold. No pre-existing row
	// moved.
	//
	// 767, up from 766 (issue #310, identity.Component gaining a Block
	// field): one ADDED row,
	// internal/live/identity/testdata/nested-block-component's
	// aws_autoscaling_traffic_source_attachment.present, rendering the
	// provider's own documented import example verbatim. The fixture's two
	// adversarial siblings (absent, impure) contribute no row. No
	// pre-existing row moved.
	//
	// 773, up from 767 (issue #191, a partial module argument composing
	// across two module calls): six ADDED rows across two new fixture
	// roots. internal/live/identity/testdata/modulearg-nested-partial
	// contributes five - aws_iam_role.r (the-role, the caller's own
	// literal) and four instances two calls down, keyed http/app and
	// https/app on each of two resources, rendering user-http-app,
	// user-https-app, group-80 and group-443. The two group rows are the
	// ones worth reading: 80 and 443 are written in the MIDDLE module's own
	// default and reach the identity through a setproduct, a merge into the
	// object carrying the unknowable leaf, a second merge and a type
	// constraint, so no fabricated or defaulted value could spell them.
	// internal/live/identity/testdata/modulearg-nested-dynkey contributes
	// one, its own aws_iam_role.r; that fixture is the mutation - the same
	// leaf moved into a map key and into a set's elements - and everything
	// below the module call contributes no row at all, which is the half
	// that has to hold. The resource reading the refused leaf itself
	// (modulearg-nested-partial's aws_iam_role.dynamic) contributes no row
	// either: a managed resource's own attribute is a separate, unmade
	// ruling and nothing here pre-empts it. No pre-existing row moved.
	// 775, up from 773 (issue #336, coalesce()'s selection rule): two ADDED
	// rows, both the same fixture resource seen twice -
	// internal/live/identity/testdata/coalesce-selection's
	// aws_iam_group.literal_wins, once as module.child's instance from the
	// root and once as the child module swept on its own. Its name is
	// coalesce("literal-name", var.name), so the rule selects the LITERAL
	// and never consults the record-backed parent at all - which is why it
	// is the one row in that fixture that renders concretely rather than as
	// a formula. The four resources whose identities really do come from
	// the record-backed parent contribute no row here, because a
	// PARENT_DERIVED identity renders empty in this sweep. No pre-existing
	// row moved.
	// 776, up from 775 (issue #326, kubernetes_config_map's ratified row):
	// one ADDED row, internal/live/identity/testdata/kubernetes-config-map's
	// kubernetes_config_map.present, rendering the real, current
	// hashicorp/kubernetes provider's own documented import example shape
	// verbatim (NAMESPACE/NAME, read out of the required metadata block via
	// identity.Component.Block). The fixture's one adversarial sibling
	// (no_namespace, metadata.namespace absent) contributes no row - the
	// half that has to hold, since namespace is Optional in the provider's
	// own schema and a resolver that defaulted it would fabricate an
	// identity the configuration never stated. No pre-existing row moved.
	// 781, up from 776 (issue #353, the provisioner crossing's fixture):
	// five ADDED rows, all of them live/e2e/provisioner-taint's
	// aws_s3_bucket instances (app[0], control, shrinker[0], shrinker[1],
	// tolerant), each rendering the client-named bucket it declares. Nothing
	// about admitting a provisioner touches how any identity renders - a
	// provisioner is not an identity argument and contributes nothing to a
	// marker - so a MODIFIED row here would have meant the fix reached
	// somewhere it has no business reaching. No pre-existing row moved.
	// 790, up from 781 (issue #346, an identity argument reading a sibling's
	// non-identity Computed attribute through a module output): nine ADDED
	// rows, all in the new fixture
	// internal/live/identity/testdata/module-output-sibling-computed. Six of
	// them are the one-element-list narrowing this golden CAN see with no
	// schemas - the endpoints_literal_list and endpoints_output_list calls'
	// aws_security_group_rule.this/.dotted, plus every call's
	// .absent instance, which takes lookup()'s third argument because the
	// element provably lacks the key. The rows that need a provider schema -
	// the deferred read of aws_vpc.this[0].cidr_block - are absent here
	// rather than wrong, exactly as TestConcreteParentAttributeNeedsSchemas
	// records for the same branch. No pre-existing row moved.
	// 791, up from 790 (issue #354, an identity argument reading an unknown
	// attribute of a for_each element the declared-type conversion produced):
	// one ADDED row, module.asg.aws_autoscaling_policy.this["p1"] in the new
	// fixture internal/live/identity/testdata/module-output-whole-resource.
	// It is the CONTROL row rather than the fix's own: it resolves today and
	// had to keep resolving, because `try(coalesce(each.value.name,
	// each.key), "")` is answered by the element VALUE (the declared type
	// makes name null, so coalesce takes the key) and an earlier version of
	// the change re-routed it through the caller's constructor and lost it.
	// No pre-existing row moved.
	// 792, up from 791 (issue #369, a Component.SoleElement alternation
	// member that is a proven zero-element list): one ADDED row,
	// internal/live/identity/testdata/sole-element-from-value's new
	// aws_security_group_rule.resolved_by_sibling, where
	// source_security_group_id supplies the identity and
	// prefix_list_ids (var.empty_prefix_list_ids, default []) is
	// demoted from "present" to "absent" by firstApplicablePresent
	// rather than read as ambiguous. The fixture's negative control,
	// all_empty_no_sibling (every alternation member a proven empty
	// list, nothing else set), stays refused and contributes no row -
	// the half that has to hold. No pre-existing row moved.
	// 793, up from 792 (issue #368, a render-time transform in Formula):
	// one ADDED row, internal/live/identity/testdata/formula-transform's
	// module.cluster.aws_ecs_cluster.this[0], a plain client-named ECS
	// cluster that renders its own name. It is incidental to the change:
	// the fixture needs a real managed resource behind the module output
	// for the transform to have a live value to split. Every row #368
	// actually adds needs provider schemas, which this sweep deliberately
	// does not have, so they are pinned by value in transform_test.go
	// instead. No pre-existing row moved.
	// 2026-08-22 (issue #375): a module-call argument the caller HOISTED into
	// a local, and one that is a child module's whole output named on its
	// own, are now substituted the same way one written out at the call
	// already was. Nine ADDED rows across two new fixtures
	// (internal/live/identity/testdata/module-arg-hoisted and
	// .../merge-bare-module-output); no pre-existing row moved, in class or
	// in value - TestIdentityGolden reported "0 identities changed, 9 added,
	// 0 removed" against the base. The equivalence the fix claims is visible
	// in the rows themselves: module.inline and module.hoisted render
	// byte-identical values on both of their resources.
	// 798, up from 793 (issue #375): five ADDED rows -
	// module.base.module.host.aws_iam_role.host[0] in merge-bare-module-output
	// (which resolved before the fix; the fixture is the pin for the shape
	// the issue named and did not turn out to be the blocker), plus
	// module-arg-hoisted's inline/hoisted aws_iam_role.gated[0] pair, its
	// module.output.aws_iam_role.gated[0] and its
	// module.output.aws_iam_role.derived[0].
	// 799, up from 798 (issue #378, the module-prefix marker symbol): one
	// ADDED row, live/e2e/limits/reserved-symbol's aws_s3_bucket.reserved,
	// the fixture for the new lint rule that reserves
	// tofu.marker_module_prefix. It is incidental to the change: the rule
	// needs a resource to hang the refused reference on, and a bucket renders
	// its own name. Nothing #378 changes about STAMPING can move a row in this
	// sweep at all - it changes what is written into a tags argument, and this
	// sweep renders identities, not tags. 0 changed, 1 added, 0 removed.
	// 799 -> 802 for corpus-sumaform-aws's static count() wall: "merged-0",
	// "sumaform-default-0" and "eu-west-1a-0", each spelled in a different
	// file from the resource that renders it. See
	// identityGoldenPinBodyDigest.
	// 802 -> 808 for corpus-alb-complete's Family A fix (GitHub issue #375's
	// module-INPUT twin - see identityGoldenPinBodyDigest for the fixture
	// and which rows are which): the new fixture's two aws_iam_role.target
	// instances, swept both from the fixture root (module.attach.aws_iam_role.target)
	// and again from its own child module directory (aws_iam_role.target),
	// plus its two aws_iam_user.tag instances.
	// 808 -> 809 for [RecordFallbackType] (corpus-autoscaling-complete's
	// marker-only aws_autoscaling_group instances, HANDOFF's first table
	// row): one ADDED row,
	// internal/live/identity/testdata/record-fallback-untaggable's
	// aws_autoscaling_group.named, which states a literal `name` and stays
	// CONCRETE precisely to prove the new fallback does not shadow the
	// ordinary path. See identityGoldenPinBodyDigest.
	// 809 -> 811 for GitHub issue #372's remainder (a per-instance
	// ClassNeedsDiscovery check settling a client-named count instance's
	// tofu-slot at migrate time): two ADDED rows,
	// internal/live/liveimport/testdata/slot-clientnamed-literal-config's
	// aws_iam_role.this[0] and .this[1], the negative-control fixture whose
	// static "name = \"task-${count.index}\"" resolves CONCRETE - proving the
	// new gate does not fire on a client-named instance whose name IS
	// statically computable. Its name_prefix'd sibling fixture
	// (slot-clientnamed-config) resolves NEEDS_DISCOVERY instead; see that
	// class's own count below.
	// 811 -> 815 for the provider-configuration dependency-order wall
	// (issue #313), rebased onto the #372 base directly above rather than
	// measured against a stale 809: module-output-hop/child's and
	// provider-config-demand/child's aws_eks_cluster.this, each a plain
	// literal name argument, each swept twice for the same reason the row
	// above is - once from its own child module directory, once as its
	// parent's module.child.aws_eks_cluster.this. See
	// identityGoldenPinInstances's own note.
	//
	// 819 -> 822 for issue #391's own two new fixtures: provider-config-
	// demand-sibling-output's own aws_eks_cluster.this, swept twice the
	// same way the row above is (once from its own child directory, once
	// as module.child.aws_eks_cluster.this from the parent), plus record-
	// parent-derived's aws_cloudwatch_log_group.app. See
	// identityGoldenPinInstances's own note.
	//
	// 822 -> 824 for corpus-eks-basic/test_plan's splat-visibility unit
	// (issue #396): provider-config-demand-splat's own aws_eks_cluster.
	// this[0] (a count-expanded, statically-computable "prod-cluster"),
	// swept twice the same way the two rows above are. See
	// identityGoldenPinInstances's own note.
	//
	// 824 -> 826 for issue #399's maintainer ruling (2026-08-24):
	// aws_lb_target_group_attachment's port component becomes
	// [identity.Component.OmitIfAbsent], the same mechanism its own
	// availability_zone and quic_server_id components already use, never a
	// type-specific branch. Two ADDED rows, both in the new fixture
	// internal/live/identity/testdata/target-group-attachment-lambda-port:
	// .lambda (port evaluates to a clean null, the shape
	// terraform-aws-modules/terraform-aws-alb's own local.lambda_target_
	// groups writes for a real Lambda target - botocore's elbv2 model
	// documents port as not applying to that target type at all) renders
	// the two-field target_group_arn/target_id form with no port segment
	// and no dangling separator; .instance (port present and non-null, the
	// ordinary shape) renders the three-field form, byte-identical to what
	// this row already produced before the ruling - the mutation boundary
	// the fix must not cost. No pre-existing row moved: every other
	// CONCRETE row in the golden, including target-group-attachment-
	// optional's own base/with_az/with_quic/aliasBase (port always present
	// there), is byte-identical; see the digest.
	//
	// The row's own leading-separator shape moved too, in the same commit:
	// the "," between target_id and port used to be a standalone bare-
	// literal component (always emitted, because port was always
	// required), which the ruling's own probe caught as a real defect the
	// instant port became omittable - a lambda attachment rendered
	// "...,function:my-function," with a trailing comma, exactly the
	// wrong-marker shape HANDOFF's safety rule forbids, not a refusal a
	// human would ever approve. Moving the "," onto port's own component
	// (identical to how availability_zone and quic_server_id already carry
	// theirs) fixes it structurally, for every OmitIfAbsent component this
	// row has or ever gains, not by naming this one type in control flow.
	// See TestTargetGroupAttachmentPortOmitIfAbsent and its own mutation
	// check (internal/live/identity/targetgroupattachment_omitifabsent_
	// test.go) plus TestComponentsFromValuePortNullOmits (valuecomponents_
	// test.go), which replaces the stale TestComponentsFromValuePortNull
	// IsNotFound that pinned the pre-ruling refusal.
	//
	// 826 -> 828 for gauntlet:sweep-moved-alias (recordOrphanReadSweep now
	// consults moved.Aliases/moved.Honoured before classifying a record's
	// address as an orphan): two ADDED CONCRETE rows, both
	// aws_iam_role_policy.inline resolving to "app:deploy", in the two new
	// fixtures internal/live/discovery/testdata/moved-record-located and
	// .../moved-record-located-nomoved (the fix's own positive and
	// mutation-check fixtures). No pre-existing row moved; see the digest.
	//
	// 828 -> 830 for gauntlet:destroy-order (moved.Newest, the forward
	// mirror of moved.Aliases/Origins): two ADDED CONCRETE rows,
	// aws_s3_bucket.x and aws_s3_bucket.y resolving to "x" and "y", in the
	// new fixture internal/live/moved/testdata/fork
	// (TestNewestRefusesAnAmbiguousFork's own two-endpoints-disagree
	// fixture - the sweep admits its two plain resource blocks the same
	// way it admits every other testdata root, independent of what the
	// fork's own moved blocks refuse to resolve). No pre-existing row
	// moved; see the digest.
	//
	// 830 -> 828 for issue #554's identity fixture fix:
	// aws_cognito_identity_pool_roles_attachment.app's identity_pool_id
	// stops being a hardcoded placeholder (CONCRETE) and becomes a
	// reference to the sibling aws_cognito_identity_pool.app.id
	// (PARENT_DERIVED) - the bug the fix corrects, since the placeholder
	// was shape-correct but fake, not a real live identity. Two rows move
	// CONCRETE -> PARENT_DERIVED (this one and
	// aws_cognito_identity_pool_provider_principal_tag.app, which chains
	// off it); see the PARENT_DERIVED note and the digest.
	//
	// 828 -> 877 for GitHub issue #580's module-boundary fix: 49 ADDED
	// rows, all of them aws_iam_role instances in the six new fixtures
	// internal/live/lint/testdata/count-index-module-foreach*. CONCRETE
	// rising is the shape a fabricated identity also has, so the values
	// were read rather than counted: the admitted fixture renders
	// "tl-pod-a-team-0000-role" through "tl-pod-b-team-0003-role", which is
	// what stock OpenTofu names the same eight objects, and the two
	// fixtures whose module call passes every instance one prefix render
	// two identical values under different addresses on purpose - that
	// collision is what internal/live/identity refuses, and pinning it here
	// is how a later change that stops refusing it becomes visible.
	// 883, up from 877 (GitHub issue #585's read-path concurrency): 6 ADDED
	// rows, all in the one new fixture
	// internal/live/projection/testdata/read-parallel, rendering
	// "read-parallel-0" through "read-parallel-5" - the literal each
	// block's own `name` argument declares. Six blocks of one client-named
	// type is what the read pass's concurrency guards need; no existing row
	// moved.
	"CONCRETE": 883,

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
	// 636, up from 630 (issue #324 item 2, splat.go's resolveConcatIndex):
	// six new server-assigned aws_security_group instances across three
	// fixtures - concat-splat-index-out-of-range's a[0], concat-splat-
	// index-second-arg's a[0]/b[0]/b[1], concat-splat-index-security-
	// group's this[0], and concat-splat-index-unrecognized-arg's a[0] -
	// all NEEDS_DISCOVERY like every other bare aws_security_group in this
	// golden. See identityGoldenPinInstances' own comment for the fixtures
	// and the PARENT_DERIVED rows the same fix adds.
	// 652, up from 636 (issue #324 item 1, splat.go's
	// resolveElementCoalescelist): sixteen new server-assigned instances
	// across three fixtures - coalescelist-element-first-arg-wins'
	// aws_route_table.database[0..2], aws_route_table.private[0..2] and
	// aws_subnet.database[0..2] (9), and coalescelist-element-second-arg-
	// wraparound's aws_route_table.private[0..1] and
	// aws_subnet.database[0..4] (7) - all NEEDS_DISCOVERY like every other
	// bare aws_route_table/aws_subnet in this golden. See
	// identityGoldenPinInstances' own comment for the fixtures and the
	// PARENT_DERIVED rows the same fix adds.
	// 654, up from 652 (module output read inside a module-CALL argument):
	// two ADDED rows, both aws_vpc.this[0] in the new
	// internal/live/identity/testdata/module-output-in-call-arg fixture -
	// once as module.vpc.aws_vpc.this[0] from the fixture root, once as
	// aws_vpc.this[0] with the child directory swept as a root of its own.
	// Server-assigned like every other bare aws_vpc in this golden, and
	// unrelated to the change: the fixture needed a real managed resource
	// for its adversarial outputs to read.
	// 656, up from 654 (issue #325's discovery double-claim fix): two ADDED
	// rows, aws_default_security_group.default and aws_security_group.other
	// in the new internal/live/discovery/testdata/default-adopter-dup
	// fixture - a config declaring both sides of a default-adopter pair,
	// the regression case claimantAlreadyPresent guards. Both bare-marker
	// NEEDS_DISCOVERY like every other resource of these types in this
	// golden.
	// 658, up from 656 (issue #302's role/service-linked-role sibling fix):
	// two ADDED rows, aws_iam_role.other and aws_iam_service_linked_role.app
	// in the new internal/live/discovery/testdata/iam-service-linked-role-
	// sibling fixture - a config declaring both an ordinary aws_iam_role
	// and an aws_iam_service_linked_role, the regression case
	// iamServiceLinkedRoleSibling guards. Both bare-marker NEEDS_DISCOVERY
	// like every other resource of these types in this golden; the fix's
	// ARN-based import ID is a discovery-time correction, not a static
	// identity change, so neither row's rendered class or identity moves.
	//
	// 660, up from 658 (issue #330, the count-keyed-module moved-block fix):
	// two ADDED rows, module.counted[0].aws_sqs_queue.doi and
	// module.counted[0].aws_sqs_queue.stray, in
	// internal/live/moved/testdata/estate/main.tf - a new module "counted"
	// (source ./modules/queues, count = 1) added so
	// TestOriginsCoversEveryCorpusShape could exercise a moved block whose
	// destination passes through a count-keyed MODULE instance, the shape
	// Honourable's own hasCountKeyedModuleStep case used to refuse on a
	// premise issue #195 already retired. Both bare-marker NEEDS_DISCOVERY,
	// aws_sqs_queue being server-assigned with no client-supplied identity,
	// the same class every other bare aws_sqs_queue in this golden already
	// carries (module.queues.aws_sqs_queue.doi/.stray, two rows above). Not
	// a moved row - no pre-existing CONCRETE/NEEDS_DISCOVERY row's rendered
	// value changed; see the digest below.
	// 663, up from 660 (issue #346): three ADDED rows, all bare aws_vpc in
	// the new module-output-sibling-computed fixture - aws_vpc.root and
	// module.vpc.aws_vpc.this[0] from the fixture root, and aws_vpc.this[0]
	// again with the child directory swept as a root of its own. Server-
	// assigned like every other bare aws_vpc in this golden, and incidental
	// to the change: the fixture needs a real managed resource in the root
	// module to make an element expression unevaluable as a value, which is
	// what binds each.value as an EXPRESSION and puts the argument on the
	// route the issue is about.
	// 664, up from 663 (issue #354): one ADDED row,
	// module.alb.aws_lb_target_group.this["ex_asg"] in the new
	// module-output-whole-resource fixture. Server-assigned like every other
	// bare aws_lb_target_group, and incidental to the change: the fixture
	// needs a real managed resource behind the module output for the deferred
	// read to have something to point at.
	// 671, up from 664 (issue #368): seven ADDED rows, every one of them a
	// bare server-assigned aws_vpc, aws_security_group or aws_ecs_service
	// in the two new fixtures - three in formula-transform (aws_vpc.this[0],
	// aws_security_group.this[0], module.svc.aws_ecs_service.this[0]) and
	// four in deferred-through-module-list (module.vpc.aws_vpc.this[0],
	// module.sg.aws_security_group.this[0], and the same two again with the
	// child directories swept as roots of their own). Incidental for the
	// same reason as #346's and #354's: a deferred read needs a real
	// resource to point at.
	// 684, up from 671 (issue #365 slice 2): thirteen ADDED rows, every one
	// of them a bare server-assigned aws_vpc, aws_subnet or aws_ebs_volume
	// in the thirteen new markers "record" fixtures. NEEDS_DISCOVERY rather
	// than RECORD_LOCATED is the fact this row records and it is the one
	// worth reading: this sweep runs WITHOUT provider schemas, and
	// identity.SelectedLocatedRefusal's remaining conditions are schema
	// reads, so the selection is not honoured here and every selected
	// resource resolves through its ordinary route and keeps its marker.
	// That is the deliberate direction - a predicate that cannot run must
	// not admit - and it is asserted directly, with schemas, by
	// internal/live/check's TestStrictMarkersRecordRendersItsIdentityByValue
	// and TestStrictMarkersRecordFailsClosedWithNoSchemas.
	// 686, up from 684 (issue #375): the bare aws_subnet each of the two new
	// fixtures declares as the thing its poisoned leaf reads.
	// 690, up from 686 (the corpus-rds-complete-postgres routing fix,
	// internal/live/identity/computedselect.go): four ADDED rows, every one
	// of them a bare server-assigned aws_vpc or aws_security_group that the
	// controls added to
	// internal/live/identity/testdata/deferred-through-module-list need in
	// order to point at something - a second aws_vpc.other, so that an
	// uncomputable lookup() fallback is a different object from the leaf the
	// caller wrote, and the new sgtyped module's own aws_security_group -
	// each counted twice because the child directory is swept as a root of
	// its own. Incidental for the same reason as #346's, #354's, #368's and
	// #375's: a deferred read needs a real resource to point at.
	// 700, up from 690 (corpus-eks-basic's count-index wall,
	// internal/live/lint/sibling_select.go): ten ADDED rows, every one of
	// them a bare server-assigned aws_subnet or aws_route_table declared by
	// the three new lint fixtures so that the aws_route_table_association
	// instances in them have parents to SELECT - four in
	// count-index-sibling-select, four in
	// count-index-sibling-select-indexed, two in
	// count-index-sibling-select-collision. Incidental for the same reason
	// as #375's and the corpus-rds-complete-postgres routing fix's: a
	// selection needs real resources to select from.
	// 700 -> 703 for corpus-sumaform-aws's static count() wall: the three
	// aws_subnet blocks the new fixture declares, which are
	// server-assigned and resolve exactly as every other subnet does.
	// 703 -> 705 for corpus-alb-complete's Family A fix: the new fixture's
	// two aws_iam_policy instances (imagebuilder, other), whose arn is what
	// every poisoned leaf in the fixture reads and which nothing here can
	// discover without the cloud.
	// 705 -> 706 for [RecordFallbackType]: one ADDED row,
	// internal/live/identity/testdata/record-fallback-untaggable's
	// aws_autoscaling_group.prefixed. This sweep resolves with no provider
	// schemas, so [RecordFallbackType] fails closed exactly as it is
	// documented to - the row stays NEEDS_DISCOVERY here, and only becomes
	// RECORD_LOCATED in a run holding a real schema and a declared
	// record_store (see TestRecordFallbackClassifiesUntaggableNamePrefix in
	// internal/live/identity/recordfallback_test.go for that assertion, by
	// value).
	// 706 -> 708 for GitHub issue #372's remainder: two ADDED rows,
	// internal/live/liveimport/testdata/slot-clientnamed-config's
	// aws_iam_role.this[0] and .this[1] - a count-expanded, client-named
	// type named through name_prefix, resolving NEEDS_DISCOVERY
	// (DiscoveryNameOmitted; aws_iam_role's component is
	// ServerAssignedIfAbsent, checked ahead of the name_prefix branch in
	// resolve.go's identityArgs) rather than the CONCRETE its literal-named
	// sibling fixture gets. See "CONCRETE"'s own note on that sibling.
	// 708 -> 710, same issue, a second pass: two ADDED rows,
	// internal/live/liveimport/testdata/slot-markerfallback-config's
	// aws_iam_role.this[0] and .this[1] - named through uuid(), an impure
	// function, so resolution is NEEDS_DISCOVERY/DiscoveryMarkerFallback
	// rather than DiscoveryNameOmitted. This is the fixture for
	// causeStableWithoutManagedResults's exclusion list, written after
	// measuring that a bare resolve's MARKER_FALLBACK verdict is not always
	// what a real live-plan's two-pass resolution settles on - see that
	// function's doc comment in internal/live/liveimport/slot.go.
	// 710 -> 711 for the provider-configuration dependency-order wall
	// (issue #313), rebased onto the #372 base directly above rather than
	// measured against a stale 706: managed-projection-live's
	// aws_instance.web, the same "needs a real account" shape every sibling
	// managed-projection-* fixture's own aws_instance already contributes.
	// 711 -> 715 for alb family B on top of #313 and #384: the four new
	// managed-read-* rows (managed-read-ambiguous-local,
	// managed-read-count-local and managed-read-count-module's two swept
	// directories), each an aws_acm_certificate whose identity needs a
	// real account, the same NEEDS_DISCOVERY shape as every sibling
	// managed-read-* fixture already contributes.
	// 715 -> 717 for GitHub issue #380 (strict { markers "record" }
	// synthesizes per-key ignore_changes instead of dropping an existing
	// marker): two ADDED rows, aws_vpc.main and aws_subnet.private, from the
	// new lint fixture testdata/strict-markers-ignore-changes-per-key. Both
	// are the same plain server-assigned NEEDS_DISCOVERY shape their
	// siblings strict-markers-ignore-changes and
	// strict-markers-ignore-changes-no-repair already contribute - this
	// sweep runs with no schemas, so the markers "record" selection is never
	// honoured here and neither resource renders as RECORD_LOCATED. See
	// identityGoldenPinBodyDigest's own note.
	// 717 -> 718 for GitHub issue #394: one ADDED row,
	// internal/live/discovery/testdata/default-adopter-sweep-orphan's
	// aws_sns_topic.unrelated, the same plain server-assigned
	// NEEDS_DISCOVERY shape every sibling aws_sns_topic fixture already
	// contributes.
	// 718 -> 720 for issue #391's own new fixture provider-config-demand-
	// sibling-output: aws_instance.other, swept twice the same way its
	// aws_eks_cluster.this sibling is (once from its own child directory,
	// once as module.child.aws_instance.other from the parent) - a plain
	// server-assigned NEEDS_DISCOVERY shape, deliberately never covered by
	// this fixture's own LiveManagedResults so it stays permanently
	// unreadable, which is the whole point of the fixture.
	//
	//
	// 720 -> 723 for the corpus-alb-complete/test_plan unit that fixed
	// [namesAModuleOutput]'s crosstalk (internal/live/identity's
	// managedprovenance.go): three ADDED rows, all from the new fixture
	// testdata/managed-read-module-blind-crosstalk -
	// aws_cognito_user_pool.this, module.wildcard_cert.aws_acm_certificate.
	// this[0], and modules/wildcard_cert's own aws_acm_certificate.this[0].
	// All three are the plain server-assigned NEEDS_DISCOVERY shape every
	// sibling ACM/Cognito fixture in this golden already contributes; the
	// fixture's fourth resource,
	// module.alb.aws_lb_listener_certificate.this["https/0"], is the one
	// this unit's fix concerns and does not appear here at all - offline,
	// with no managed results supplied, it declines with the ordinary
	// "Non-static identity argument" refusal, exactly as it did before the
	// fix (this golden sweeps with no [Context.ManagedResults], so the
	// bug's own trigger - a module-output leg and a direct leg racing under
	// real managed results - never fires in this sweep at all). "0
	// identities changed, 3 added, 0 removed" confirmed by diffing
	// testdata/identity-golden.txt before and after regenerating.
	//
	// 723 -> 725 for corpus-eks-basic/test_plan's own moduleOutputLookup
	// dependency-scoping unit (issue #391 continued): the SAME fixture's
	// third addition, aws_instance.gatekeeper (named in depends_on by the
	// new data.aws_zone.poison, so a third sibling output can carry a
	// dependency that always refuses classification, deliberately unrelated
	// to cluster_id), swept twice for the same own-directory-plus-parent
	// reason.
	//
	// 725 -> 728 for the corpus-alb-complete/test_plan unit continuing
	// gauntlet issue #397 (internal/live/identity's localvalue.go gains a
	// `values(X)` case in staticCollElems): three ADDED rows, all from the
	// new fixture testdata/values-splat-per-element -
	// aws_cognito_user_pool.this, module.wildcard_cert.aws_acm_certificate.
	// this[0], and modules/wildcard_cert's own aws_acm_certificate.this[0].
	// All three are the plain server-assigned NEEDS_DISCOVERY shape every
	// sibling ACM/Cognito fixture in this golden already contributes
	// (including testdata/managed-read-module-blind-crosstalk's own three,
	// added above); the fixture's fourth resource,
	// aws_lb_listener_certificate.this["https/0"], is the one this unit's
	// fix concerns and does not appear here at all - offline, with no
	// managed results supplied, it declines with the ordinary "Non-static
	// identity argument" refusal, exactly as it did before the fix (this
	// golden sweeps with no [Context.ManagedResults], so the values()/
	// merge() flatten this fixture exercises never produces the unknown
	// this unit's fix is about). "0 identities changed, 3 added, 0 removed"
	// confirmed by diffing testdata/identity-golden.txt before and after
	// regenerating.
	//
	// 728 -> 731 for the corpus-alb-complete/test_plan unit landing gauntlet
	// issue #397's two remaining blockers (a for-expression NESTED inside
	// another, reading the outer loop variable, and a filter clause decided
	// from the element's own rebuilt skeleton): three ADDED rows, all from
	// the new fixture testdata/nested-for-scope-per-element, and all three
	// the same plain server-assigned NEEDS_DISCOVERY shape its sibling
	// fixtures values-splat-per-element and managed-read-module-blind-
	// crosstalk already contribute - aws_cognito_user_pool.this,
	// module.wildcard_cert.aws_acm_certificate.this[0], and
	// modules/wildcard_cert's own aws_acm_certificate.this[0]. The two
	// instances this unit's fix actually concerns,
	// module.alb.aws_lb_listener_certificate.this["https/0"] and
	// ["cognito/0"], do not appear here at all, for the same reason the
	// values-splat note above gives: this golden sweeps with no
	// [Context.ManagedResults], so nothing in the fixture is ever unknown
	// and the ordinary "Non-static identity argument" refusal stands offline
	// exactly as it did before. "0 identities changed, 3 added, 0 removed"
	// across all 615 pre-existing directories, confirmed by running the
	// golden BEFORE regenerating.
	//
	// Then 731 -> 733 for the same unit's third fix (the record rung reached
	// through [resolver.siblingApplyResolution]'s own door): two ADDED rows
	// from the new fixture testdata/record-fallback-sibling-apply,
	// aws_acm_certificate.this and aws_s3_bucket.logs, both the plain
	// server-assigned shape. The fixture's THIRD resource,
	// aws_route53_record.validation, is the one the fix concerns and does
	// not appear: this golden supplies no [Context.ManagedResults], so the
	// certificate's domain_validation_options is never unknown, the
	// sibling-apply branch is never entered, and the instance refuses
	// offline exactly as it did before. Asserted by class instead, in both
	// directions, in TestRecordFallbackClassifiesSiblingApplyUntaggable.
	// 733 -> 735 for [gauntlet:corpus-dynamodb-table-basic/day2_remove]: two ADDED
	// rows, aws_dynamodb_table.this in each of the new fixtures
	// internal/live/identity/testdata/parent-derived-parent-attr and
	// .../parent-derived-parent-attr-unknown. Both fixtures' table reads its own
	// `name` from a record-backed random_pet sibling; this golden sweep supplies no
	// schemas, so resolver.parentPart's record-backed branch cannot confirm the
	// formula and the resolution downgrades to NEEDS_DISCOVERY rather than erroring,
	// the same graceful path any other unresolvable-offline config-identified
	// instance takes.
	//
	// 736, up from 735 ([gauntlet:reference-ec2-vpc/greenfield]):
	// internal/live/discovery/testdata/propagated-child-marker's
	// aws_instance.main, a bare server-assigned EC2 instance - reference-ec2-vpc's
	// own greenfield shape - which resolves offline to no identity at all and so
	// defers to the live read, the way every other unmarked server-assigned
	// instance in this file does.
	//
	// 746, up from 736 (GitHub issue #415's collision-outcome matrix): ten
	// ADDED rows in the new fixture
	// internal/live/discovery/testdata/collision-matrix, one per resource
	// block the matrix collides (aws_vpc.scalar_server, aws_sns_topic's
	// scalar/count/for_each variants, aws_eip.count_server[0..1] and
	// aws_subnet.foreach_server["a"/"b"]) - all NEEDS_DISCOVERY by
	// construction (see TestCollisionMatrixFixtureNeedsDiscovery in
	// internal/live/discovery/collisionmatrix_test.go), confirmed by `git
	// diff internal/live/check/testdata/identity-golden.txt` showing
	// exactly those ten lines added and nothing else changed.
	//
	// 747, up from 746 (issue #541's deterministic-identity fixture): one
	// ADDED row, aws_iam_policy.subject in the new fixture
	// live/e2e/deterministic-recreate. aws_iam_policy is ServerAssigned in
	// internal/live/identity/table_generated.go (the provider mints the
	// ARN and hands it back rather than this fork assembling it
	// component-by-component), so the static sweep defers to a live read
	// even though the e2e fixture's own run.sh proves that ARN is in fact
	// deterministic from account+name+path across a real destroy-and-
	// recreate - NEEDS_DISCOVERY here describes what the OFFLINE golden
	// sweep can resolve without a cloud in the loop, not whether the
	// identity is stable once one is. Confirmed by `git diff
	// internal/live/check/testdata/identity-golden.txt` showing exactly
	// that one line added and nothing else changed.
	//
	// 747 -> 748 for issue #554's ecs-eks fixture fix: one ADDED row,
	// aws_ecs_task_definition.ecs-eks, the new supporting resource
	// aws_ecs_service.app's own task_definition argument now references.
	// aws_ecs_task_definition is ServerAssigned (family+revision, minted
	// by ECS at create time), so it resolves NEEDS_DISCOVERY like every
	// other server-assigned row.
	//
	// 748 -> 749 for GitHub issue #790 (live-check -json's declared roster
	// and references[]): one ADDED row, aws_subnet.app in the new fixture
	// live/e2e/estate-references (see that directory's own README for what
	// it is for - the smallest configuration exercising live/OUTPUTS.md's
	// cross-estate data-source pattern). aws_subnet is server-assigned with
	// no client-supplied identity, the same NEEDS_DISCOVERY shape every
	// other bare aws_subnet in this golden already carries. `git diff
	// internal/live/check/testdata/identity-golden.txt` reads "0 identities
	// changed, 1 added, 0 removed" - the fixture's own data source,
	// data.aws_vpc.network, contributes no row at all, since this golden
	// sweeps managed resource identities only.
	"NEEDS_DISCOVERY": 749,

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
	// 107, up from 105 (issue #324 item 2, splat.go's resolveConcatIndex):
	// two new rows, concat-splat-index-second-arg's and concat-splat-
	// index-security-group's aws_security_group_rule.ingress, resolving
	// concat(A[*].id, B[*].id, [literal])[N] reached through a local value
	// - security-group's formula reads
	// ${aws_security_group.this[0].id}_ingress_tcp_80_80_0.0.0.0/0 (index
	// 0 lands on the first splat's own single instance, the second splat
	// contributing zero elements); second-arg's reads
	// ${aws_security_group.b[1].id}_ingress_tcp_80_80_0.0.0.0/0 (index 2
	// lands on the SECOND splat's second element, proving the cumulative-
	// length offset arithmetic across two non-empty splats). See
	// identityGoldenPinInstances' own comment for the fixtures.
	// 115, up from 107 (issue #324 item 1, splat.go's
	// resolveElementCoalescelist): eight new rows resolving
	// element(coalescelist(A[*].attr, B[*].attr), idx) - three in
	// coalescelist-element-first-arg-wins (database provably non-empty,
	// so coalescelist() selects it over private; formula reads
	// ${aws_subnet.database[i].id}/${aws_route_table.database[i].id})
	// and five in coalescelist-element-second-arg-wraparound (database
	// provably expands to zero instances, so coalescelist() selects
	// private instead, and element()'s own wraparound then applies to
	// PRIVATE's own 2-instance length against a 5-instance block; formula
	// reads ${aws_subnet.database[i].id}/${aws_route_table.private[i%2].id}).
	// See identityGoldenPinInstances' own comment for the fixtures.
	// 116, up from 115 (issue #354): one ADDED row, and it is the fix itself -
	// module.asg.aws_autoscaling_traffic_source_attachment.this["ex-alb"]
	// renders asg-fixed,elbv2,${module.alb.aws_lb_target_group.this["ex_asg"].arn}.
	// The value in that row is the whole assertion: the formula has to name
	// the target group INSTANCE the caller indexed, and three other strings
	// would have satisfied a class check - the group's own name, the module
	// output's name, and the type default that supplies the "elbv2" beside it.
	// 118, up from 116 (issue #375): module-arg-hoisted's inline/hoisted
	// aws_iam_role.derived[0] pair, both rendering the symbolic formula
	// derived-${aws_subnet.s.id}. They are the negative half of the fix -
	// the leaf that really is a live subnet ID stays a parent's value and
	// never becomes a concrete marker - and they are byte-identical to each
	// other, which is the equivalence the fix claims.
	// 119, up from 118 (the corpus-rds-complete-postgres routing fix): one
	// ADDED row, and it is #375's OWN control - merge-bare-module-output's
	// module.base.module.host.aws_iam_role.poisoned[0], whose name reads the
	// one member of the merged map that is a live subnet ID. It now renders
	// the symbolic formula role-${aws_subnet.public.id}, which is what that
	// control is about rather than an exception to it: a formula names the
	// exact parent instance and attribute, renders off the LIVE object, and
	// carries an EMPTY import ID until that object is read. #375's own
	// assertion - moduleargspelling_test.go, "id must be empty" - is
	// unchanged and still passes; a CONCRETE row there would be the failure.
	// 128, up from 119 (corpus-eks-basic's count-index wall,
	// internal/live/lint/sibling_select.go): nine ADDED rows, three per new
	// lint fixture, and they are the whole point of the change - the
	// aws_route_table_association instances that terraform-aws-modules/vpc
	// builds with element(<sibling splat>, count.index) and that
	// RuleCountIndex refused before ever reaching resolution. Six of them
	// are the SAME three identities twice over, once per spelling
	// (element(R[*].attr, idx) and R[idx].attr), which is the claim those
	// two fixtures make; the other three are deliberately identical to each
	// other, in count-index-sibling-select-collision, and that fixture's
	// whole job is to be refused by [resolver.checkCollisions] rather than
	// by the lint rule. Read the values: the first six carry a route table
	// that is the SAME instance for all three and a subnet that differs, so
	// the pairs are distinct; the last three are byte-identical, which is
	// what a collapse looks like when it really is one.
	// 128 -> 131 for corpus-sumaform-aws's static count() wall: the three
	// resources whose names read a SUBSTITUTED member of the tolerated
	// value. Their rows carry the reference unrendered, which is the
	// adversarial half of that change.
	// 131 -> 135 for corpus-alb-complete's Family A fix: the new fixture's
	// two aws_iam_role_policy_attachment.this instances (a plain each.value
	// selection over the poisoned element, once the for-expression filter
	// that used to refuse the whole comprehension can decide) and its two
	// aws_iam_role_policy_attachment.byindex instances (an indexed reference
	// into a sibling resource, where the index itself is a plain-literal
	// attribute of the same poisoned element). See identityGoldenPinBodyDigest.
	//
	// 135 -> 137 for issue #554's identity fixture fix: two rows move
	// CONCRETE -> PARENT_DERIVED (see the CONCRETE note directly above) -
	// aws_cognito_identity_pool_roles_attachment.app's own identity_pool_id
	// now reads ${aws_cognito_identity_pool.app.id}, and
	// aws_cognito_identity_pool_provider_principal_tag.app's identity_pool_id
	// (already a reference to the roles_attachment row's own identity_pool_id
	// via gen.go's parentRef) renders that same chain one level further, so
	// its own rendered identity picks up the new interpolation too.
	"PARENT_DERIVED": 137,

	// 2026-08-19 (issue #314): 17 -> 19. The two local_file fixtures that
	// already existed - internal/live/lint/testdata/logical and
	// live/e2e/limits/local-file - contributed a directory each and no
	// instance line at all while the type had no identity.DefaultTable row.
	// It has one now, RecordBacked, so both render. Both values are EMPTY:
	// hashicorp/local 2.9.0 implements no ImportState for local_file, so
	// there is no import identity to render and a record is the only thing
	// that can bring the instance's prior state back. Nothing else in the
	// class moved.
	// 21, up from 19 (issue #336): one random_pet.suffix from each of the
	// two new coalesce fixtures' root modules, both rendering an empty
	// value - the honest answer for a resource whose whole object lives in
	// the estate's record store and nowhere else.
	// 24, up from 21 (GitHub issue #365 slice 3): three ADDED rows, all with
	// an EMPTY value, and the reason they are here at all is the whole of
	// the slice. random_password, local_sensitive_file and tls_private_key
	// carry secret material in their schemas, and until this slice that made
	// them absent from internal/live/identity.DefaultTable - which said the
	// record COULD NOT hold their prior state, when what was actually true
	// is that it should not hold it unless the operator asked. They now
	// carry a RecordBacked row with SecretMaterial set, resolve
	// RECORD_BACKED under the default `strict { secrets = "store" }`, and
	// are refused by name under `secrets = "refuse"`.
	//
	// The empty value is the honest one and is worth reading rather than
	// skipping past: a record-backed resource has no cloud object and
	// therefore no rendered identity to write into a tag, so nothing about
	// these three rows is a marker this tool will put anywhere. The value
	// that DOES move is the record's contents, and that is asserted by
	// value in internal/live/projection's residue tests and
	// internal/live/lint's TestSecretsStoreAdmitsASecretGeneratingType,
	// neither of which this sweep can reach.
	//
	// The three fixtures: internal/live/lint/testdata/logical (which already
	// declared tls_private_key.signing and contributed no row for it),
	// live/e2e/limits/local-sensitive-file and live/e2e/limits/
	// random-password (both of which already existed as limits fixtures and
	// stay refused there, for want of a record_store rather than for want of
	// admission).
	//
	// 24 -> 25 for issue #391's own new fixture record-parent-derived:
	// null_resource.suffix, a logical resource with no live object at all,
	// standing in for corpus-eks-basic's random_string.suffix - the
	// record-backed parent a sibling ClassParentDerived formula (built by
	// hand in the test, not derived from this fixture) names.
	//
	// 25 -> 27 for [gauntlet:corpus-dynamodb-table-basic/day2_remove]: two ADDED
	// rows, random_pet.suffix in each of the same two new fixtures
	// (parent-derived-parent-attr and parent-derived-parent-attr-unknown) - the
	// record-backed grandparent aws_dynamodb_table.this's name formula reads.
	"RECORD_BACKED": 27,
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
// 2026-08-19 (issue #324 item 2, splat.go's resolveConcatIndex): body
// digest moved because nine rows were ADDED (see the CONCRETE,
// NEEDS_DISCOVERY and PARENT_DERIVED class comments above and
// identityGoldenPinInstances' own comment below for the five fixtures);
// no pre-existing row's rendered value changed - TestIdentityGolden's own
// diff read "0 identities changed, 9 added, 0 removed" before this line
// was edited.
// 2026-08-19 (issue #324 item 1, splat.go's resolveElementCoalescelist):
// body digest moved because twenty-five rows were ADDED (see the CONCRETE,
// NEEDS_DISCOVERY and PARENT_DERIVED class comments above and
// identityGoldenPinInstances' own comment below for the three fixtures);
// no pre-existing row's rendered value changed - TestIdentityGolden's own
// diff read "0 identities changed, 25 added, 0 removed" before this line
// was edited.
// 2026-08-19 (issue #323, partialargs.go's tolerantPart): body digest
// moved because two rows were ADDED (see the CONCRETE class comment above
// and identityGoldenPinInstances' own comment below for the one fixture);
// no pre-existing row's rendered value changed - TestIdentityGolden's own
// diff read "0 identities changed, 2 added, 0 removed" before this line
// was edited. Read together with the corpus measurement that change was
// landed on: across all 250 offline-corpus entries it resolved not one
// new instance, so the two rows here are the ONLY new rendered identities
// it produces anywhere, and both are asserted by value in
// internal/live/identity's TestPartialModuleArgumentResolvesALiteralLeaf.
// 2026-08-19 (module output read inside a module-CALL argument,
// identity/moduleoutputvalue.go): body digest moved because three rows were
// ADDED (see the CONCRETE and NEEDS_DISCOVERY class comments above and
// identityGoldenPinInstances' own comment below for the one fixture); no
// pre-existing row's rendered value changed - TestIdentityGolden's own diff
// read "0 identities changed, 3 added, 0 removed" before this line was
// edited. Read together with the corpus measurement the change was landed
// on: across all 250 offline-corpus entries it moved nothing at all -
// sites 16165 -> 16165, instances 4394 -> 4394, blocked 194 -> 194, with
// the "Module output not supported in static context" class unchanged at
// 58 - because every one of those 58 sites reads an output defined as
// try(<managed resource attribute>, fallback), which this deliberately
// does not answer. The three rows here are therefore the ONLY new rendered
// identities it produces anywhere.
// 2026-08-19 (issue #302's role/service-linked-role sibling fix,
// iamServiceLinkedRoleSibling in internal/live/discovery/discovery.go):
// body digest moved because two rows were ADDED (see the NEEDS_DISCOVERY
// class comment above and identityGoldenPinInstances' own comment below for
// the one fixture); no pre-existing row's rendered value changed -
// TestIdentityGolden's own diff read "0 identities changed, 2 added, 0
// removed" before this line was edited.
//
// 2026-08-19 (issue #310, identity.Component gaining a Block field, merged
// on top of #302 above): body digest moved again because one more row was
// ADDED (see the CONCRETE class comment above and identityGoldenPinInstances'
// own comment below for the one fixture); no pre-existing row's rendered
// value changed.
//
// 2026-08-19 (issue #330, the count-keyed-module moved-block fix,
// internal/live/moved's Honourable, merged on top of #310 above): body
// digest moved again because two more rows were ADDED (see the
// NEEDS_DISCOVERY class comment above and identityGoldenPinInstances' own
// comment below for the one fixture); no pre-existing row's rendered value
// changed. Each of #302/#310/#330 independently regenerated the golden from
// a different ancestor, producing a real merge conflict in the data file
// itself at every step; resolved per this repository's standing rule by
// regenerating fresh against the fully merged code (-update) rather than
// hand-merging the diffs, then copying the regenerated body-sha256 here.
//
// 2026-08-19 (issue #191, a partial module argument composing across two
// module calls, merged on top of #330 above): body digest moved because six
// more rows were ADDED, all six in two new fixture roots (see the CONCRETE
// class comment above and identityGoldenPinInstances' own comment below).
// TestIdentityGolden's own diff, read before this line was edited, reported
// "0 identities changed, 6 added, 0 removed". That zero is the load-bearing
// half twice over here, because this change makes an EARLIER evaluation
// succeed where it used to fail, which is the exact shape that broke
// testdata/shapeb-tryref the first time this wrapper was written: a
// resolution that was concrete becoming a refusal, or a formula becoming an
// unknown, would show as a REMOVED or MODIFIED row and there are none of
// either. The same comparison was run over .corpus as well, which this
// sweep deliberately does not cover - 6154 directories, 22398 -> 22424
// instances, 0 rows removed, 0 rows modified, and all 26 added rows
// NEEDS_DISCOVERY with an empty rendered value.
// 2026-08-19 (issue #314, local_file's fourth LogicalClass): body digest
// moved because two more rows were ADDED, both local_file, both in fixture
// directories the sweep already walked. TestIdentityGolden's own diff, read
// before this line was edited, reported "0 identities changed, 2 added, 0
// removed". The zero is the load-bearing half: this change gives a type an
// identity row where it had none, and gives lint a class that admits it under
// a record_store, and neither of those can move an existing resource's
// rendered identity - a MODIFIED row here would have meant it had.
// 2026-08-20 (issue #326, kubernetes_config_map's ratified row): body digest
// moved because one more row was ADDED,
// internal/live/identity/testdata/kubernetes-config-map's
// kubernetes_config_map.present (see the CONCRETE class comment above and
// identityGoldenPinInstances' own comment below). TestIdentityGolden's own
// diff, read before this line was edited, reported "0 identities changed, 1
// added, 0 removed" - the load-bearing zero, since this is the first row
// any Kubernetes-provider type has ever contributed to this golden and a
// MODIFIED or REMOVED row here would have meant an existing marker moved.
// 2026-08-21 (issue #353, provisioners admitted under a record_store): body
// digest moved because five more rows were ADDED, all five from the one new
// fixture directory live/e2e/provisioner-taint (aws_s3_bucket app[0],
// control, shrinker[0], shrinker[1], tolerant - see the CONCRETE class
// comment above). TestIdentityGolden's own diff, read before this line was
// edited, reported "0 identities changed, 5 added, 0 removed". The zero is
// the load-bearing half twice over here: a provisioner block is not an
// identity argument, so admitting one must not move any existing marker,
// and the fix also writes a new kind of record - a MODIFIED row would have
// meant that record had somehow reached identity resolution.
//
// 2026-08-21 (issue #346): digest moved because twelve more rows were ADDED,
// every one of them from the single new fixture directory
// internal/live/identity/testdata/module-output-sibling-computed (and its two
// child module directories swept as roots of their own). TestIdentityGolden's
// own diff, read before this line was edited, reported "0 identities changed,
// 12 added, 0 removed" over 515 directories. The zero is the load-bearing half:
// #346 widens which parent classes a non-identity attribute may be deferred
// to, and adds a narrowing rule for one-element lists on the each.value route,
// and neither may change a marker any existing fixture already renders.
//
// 2026-08-21 (issue #354): digest moved because three more rows were ADDED,
// every one of them from the single new fixture directory
// internal/live/identity/testdata/module-output-whole-resource.
// TestIdentityGolden's own diff, read before this line was edited, reported
// "0 identities changed, 3 added, 0 removed" over 524 directories. The zero is
// again the load-bearing half: #354 layers an element's own EXPRESSION under
// the element VALUE that was already bound, consulted only where that value
// came back unknown, so nothing that renders a marker today can be re-routed
// by it.
//
// 2026-08-21 (issue #369): digest moved because one more row was ADDED,
// internal/live/identity/testdata/sole-element-from-value's new
// aws_security_group_rule.resolved_by_sibling (see identityGoldenPinInstances'
// own comment above). TestIdentityGolden's own diff, read before this line
// was edited, reported "0 identities changed, 1 added, 0 removed" over 524
// directories. The zero is the load-bearing half: firstApplicablePresent only
// ever demotes a Component.SoleElement alternation member that is a PROVEN
// zero-element list from "present" to "absent" so the search can try the
// next alternative - it never resolves or picks a value itself, so no
// existing fixture's rendered identity can move.
//
// 2026-08-21 (issue #368): digest moved because eight more rows were ADDED,
// every one of them from the two new fixture roots
// internal/live/identity/testdata/formula-transform and
// .../deferred-through-module-list (and their child modules swept as roots
// of their own). TestIdentityGolden's own diff, read before this line was
// edited, reported "0 identities changed, 8 added, 0 removed" over 530
// directories. The zero is the load-bearing half: a transform is only ever
// reached after every existing route has declined, and it declines in turn
// unless it finds a recognized pipeline over exactly one deferred parent
// read, so no expression that renders a marker today can be re-routed by it.
// 2026-08-21 (issue #365, slice 2): digest moved because thirteen more rows
// were ADDED, every one of them from the thirteen new markers "record"
// fixtures (internal/live/lint/testdata's selection matrix,
// internal/live/check/testdata's two by-value fixtures, and one child module
// swept as a root of its own). TestIdentityGolden's own diff, read before
// this line was edited, reported "0 identities changed, 13 added, 0 removed"
// over 550 directories.
//
// The zero is the load-bearing half, and here it is guaranteed twice over.
// The selected route is consulted only after the automatic located route and
// only for a resource the configuration's own strict block names, so no
// fixture without such a block can be re-routed by it - and this sweep holds
// no provider schemas, so identity.SelectedLocatedType fails closed and the
// route does not fire even in the fixtures that DO name resources. The
// selection's effect on a rendered identity is pinned by value elsewhere,
// with schemas: internal/live/check's
// TestStrictMarkersRecordRendersItsIdentityByValue.
// Then re-pinned for the corpus-rds-complete-postgres routing fix
// (internal/live/identity/computedselect.go): "0 identities changed, 5
// added, 0 removed" over 559 directories, read before this line was edited.
// Four of the five are bare server-assigned resources the new controls in
// testdata/deferred-through-module-list need to point at, each rendering an
// empty value; the fifth is #375's own poisoned control, described under
// PARENT_DERIVED above.
//
// The zero is the half worth being suspicious of, because this change DOES
// widen a resolution route. It holds for the same reason #368's did: every
// identity the fold produces is a deferred parent read, and this sweep runs
// without provider schemas, so [resolver.parentPart]'s stringAttrInSchema
// gate turns away everything that is not already a declared identity
// attribute of the parent. The identities the fold renders WITH schemas are
// pinned by value, against a lookup that hands back a real CIDR, in
// internal/live/identity's deferred_through_module_list_test.go.
//
// Then re-pinned again for GitHub issue #378: one row added, the
// aws_s3_bucket in the new live/e2e/limits/reserved-symbol fixture (the
// lint rule reserving tofu.marker_module_prefix). #378's own change is to
// what internal/live/stamp writes into a tags argument, which this
// schema-less sweep does not render at all, so its whole effect here is the
// fixture it brought with it. "0 identities changed, 1 added, 0 removed"
// over 560 directories, read before this line was edited.
//
// Then re-pinned for GitHub issue #365 slice 3, reconciled onto the three
// changes above rather than measured against the base it was written on:
// three rows ADDED, all RECORD_BACKED and all rendering an EMPTY value
// (random_password, local_sensitive_file, tls_private_key gaining a
// SecretMaterial row in identity.DefaultTable), and four new configuration
// directories that declare no resource at all. Read against the merged tree,
// not against 350afb5925 - see identityGoldenPinInstances. The zero-changed
// half holds for the reason the slice's own design gives it: the setting
// reaches one decision, whether a SecretMaterial row is REFUSED, and never
// resolves any instance to a different object, so no row this sweep already
// rendered can move. Slice 3 and the three changes above touch disjoint
// routes - nothing in identity.DefaultTable is read by computedselect.go or
// by internal/live/stamp - so their row sets add rather than interact. The
// reconciled regeneration, diffed against 292bff5932's own golden rather
// than against the slice's original base, reports exactly that: three lines
// added (internal/live/lint/testdata/logical's tls_private_key.signing,
// live/e2e/limits/local-sensitive-file's local_sensitive_file.rendered and
// live/e2e/limits/random-password's random_password.db), none modified and
// none removed, over 564 directories.
//
// Then re-pinned again for corpus-eks-basic's count-index wall
// (internal/live/lint/sibling_select.go). "0 identities changed, 19 added, 0
// removed" over 563 directories (against ITS base, before this line was
// edited) - and the zero is structural here rather than lucky: the change is
// to what internal/live/lint refuses, and this sweep calls identity.Resolve
// directly without consulting lint at all, so no row it already held could
// move. What the added rows are FOR is that the values the lint rule now
// lets a real run reach are pinned by value somewhere, which is what
// internal/live/lint/sibling_select_test.go asserts directly and what these
// nineteen rows hold to the tree. Reconciled onto slice 3's own tally above
// rather than measured against this branch's original base: the two touch
// disjoint routes (lint's admission decisions vs. identity.DefaultTable),
// so their row sets add - 564 + 3 dirs = 567, 1632 + 19 instances = 1651 -
// confirmed by regenerating on the fully-merged tree rather than by adding
// the two deltas by hand.
//
// 2026-08-22 (corpus-sumaform-aws's static count() wall): dirs 567 -> 571,
// instances 1651 -> 1660, and the digest moved because rows were ADDED -
// "0 identities changed, 9 added, 0 removed", read off the -update run's own
// report and not inferred from the totals. The change is
// [configs.StaticEvaluator.WithUnknownForRefusedReferences], a tolerant
// static scope reached only through partialargs.go's own last-resort retry:
// a managed-resource or data-source reference inside a LOCAL of the module
// being read becomes an unknown instead of refusing the whole expression,
// and a child module's OUTPUT is answered by evaluating that child's outputs
// the same way. Nothing that already resolved could resolve differently -
// the strict evaluation still runs first and its answer is still used
// whenever it has one - and the zero changed rows are the evidence rather
// than the argument.
//
// The nine added rows are two fixtures. Two are
// internal/live/identity/testdata/module-arg-hoisted's "merged" module,
// whose boundary this moved on purpose: merge() written as the argument now
// resolves, because the call is RUN on a value with a substituted leaf
// rather than rebuilt, and its sibling `derived` - which reads that very
// leaf - stays PARENT_DERIVED with the reference unrendered, which is the
// half that says nothing was guessed. Seven are the new
// internal/live/identity/testdata/tolerant-module-output (four directories:
// the fixture, base, host, net; three of them contribute rows). Read their
// values: "sumaform-default-0" is spelled from the estate's own call
// through two merges and a module output, "eu-west-1a-0" only from inside
// module.net's output expression two calls away, and the two PARENT_DERIVED
// rows carry `derived-${aws_subnet.live.id}` and
// `profiled-${module.base.aws_subnet.inner.id}` unrendered - the substituted
// members, refused exactly where they should be.
//
// 2026-08-22 (corpus-alb-complete's Family A wall, GitHub issue #375's
// module-INPUT twin): dirs 571 -> 573, instances 1660 -> 1672, "0 identities
// changed, 12 added, 0 removed" read off the -update run's own report. The
// new fixture, internal/live/identity/testdata/module-foreach-forexpr-filter-sibling-value
// (its ./attach child swept as a root of its own too), is corpus-alb-complete's
// shape reduced: a module call argument's object literal has one poisoned
// leaf (a sibling resource's identity attribute) beside plain-literal
// siblings, and the child module's own for_each over that argument is
// FILTERED by a lookup() on the whole element - so the filter needed the
// poisoned element's whole value just as much as any each.value.<attr>
// selection inside the resource block does. Three widenings, all in
// internal/live/identity (localvalue.go, resolve.go), none naming a
// concrete aws_* type:
//
//   - resolver.forCondIncludesTolerant: a for-expression's own filter
//     clause (lookup(v, "key", default) or try(v.key, default), composed
//     with &&/||/! by ordinary three-valued/Kleene logic) falls back to the
//     same each.value absence proof (resolver.objectLacksKey) lookup()/
//     try() already use for a bare each.value.<attr> selection, instead of
//     refusing the WHOLE comprehension the moment v's value cannot be
//     proven.
//   - resolver.eachValueCondTolerant: a conditional's own condition
//     (an equality test, composed the same way) is resolved through the
//     ordinary resolveExpr entry point instead of refusing outright merely
//     because it reads each.value.<attr> at all - isSymbolic's each.value
//     case is blanket over the WHOLE element once one leaf is unprovable,
//     not over which attribute a reference selects.
//   - resolver.resolveIndexedTraversal: an indexed reference into a
//     DIFFERENT resource, where the index itself is each.value.<attr>, now
//     tries the same eachValueCondOperand fallback when its own strict
//     evaluation of the index fails.
//
// Every added row is either a bare server-assigned aws_iam_policy the
// poisoned leaves read (NEEDS_DISCOVERY, nothing here can discover it
// without the cloud) or a resolution one of the three widenings newly
// reaches (CONCRETE for a plain literal read through the now-decidable
// filter, PARENT_DERIVED for the two resources whose identity is composed
// with a sibling's). TestModuleForeachFilterOverPoisonedValueResolves
// (internal/live/identity) pins the same three shapes by value; this
// digest is the confirmation that nothing else in the fixture corpus moved.
//
// f8676745... -> 7903413d... for [RecordFallbackType]: two ADDED rows in
// the new internal/live/identity/testdata/record-fallback-untaggable -
// aws_autoscaling_group.named (CONCRETE, "web-static", proving the
// fallback does not shadow the ordinary literal-name path) and
// aws_autoscaling_group.prefixed (NEEDS_DISCOVERY here, since this sweep
// holds no provider schemas and the fallback fails closed without one).
// No pre-existing row moved, in class or in value.
//
// 2026-08-22 (GitHub issue #372's remainder): dirs 574 -> 576, instances
// 1674 -> 1678, "0 identities changed, 4 added, 0 removed" read off the
// -update run's own report. Two new root-module-only fixtures under
// internal/live/liveimport/testdata, written for
// TestApprove_WritesSlotForANamePrefixedClientNamedInstance and its negative
// control: slot-clientnamed-config (aws_iam_role.this[0..1], named through
// name_prefix, NEEDS_DISCOVERY) and slot-clientnamed-literal-config (the
// same shape named through a static literal instead, CONCRETE). Nothing in
// internal/live/identity changed; the four rows are new fixtures being swept
// for the first time, not an existing row moving.
//
// 2026-08-22, same issue, a second pass found by re-verifying the fix
// against corpus-ecs-fargate for real: dirs 576 -> 577, instances
// 1678 -> 1680, "0 identities changed, 2 added, 0 removed". One more new
// root-module-only fixture, slot-markerfallback-config
// (aws_iam_role.this[0..1], named through uuid(), NEEDS_DISCOVERY/
// DiscoveryMarkerFallback) - the fixture for
// causeStableWithoutManagedResults, added after the estate's real
// aws_ecs_service.this[0] proved a bare resolve's MARKER_FALLBACK verdict
// is not always the one a real live-plan settles on. See that function's
// doc comment.
//
// Both of the above are reconciled onto [RecordFallbackType]'s own base
// (574/1674, f8676745...->7903413d...) by rebasing rather than by
// recomputing deltas by hand: regenerated with -update on the merged tree
// and diffed, confirming exactly these six rows added and nothing else
// moved.
//
// Then c83a29c6... -> ff2bcef1... for issue #313 on top of #384, rebased
// onto main's actual current base (578/1680, c83a29c6...) rather than the
// provider-configuration branch's own original base: five added rows
// (managed-projection-live's aws_instance.web, and module-output-hop,
// module-output-hop/child, provider-config-demand and
// provider-config-demand/child's aws_eks_cluster.this rows) change the hash
// of a file whose rows they now join; no existing row's bytes moved,
// confirmed by regenerating with -update on the rebased tree and diffing
// internal/live/check/testdata/identity-golden.txt against main's copy,
// which shows exactly five added lines and nothing else changed except the
// header's shape line. See identityGoldenPinInstances's own note.
// Then ff2bcef1... -> ee82b073... for alb family B on top of #313 and
// #384, rebased onto main's actual current base (583/1685, ff2bcef1...):
// four added rows for the three new managed-read-* fixtures
// (managed-read-ambiguous-local, managed-read-count-local and
// managed-read-count-module, the last swept twice, once at its own root
// and once for its ./modules/acm child) change the hash of a file whose
// rows they now join; no existing row's bytes moved, confirmed by
// regenerating with -update on the rebased tree and diffing
// internal/live/check/testdata/identity-golden.txt against main's copy,
// which shows exactly four added lines and nothing else changed except
// the header's shape line.
// Then ee82b073... -> 8d8efc8d... for GitHub issue #380: two added rows
// (aws_vpc.main, aws_subnet.private) for the new lint fixture
// testdata/strict-markers-ignore-changes-per-key change the hash of a file
// whose rows they now join; no existing row's bytes moved, confirmed by
// regenerating with -update and diffing
// internal/live/check/testdata/identity-golden.txt against the prior copy,
// which shows exactly two added lines and nothing else changed except the
// header's shape line.
const identityGoldenPinBodyDigest = "2b58807fada16adc669117ae269727d6585feb31dc4289a55c181f94c5be4e44" // GitHub issue #790 (live-check -json's declared roster and references[]): one ADDED NEEDS_DISCOVERY row, aws_subnet.app in the new fixture live/e2e/estate-references - the smallest configuration exercising live/OUTPUTS.md's cross-estate data-source pattern, whose own data source contributes no row (this golden sweeps managed resource identities only). Confirmed by `git diff internal/live/check/testdata/identity-golden.txt` showing exactly that one line added and nothing else changed - "0 identities changed, 1 added, 0 removed". Previously "4863ca491fb1d200caad4f18f232a7b300a48011bc98b6776434ccdb03901a3f" // GitHub issue #585's read-path concurrency: 6 ADDED CONCRETE rows and nothing else - `git diff internal/live/check/testdata/identity-golden.txt` reads "0 identities changed, 6 added, 0 removed", every added row in the one new fixture internal/live/projection/testdata/read-parallel. The values were read, not counted: aws_cloudwatch_log_group.g0 through .g5 render "read-parallel-0" through "read-parallel-5", name=read-parallel-N, which is the literal each block's own `name` argument declares. The fixture exists because the read pass's concurrency guards need six independently readable instances of one type; nothing about identity resolution changed, and the golden was green under the concurrent read pass before the fixture was added. Previously "f0e570718b8068c5ce3b63ccb62e011c12f199e4702cd3129d765964fdb0de7e" // GitHub issue #580's module-boundary fix: 49 ADDED CONCRETE rows and nothing else - `git diff internal/live/check/testdata/identity-golden.txt` reads "0 identities changed, 49 added, 0 removed", every added row in one of the six new fixtures internal/live/lint/testdata/count-index-module-foreach*. The values were read, not counted: the admitted fixture renders "tl-pod-a-team-0000-role" through "tl-pod-b-team-0003-role", matching what stock OpenTofu names the same eight objects; count-index-module-foreach-shared and -flattened deliberately render two identical values under different addresses, which internal/live/identity refuses as a collision. Previously "59ac1908b594ba25a7ceac72c868970c6425190c3ebd4f5d9541b3484437debe" // issue #554's identity and ecs-eks fixture fixes: two CHANGED rows, aws_cognito_identity_pool_roles_attachment.app and aws_cognito_identity_pool_provider_principal_tag.app (both CONCRETE -> PARENT_DERIVED - see identityGoldenPin's own "CONCRETE" and "PARENT_DERIVED" notes), and one ADDED NEEDS_DISCOVERY row, aws_ecs_task_definition.ecs-eks in the same-named cohort (see identityGoldenPin's own "NEEDS_DISCOVERY" note), confirmed by `git diff internal/live/check/testdata/identity-golden.txt` showing exactly those three lines changed and nothing else - "2 identities changed, 1 added, 0 removed". Previously "6261686d429a9d106c29c32aa5aa64e2303f6afdadbbf399fed539596ce46ea5" // issue #541's deterministic-identity fixture: one ADDED NEEDS_DISCOVERY row, aws_iam_policy.subject in the new fixture live/e2e/deterministic-recreate (see identityGoldenPin's own "NEEDS_DISCOVERY" note), confirmed by `git diff internal/live/check/testdata/identity-golden.txt` showing exactly that one line added and nothing else changed - "0 identities changed, 1 added, 0 removed". Previously "2883cf0bddd3543cc874a8c9220e3d1ca566a4b7617126d1bcd4f16bee3e15fd" // GitHub issue #415's collision-outcome matrix: ten ADDED NEEDS_DISCOVERY rows in the new fixture internal/live/discovery/testdata/collision-matrix (see identityGoldenPin's own "NEEDS_DISCOVERY" note), confirmed by `git diff internal/live/check/testdata/identity-golden.txt` showing exactly those ten lines added and nothing else changed - "0 identities changed, 10 added, 0 removed". Previously "c8a3aacc699c40cd9aeac65fd68018fbcc1292d19c6b2a81c806e0e8a32b46c7" // [gauntlet:reference-ec2-vpc/greenfield]: one ADDED NEEDS_DISCOVERY row, aws_instance.main in the new fixture internal/live/discovery/testdata/propagated-child-marker (see identityGoldenPin's own "NEEDS_DISCOVERY" note), confirmed by `git diff internal/live/check/testdata/identity-golden.txt` showing exactly that line added and nothing else changed - "0 identities changed, 1 added, 0 removed". Previously "026129693d81c6a714e48f6151535324a6e315cc0177a2a81f245126e87fe2c2" // merge union of gauntlet:destroy-order and [gauntlet:corpus-dynamodb-table-basic/day2_remove]: on top of the destroy-order rows, four ADDED rows (two NEEDS_DISCOVERY, two RECORD_BACKED) in the new fixtures internal/live/identity/testdata/parent-derived-parent-attr and .../parent-derived-parent-attr-unknown, golden regenerated over the merged fixture set and `git diff` against pre-merge main shows exactly those four lines added and nothing else changed. Previously "0e65b7e0f15154f810ebbe3acdf13dc35c84ac90547f99464daa6973fe15ab15" // gauntlet:destroy-order: two ADDED CONCRETE rows, aws_s3_bucket.x and aws_s3_bucket.y resolving to "x" and "y", in the new fixture internal/live/moved/testdata/fork, confirmed by `git diff internal/live/check/testdata/identity-golden.txt` showing exactly those two lines added and nothing else changed. Previously "a10f18d4ec775d05ca2624be7bb520308d6c2a8da01f31e18c843f434585a6e9" // gauntlet:sweep-moved-alias: two ADDED CONCRETE rows, both aws_iam_role_policy.inline resolving to "app:deploy", in the new fixtures internal/live/discovery/testdata/moved-record-located and .../moved-record-located-nomoved, confirmed by `git diff internal/live/check/testdata/identity-golden.txt` showing exactly those two lines added and nothing else changed. Previously "98e51bd22be1809e306c1ed770706af480ca7f880505d7aea3c6fcabcd875be7" // the same unit's record-rung fix: two ADDED NEEDS_DISCOVERY rows in the new fixture internal/live/identity/testdata/record-fallback-sibling-apply, confirmed by `git diff internal/live/check/testdata/identity-golden.txt` showing exactly those two lines added and nothing else changed. Previously "b94f96c1b800c943add2f5d9b39751e13c21c742007020731cea123bcf50ef26" // gauntlet issue #397's two remaining blockers: three ADDED NEEDS_DISCOVERY rows in the new fixture internal/live/identity/testdata/nested-for-scope-per-element (see identityGoldenPin's own "NEEDS_DISCOVERY" note), confirmed by `git diff internal/live/check/testdata/identity-golden.txt` showing exactly those three lines added and nothing else changed. Previously "8739fca5b0eb799afe1d7a50355ced2bef9f403e6bc5dbd2c80b7e3ae56d4467" // issue #399's maintainer ruling: two ADDED CONCRETE rows in the new fixture internal/live/identity/testdata/target-group-attachment-lambda-port (aws_lb_target_group_attachment.lambda and .instance - see identityGoldenPin's own "CONCRETE" note), confirmed by `git diff internal/live/check/testdata/identity-golden.txt` showing exactly those two lines added and nothing else changed

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
//
// 2026-08-19 (issue #324 item 2, splat.go's resolveConcatIndex): instances
// 1513 -> 1522, dirs 475 -> 480. Five new fixture roots -
// internal/live/identity/testdata/concat-splat-index-security-group,
// concat-splat-index-second-arg, concat-splat-index-literal-fallback,
// concat-splat-index-out-of-range and concat-splat-index-unrecognized-arg
// - pinning concat(A[*].attr, B[*].attr, ..., [literal])[N] reached
// through a local value, the shape terraform-aws-modules/security-
// group's own this_sg_id accessor uses (#324's motivating site,
// aws_security_group_rule.ingress_with_cidr_blocks[0].security_group_id
// in corpus-rds-complete-postgres). security-group contributes one
// aws_security_group.this[0] (NEEDS_DISCOVERY) plus one
// aws_security_group_rule.ingress (PARENT_DERIVED, index 0 landing on
// the first splat's own single instance while the second splat's zero
// instances contribute nothing); second-arg contributes three
// aws_security_group instances across two splats (NEEDS_DISCOVERY) plus
// one aws_security_group_rule.ingress (PARENT_DERIVED, index 2 landing
// on the SECOND splat's second element - the cumulative-length offset
// arithmetic across two non-empty splats); literal-fallback contributes
// zero aws_security_group rows (both splats expand to zero instances)
// plus one aws_security_group_rule.ingress (CONCRETE, the provable index
// landing on the trailing literal rather than any resource); out-of-
// range and unrecognized-arg each contribute one aws_security_group.a[0]
// (NEEDS_DISCOVERY) and refuse identity resolution for their own rule
// resource (an out-of-range index and an unsizeable argument
// respectively), so neither contributes a rule-resource row. Totals: +9
// instances (+1 CONCRETE, +6 NEEDS_DISCOVERY, +2 PARENT_DERIVED), +5
// dirs. Every pre-existing row is byte-identical; this is a pure
// addition, matching TestIdentityGolden's own "0 identities changed, 9
// added, 0 removed".
// 2026-08-19 (issue #324 item 1, splat.go's resolveElementCoalescelist):
// instances 1522 -> 1547, dirs 480 -> 485. Three new fixture roots -
// internal/live/identity/testdata/coalescelist-element-first-arg-wins,
// coalescelist-element-second-arg-wraparound and
// coalescelist-element-literal-fallback - pinning
// element(coalescelist(A[*].attr, B[*].attr), idx), the shape
// terraform-aws-modules/vpc's own route_table_id accessor for
// aws_route_table_association.database uses (#324's own motivating site
// in corpus-rds-complete-postgres). first-arg-wins contributes three
// aws_route_table.database, three aws_route_table.private and three
// aws_subnet.database (all NEEDS_DISCOVERY, coalescelist() selecting the
// first, provably non-empty argument) plus three
// aws_route_table_association.database (PARENT_DERIVED, resolving
// through database - not private); second-arg-wraparound contributes two
// aws_route_table.private and five aws_subnet.database (NEEDS_DISCOVERY;
// aws_route_table.database itself contributes zero rows, provably
// expanding to no instances) plus five
// aws_route_table_association.database (PARENT_DERIVED, resolving through
// private with element()'s own wraparound applied to private's 2-instance
// length against a 5-instance block); literal-fallback contributes zero
// aws_route_table rows (both splats provably expand to zero instances)
// plus one aws_route_table_association.database (CONCRETE, the provable
// index landing on a trailing literal rather than any resource). Two more
// fixtures, coalescelist-element-all-empty and
// coalescelist-element-unrecognized-arg, contribute two more directories
// to the dirs total but zero rows - both refuse identity resolution for
// their own association resource (every branch provably empty with no
// literal fallback, and an unsizeable second argument respectively), and
// their own route_table resources have zero instances by construction.
// Totals: +25 instances (+1 CONCRETE, +16 NEEDS_DISCOVERY,
// +8 PARENT_DERIVED), +5 dirs. Every pre-existing row is byte-identical;
// this is a pure addition, matching TestIdentityGolden's own "0
// identities changed, 25 added, 0 removed".
// 2026-08-19 (issue #323, resolve.go's tolerantPart): instances 1547 ->
// 1549, dirs 485 -> 487. One new fixture root,
// internal/live/identity/testdata/modulearg-partial-value (two
// directories - the root and its ./mod child), pinning the identity-
// ARGUMENT half of the shape modulearg-partial already pins the key-set
// half of: a caller writes a composite module argument whose skeleton is
// literal and one of whose leaves names a resource, and the child builds
// an identity out of it. It contributes aws_iam_role.r (CONCRETE,
// the-role - the caller's own literal, unrelated to the change) and
// module.u.aws_iam_user.literal[0] (CONCRETE, platform-alpha - the two
// literal leaves the caller wrote, joined by a template, read through a
// list(map(string)) type constraint). Its sibling
// module.u.aws_iam_user.dynamic reads the ONE leaf that is not in the
// configuration and contributes NO row, which is the half that has to
// hold: an unknown leaf is turned away rather than standing in for
// lookup()'s default. Totals: +2 instances (+2 CONCRETE), +2 dirs. Every
// pre-existing row is byte-identical; this is a pure addition, matching
// TestIdentityGolden's own "0 identities changed, 2 added, 0 removed".
// 2026-08-19 (module output read inside a module-CALL argument,
// identity/moduleoutputvalue.go): instances 1549 -> 1552, dirs 487 -> 490.
// One new fixture root, internal/live/identity/testdata/
// module-output-in-call-arg (three directories - the root and its ./vpc and
// ./sg children), pinning the shape terraform-aws-modules/terraform-aws-rds's
// complete-postgres example writes at its own main.tf:224: a module call
// argument that is a literal list of objects, one of whose leaves reads
// another module call's output. It contributes
// module.sg.aws_security_group_rule.a[0] (CONCRETE,
// sg-fixed_ingress_tcp_5432_5432_10.77.0.0/16) and two rows for the
// fixture's aws_vpc.this[0] (NEEDS_DISCOVERY, unrelated to the change).
// Its five adversarial siblings contribute NO row, which is the half that
// has to hold: an output reading a managed resource's attribute (whether
// Optional+Computed or plain Optional), one calling uuid(), one declared
// sensitive, and one carrying two CIDRs are each turned away rather than
// standing in for the value. Totals: +3 instances (+1 CONCRETE,
// +2 NEEDS_DISCOVERY), +3 dirs. Every pre-existing row is byte-identical;
// this is a pure addition, matching TestIdentityGolden's own "0 identities
// changed, 3 added, 0 removed".
// 2026-08-19 (issue #325's discovery double-claim fix, claimantAlreadyPresent
// in internal/live/discovery/discovery.go): instances 1552 -> 1554, dirs
// 490 -> 491. One new fixture, internal/live/discovery/testdata/
// default-adopter-dup - a config declaring both aws_default_security_group
// and an unrelated aws_security_group, the shape that produced a false
// ProblemCollision before the fix. Contributes exactly two rows,
// aws_default_security_group.default and aws_security_group.other, both
// NEEDS_DISCOVERY. Every pre-existing row is byte-identical; this is a pure
// addition.
// 2026-08-19 (issue #302's role/service-linked-role sibling fix,
// iamServiceLinkedRoleSibling in internal/live/discovery/discovery.go):
// instances 1554 -> 1556, dirs 491 -> 492. One new fixture,
// internal/live/discovery/testdata/iam-service-linked-role-sibling - a
// config declaring both an ordinary aws_iam_role and an
// aws_iam_service_linked_role, the shape iam:ListRoles' own listing overlap
// produced a false malformed-marker refusal for before the fix. Contributes
// exactly two rows, aws_iam_role.other and aws_iam_service_linked_role.app,
// both NEEDS_DISCOVERY. Every pre-existing row is byte-identical; this is a
// pure addition.
//
// 2026-08-19 (issue #310, identity.Component gaining a Block field, merged
// on top of #302 above): instances 1556 -> 1557, dirs 492 -> 493. One new
// fixture, internal/live/identity/testdata/nested-block-component - one
// ADDED row, aws_autoscaling_traffic_source_attachment.present, rendering
// "example,elbv2,arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/example/1234567890123456"
// (the provider's own documented import example, verbatim), with identity
// attributes autoscaling_group_name/identifier/type - the second and third
// read out of the fixture's own traffic_source nested block rather than the
// top level. The fixture's other two instances (absent: no traffic_source
// block at all; impure: identifier built from uuid()) contribute no row,
// which is the half that has to hold - both are refused, not fabricated or
// defaulted. Every pre-existing row is byte-identical; this is a pure
// addition.
//
// 2026-08-19 (issue #330, the count-keyed-module moved-block fix, merged on
// top of #310 above): instances 1557 -> 1559, dirs unchanged at 493 (both
// new rows land in the existing internal/live/moved/testdata/estate
// directory, which gained a module "counted" call rather than a new fixture
// root). Two new rows, module.counted[0].aws_sqs_queue.doi and
// module.counted[0].aws_sqs_queue.stray, both NEEDS_DISCOVERY. See
// identityGoldenPin's own comment above for the fixture and the shape it
// proves. Three independent branches (#302/#310/#330) each regenerated the
// golden from a different ancestor (see the body-digest comment above for
// the merge resolution); the arithmetic checks: 1554 (pre-#302) + 2 (#302's
// own delta) + 1 (#310's own delta) + 2 (#330's own delta) = 1559, exactly
// the regenerated total.
// 2026-08-19 (issue #191, a partial module argument composing across two
// module calls, merged on top of #330 above): instances 1559 -> 1565 and
// dirs 493 -> 499. Two new fixture roots, each three directories deep
// (root, the middle module, the module it calls), because the shape this
// fixes only exists across TWO module calls and one call cannot exercise
// it: modulearg-nested-partial contributes five instances and
// modulearg-nested-dynkey, the mutation, contributes one. 6 = 5 + 1 and
// 6 = 3 + 3, both exact.
// 2026-08-19 (issue #314, local_file's fourth LogicalClass): instances 1565
// -> 1567 and dirs unchanged at 499. 0 changed, 2 added, 0 removed - the zero
// changed is the load-bearing half, since nothing about this change touches
// how any existing resource's identity renders.
//
// The two added rows are the two local_file fixtures that already existed
// (internal/live/lint/testdata/logical and live/e2e/limits/local-file), which
// contributed a directory each and no instance line while the type had no
// identity.DefaultTable row at all. Both now render RECORD_BACKED with an
// EMPTY value, and the emptiness is the part worth reading rather than a gap:
// hashicorp/local 2.9.0 implements no ImportState for local_file (`tofu
// import local_file.f <path>` answers "Resource Import Not Implemented"), so
// there is no import identity to render and the record store is the only
// carrier that can bring the instance's prior state back. A row here carrying
// the filename would be a string nothing can import by, which is exactly the
// wrong-marker shape this file exists to catch.
// 2026-08-19 (issue #336, deciding which argument coalesce() selects):
// instances 1567 -> 1571 and dirs 499 -> 503. 0 changed, 4 added, 0 removed -
// the zero changed is the load-bearing half here, since the whole change is a
// rule that makes an EARLIER evaluation succeed, and the shape a bad one
// takes is an existing fixture's marker quietly moving.
//
// Two new fixture roots, two directories each (root plus the module it
// calls), because the shape only exists across a module-call argument:
// coalesce-selection and coalesce-undecidable, the second being the
// adversarial mutation. 4 = 2 + 2 exactly.
//
// The four added rows are worth reading, because most of both fixtures
// contributes none. They are, exactly: coalesce-selection's
// module.child.aws_iam_group.literal_wins and the same resource again with
// the child module swept on its own, both CONCRETE literal-name - its name
// is coalesce("literal-name", var.name), so the rule selects the literal and
// never consults the parent; plus one RECORD_BACKED random_pet.suffix with an
// empty value from each fixture's root.
//
// Everything else contributes nothing, and both silences are load-bearing.
// coalesce-selection's other four children resolve PARENT_DERIVED over the
// record-backed parent, which renders empty in this sweep, so their values
// are asserted by TestCoalesceSelectsThroughToTheRecordBackedParent instead.
// coalesce-undecidable/child appears in the sweep and contributes no
// instance line at all, because all three of its children still refuse -
// that directory being absent from the golden is the adversarial half
// holding.
// 2026-08-20 (issue #326, kubernetes_config_map's ratified row): instances
// 1571 -> 1572 and dirs 503 -> 504. 0 changed, 1 added, 0 removed - the zero
// changed is the load-bearing half, since nothing about admitting this type
// touches how any existing resource's identity renders.
//
// One new fixture root, internal/live/identity/testdata/kubernetes-config-map,
// contributing exactly one instance: kubernetes_config_map.present, CONCRETE,
// rendering the real provider's own documented NAMESPACE/NAME import example
// shape verbatim. Its adversarial sibling (no_namespace, metadata.namespace
// absent) contributes no row at all - the half that has to hold, the same
// "wrong marker outranks a missing one" discipline every other addition
// above holds to.
//
// 2026-08-21 (issue #353, provisioners admitted under a record_store):
// instances 1572 -> 1577 and dirs 507 -> 508. 0 changed, 5 added, 0
// removed. One new fixture root, live/e2e/provisioner-taint, contributing
// five aws_s3_bucket instances, each rendering the client-named bucket it
// declares. Its provisioner blocks contribute nothing to any identity,
// which is the point: a provisioner is an effect, not an identity argument.
//
// 2026-08-21 (issue #346): instances 1577 -> 1589 and dirs 511 -> 515. 0
// changed, 12 added, 0 removed. Four new directories - the fixture root
// internal/live/identity/testdata/module-output-sibling-computed, its two
// child modules swept as roots of their own, and
// internal/live/identity/testdata/synthesized-parent-attr, which contributes
// no row at all (its parent's entry is synthesized rather than ratified, which
// is precisely the condition #346 did not relax, and its child therefore stays
// refused).
//
// 2026-08-21 (issue #354): instances 1589 -> 1592 and dirs 521 -> 524. 0
// changed, 3 added, 0 removed. Three new directories - the fixture root
// internal/live/identity/testdata/module-output-whole-resource and its two
// child modules swept as roots of their own. The child directories contribute
// no rows: swept alone, alb/ has an unset required `groups` variable and asg/
// has no attachments at all, so neither expands an instance.
const (
	// 2026-08-21 (issue #369): instances 1592 -> 1593, dirs unchanged at
	// 524. 0 changed, 1 added, 0 removed. No new directory - the existing
	// internal/live/identity/testdata/sole-element-from-value fixture
	// gained one new resolved resource,
	// aws_security_group_rule.resolved_by_sibling, whose
	// source_security_group_id and zero-element prefix_list_ids exercise
	// [firstApplicablePresent]'s fix: a Component.SoleElement alternation
	// member that is a definite empty list defers to an already-satisfied
	// sibling instead of refusing "Ambiguous list-valued identity
	// argument". The fixture's second new resource,
	// all_empty_no_sibling, is the negative control (every alternation
	// member proven empty, no sibling set) and stays refused, so it
	// contributes no row.
	//
	// 2026-08-21 (issue #368): instances 1593 -> 1601, dirs 524 -> 530.
	// 0 changed, 8 added, 0 removed. Two new fixture roots -
	// internal/live/identity/testdata/formula-transform (the ECS and
	// security-group shapes a render-time [ParentRef.Transform] makes
	// expressible) and .../deferred-through-module-list (the measurement
	// that refutes #368's own reading of corpus-rds-complete-postgres) -
	// plus their four child modules swept as roots of their own.
	//
	// Every added row is a plain server-assigned or client-named parent
	// the fixtures need a live value from; NOT ONE of them is a transformed
	// identity. That is a property of this sweep, not of the change: every
	// resolution a transform produces here goes through
	// [resolver.parentPart]'s deferred-read branch, which needs provider
	// schemas, and this sweep runs without them. The transformed identities
	// are pinned BY VALUE in internal/live/identity/transform_test.go,
	// rendered against a lookup that hands back a real ARN, which is where
	// HANDOFF.md's safety rule is actually discharged for this change.
	//
	// The zero changed is the load-bearing half here as everywhere: the two
	// entry points [resolver.resolveTransformCall] and
	// [resolver.soleElementDeferred] are reached only after every existing
	// route has already declined, and each declines again unless it finds a
	// recognized pipeline over exactly one deferred read, so nothing that
	// renders a marker today can be re-routed by them.
	//
	// Then 1601 -> 1614 for GitHub issue #365 slice 2: thirteen added rows
	// across the thirteen new markers "record" fixtures, nothing modified.
	// Every one is a plain server-assigned resource the fixtures need in
	// order to have something to select, or something to leave unselected
	// beside it. See identityGoldenPinBodyDigest's own comment for why none
	// of them renders as RECORD_LOCATED here.
	// Then 1614 -> 1623 for GitHub issue #375: nine added rows across the two
	// new fixtures, none modified, none removed. See the class comments
	// above for which row is which.
	// Then 1623 -> 1628 for the corpus-rds-complete-postgres routing fix:
	// five added rows, none modified, none removed. Four are plain
	// server-assigned resources the new negative controls in
	// testdata/deferred-through-module-list need in order to have something
	// to point at; the fifth is #375's own poisoned control, which renders a
	// formula and an empty import ID. See the class comments above.
	//
	// Then 1628 -> 1629 for GitHub issue #378: one added row, the
	// aws_s3_bucket in the new live/e2e/limits/reserved-symbol fixture.
	// #378's own change is to what internal/live/stamp writes into a tags
	// argument, which this sweep does not render at all, so its whole effect
	// here is the fixture it brought with it.
	//
	// Then 1629 -> 1632 for GitHub issue #365 slice 3: three added rows, zero
	// modified, zero removed. All three are RECORD_BACKED and all three
	// render an empty value; see the RECORD_BACKED entry in
	// identityGoldenPinClasses for which fixtures and why the empty value is
	// the right one. The slice was originally measured as 1614 -> 1617
	// against 350afb5925; the delta it contributes is unchanged by the three
	// changes above, which is what the reconciled figure says - +3 either
	// way, because the rows come from identity.DefaultTable gaining
	// SecretMaterial entries and nothing above reads that table.
	//
	// Then 1632 -> 1651 for corpus-eks-basic's count-index wall
	// (internal/live/lint/sibling_select.go): nineteen added rows across
	// three new lint fixtures, none modified, none removed. Nine are the
	// aws_route_table_association instances the change exists to reach; ten
	// are the aws_subnet and aws_route_table instances those associations
	// select. The change itself is to what internal/live/lint refuses, and
	// this sweep calls identity.Resolve directly without going through lint
	// at all, so it could not have moved an existing row and did not: "0
	// identities changed, 19 added, 0 removed" over its own base of 563
	// directories / 1629 instances, reconciled onto slice 3's +3 above by
	// regenerating on the merged tree rather than by adding deltas by hand.
	// Then 1651 -> 1660 for corpus-sumaform-aws's static count() wall: nine
	// added rows, none changed, none removed. See
	// identityGoldenPinBodyDigest's own note for which fixtures and what
	// their rendered values are.
	// Then 1660 -> 1672 for corpus-alb-complete's Family A wall: twelve
	// added rows, none changed, none removed. See identityGoldenPinBodyDigest's
	// own note for the fixture and which rows are which.
	// Then 1672 -> 1674 for [RecordFallbackType]: two added rows, none
	// changed, none removed. See identityGoldenPinBodyDigest's own note.
	// Then 1674 -> 1678 for GitHub issue #372's remainder: four added rows
	// across the two new liveimport fixtures (two NEEDS_DISCOVERY, two
	// CONCRETE - see the "NEEDS_DISCOVERY" and "CONCRETE" class notes above),
	// none changed, none removed.
	// Then 1678 -> 1680, same issue, a second pass: two more added rows,
	// slot-markerfallback-config's aws_iam_role.this[0..1] - see
	// identityGoldenPinBodyDigest's own note.
	//
	// Then 1680 -> 1685 for the provider-configuration dependency-order
	// wall (issue #313, corpus-eks-basic's boundary), rebased onto the
	// #372 base directly above (577/1680) rather than issue #313's own
	// original base of 574/1674: three new fixtures across five new
	// directories / five new instances, none changed, none removed:
	//   - internal/live/dataread/testdata/managed-projection-live pins
	//     Options.LiveManagedResults (dataread's live-managed-value
	//     fallback for a managed reference no literal argument covers, the
	//     seam projection.ReadInstances now feeds). Its one instance,
	//     aws_instance.web, is the same shape every sibling
	//     managed-projection-* fixture already contributes:
	//     NEEDS_DISCOVERY with no rendered value, since an aws_instance's
	//     identity needs a real account.
	//   - internal/live/dataread/testdata/module-output-hop (and its
	//     ./child) pins configs.StaticEvaluator.WithModuleOutputResults (a
	//     data source's own argument crossing into a child module's own
	//     output expression, which in turn needs the same live-managed
	//     fallback) - the two hops "provider.kubernetes { host =
	//     data.aws_eks_cluster.cluster.endpoint }" needs, with
	//     data.aws_eks_cluster.cluster's own "name = module.eks.cluster_id"
	//     in between. Its child module's aws_eks_cluster.this resolves
	//     CONCRETE/"prod-cluster" - a plain literal name argument, swept
	//     twice (once as module-output-hop/child's own root, once as
	//     module-output-hop's module.child.aws_eks_cluster.this), the same
	//     "child swept as a root of its own" duplication
	//     tolerant-module-output's own note above already explains.
	//   - internal/live/dataread/testdata/provider-config-demand (and its
	//     ./child) pins [dataread.AnalyzeProviderConfigs]/[dataread.
	//     ReadProviderConfigs], the phase's third demand class: a PROVIDER
	//     BLOCK's own argument (never an identity-bearing position, so
	//     never probed by the other two classes) demanding a data source
	//     that then needs both of the two seams above. Same duplication
	//     again for the same reason: aws_eks_cluster.this swept from its
	//     own child module directory and again as
	//     provider-config-demand's module.child.aws_eks_cluster.this.
	// "0 identities changed, 5 added, 0 removed" confirmed by diffing
	// testdata/identity-golden.txt before and after regenerating, rebased
	// onto GitHub issue #372's own two-pass total directly above (577/1680)
	// rather than measured against a stale 574/1674 base.
	//
	// Then 1685 -> 1689 for alb family B on top of #313 and #384, rebased
	// onto #313's own base directly above (583/1685): four new instances
	// across the four new managed-read-* directories from
	// identityGoldenPinDirs's own note - one aws_acm_certificate row per
	// directory, each NEEDS_DISCOVERY with no rendered value, the same
	// shape every managed-read-* sibling fixture already contributes.
	// "0 identities changed, 4 added, 0 removed" confirmed by diffing
	// testdata/identity-golden.txt before and after regenerating on the
	// rebased tree.
	//
	// Then 1689 -> 1691 for GitHub issue #380: two new instances,
	// aws_vpc.main and aws_subnet.private, from the new lint fixture
	// testdata/strict-markers-ignore-changes-per-key (see
	// identityGoldenPin's "NEEDS_DISCOVERY" note). "0 identities changed, 2
	// added, 0 removed" confirmed by diffing testdata/identity-golden.txt
	// before and after regenerating.
	//
	// Then 1695 -> 1696 for issue #394's own fix (a default_* companion's
	// mismatched identity can only be recomposed by a native per-type list
	// call, not the ARN-join tag sweep): one new instance,
	// internal/live/discovery/testdata/default-adopter-sweep-orphan's
	// aws_sns_topic.unrelated, a plain server-assigned NEEDS_DISCOVERY
	// fixture whose only purpose is to declare something so the config
	// directory parses while declaring neither side of any companion pair.
	// "0 identities changed, 1 added, 0 removed" confirmed by diffing
	// testdata/identity-golden.txt before and after regenerating.
	//
	// Then 1696 -> 1702 for issue #391: six new instances across the two
	// new fixtures identityGoldenPinBodyDigest's own note names -
	// provider-config-demand-sibling-output's aws_eks_cluster.this and
	// aws_instance.other, each swept twice (own child directory plus the
	// parent's module.child.* row), and record-parent-derived's
	// aws_cloudwatch_log_group.app and null_resource.suffix. "0 identities
	// changed, 6 added, 0 removed" confirmed by diffing testdata/identity-
	// golden.txt before and after regenerating with -update.
	//
	// Then 1702 -> 1705 for the corpus-alb-complete/test_plan unit that
	// fixed [namesAModuleOutput]'s crosstalk: three new instances, all from
	// testdata/managed-read-module-blind-crosstalk (see identityGoldenPin's
	// "NEEDS_DISCOVERY" note for which three, and why the fixture's fourth
	// resource is not among them). "0 identities changed, 3 added, 0
	// removed" confirmed by diffing testdata/identity-golden.txt before and
	// after regenerating.
	//
	// Then 1702 -> 1704, corpus-eks-basic/test_plan's own moduleOutputLookup
	// dependency-scoping unit (issue #391 continued): two new instances,
	// aws_instance.gatekeeper, added to the SAME provider-config-demand-
	// sibling-output fixture (a third sibling output, poison_output, wired
	// through a NEW data.aws_zone.poison that names this managed resource
	// in depends_on - see the fixture's own comment), swept twice for the
	// same own-directory-plus-parent reason the six above already are. "0
	// identities changed, 2 added, 0 removed" confirmed by diffing testdata/
	// identity-golden.txt before and after regenerating with -update.
	//
	// Then 1707 -> 1709, corpus-eks-basic/test_plan's splat-visibility unit
	// (issue #396): two new instances, aws_eks_cluster.this[0], both
	// CONCRETE ("prod-cluster"), added by internal/live/dataread/testdata/
	// provider-config-demand-splat/ (its own directory plus its child/
	// module, the same own-directory-plus-parent shape every fixture above
	// sweeps twice) - a count-expanded managed resource whose child module
	// output reads it back through a legacy 0.11-style splat
	// (element(concat(aws_eks_cluster.this.*.id, tolist([""])), 0)), the
	// exact shape internal/configs/splat_coverage.go now makes visible to
	// static reference coverage. "0 identities changed, 2 added, 0 removed"
	// confirmed by diffing testdata/identity-golden.txt before and after
	// regenerating with -update.
	//
	// Then 1709 -> 1712, the corpus-alb-complete/test_plan unit continuing
	// gauntlet issue #397: three new instances from testdata/
	// values-splat-per-element (see identityGoldenPinBodyDigest's own
	// note). "0 identities changed, 3 added, 0 removed" confirmed the same
	// way.
	//
	// Then 1712 -> 1714, issue #399's maintainer ruling: two new instances
	// from testdata/target-group-attachment-lambda-port (see
	// identityGoldenPin's own "CONCRETE" note and identityGoldenPin
	// BodyDigest's note). "0 identities changed, 2 added, 0 removed"
	// confirmed the same way.
	//
	// Then 1714 -> 1717, the corpus-alb-complete/test_plan unit landing
	// gauntlet issue #397's two remaining blockers: three new instances from
	// testdata/nested-for-scope-per-element (see identityGoldenPin's own
	// "NEEDS_DISCOVERY" note). "0 identities changed, 3 added, 0 removed"
	// confirmed the same way.
	//
	// Then 1717 -> 1719, the same unit's record-rung fix: two new instances
	// from testdata/record-fallback-sibling-apply (see identityGoldenPin's
	// own "NEEDS_DISCOVERY" note). "0 identities changed, 2 added, 0
	// removed" confirmed the same way.
	//
	// Then 1719 -> 1721, gauntlet:sweep-moved-alias: two new instances from
	// internal/live/discovery/testdata/moved-record-located and
	// .../moved-record-located-nomoved (see identityGoldenPin's own
	// "CONCRETE" note). "0 identities changed, 2 added, 0 removed"
	// confirmed the same way.
	//
	// Then 1721 -> 1723, gauntlet:destroy-order: two new instances from
	// internal/live/moved/testdata/fork (see identityGoldenPin's own
	// "CONCRETE" note). "0 identities changed, 2 added, 0 removed"
	// confirmed the same way.
	// Then 1723 -> 1727, merging gauntlet/dynamodb-clear: four new instances,
	// two per new fixture (parent-derived-parent-attr and
	// parent-derived-parent-attr-unknown). "0 identities changed, 4 added, 0
	// removed" on the merge.
	// Then 1727 -> 1728, [gauntlet:reference-ec2-vpc/greenfield]: one new
	// instance, aws_instance.main in the new fixture
	// internal/live/discovery/testdata/propagated-child-marker. "0 identities
	// changed, 1 added, 0 removed".
	// Then 1728 -> 1738, GitHub issue #415's collision-outcome matrix: ten
	// new instances in the new fixture
	// internal/live/discovery/testdata/collision-matrix (see
	// identityGoldenPin's own "NEEDS_DISCOVERY" note). "0 identities
	// changed, 10 added, 0 removed".
	// Then 1738 -> 1739, issue #541's deterministic-identity fixture: one
	// new instance, aws_iam_policy.subject in the new fixture
	// live/e2e/deterministic-recreate (see identityGoldenPin's own
	// "NEEDS_DISCOVERY" note). "0 identities changed, 1 added, 0 removed".
	// Then 1739 -> 1740, issue #554's ecs-eks fixture fix: one new
	// instance, aws_ecs_task_definition.ecs-eks (see identityGoldenPin's
	// own "NEEDS_DISCOVERY" note). "2 identities changed, 1 added, 0
	// removed" - the two CHANGED rows are identity's own fix, both moving
	// CONCRETE -> PARENT_DERIVED (see identityGoldenPin's "CONCRETE" and
	// "PARENT_DERIVED" notes), which is why this is 1 add on top of 1739
	// rather than a bare increment: no row was removed, one was added, and
	// two existing rows changed value without changing count.
	// Then 1740 -> 1789, GitHub issue #580's module-boundary fix: 49 new
	// instances across the six new fixtures
	// internal/live/lint/testdata/count-index-module-foreach* (eight
	// aws_iam_role instances each, plus one root-level aws_iam_role.seed in
	// the -unprovable fixture). "0 identities changed, 49 added, 0
	// removed". Every one of the 49 is a row that did not exist because the
	// DIRECTORY did not exist: check.Analyze resolves identities whether or
	// not lint refused (analyze.go runs identity.ResolveWith after
	// lint.CheckWith unconditionally), so this golden is not itself
	// sensitive to a lint change. What it is doing here is putting the
	// values the widened admission newly lets through in front of a reader:
	// "tl-pod-a-team-0000-role" through "tl-pod-b-team-0003-role" in the
	// admitted fixture, and - in the two whose module call passes every
	// instance the same prefix - two visibly identical rows under different
	// addresses, which is the collision internal/live/identity refuses and
	// this file makes legible.
	// Then 1789 -> 1795, GitHub issue #585's read-path concurrency: the six
	// aws_cloudwatch_log_group blocks of the new fixture
	// internal/live/projection/testdata/read-parallel (the CONCRETE count
	// moves in step - see its own note).
	// Then 1795 -> 1796, GitHub issue #790 (live-check -json's declared
	// roster and references[]): the one aws_subnet.app instance in the new
	// fixture live/e2e/estate-references (identityGoldenPinDirs and the
	// NEEDS_DISCOVERY count both move in step - see their own notes).
	identityGoldenPinInstances = 1796
	// identityGoldenPinDirs moved 503 -> 504 for GitHub issue #348's fix:
	// internal/live/projection/testdata/output-eval is a new fixture (a
	// stub_cert resource plus root-level outputs, used to pin
	// ApplyRootOutputValues), and stub_cert is not an admitted type, so it
	// contributes zero rows to the body - identityGoldenPinInstances and
	// identityGoldenPinBodyDigest are both unchanged, confirmed by diffing
	// testdata/identity-golden.txt before and after regenerating: only the
	// header's "dirs=" line moved.
	//
	// Then 504 -> 506 for GitHub issue #349's fix: two more fixture
	// directories, internal/live/projection/testdata/output-eval-zero and
	// its ./layer child, which pin withZeroInstanceBlocks - a root output
	// reaching a provably-zero-instance block through count, through
	// for_each, through a data source and through a module output, plus the
	// negative control for a count that does not resolve. Same reason the
	// #348 fixture contributed no rows: stub_cert and stub_lookup are not
	// admitted types, and the zero-instance blocks have no instances to
	// render an identity for either way. identityGoldenPinInstances and
	// identityGoldenPinBodyDigest are both unchanged, confirmed by diffing
	// testdata/identity-golden.txt before and after regenerating: only the
	// header's "dirs=" line moved, and TestIdentityGolden itself reported
	// "differs but no instance's identity did".
	//
	// Then 506 -> 507 dirs and 1571 -> 1572 instances for GitHub issue #326
	// (merged after #348/#349), kubernetes_config_map's ratified row: one
	// new fixture root, internal/live/identity/testdata/kubernetes-config-map,
	// contributing exactly one instance (kubernetes_config_map.present,
	// CONCRETE, rendering the provider's own documented NAMESPACE/NAME
	// import example verbatim). Its adversarial sibling (no_namespace,
	// metadata.namespace absent) contributes no row at all. 0 existing
	// instances changed, 1 added, 0 removed.
	//
	// Then 507 -> 510 dirs for GitHub issue #349's sub-problem 2, the
	// root-output data-read class: three more fixture directories -
	// internal/live/dataread/testdata/root-output-data and its ./m child,
	// which pin the demand walk and the local-execution provider boundary,
	// and internal/live/projection/testdata/output-eval-data, which pins
	// the seeded value reaching the output evaluation. Same reason as every
	// entry above: test_thing, stub_cert and stub_lookup are not admitted
	// types, so none of the three contributes a row. identityGoldenPinInstances
	// and identityGoldenPinBodyDigest are both unchanged, confirmed by
	// diffing testdata/identity-golden.txt before and after regenerating -
	// only the header's "dirs=" line moved, and TestIdentityGolden itself
	// reported "differs but no instance's identity did".
	//
	// Then 510 -> 511 dirs and 1572 -> 1577 instances for GitHub issue
	// #353: one new fixture root, live/e2e/provisioner-taint, contributing
	// five aws_s3_bucket instances. See identityGoldenPinInstances' own
	// comment directly above.
	//
	// Then 511 -> 515 dirs for the 2026-08-21 data-read safety audit: four
	// more fixture directories - internal/live/dataread/testdata's
	// identity-local-execution (a local-execution data source in an
	// identity-bearing position), scope-recursion (an out-of-scope data
	// source reached only through classify's recursion) and
	// aliased-provider-source (a required_providers entry binding a local
	// name to a provider that does not serve the type), plus
	// internal/live/projection/testdata/output-eval-sensitive (a root output
	// reaching a sensitive schema attribute). Same reason as every entry
	// above: external, test_thing and stub_cert are not admitted types, and
	// the one aws_cloudwatch_log_group pair in identity-local-execution
	// resolves no instance without schemas, so none of the four contributes
	// a row. identityGoldenPinInstances and identityGoldenPinBodyDigest are both
	// unchanged, confirmed by diffing testdata/identity-golden.txt before and
	// after regenerating - only the header's "dirs=" line moved, and
	// TestIdentityGolden itself reported "differs but no instance's identity
	// did".
	//
	// Then 515 -> 517 dirs for the two call sites that put a MARKED value to
	// a provider RPC: internal/live/projection/testdata/plan-sensitive, whose
	// resource takes an argument from a `sensitive = true` variable, and
	// .../testdata/tags-sensitive, whose two resources take a TAG VALUE from
	// one - on the map's element and on the container respectively, the two
	// places the mark lands. Same reason as every entry above: stub_db and
	// stub_bucket are not admitted types, so neither directory contributes a
	// row. identityGoldenPinInstances and identityGoldenPinBodyDigest are
	// both unchanged, confirmed by diffing testdata/identity-golden.txt
	// before and after regenerating - only the header's "dirs=" line moved,
	// and TestIdentityGolden itself reported "differs but no instance's
	// identity did".
	//
	// Then 517 -> 521 dirs and 1577 -> 1589 instances for GitHub issue #346:
	// four new directories, twelve added rows, nothing modified. See
	// identityGoldenPinInstances' own comment directly above.
	//
	// Then 521 -> 524 dirs and 1589 -> 1592 instances for GitHub issue #354:
	// three new directories, three added rows, nothing modified. See
	// identityGoldenPinInstances' own comment directly above.
	//
	// Then 524 -> 529 dirs for GitHub issue #365's strict block: five new
	// configuration directories, none of which declares a resource at all -
	// four under internal/live/lint/testdata (the marker_repair matrix: the
	// default omitted, the default written out, an unimplemented setting, a
	// setting outside the vocabulary) and live/e2e/limits/
	// strict-marker-repair. Every one is a terraform{live{strict{}}} block
	// and nothing else, so none contributes a row.
	// identityGoldenPinInstances and identityGoldenPinBodyDigest are both
	// unchanged, confirmed by regenerating and diffing: only the header's
	// "dirs=" line moved, and TestIdentityGolden itself reported "differs
	// but no instance's identity did".
	//
	// Then 529 -> 535 dirs and 1593 -> 1601
	// instances for GitHub issue #368: six new directories (the two fixture
	// roots internal/live/identity/testdata/formula-transform and
	// .../deferred-through-module-list, plus four child modules swept as
	// roots of their own), eight added rows, nothing modified. See
	// identityGoldenPinInstances' own comment directly above. #365 and #368
	// landed independently on top of the same base and are merged here
	// together, so this pin reflects both.
	//
	// Then 535 -> 550 dirs for GitHub issue #365 slice 2's markers "record"
	// selection: fifteen new configuration directories. Ten under
	// internal/live/lint/testdata (the selection matrix - an empty block, no
	// record_store, an instance-keyed address, an unknown address, a
	// module-qualified address and its child module, an unrecordable type,
	// marker_repair = "never" with a selection, and the three
	// ignore_changes compositions), two under internal/live/check/testdata
	// (the by-value identity fixtures), two under live/e2e/limits (the two
	// new limits-wing entries), and one child module swept as a root of its
	// own. Thirteen of them declare resources; see
	// identityGoldenPinInstances directly above.
	// Then 550 -> 558 dirs for GitHub issue #375: eight new configuration
	// directories, four per fixture -
	// internal/live/identity/testdata/module-arg-hoisted with its gate, net
	// and secret-net child modules, and .../merge-bare-module-output with
	// its base, network and host child modules. Each child is swept as a
	// root of its own; the ones declaring no resource of an admitted type
	// contribute a directory and no row.
	// Then 558 -> 559 for the corpus-rds-complete-postgres routing fix: one
	// new configuration directory,
	// internal/live/identity/testdata/deferred-through-module-list/sgtyped,
	// the module whose three variable declarations are the declared-type
	// gate's own controls.
	// Then 559 -> 560 for GitHub issue #378: live/e2e/limits/reserved-symbol,
	// the fixture for the lint rule reserving tofu.marker_module_prefix.
	//
	// Then 560 -> 564 dirs for GitHub issue #365 slice 3's secrets toggle:
	// four new configuration directories, none of which declares a resource
	// at all - three under internal/live/lint/testdata (the two valid
	// spellings and one outside the vocabulary) and live/e2e/limits/
	// strict-secrets. Every one is a terraform{live{strict{}}} block and
	// nothing else, so none contributes a row; the three ADDED rows come
	// from fixtures that already existed. See identityGoldenPinInstances'
	// own comment directly above. Measured as 550 -> 554 against
	// 350afb5925 and +4 either way, since none of the three changes above
	// added or removed a directory this slice's fixtures sit in.
	//
	// Then 564 -> 567 for corpus-eks-basic's count-index wall: three new
	// lint fixtures under internal/live/lint/testdata -
	// count-index-sibling-select, count-index-sibling-select-indexed and
	// count-index-sibling-select-collision - the two spellings of a
	// sibling-instance selection and the collapse that really is a
	// collision. They are three directories rather than one because all
	// three render the SAME identities, which is the claim, and a
	// configuration holding two of them is what checkCollisions correctly
	// refuses. Measured as 560 -> 563 against this branch's own base and +3
	// either way, reconciled the same way as the instance count above.
	// Then 567 -> 571 for corpus-sumaform-aws's static count() wall:
	// internal/live/identity/testdata/tolerant-module-output and its three
	// submodules (base, host, net) - one fixture, four directories, because
	// the sweep enters each module directory that loads on its own.
	// Then 571 -> 573 for corpus-alb-complete's Family A wall:
	// internal/live/identity/testdata/module-foreach-forexpr-filter-sibling-value
	// and its ./attach child module - one fixture, two directories, the
	// child swept as a root of its own the same way tolerant-module-output's
	// submodules are.
	// Then 573 -> 574 for [RecordFallbackType]: one new fixture,
	// internal/live/identity/testdata/record-fallback-untaggable.
	// Then 574 -> 576 for GitHub issue #372's remainder:
	// internal/live/liveimport/testdata/slot-clientnamed-config and its
	// literal-named negative-control sibling, slot-clientnamed-literal-config
	// - two new fixtures, each its own directory, no module tree.
	// Then 576 -> 577, same issue, a second pass:
	// internal/live/liveimport/testdata/slot-markerfallback-config, one more
	// new fixture, its own directory.
	// Then 577 -> 578 for GitHub issue #384 (a Component.SoleElement
	// alternation with two genuinely non-empty alternatives no longer binds
	// the wrong live object): one new fixture,
	// internal/live/identity/testdata/record-fallback-solelement-conflict,
	// used only by a test that passes it a synthetic schema through
	// ResolveWith - the schema-less sweep this pin covers resolves nothing
	// in it (no record_store fallback without a schema, and the conflicting
	// instance correctly refuses instead of guessing), and the fixture the
	// fix's OTHER test reads, testdata/sole-element-from-value, gained a
	// resource in the SAME directory rather than a new one. So dirs moves by
	// one and identityGoldenPinInstances/identityGoldenPinBodyDigest below
	// are unchanged - confirmed by diffing testdata/identity-golden.txt
	// before and after regenerating: only the header's dirs count differs.
	//
	// 578 -> 583 for issue #313 on top of #384: five new fixture
	// directories - managed-projection-live, module-output-hop,
	// module-output-hop/child, provider-config-demand and
	// provider-config-demand/child - for the provider-configuration
	// dependency-order wall. Rebased onto #384's 578 rather than #313's own
	// original base of 574. See identityGoldenPinInstances's own note for
	// detail.
	//
	// 583 -> 587 for alb family B on top of #313 and #384: four new
	// fixture directories - managed-read-ambiguous-local,
	// managed-read-count-local, managed-read-count-module and
	// managed-read-count-module/modules/acm (the last swept twice, once at
	// its own root and once for its ./modules/acm child, the same way
	// tolerant-module-output's submodules are) - for corpus-alb-complete's
	// Family B fix (server-minted ACM domain_validation_options). Rebased
	// onto #313's 583 rather than this branch's own original base of 577.
	//
	// 587 -> 588 for GitHub issue #380: one new fixture directory,
	// internal/live/lint/testdata/strict-markers-ignore-changes-per-key,
	// proving checkIgnoreChanges declines the per-key ignore_changes shape
	// internal/live/stamp now synthesizes the same way it already declines
	// the whole-tags shape.
	//
	// 590 -> 595 for GitHub issue #388's wiring unit (ruling 3, the
	// plan-node seam): five new fixture directories, none producing an
	// admitted managed instance - internal/live/identity/testdata/
	// node-seam-computed-boundary (the fixture proving the static
	// evaluator refuses a shape the node-seam's ComponentsFromValue
	// resolves), internal/live/lint/testdata/strict-nosourcecreate-refuse/
	// -create/-invalid (GitHub issue #365 ruling 4's no_source_create
	// toggle), and live/e2e/limits/strict-no-source-create (that toggle's
	// limits-wing fixture). identityGoldenPinInstances is unchanged at
	// 1695: none of the five resolves a managed instance identity.
	//
	// 595 -> 596 for GitHub issue #394: one new fixture directory,
	// internal/live/discovery/testdata/default-adopter-sweep-orphan (see
	// identityGoldenPinInstances's own note immediately above).
	//
	// 596 -> 599 for issue #391: three new fixture directories -
	// internal/live/dataread/testdata/provider-config-demand-sibling-
	// output and its own child/ submodule, plus internal/live/projection/
	// testdata/record-parent-derived (see identityGoldenPinInstances's own
	// note immediately above for the six rows they contribute).
	//
	// Then 599 -> 602 for the corpus-alb-complete/test_plan unit that fixed
	// [namesAModuleOutput]'s crosstalk: three new fixture directories -
	// internal/live/identity/testdata/managed-read-module-blind-crosstalk
	// and its two submodule directories, modules/alb and
	// modules/wildcard_cert (see identityGoldenPinInstances's own note
	// immediately above).
	//
	// Then 602 -> 604 for corpus-eks-basic/test_plan's splat-visibility unit
	// (issue #396): two new fixture directories - internal/live/dataread/
	// testdata/provider-config-demand-splat and its own child/ submodule
	// (see identityGoldenPinInstances's own note immediately above).
	//
	// Then 604 -> 606 for corpus-eks-basic/test_plan's own follow-up unit
	// (the estate's launch-configuration wall, closed by
	// configuredAttrsSeed and its residue-record pre-read seed in
	// internal/live/projection/build.go): two new fixture directories,
	// internal/live/projection/testdata/attrs-seed and attrs-seed-data,
	// neither producing an admitted managed instance - stub_lc and
	// stub_data are test-only schemas, never real provider types the
	// admission table resolves. identityGoldenPinInstances is unchanged at
	// 1709 for the same reason.
	//
	// Then 606 -> 607 for the corpus-ecs-fargate/test_plan unit that fixed
	// GitHub issues #395 and #376 - the SAME configuredAttrsSeed mechanism
	// as corpus-eks-basic's unit above, independently generalized and then
	// reconciled onto the identical implementation (rebased 2026-08-24,
	// see build.go's own doc comment on configuredAttrsSeed and
	// configuredTagsSeed for which parts of each unit's design survived):
	// one new fixture directory, internal/live/projection/testdata/
	// attrs-seed-fargate (named to avoid colliding with corpus-eks-basic's
	// own testdata/attrs-seed), whose three stub_* resource types are
	// test-only fictions with no identity.DefaultTable entry and no
	// provider schema, so they resolve no admitted managed instance
	// identity at all.
	//
	// Then 607 -> 608 for the same unit's second fixture directory,
	// internal/live/projection/testdata/residue-seed-managed-ref: the
	// harder half of #395, a task_definition set to a REFERENCE to
	// another resource's computed attribute rather than a literal, which
	// needed residueSeedFor (a residue-record seed, not just
	// configuredAttrsSeed's static-config one) to fix. Same reason as
	// above: stub_service/stub_task_definition are test-only fictions
	// with no admitted identity. identityGoldenPinInstances is unchanged
	// at 1709 across all four of this pin's own fixtures.
	//
	// Then 608 -> 610 for the corpus-alb-complete/test_plan unit continuing
	// gauntlet issue #397: two new fixture directories, testdata/
	// values-splat-per-element and its own modules/wildcard_cert submodule
	// (see identityGoldenPinBodyDigest's own note).
	//
	// Then 610 -> 611 for issue #399's maintainer ruling: one new fixture
	// directory, internal/live/identity/testdata/target-group-attachment-
	// lambda-port (see identityGoldenPin's own "CONCRETE" note).
	//
	// Then 611 -> 615 for the day2_rename record-located-follows-a-moved-
	// address fix (internal/live/projection/located.go's
	// locatedIdentityWithAliases): two new fixture directories,
	// internal/live/projection/testdata/located-moved-module and
	// located-moved-module-ambiguous, each with its own "child" module
	// subdirectory swept as a standalone root the same way every other
	// module-source fixture here is, so one new fixture is two new rows in
	// this count. identityGoldenPinInstances and identityGoldenPinBodyDigest
	// are both unchanged at 1714 and the digest below: aws_eip_association
	// has no ratified table row and neither fixture declares a live block,
	// so this schema-less sweep resolves nothing in any of the four new
	// directories - confirmed by regenerating with -update and diffing
	// internal/live/check/testdata/identity-golden.txt against the prior
	// copy, which shows only the header's "dirs=" line moved.
	//
	// Then 615 -> 618 for gauntlet issue #397's two remaining blockers: one
	// new fixture, internal/live/identity/testdata/nested-for-scope-per-
	// element, whose two module sources (modules/alb and
	// modules/wildcard_cert) are each swept as a standalone root the same
	// way every other module-source fixture here is, so one new fixture is
	// three new rows in this count. modules/alb contributes no INSTANCE row
	// of its own: swept standalone its `listeners` variable takes its {}
	// default, so aws_lb_listener_certificate.this has no instances at all.
	//
	// Then 618 -> 619 for the same unit's record-rung fix: one new fixture,
	// internal/live/identity/testdata/record-fallback-sibling-apply, with no
	// module sources of its own.
	//
	// Then 619 -> 620 for issue #365's closeout audit: one new fixture,
	// live/e2e/limits/strict-secrets-refusal (a random_password refused by
	// an ACTUALLY-SET strict { secrets = "refuse" }, proving the toggle
	// rather than the invalid-value typo case the older strict-secrets
	// fixture covers). identityGoldenPinInstances is unchanged at 1719:
	// confirmed by regenerating with -update and reading
	// TestIdentityGolden's own "no instance's identity did [differ]"
	// check, which only the header's dirs= line failed before the
	// regeneration.
	//
	// Then 620 -> 622 for gauntlet:sweep-moved-alias: two new fixtures,
	// internal/live/discovery/testdata/moved-record-located and
	// .../moved-record-located-nomoved, each a standalone root with no
	// module sources of its own. identityGoldenPinInstances moves in step
	// (see its own note).
	//
	// Then 622 -> 624 for gauntlet:destroy-order: two new fixtures,
	// internal/live/moved/testdata/fork (identityGoldenPinInstances moves
	// in step - see its own note) and
	// internal/live/discovery/testdata/moved-record-located-blockremoved
	// (a single `moved` block with no resource block on either side, so it
	// adds a directory but no instance).
	//
	// Then 624 -> 626, merging gauntlet/dynamodb-clear: two new fixtures,
	// internal/live/identity/testdata/parent-derived-parent-attr and
	// .../parent-derived-parent-attr-unknown (identityGoldenPinInstances
	// moves in step - see its own note).
	// Then 626 -> 627, [gauntlet:reference-ec2-vpc/greenfield]: one new
	// fixture, internal/live/discovery/testdata/propagated-child-marker
	// (identityGoldenPinInstances moves in step - see its own note).
	// Then 627 -> 628, GitHub issue #415's collision-outcome matrix: one new
	// fixture, internal/live/discovery/testdata/collision-matrix
	// (identityGoldenPinInstances moves in step - see its own note).
	// Then 628 -> 629, issue #541's deterministic-identity fixture: one new
	// fixture, live/e2e/deterministic-recreate (identityGoldenPinInstances
	// moves in step - see its own note).
	// Then 629 -> 641, GitHub issue #580's module-boundary fix: six new
	// fixtures under internal/live/lint/testdata/count-index-module-foreach*,
	// each of which is two directories (the root and its ./m child), so
	// twelve (identityGoldenPinInstances moves in step - see its own note).
	// Then 641 -> 642, GitHub issue #585's read-path concurrency: one new
	// fixture, internal/live/projection/testdata/read-parallel
	// (identityGoldenPinInstances moves in step - see its own note).
	// Then 642 -> 643, GitHub issue #790 (live-check -json's declared
	// roster and references[]): one new fixture, live/e2e/estate-references
	// (identityGoldenPinInstances moves in step - see its own note).
	identityGoldenPinDirs = 643

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
