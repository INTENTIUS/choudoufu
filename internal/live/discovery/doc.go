// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package discovery finds the live resources of a stateless estate by their
// ownership markers and binds them to the addresses that declare them.
//
// It is admission path 2 (live/MARKERS.md): a resource whose identity
// the provider assigned at create time carries a tofu-estate tag naming its
// estate and a tofu-address tag naming its place in configuration, and those
// two tags are the whole recovery mechanism. [Discover] lists the live
// resources of each needs-discovery type, reads those tags, and turns the
// instances the identity package could not name into
// [identity.ClassConcrete] resolutions with a real import ID.
//
// The output is shaped for [projection.BuildFrom]: [Result.Resolutions] is
// the caller's original resolution list with every bound instance replaced
// by its concrete form, so the whole of P2.5's wiring is
//
//	res, diags := discovery.Discover(ctx, discovery.Request{...})
//	proj, diags := projection.BuildFrom(ctx, cfg, res.Resolutions, provs)
//
// and the parent-derived chains that bottomed out in a server-assigned
// parent in phase 1 materialize with no further work.
//
// # Comparison is escaped-to-escaped, never decoded
//
// AWS tag values cannot hold [, ] or ", so an address is escaped before it
// is written as a tag: [ becomes :, and ] and " are dropped. The spec is
// explicit that no code path reverses that transformation, because the
// escaping is lossy by design. Binding therefore escapes the address the
// configuration declares and compares strings, and this package has no
// unescape function to be tempted by.
//
// One tolerance is layered on top, and it is deliberate rather than
// accidental: the observed tag value is itself run through [EscapeAddress]
// before the comparison. Escaping is idempotent over already-escaped values
// (they contain none of the three characters it rewrites), so a
// spec-conformant marker is unaffected, while a marker written by a tool
// that predates the escaping rule - the P0.1 estate fixture writes
// aws_eip.pool[0] literally - still binds. That is normalization of an
// observed value, not decoding: nothing is ever turned back into an
// unescaped address. A binding that needed it sets [Binding.Normalized] so
// a caller can report the estate as carrying pre-spec markers.
//
// # What this package refuses to guess
//
// Every ambiguity the marker spec names is a [Problem] with an error
// diagnostic beside it, never a guess:
//
//   - Two live resources claiming the same estate and address: a collision,
//     naming both live IDs.
//   - A live resource carrying tofu-estate with a missing or unparseable
//     tofu-address: malformed, named as such rather than treated as either
//     unowned or "close enough" to some address.
//   - A live resource whose tofu-address names an address of another type -
//     a subnet tagged aws_eip.pool:0, or an address inside a child module:
//     also malformed. A marker names the resource it is written on, so its
//     leading segment is that resource's own type; anything else describes a
//     resource this one is not. These used to vanish (audit finding C4): the
//     declared-address set they were checked against carried no type key, so
//     a cross-type marker matched it and the resource was dropped without
//     appearing anywhere at all.
//   - Several live resources sharing one count instance's address with no
//     slot markers to tell them apart: not a plain collision but the absence
//     of the marker that answers it, reported as [ProblemNeedsSlotMarkers]
//     so that nothing binds arbitrarily.
//   - A count set where some live members carry a tofu-slot and some do not:
//     two answers to which member is which, reported as
//     [ProblemMixedSlots] rather than resolved by preferring one.
//   - A slot value outside the marker grammar, or one claimed by two live
//     resources at once: [ProblemMalformedSlot] and [ProblemDuplicateSlot].
//
// # count instances bind as a set
//
// A count block declares a cardinality, not a list of addresses. Its
// instances are interchangeable - the lint boundary makes sure no argument
// can read count.index - so "which live resource is instance 2" is a
// question about the whole block, and it is answered by the tofu-slot
// marker: an opaque monotonic integer carried on the resource, compared
// numerically, minted once and not derived from any lexical position.
//
// [Discover] therefore gathers every live resource that named a count block -
// by its bare address, by one of its current instance addresses, or by an
// instance address the configuration no longer expands to - and hands the
// whole set to internal/live/slots. Ascending slot pairs with ascending
// index; live members past the declared count come back in [Result.Surplus]
// at the instance addresses just above it, which is where a shrunken count's
// leftovers sit in a stock run's prior state, so the plan engine's own orphan
// handling destroys them; declared indices with no live member stay unbound
// and are minted fresh slots in [Result.Slots], which marker stamping writes
// so a create carries its slot from birth.
//
// A set where no member carries a slot binds by address, exactly as it did
// before slots existed, and [Result.Slots] carries the assignment that
// migrates it - slot i for index i, which is the only assignment that leaves
// the existing binding alone.
//
// # Slots bind; addresses follow
//
// When a count set carries slots, the slot decides which instance a live
// resource is, and its tofu-address tag has no say. A member's address can
// disagree with where it bound - destroy the lowest slot of [0 1 2] out of
// band and the survivors rebind to indices 0 and 1 while still tagged
// aws_eip.pool:1 and aws_eip.pool:2 - and that disagreement is a stale tag
// rather than a competing claim. Such a binding sets [Binding.AddressStale].
//
// The stale tag is repaired rather than tolerated, and the repair needs no
// machinery: marker stamping writes tofu-address from the index the slot
// bound to, so the plan shows an ordinary in-place tags update and an apply
// writes it. That is deliberate and not merely convenient. MARKERS.md makes
// tofu-address mandatory on every managed resource and treats a missing or
// unparseable one as malformed, so the tag is not optional for a slotted
// resource either - and a mandatory tag that is allowed to be wrong is worse
// than no tag, because a tool reading the spec as written (chant, or
// anything else honoring the three keys) would bind the wrong resource. The
// spec also says old markers never linger: "there is exactly one
// tofu-address value on a resource at any time, and after a rewrite that
// value is the new address, not a history of addresses it once had." A stale
// index is exactly such a history.
//
// The alternative - declaring the address advisory for slotted resources -
// was rejected because it silently narrows the contract for every reader
// that has not heard of slots, which is the one thing an interop spec
// cannot afford.
//
// Note what the repair does not do: it never rewrites a slot. Rewriting a
// slot would rename a member of the set, and a rebinding is not a rename.
//
// A declared address that nothing claims is not an error: the resource does
// not exist and the plan should propose creating it. Those instances stay in
// [Result.Resolutions] as needs-discovery and appear in [Result.Unbound].
//
// A live resource carrying a different estate's tofu-estate value is ignored
// entirely - not unclaimed, not foreign, not this estate's business.
//
// # Unclaimed resources are collected, not classified
//
// Live resources of an in-scope type carrying no tofu-estate tag at all come
// back in [Result.Unclaimed] with their identity, tags and full object, and
// this package draws no conclusion about them. Deciding which are foreign,
// which are bind candidates for a declared-but-unbound address, and how they
// are protected from deletion is P2.4's job, built on that return value.
//
// Seeing them at all costs a wider list call, so it is opt-in:
// [Request.CollectUnclaimed] switches the scan from the server-side
// tag-filtered list to an unfiltered one. With it off, [Result.Unclaimed] is
// empty because nothing looked, which is not the same as "there are none" -
// [TypeScan.Scope] records which of the two happened per type.
//
// Even a wide scan is not a census, and P2.4 must not treat it as one. The
// AWS provider appends filters of its own to every EC2 list, and one of them
// is is-default = false, so an account's default VPC never appears in a list
// of aws_vpc at all. Against floci the wide scan finds the default subnets,
// route tables, gateway and security groups but not the default VPC, and no
// call this package can make changes that.
//
// # Guided discovery (issue #64)
//
// [Request.Guided] is an opt-in (default off) cost optimization over the
// estate-wide sweep, nothing more: it reads the most recent hint the
// estate's record store carries (issue #109; written by
// internal/live/projection after every apply) through
// [projection.ReadHintStore] as a HINT of which
// admitted types this estate has ever held, and skips re-listing a type the
// hint has no record of on a routine pass rather than paying one List call
// per admitted type on every plan.
//
// The hint is never authority, and the package's central safety claim -
// nothing here guesses at ownership - extends to it without exception. A
// type absent from the hint is swept in full on every run regardless, and
// any problem trusting the hint at all (no store configured, a missing or
// corrupted hint, one older than [Request.GuidedMaxAge]) falls back to
// exactly today's full enumeration, silently: [Result.GuidedFallback] names
// why for an operator who wants to know, and Discover never returns an
// error for it. See guided.go and TestGuided_equivalence for the mechanism
// and its proof: guided discovery, given any such problem, produces
// byte-identical output to an unguided pass over the same estate, only
// slower.
//
// What guided mode trades away, on purpose, on a routine (non-verification)
// pass: a type the hint already has evidence for is not re-swept this run,
// so a standing orphan of that type may not resurface on every single plan -
// only at the next full sweep or the next [Request.GuidedVerify] pass, which
// a caller schedules on its own cadence (Discover is stateless between
// calls and only honors the flag it is given). That is a cost/latency trade
// on when a real removal gets proposed, never a change to what gets
// destroyed once it is - the only thing Guided ever changes is which run
// notices something, not what the something is.
//
// # Server-side filtering and the owner-id trap
//
// Where a type's list configuration schema offers EC2-style filter blocks,
// the estate filter is pushed to the server as tag:tofu-estate = <estate>,
// so a large account is not swept client-side. Types whose list schema has
// no filter argument - aws_eip is the one in the v0 subset - are listed
// whole and filtered here, with the reason recorded in [TypeScan].
//
// The AWS provider appends its own filters to whatever the configuration
// asks for, and one of them is owner-id, taken from the account ID it
// resolved at configure time. With skip_requesting_account_id = true the
// provider never resolves one and sends owner-id with an empty value, which
// an emulator ignores and real EC2 matches nothing against. The estate
// fixture therefore does not set that flag: it lets the provider call STS
// GetCallerIdentity once at configure time, which is what makes a filtered
// list correct against real AWS. Discovery cannot strip the filter, because
// the provider builds it after the configuration this package supplies, so
// the only defence available on this side is to notice the symptom: every
// listed identity of a type carries the account ID the provider resolved,
// and when those all come back empty the provider has no account ID and its
// owner-id filter went out empty too. That is [ProblemUnresolvedAccount], a
// warning naming the provider flag to remove. It cannot fire when the list
// returns nothing at all - which is exactly what an empty owner-id filter
// causes on real AWS - so it is a smoke alarm, not a guarantee.
package discovery
