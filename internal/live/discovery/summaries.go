// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

// This file holds the summaries that reach a diagnostic through a variable
// rather than as a literal field, the same way internal/live/stamp's
// summaries.go does and for the same reason: refusals.go is the one file
// TestRefusalsRegistered does not scan, so a constant declared there would
// be invisible to it.

// SummaryUnclassifiedProblem is the summary for a [ProblemKind] with no
// entry in [problemSummaries].
//
// It is a constant rather than an interpolation of the kind because a
// summary assembled at runtime cannot be registered, and this one - the
// diagnostic that means "this package has not classified this yet" - was
// otherwise the single refusal that could never be documented. The kind is
// in the detail.
const SummaryUnclassifiedProblem = "Unclassified discovery problem"

// SummaryIncompleteSweep is the summary [sweepGapDiag] raises for a sweep
// gap the operator is told about out loud - a list call that failed, a list
// configuration that could not be built. It is a constant so that
// [SeverityForRefusal] and the call site name the same string rather than
// two copies of it that can drift.
const SummaryIncompleteSweep = "Incomplete sweep for undeclared resources"
