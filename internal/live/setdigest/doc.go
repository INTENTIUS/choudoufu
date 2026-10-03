// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package setdigest is GitHub issue #1754's set digest: one digest over a
// set of per-root plans, order-independent, moving whenever any one root's
// change set moves. internal/live/setplan prints it and internal/live/waves
// checks an approval against it.
//
// It imports the standard library only, and must keep doing so:
// internal/live/setplan reaches internal/command/views and from there
// almost every package, so anything this package imported would join that
// graph (the import cycles that broke #1816's first gate).
// TestImportsStdlibOnly holds it.
//
// # What a root digest covers
//
// SHA-256 over a canonical encoding of what that root's plan would change,
// and nothing that varies between two plans that change the same things:
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
package setdigest
