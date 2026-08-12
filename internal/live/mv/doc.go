// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package mv performs the rename operation stateless mode has instead of
// `moved` blocks and state surgery: it rewrites the tofu-address ownership
// marker on one live resource.
//
// live/MARKERS.md, "The rename rule", is the specification. A resource's
// address lives in one tag value, so overwriting that value *is* the move -
// there is no record to edit, no two-step "mark old, create new" dance, and
// no way for the old address to linger, because a single tag value cannot
// hold two addresses at once. After the write, a plan against the old address
// finds nothing and a plan against the new one finds the resource already
// bound.
//
// # Finding the resource
//
// Two paths, chosen by the admission rule's own ordering - strongest claim
// first - rather than by what the provider happens to be able to list:
//
//   - The identity path, for an instance whose identity is in configuration
//     (a bucket name, a role name, a composite of both). The resource is
//     materialized directly from that identity and its markers are read off
//     the object. The identity is the stronger claim by construction: it says
//     which cloud object this address means without trusting a tag anyone
//     could have edited.
//   - The list path, for an instance whose identity is assigned by the
//     provider at create time and appears nowhere in configuration. Every live
//     resource of the type is enumerated and the one carrying this estate's
//     tofu-estate tag and the old escaped address is the subject, because the
//     marker is the only thing connecting a live ID to an address. A type in
//     this position that the provider cannot list is not findable at all, and
//     that is a named error rather than a silent miss.
//
// The identity path also lists, when the provider offers it, for one thing:
// answering whether anything else already claims the destination address. A
// collision refusal that could only be made for half the resource types would
// be worth very little.
//
// Every list is deliberately unfiltered rather than server-side filtered by
// tofu-estate. A rename is a rare, interactive operation, so the wider scan
// costs nothing worth having, and it sidesteps the owner-id trap that a
// filtered EC2 list walks into when the provider has resolved no account
// (see discovery's ProblemUnresolvedAccount).
//
// # The write
//
// The rewrite is a real, minimal apply of a tags-only change through the
// provider, not a shell out to a cloud CLI and not provider-specific code:
//
//  1. The resource is materialized exactly as a projection materializes it -
//     ImportResourceState then ReadResource, through [projection.BuildFrom] -
//     so the prior state fed to the provider is the live object.
//  2. The same object is synthesized with one difference: the tags map
//     carries tofu-address = the new escaped address (and tofu-estate = this
//     estate, which is already its value in every ordinary case).
//  3. PlanResourceChange and then ApplyResourceChange run for that single
//     instance, with the configuration value derived from the live object by
//     nulling the attributes only the provider can set - the shape a
//     configuration that fully specified this resource would have.
//
// Two refusals guard the apply. A plan that requires replacement is never
// applied: a rename must never destroy anything. Neither is a plan that
// changes any attribute other than tags and tags_all - if the provider
// proposes more than the marker, the operator hears about it instead of
// getting it silently.
//
// # What this package does not do
//
// It does not touch configuration. Renaming the resource block is the
// operator's edit, and this package's job is to make the live system agree
// with it; by default it refuses to run at all until the destination address
// exists in configuration, which is the ordering that leaves no window where
// a marker names an address nothing declares.
package mv
