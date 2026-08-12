// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package stateless implements OpenTofu's stateless mode: a run mode with
// no authoritative state file, no backend, and no lock. Identity is
// recovered from the live system on every operation instead of being read
// from a stored record.
//
// # Projections
//
// The state data structure does not disappear; its authority does. A
// projection is an ephemeral prior-state, built at the start of an
// operation by reading the live system and discarded once the operation
// completes. It is fed to the unchanged plan engine in place of a state
// read and is never persisted. Same shape as a states.State, none of the
// authority: nothing downstream trusts a projection to be correct, because
// nothing depends on it surviving past the run that built it.
//
// Contrast with authoritative state, whose test is that a wrong record
// makes OpenTofu do the wrong thing to the world. A wrong projection costs
// at most a re-plan.
//
// # Admission
//
// A resource type participates in stateless mode only if its identity is
// recoverable with no memory. Four paths admit a resource, strongest first:
//
//  1. Client-assigned identity: the name is already in the configuration
//     (a bucket name, a role name, a log group name). Nothing to recover.
//  2. Marker: the resource carries ownership tags, defined in
//     stateless/MARKERS.md, and is found by a tag-filtered list call.
//  3. Parent-derived: identity is a composite key built from already-admitted
//     parents, such as a route keyed by route table and destination, or an
//     association keyed by subnet and route table.
//  4. List and content match: the provider's list protocol enumerates
//     candidates and binding proceeds by content; identical siblings bind
//     as a fungible set (see Count below).
//
// A resource type with none of these four paths is out of the stateless
// subset and is rejected by lint before a projection is ever built.
//
// # Foreign resources
//
// A live resource of an in-scope type with no admission path binding it to
// configuration is foreign: unrecognized, unmanaged, and never
// auto-deleted. Stateless mode reports it for review rather than guessing
// whether it should be adopted or destroyed. A foreign resource that
// happens to match a declared-but-unbound resource is a bind candidate,
// surfaced for explicit adoption and never bound automatically.
//
// # Count
//
// count survives stateless mode as cardinality over a fungible set rather
// than as a positional index. Each instance carries a tofu-slot marker: a
// stable, opaque identifier assigned once at creation and never reused.
// Binding N declared instances against M live, owned instances is set
// matching, not index matching: a deficit creates new slots, a surplus
// deletes the highest slots. Nothing about identity depends on
// count.index, which is why count.index is banned from identity-bearing
// resource arguments while the count = var.enabled ? 1 : 0 idiom keeps
// working unchanged.
//
// # Where the phases land
//
// The projection is built and consumed at three seams in the existing
// codebase, not in a parallel state system:
//
//   - internal/states/statemgr: Filesystem is the model for a projection
//     manager. A projection manager's Refresh/State builds its snapshot
//     from live reads instead of a file, and Persist writes nothing (or,
//     for the optional snapshot cache, a scrubbed, observational-only
//     record that no code path reads back as truth).
//   - Import machinery in internal/tofu: resource identity is merged
//     upstream (opentofu#2854); the same import-by-identity path that
//     backs tofu import is how a projection materializes a states.State
//     for the client-named and parent-derived admission paths.
//   - The provider list protocol: the mechanism behind admission path 4
//     and behind tag-filtered marker discovery. Core-side client support
//     tracks the shape of opentofu#3787 to stay mergeable with upstream.
//
// This package holds the model. Concrete implementations live in its
// subpackages as they land: lint, identity, projection, and the rest of
// the roadmap in stateless/.
package stateless
