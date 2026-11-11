// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package conditions is a choudoufu fork port (#2379, of #2256): a condition
// result derived from a sensitive or ephemeral value, on OpenTofu's new
// runtime.
//
// The new runtime evaluates variable validations and output preconditions
// through one CheckRule (internal/lang/eval/internal/configgraph), which
// refuses a condition result carrying the sensitive mark as "Invalid check
// condition". The old runtime discards a condition result's marks instead
// (internal/tofu's eval_variable.go and eval_conditions.go), so a
// validation on a sensitive variable is checked like any other. [Unmark]
// does what the old runtime does, at that one shared site.
//
// No upstream issue is filed. The call site is commented "choudoufu fork
// port (#2379): remove when the variable-validation row closes". Removing
// the port is deleting this package, that call site and the
// "variable-validation" row's fork_port and switch in
// internal/live/newruntime/gaps.json.
//
// It is registered in internal/engine/forkport as [Name]. Switched off
// (forkport.Enabled), [Unmark] changes nothing and the gap table's
// preflight refuses validation blocks again, under its
// "variable-validation" row.
package conditions

import (
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/engine/forkport"
	"github.com/intentius/choudoufu/internal/lang/marks"
)

// Name is the port's name in the internal/engine/forkport registry.
const Name = "conditions"

// Unmark drops the sensitive and ephemeral marks from a condition result's
// marks while the port is on, as the old runtime discards them: they say
// nothing about whether the condition holds. Other marks are kept.
func Unmark(resultMarks cty.ValueMarks) {
	if !forkport.Enabled(Name) {
		return
	}
	delete(resultMarks, marks.Sensitive)
	delete(resultMarks, marks.Ephemeral)
}
