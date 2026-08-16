// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package dataread is issue #179's pre-resolution data-read phase, stage 1:
// same-stack provider data sources whose values identity resolution needs.
//
// The gap it closes is ordering, not evaluation. Stock OpenTofu reads data
// sources during the plan walk and has the value before it needs any
// resource's identity; this fork resolves identity in front of the
// providers, so a data-source value feeding an identity argument, a count
// or a for_each used to refuse as non-static even though the provider could
// have answered. The phase moves the read in front of resolution: the value
// used is the provider's own answer, never an inference, because any value
// on this path becomes a live ownership marker and guessing is the one move
// that is never available.
//
// Two halves, deliberately separable:
//
//   - [Analyze] is offline: it derives which data sources identity
//     resolution demands (the transitive closure reachable from
//     identity-bearing positions, discovered by probing resolution itself),
//     classifies each as readable-before-the-plan or not, and orders the
//     readable ones over their data-to-data references. live-check runs
//     exactly this and nothing else, keeping its no-cloud-calls contract.
//   - [Read] performs the reads, in [Analyze]'s order, against the same
//     configured provider instances the projection builder already uses
//     (statelessProviders.ConfiguredProvider) - ReadDataSource is the third
//     pre-plan cloud call in the pipeline, reusing the second's plumbing.
//
// Results enter resolution through [identity.Context.DataResults]; this
// package never touches the resolver, and internal/live/identity stays
// cloud-free.
//
// Cost and safety, per #64's prior art: reads are read-only by protocol,
// O(data source blocks identity needs), and go out through the provider
// plugin over the same endpoint seam the plan-call budget ratchet counts,
// so the phase's calls fold into live/plan-budget.json when measured. No
// caching, ever: a stale hint elsewhere costs a re-read, but a stale data
// value here becomes a wrong marker - the data-loss shape - so every run
// reads live, the same price stock OpenTofu pays every plan.
//
// The cross-stack flavors (terraform_remote_state, tfe_outputs) are
// mechanically provider data sources too, but they carry their own auth
// surfaces and failure modes and land as stages 2 and 3; this package
// leaves them exactly as refused as they were.
package dataread
