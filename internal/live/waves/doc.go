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
// The digest lives in internal/live/setdigest, which imports nothing of
// this module so internal/live/setplan can print it; its package doc says
// what it covers. The names below are aliases of it.
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
//
// # Wave apply
//
// [Apply] applies one wave of a set whose digest was approved elsewhere.
// The set plan document must hash to the approved digest. Every root of
// the wave that has not already landed is planned again, and unless each
// fresh plan's root digest equals its approved one, nothing in the wave is
// applied and the result is exit 3 naming the roots that moved: a set
// extension of "apply PLANFILE"'s own refusal. A root whose fresh plan
// fails is not a moved set; it is a failed root.
//
// Roots apply one at a time, producers first. A root that fails skips
// every root that reads its estate, in its wave and in later ones, and
// roots that read nothing that failed still apply. Each outcome is written
// to the resume file as it is decided, and a later run with that file
// plans and applies only roots that have not landed. A root in a later
// wave whose producer has not landed, for any reason including its wave
// never having run, is skipped rather than applied ahead of it.
//
// A reader in a later wave was planned before its producer applied. When
// the producer's apply changes a value the reader reads, the reader's
// fresh plan differs from its approved one and its wave exits 3: the set
// has to be planned and approved again from that point. That is the
// refusal working, not a fault in it.
package waves
