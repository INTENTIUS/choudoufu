// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package untag is GitHub issue #67's apply-time half of the
// undeclared_tagged = "untag" verb: releasing one tag key from a live
// resource this estate owns but no configuration declares.
//
// internal/live/discovery/policy.go's applyOrphanPolicy already withholds
// such a resource from the sweep's destroy proposal and records why in the
// plan - the plan-time half, which needed no cloud write at all. What it
// could not do is release the tag, because an undeclared orphan has no
// configuration block for the ordinary plan graph to hang an update off
// of: OpenTofu's graph either destroys an orphan with no config or leaves
// it alone, nothing in between. This package is the "in between" - one
// provider round trip per resource, run once, after a real apply, outside
// the graph entirely. See internal/backend/local's StatelessRun.AfterApply
// and internal/command/live_mode.go's statelessRunner for where it is
// called from and why that is the one place a real apply, and never a
// plan, is known to have happened.
//
// Every write here follows internal/live/liveimport's own tags-only
// pattern (see that package's stamp.go and tags.go): PlanResourceChange
// then ApplyResourceChange, with a change touching anything but the one
// key refused before ApplyResourceChange is ever called. It is duplicated
// rather than shared, the same choice liveimport made against
// internal/live/mv's identical shape and for the same reason: there is no
// exported seam to call through without exposing another package's private
// state for one caller, and every version is small enough to read on its
// own.
package untag
