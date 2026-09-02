// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package policy is GitHub issue #67's ownership-policy matrix, given a
// settled shape: a verb per quadrant (declared-in-source x carries-the-tag),
// the tag those quadrants read, and the delete quadrant's safety rails.
//
// It is a leaf package, like internal/live/markers: it does not import
// internal/configs, so the two never end up on each other's side of an
// import cycle. The bridge between the two is [Raw], filled in by whichever
// caller has both the parsed *configs.LivePolicy and a settled estate name -
// today, internal/live/lint (validation) and internal/command (construction
// for the live commands' setup) - by copying the handful of fields across.
//
// This package started as issue #67's config/lint half only, with the
// quadrant behavior itself following once #59b and #60 landed. It has: the
// two declared quadrants are read in internal/live/projection, the two
// undeclared quadrants in internal/live/discovery, and the declared+tagged
// untag verb's marker suppression in internal/live/stamp. Every consumer
// reads a [Policy] built once, in internal/command, from the same
// [configs.Live] block that internal/live/lint validated, so a verb none of
// them ever sees is a verb lint would already have refused.
package policy
