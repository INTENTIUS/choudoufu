// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package projection materializes an ephemeral prior state by reading the
// live system. It is the second half of stateless mode's answer to "what
// already exists": the identity package decides what OpenTofu can name,
// and this package goes and fetches it.
//
// [Build] takes a configuration, the identity package's classification of
// it, and a way to reach configured providers, and returns a
// [states.State] holding one resource instance object per live object it
// could find. Nothing is written to disk, ever: the state is built in
// memory, handed to the caller, and expected to be discarded when the
// operation that asked for it ends. This package has no file access and no
// statemgr dependency, so that property is structural rather than a
// promise.
//
// # What gets materialized
//
// The identity package hands over three classes of instance, and this
// package treats each differently:
//
//   - [identity.ClassConcrete] instances have an import ID already. They
//     are materialized first, in address order.
//   - [identity.ClassParentDerived] instances have a formula whose inputs
//     are parents' live IDs. They are materialized after their parents, in
//     dependency order, with the formula rendered from the parents'
//     just-materialized attribute values.
//   - [identity.ClassNeedsDiscovery] instances have no identity at all
//     until P2's marker discovery lands. They are omitted, with a reason.
//
// A parent-derived instance whose parent is itself absent from the
// projection - because the parent needs discovery, because the parent's
// live object does not exist, or because reading the parent failed - has no
// identity to render, and is omitted with a reason naming the parent. That
// is not a silent hole: [Result.Omitted] records every one of them, with
// the address, the machine-readable [Reason], and a sentence for an
// operator.
//
// # Ownership is checked before anything is admitted
//
// Finding a live object at an instance's identity is not the same as that
// object being this estate's, and the difference matters most exactly where
// it was missed: a client-named resource's identity comes out of the
// configuration, so naming an existing bucket or log group used to be enough
// to read it into prior state - to plan updates against somebody else's
// resource, and to destroy it when the block went away. [Options.Ownership]
// closes that: a live object whose type can carry an ownership marker enters
// the state only when it carries this estate's, or when the caller already
// established ownership through marker discovery. Everything else is recorded
// in [Result.Unowned], omitted with [ReasonUnowned], and left completely
// alone - which makes the subsequent plan propose creating what the
// configuration declares, the same answer it gives for an object that is not
// there at all.
//
// The check reads the tags on the object the provider just returned, never
// the tags the configuration declares. In the case it exists for, the
// configuration is the thing making the unfounded claim.
//
// # Absence is not an error
//
// An instance the projection cannot materialize is simply not in the
// returned state, which makes the subsequent plan propose creating it.
// That is the correct answer when the live object really does not exist,
// and an over-eager answer when the object exists but stateless mode
// cannot yet find it (the needs-discovery case, until P2). Neither is a
// failure of this package, so neither produces an error diagnostic. What
// does produce an error diagnostic is a provider misbehaving: an import
// call that returns an error, a read that returns an object that does not
// conform to the provider's own schema, a formula whose parent turns out
// not to have the attribute the formula reads.
//
// The distinction is drawn at the provider protocol level. An import of a
// nonexistent object is normal traffic: the provider either returns no
// imported resources at all, or returns a stub whose subsequent
// ReadResource yields a null object. Both mean "not there". An import that
// returns diagnostics with error severity means the provider could not
// answer the question, which is a different thing entirely and is reported
// as such.
//
// # The import seam
//
// Materializing one instance is exactly what tofu import does, minus the
// graph: call the provider's ImportResourceState with the import ID, then
// ReadResource on what comes back to refresh it into a complete object.
// This package calls those two provider methods directly rather than
// building a [tofu.Context] and walking an import graph.
//
// The trade is deliberate. The graph path (graphNodeImportState and its
// graphNodeImportStateSub) carries a large amount of machinery this
// package has no use for: provider resolution from graph edges, hooks,
// evaluation contexts, the "resource already managed by OpenTofu" check
// that exists because import writes to a real state file, and the
// config-marks merge that needs a live EvalContext. Underneath all of it,
// the two provider calls and the schema-conformance handling are the whole
// operation, and they are reproduced here in about eighty lines with the
// same semantics: same request shapes, same normalization
// (objchange.NormalizeObjectFromLegacySDK), same conformance assertion.
// What is deliberately not reproduced is import's treatment of a
// nonexistent object as a hard error, because for a projection it is a
// routine outcome (see above).
//
// The cost of that choice is that this code has to track changes to the
// refresh path in internal/tofu by hand. The benefit is that a projection
// can be built from a bare provider instance, which means the unit tests
// in this package drive a mock provider directly with no graph, no
// EvalContext, and no plan walk, and the integration test drives the real
// AWS provider plugin the same way.
//
// # Dependencies
//
// Each materialized object records the configuration-level dependencies of
// its resource block, derived from the references in the block's body. The
// plan engine uses these to order destroys for objects whose configuration
// has gone away, so a projection that omitted them would produce correct
// plans right up until someone deleted a resource block.
package projection
