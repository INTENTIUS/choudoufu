// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package liveimport is the bulk migration path from a state-backed estate
// to ownership markers (issue #61): read an existing tfstate file once,
// verify every resource it names against the live system, and - only when
// the operator says so a second time - stamp this estate's markers onto
// everything that verified.
//
// # Why this is not the stamp package
//
// [github.com/intentius/choudoufu/internal/live/stamp] writes markers by
// rewriting HCL configuration bodies so that an ordinary plan and apply carry
// them to the cloud. That seam needs a resource block whose identity the
// stamping pass can read from configuration - it is how a stateless run
// marks resources it is about to plan.
//
// A migration has no such moment. The whole point is that the estate already
// exists, as a state file, and the identity of every resource in it is
// already settled - the state's own attributes name it. Routing that through
// a plan would mean re-deriving identities the state already has, against
// configuration that may not even be the configuration the state was last
// applied with. So this package writes the same two tags directly through
// the provider's plan/apply pair, on one resource at a time, mirroring the
// tags-only write [github.com/intentius/choudoufu/internal/live/mv] already
// proves safe: read the live object, change only tofu-estate and
// tofu-address, refuse any plan that would replace the resource or move
// anything else, apply. mv's own tag-rewriting helpers are private to its
// *mover type, so stamp.go here carries its own copy of the same small
// pattern rather than reaching into that package - documented at the top of
// that file, not silently repeated.
//
// # The sequencing
//
// chant's carve (docs/src/content/docs/cli/carve-out.mdx) is the reference
// this package's shape follows, adapted rather than copied: emit adopts an
// existing resource into new-tool ownership and stops before any write,
// bridge patches the survivors, and only carve apply performs the graduating
// write - with the note that at every step before that write, rollback is
// "terraform import" because nothing about the old system's ability to
// manage the resource was disturbed.
//
// [Ratify] is this package's observe step: read-only, against the live
// system, producing a per-resource verdict and nothing else.
// [Ratification.Approve] is the stamp step, and it is the only thing in this
// package that writes. Nothing in between removes a resource from the
// tfstate or changes it in any way - this package does not even open it a
// second time - so the rollback path chant's design leans on is open by
// construction: the state file this run read is exactly as usable by
// ordinary state-backed OpenTofu after a stamp as it was before, tags being
// additive and invisible to a plan that does not look for them. There is no
// separate "graduation" step here the way carve has one, because a marker
// write does not revoke the state file's authority; it only gives a later
// "choudoufu live-plan" something to find.
//
// # What Ratify never does
//
// Ratify opens the state file exactly once, through
// [github.com/intentius/choudoufu/internal/states/statefile] (the command
// layer's job, ahead of calling in here), and never opens it again - not to
// write it, and not to re-read it during Approve. Every fact Approve needs
// (the live object, its private data, its identity, the provider connection)
// is carried forward in memory from the read Ratify already did. This is
// deliberate: state authority and marker authority are not meant to overlap
// even for the duration of one command run.
//
// # Scope (v1)
//
// Root module managed resources only (see issue #59: module-structured state
// needs the module epic's addressing first). Every resource type this
// package can act on has to appear in
// [github.com/intentius/choudoufu/internal/live/identity]'s admission table,
// the same table live-mv and live-plan check against - an unlisted type is
// reported, never guessed at. Only two marker keys are stamped, tofu-estate
// and tofu-address; tofu-slot is a fact about a *live* count set that only a
// marker discovery pass can construct (see the stamp package's doc comment),
// and a migration run performs no discovery, so slot assignment for a
// migrated count block is left to the first ordinary live-plan run
// afterward.
package liveimport
