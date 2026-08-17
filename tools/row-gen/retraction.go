// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"sort"
	"strings"
)

// This file is -emit's retraction gate (issue #263), and the property it
// exists for is not detection but RECOVERY.
//
// -emit is a fixed point over already-ratified rows, not a fresh derivation
// - main.go's package comment has always said so, and emit.go's
// [emittedRows] is where it is literally true: every non-RecordBacked row is
// copied verbatim out of [identity.DefaultTable], which is the file -emit
// itself wrote on the previous run. The ratified rows have no other home.
// tools/row-gen/annotations.json carries the RULINGS that justify a row
// diverging from the fresh classifier, not the rows; the fresh classifier
// deliberately does not reconstruct a ratified row's fields (see emit.go's
// own doc comment on why templated Reason prose must not be regenerated).
//
// The consequence, measured on a clean tree at 5502e8a3de: emptying
// DefaultTable's literal and running -emit twice produces a 14-row table -
// exactly the RecordBacked rows [recordBackedTypes] derives from
// live/logical-schemas.json - byte-identical across both runs, exit 0, and
// 878 AWS rows gone. The 14-row table is as much a fixed point of -emit as
// the 892-row one. "Run it twice and diff" distinguishes neither.
//
// So a row that leaves the table cannot be brought back by re-running the
// generator. Reverting whatever caused the retraction and re-emitting
// re-emits the SMALLER table, because the smaller table is now the input.
// The only restore is `git checkout --` on the generated files themselves.
//
// That is why this gate stops the WRITE rather than reporting after it. By
// the time the four files are on disk the ratified corpus for the retracted
// types is gone from the working tree, and the operator's next instinct -
// undo the change, re-run - confirms the loss instead of repairing it.
//
// The gate is deliberately not a judgement about whether a retraction is
// correct. Retractions are legitimate and have shipped: issue #249's
// markerless veto reaches backwards over rows admitted before the rule
// existed, and that is the design. -allow-retraction is how an operator says
// "yes, and I know re-running will not undo it".

// allowRetractionFlag is the opt-in's spelling, carried here so the refusal
// message and main.go's flag registration cannot drift apart.
const allowRetractionFlag = "-allow-retraction"

// retractedTypes is every type present in the table -emit read and absent
// from the table it is about to write, sorted.
//
// Both arguments are key sets, not rows: a type that stays admitted while
// its fields change is not a retraction, because its ratified content
// survives into the next input. Only disappearance is unrecoverable.
func retractedTypes(previous, emitted []string) []string {
	keep := make(map[string]struct{}, len(emitted))
	for _, t := range emitted {
		keep[t] = struct{}{}
	}
	var out []string
	for _, t := range previous {
		if _, ok := keep[t]; !ok {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// retractionRefusal is the error -emit returns when it would drop rows and
// the operator has not said so. It names every type, because the list is the
// thing the operator has to check against intent, and because a count alone
// has repeatedly read as plausible in this repository.
func retractionRefusal(retracted []string, allowFlag string) error {
	return fmt.Errorf(
		"row-gen -emit: %d admitted type(s) would be retracted from %s, and re-running this generator CANNOT undo that - "+
			"the ratified rows live only in the file -emit is about to overwrite, so reverting the cause and re-emitting "+
			"re-emits the smaller table. Nothing has been written. If the retraction is intended, pass %s; if it is not, "+
			"fix the cause. If a retraction has already been written, restore it with `git checkout -- %s %s %s %s`:\n  %s",
		len(retracted), identityTableRel, allowFlag,
		identityTableRel, lintTableRel, logicalTableRel, markerlessTableRel,
		strings.Join(retracted, "\n  "))
}
