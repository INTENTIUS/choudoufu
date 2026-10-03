// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package waves is GitHub issue #1754's primitives for rolling one change
// across many estate roots (epic #1749): a digest over a set of per-root
// plans, and a split of the set into ordered waves. The 2026-09-30 ruling
// bounds it: choudoufu supplies primitives, and where an approval lives is
// chant's side. Nothing here stores an approval.
//
// # The set digest
//
// A root's digest is SHA-256 over a canonical encoding of what that root's
// plan would change, and nothing that varies between two plans that change
// the same things. It covers:
//
//   - the root's directory as the set names it, and its estate;
//   - its status and, for a failed root, its error text, so a failed root
//     never shares a digest with a planned one;
//   - whether stock marked the plan errored;
//   - every element of the plan's resource_changes whose actions are not
//     exactly ["no-op"], whole and unedited: address, deposed key, actions,
//     before, after, after_unknown, the sensitivity maps, replace_paths,
//     action_reason, importing, every field stock writes there;
//   - every entry of output_changes whose actions are not ["no-op"].
//
// Each JSON value is decoded and re-encoded so that key order and
// whitespace cannot move the digest, numbers keep their literal text, and
// resource_changes are sorted by address then deposed key, which are unique
// within a plan. Nothing is normalized away: two plans that differ in any
// value they would write have different digests. The no-op entries are left
// out because they write nothing, so the digest does not move when a
// resource the plan does not touch drifts. The top-level timestamp,
// prior_state, configuration and resource_drift are left out for the same
// reason: they describe the plan, not what it changes.
//
// The set digest is SHA-256 over the root digests, each framed with its
// root's directory, in directory order. It is therefore independent of the
// order roots were given in and changes whenever any one root's digest
// does. A set that names one directory twice has no digest.
//
// # Waves
//
// [Split] orders a set of roots so that no root lands in a wave before a
// root whose estate it reads. A read is what [ReadsOf] finds in
// configuration: a data source filtered on the producer's tofu-estate marker
// (live/OUTPUTS.md's pattern), whether by a "filter" block or a "tags"
// argument, and a terraform_estate_outputs data source (#1371). A read of an
// estate outside the set orders nothing and is reported.
//
// The explicit canaries, when there are any, are wave 1. Every other root
// lands in the earliest wave after every root it reads. A canary that reads
// a root of the set which is not itself a canary is refused, because wave 1
// would then land a reader before what it reads; two canaries may read each
// other's estates in one direction, and wave 1 then has an order of its own,
// which [Wave.Edges] carries. A cycle among the roots is refused with the
// cycle named, since no order puts every reader after what it reads.
package waves
