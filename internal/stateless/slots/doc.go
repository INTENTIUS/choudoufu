// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package slots is the set matcher for count instances: the rule that turns
// "N declared instances and M live resources" into a binding, without any
// index participating in identity.
//
// A count block declares a cardinality, not a list. Its instances are
// interchangeable by construction - the lint boundary (P3.4) makes sure no
// argument can read count.index, so nothing about instance 2 distinguishes it
// from instance 0 - and the lexical index is therefore an artifact of
// expansion rather than a name. The tofu-slot marker (stateless/MARKERS.md)
// is the name: an opaque, monotonic, never-reused integer minted once when an
// instance is first created, carried on the live resource as a tag, and
// compared numerically.
//
// # The matching rule
//
// [Match] takes the declared count and the live members of the set, each
// carrying its slot, and pairs them in one deterministic sweep:
//
//   - Sort the live members by slot, ascending and numerically.
//   - Pair the k-th lowest slot with index k.
//   - Live members past the declared count (the HIGHEST slots) are surplus.
//   - Declared indices past the live count are deficit: they have no live
//     resource, so they plan as creates, and each is minted a fresh slot.
//
// Sorting by slot rather than by anything about the resource is what makes
// the answer stable across runs: the same live set and the same count produce
// the same pairing whatever order the provider listed them in, whatever their
// identities are, and whatever their tofu-address tags happen to say.
//
// Surplus being the highest slots is the whole of the scale-down semantics.
// It is not an arbitrary tie-break: it is the only choice that leaves every
// survivor on the index it already occupied, so shrinking a count from 3 to 2
// moves nothing. Deleting the lowest slot instead would renumber every
// survivor and turn a one-resource delete into a churn of the whole set.
//
// # Minting
//
// A minted slot is one past the highest slot in the live set, and mints
// continue upward from there. The high-water mark counts the surplus members
// too, so a run that shrinks and grows in the same breath cannot hand a
// departing member's slot to an arriving one.
//
// The mark is the highest slot this run can see, and a stateless estate can
// see only what is live: nothing records the slot of a resource that has
// already been deleted. MARKERS.md's "never reused, even after that instance
// is deleted" therefore holds exactly as far as the estate's own memory goes,
// which is the live tags. Scale 3 down to 2 and back up to 3 and the slot the
// scale-down retired is minted again, because after the delete there is no
// artifact anywhere that says it ever existed. See [Match] for what that
// costs and does not cost.
//
// # What this package does not decide
//
// It knows nothing about addresses, configuration, or the cloud. Which live
// resources belong to which count block, what to do about a resource carrying
// a stale tofu-address, and how a surplus member reaches the prior state are
// all discovery's business (internal/stateless/discovery); this package is
// the arithmetic underneath.
package slots
