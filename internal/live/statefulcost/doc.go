// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package statefulcost holds one measurement and nothing else: what a plan
// costs when this fork's binary runs a configuration that has NO live
// block.
//
// The question it answers (issue #588, question restated on #582): is
// choudoufu, run statefully, equivalent in cost to the stock binaries it
// forked from? Everything measured about this fork so far has been measured
// with a live block present, so "choudoufu plan takes 200s and terraform
// plan takes 3s" has never been separable into "the fork costs this" and
// "statelessness costs this". Those are different claims with different
// consequences, and only one of them is a reason not to adopt the binary.
//
// The comparison is four plan columns over the same generated terralith
// (tools/terralith-gen), all against the pinned floci emulator, in one
// process, minutes apart:
//
//	stock terraform, state file, no live block   - the baseline
//	stock tofu, state file, no live block        - the fork's own upstream
//	choudoufu, state file, no live block         - the question
//	choudoufu, live block, migrated, no state    - the stateless path
//
// The third column exists to isolate the fork from statelessness; the
// second exists to isolate the fork from OpenTofu, because choudoufu is an
// OpenTofu fork and Terraform 1.15 is not OpenTofu. Without it, any
// difference between columns one and three is unattributable.
//
// API call counts, not wall clock, are the load-bearing measurement, for
// the reason live/plan-budget.json gives: wall clock grades the machine.
// Wall clock is reported anyway because #588's audience asked in seconds.
package statefulcost
