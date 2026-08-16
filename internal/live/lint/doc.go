// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package lint is the stateless-mode subset check: the pass that decides
// whether a configuration can be planned with no authoritative state at all.
//
// It runs against an already-loaded [configs.Config] — the same tree the
// commands build via Meta.loadConfig — before any identity resolution or
// projection work begins, so that a configuration outside the subset fails
// with an explanation instead of failing later as a confusing plan.
//
// # What it rejects
//
// Every rule here traces back to one of two sections of the roadmap. The
// "Banned, and why" section covers constructs that exist only to maintain or
// repair a state file: provisioners and connection blocks, moved blocks,
// logical resources whose existence is the store (random_, tls_, time_,
// null_, local_), and backend or cloud blocks. terraform_remote_state used
// to be named here too; issue #179 stage 3 gave it its own read pipeline
// (internal/live/dataread) instead, so this package no longer has a rule for
// it at all - a reference to it now succeeds or refuses through the same
// data-source eligibility and read-time classes any other cross-stack
// source draws. The "The admission rule" section covers resource types:
// a type participates only if its identity is recoverable with no memory. The
// set that qualifies is generated into admission_generated.go by
// tools/row-gen -emit; admission.go holds only admitted(), which reads that
// set and then falls back to the provider's own resource identity schema
// plus the configuration's naming signal (issue #22).
//
// # Why an Issue slice and not tfdiags
//
// [CheckContext] returns a []Issue rather than tfdiags.Diagnostics. An Issue is a
// structured record — the rule that fired, the construct that tripped it, the
// module it was found in, the roadmap section that justifies it, and a source
// range — so tests can assert on the rule and the location without matching
// rendered English, and so a later caller (a --json mode, a report, the
// live-mv flow) can group or filter by rule.
//
// P1.4 wires this into the live-plan command, which speaks tfdiags like
// every other command, so [Diagnostics] converts a slice of issues into
// diagnostics carrying the same summary, detail, and source range. That
// direction is one-way and lossless enough: nothing needs to recover an Issue
// from a diagnostic. Returning tfdiags directly would have cost the structure
// for no gain, since the command re-renders them anyway.
//
// # Scope of v0
//
// Rules that classify a resource type — the logical-resource ban and the
// admission table — apply to managed resources only. A data source is
// re-read on every operation and stores nothing, so it has no identity to
// recover and no admission question to answer; this package has no
// data-source rule at all as of issue #179 stage 3, which moved
// terraform_remote_state's coverage to internal/live/dataread alongside
// every other data source. Ephemeral resources are likewise untouched by
// the type rules.
//
// The check walks the whole module tree, including child modules, and reports
// the module path on each issue.
//
// # Scope of the count.index rule
//
// [RuleCountIndex] (count_index.go) is the one rule that does expression
// analysis rather than construct or type classification: count.index is
// rejected wherever it is reachable from an argument that could plausibly
// feed the resource's own live identity, because a property built from the
// lexical index of a count instance cannot be recovered from the live
// system with no memory, and it makes that instance non-fungible with its
// siblings (live/LIMITATIONS.md, "count-index-in-tag"). It is not a ban on
// count.index in general — count survives under live resource markers as
// cardinality over a fungible set, and plenty of a resource's own arguments
// (a tag, a description, an ordinary non-identity property) are exactly
// that: fungible content that does not change which live object a config
// address refers to.
//
// What counts as identity-relevant is read from the same data
// [identity.Resolve] itself trusts (countIndexScopeForType,
// count_index.go), not asserted independently:
//
//   - A [ClassRecordAdmitted] logical type (logical_type.go) or a
//     [identity.TypeIdentity.RecordBacked] type has no argument-derived
//     identity at all — its whole existence is a persisted micro-state
//     record addressed by the resource's own instance address — so nothing
//     in its body is in scope.
//   - A [identity.TypeIdentity.ServerAssigned] type's Resolve
//     (internal/live/identity/resolve.go) returns ClassNeedsDiscovery
//     before reading a single configuration attribute; discovery then
//     matches the live object by its tofu-address marker, which
//     internal/live/stamp recomputes fresh from the resource's own address
//     at every run, never from any argument. Nothing in such a type's body
//     is in scope either.
//   - Otherwise the type's [identity.TypeIdentity.Components] name exactly
//     which top-level arguments build its import identity
//     (internal/live/identity/resolve.go's identityArgs reads only those,
//     via a top-level-only PartialContent) — those, and only those, are in
//     scope. A nested block can never be identity-relevant this way,
//     because Components never names one.
//   - A type absent from the table entirely gets no narrowing: every
//     argument at every depth stays in scope, the conservative default for
//     a type nobody has reviewed.
//
// Within an in-scope argument the walk is unchanged from before this
// narrowing: a plain reference, a tag-shaped map value, a conditional, or a
// template all get caught the same way, because [exprVariables] inspects
// the expression's referenced variables rather than matching literal text.
// The line is also still drawn at the resource body, not at the resource
// block: the count expression itself, and the depends_on, provider,
// lifecycle, connection, and provisioner positions, are meta-arguments that
// cannot leak into a resource's identity-bearing properties, so count.index
// is left alone there regardless of scope. Concretely, the walk starts from
// [configs.Resource.Config] — the body [configs.decodeResourceBlock] leaves
// behind after extracting exactly those meta-argument names via
// [configs.ResourceBlockSchema] — and skips the same names again at the top
// level as a second, explicit check, since the extraction only marks them
// hidden rather than removing them from the underlying body. count
// survives, unrejected, wherever it is used only for cardinality:
// `count = 3`, `count = var.enabled ? 1 : 0`, and a resource that declares
// count but never reads count.index are all clean.
//
// One further exemption lives inside the walk itself, not at the
// resource-body boundary: a `tags = { ... }`-shaped object's tofu-address
// entry (live/MARKERS.md) is allowed to contain count.index, because a
// count instance's canonical address permanently includes its instance
// index — the marker doing its specified job, not a leak into an
// identity-bearing property. Every other key in that same object, when the
// object's own attribute is in scope at all, is still checked; see
// markerKeysExemptFromCountIndex in count_index.go for the one key this
// applies to and why tofu-slot is deliberately not among them.
package lint
