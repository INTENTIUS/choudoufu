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
// This package is issue #67's config/lint half only. Nothing in this fork
// reads a [Policy] value yet: the quadrant behavior itself - which verb runs
// against which resource, in discovery, projection, lifecycle and stamp -
// lands behind #59b and #60, per the issue's Sequencing section. Until then,
// this package exists so that work starts from an agreed, already-validated
// shape instead of parsing the live block's policy block a second time.
package policy
