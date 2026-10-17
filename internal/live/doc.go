// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package live implements OpenTofu's live mode. An estate's live backend is
// three pieces: identity carried as ownership markers (tags, or a label on
// Kubernetes) on the objects themselves, the values a provider never echoes
// back held in the record store, and the effects of an apply as receipts.
// The state file a run writes is a cache: a projection of those three that
// may always be thrown away.
//
// # Records
//
// Every managed instance has a record in the estate's record store
// (internal/live/staterecord: a local directory by default, or an s3 or
// kubernetes store for a team): its identity, the arguments the provider
// does not return, sensitivity marks, taint and deposed keys, one per
// resource, namespaced per estate and written with compare-and-swap.
// live-import and every apply write it.
//
// Binding is record first (the foundation-order ruling, #388): a plan reads
// an instance's record and verifies it against the live object's marker.
// Marker discovery (internal/live/discovery) and derivation from
// configuration are the recovery paths, taken when there is no record or the
// record and the marker disagree. A record is never read as permission to
// delete; the marker decides ownership.
//
// # Projections and the cache
//
// A projection is the prior state a run plans against, built at the start
// of the operation from records, markers and live reads, and fed to the
// unchanged plan engine in place of a state read. Same shape as a
// states.State, none of the authority.
//
// After a run, the projection is written to choudoufu-cache.tfstate under
// the data dir (#685). Three rules govern it, pinned by
// live/stale_state_ruling_test.go: the cache is never consulted for
// ownership; when cache and live disagree, live wins; losing the cache costs
// a slower run and nothing else. A cached object stands in for a read only
// for an instance the sweep vouched for in the same run, and only under
// -refresh=false. A wrong or missing cache costs reads and cannot change a
// plan.
//
// # Admission
//
// Admission is how an instance's identity is recovered without its record,
// which is what rebuilds a lost record and checks a present one. Four paths,
// strongest first:
//
//  1. Client-assigned identity: the name is already in the configuration
//     (a bucket name, a role name, a log group name). Nothing to recover.
//  2. Marker: the resource carries ownership tags, defined in
//     live/MARKERS.md, and is found by a tag-filtered list call.
//  3. Parent-derived: identity is a composite key built from already-admitted
//     parents, such as a route keyed by route table and destination, or an
//     association keyed by subnet and route table.
//
// A resource type with none of these three paths holds its identity in its
// record alone, the record rung (identity.ClassRecordBacked and
// identity.ClassRecordLocated); lint checks before a projection is built
// that such a type has a record store to live in.
//
// A fourth exists, narrowly: "list and content match" (issue #272,
// discovery/contentmatch.go). It applies only to the handful of types in
// identity.ContentMatchTypes - types with no tags argument at all, admitted
// on a two-source uniqueness proof - and it binds a SINGLE listed candidate
// whose content matches the declared instance, refusing ambiguity. For
// every other type the old statement still holds: the Cloud Control leg
// lists an object, refines it with GetResource, and DISCARDS it when
// neither carried tags (cloudcontrol.go's ProblemNoTags, an error), and the
// foreign-resources paragraph below - a content match "surfaced for
// explicit adoption and never bound automatically" - describes that general
// case, not the ContentMatchTypes carve-out. (An earlier revision of this
// comment denied the path outright while its own subpackage implemented it;
// the 2026-09-02 architecture review caught the contradiction.)
//
// Enumerability is still a real and separately useful fact about a type: it
// is what a sweep needs to notice an undeclared object at all. It is not an
// admission path, because admission is about naming the object this
// configuration means, and a listing that cannot read a marker names nothing.
// tools/survey-gen records the enumeration fact under its own label for that
// reason.
//
// # Foreign resources
//
// A live resource of an in-scope type with no admission path binding it to
// configuration is foreign: unrecognized, unmanaged, and never
// auto-deleted. Live mode reports it for review rather than guessing
// whether it should be adopted or destroyed. A foreign resource that
// happens to match a declared-but-unbound resource is a bind candidate,
// surfaced for explicit adoption and never bound automatically.
//
// # Count
//
// count survives live mode as cardinality over a fungible set rather
// than as a positional index. Each instance of such a set carries a
// tofu-slot marker: a stable, opaque identifier assigned once at creation
// and never reused. Binding N declared instances against M live, owned
// instances is set matching, not index matching: a deficit creates new
// slots, a surplus deletes the highest slots.
//
// A count block is a fungible set only when the configuration does not
// itself say which live resource is which instance. lint admits
// count.index in an identity-bearing argument where it can prove each
// instance renders a distinct, scale-down-stable value (see
// internal/live/lint's analyzeCountIndexSafety), and a block that names
// its members that way resolves identity.ClassConcrete per instance:
// discovery never indexes it as a count set, no slot is minted, and its
// instances carry tofu-estate and tofu-address alone. See live/MARKERS.md,
// "Which count instances carry one" - and GitHub issue #969, which is what
// reading the old wording here and in the spec ("each instance carries a
// tofu-slot marker") led an operator to expect. The count = var.enabled ?
// 1 : 0 idiom keeps working unchanged, and is one of the shapes that
// carries no slot.
//
// # Where the phases land
//
// The projection is built and consumed at three seams in the existing
// codebase, not in a parallel state system:
//
//   - internal/states/statemgr: projection.Manager is the state manager a
//     live run uses. The prior state does not arrive through its Refresh;
//     the run's own pipeline builds the projection and hands it over.
//     Persist writes the cache (#685) and, with a record store, guided
//     discovery's plan-cost hint (a type roster and a timestamp; issue
//     #109). Neither is read back as truth, and neither can fail the
//     operation.
//   - Import machinery in internal/tofu: resource identity is merged
//     upstream (opentofu#2854); the same import-by-identity path that
//     backs tofu import is how a projection materializes a states.State
//     for the client-named and parent-derived admission paths.
//   - The provider list protocol: the mechanism behind admission path 4
//     and behind tag-filtered marker discovery. Core-side client support
//     tracks the shape of opentofu#3787 to stay mergeable with upstream.
//
// This package holds the model. Concrete implementations live in its
// subpackages: lint, identity, discovery, stamp, projection, and the rest.
// The operator-facing specs are in live/.
package live
