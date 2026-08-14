// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package check answers "would this configuration move under live resource
// markers, and if not, what stops it" using only the configuration: no
// backend, no state, no cloud reads, and no provider process beyond reading
// schemas.
//
// It is the analysis half of two GitHub issues that turned out to be one
// program with two front ends, which is what #114 asked for in as many
// words:
//
//   - #102 wants a corpus of real OpenTofu configurations measured, so that
//     the work queue is ranked by which refusals actually block real
//     configurations rather than by which are easiest to notice. Its front
//     end is tools/corpus-gen, which runs [Analyze] over many directories
//     and folds the results with [Corpus.Add].
//   - #114 wants an evaluator to point the same question at their own repo
//     and get a verdict. Its front end is "choudoufu live-check", in
//     internal/command.
//
// Keeping one analysis under both is not tidiness. If they diverge, the
// compatibility claim the project publishes and the verdict a user gets on
// their own configuration eventually disagree, and the user is right.
//
// # What it checks, and what it does not
//
// Two passes run here: [lint.CheckWith] and [identity.ResolveWith]. That is
// the whole of what can be decided from a configuration. Stamping,
// discovery and projection all need a cloud, and this package never
// contacts one, so their refusals are invisible to it. Every report says so
// (see [Report.Unchecked] and [Layers]) because a clean verdict that reads
// as "this will work" while three layers went unexamined would be the same
// defect #101 spent a campaign removing.
//
// # Where the refusal set comes from
//
// Nothing here hand-lists a refusal. The catalog is assembled from
// [lint.Rules] and [identity.Refusals], the two test-enforced tables the
// packages already keep, and a finding is keyed by [lint.Rule] or by the
// identity diagnostic's Summary. Prose is never matched: roughly fifteen of
// those messages were rewritten during #101 and #110's second half will
// rewrite more, so an instrument that read them would measure its own
// staleness. See [Catalog].
package check
