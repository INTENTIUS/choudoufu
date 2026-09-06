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

// This file is -emit's retraction gate, and what it exists for changed when
// issue #263's cure landed. It was built for RECOVERY; it is now about
// INTENT. Both halves of that are worth writing down, because the gate's own
// refusal message used to make a claim that is no longer true.
//
// # What it was built for
//
// -emit is a fixed point over already-ratified rows, not a fresh derivation
// - main.go's package comment has always said so, and emit.go's
// [emittedRows] is where it is literally true: every non-RecordBacked row is
// copied verbatim out of the corpus [buildEmitFiles] hands it. That corpus
// used to be [identity.DefaultTable] - the file -emit itself wrote on the
// previous run. tools/row-gen/annotations.json carries the RULINGS that
// justify a row diverging from the fresh classifier, not the rows; the fresh
// classifier deliberately does not reconstruct a ratified row's fields (see
// emit.go's own doc comment on why templated Reason prose must not be
// regenerated).
//
// Measured on a clean tree at 5502e8a3de: emptying DefaultTable's literal and
// running -emit twice produced a 14-row table - exactly the RecordBacked rows
// [recordBackedTypes] derives from live/logical-schemas.json - byte-identical
// across both runs, exit 0, and 878 AWS rows gone. The 14-row table was as
// much a fixed point of -emit as the 892-row one, and "run it twice and diff"
// distinguished neither. So a row that left the table could not be brought
// back by re-running: reverting the cause and re-emitting re-emitted the
// SMALLER table, because the smaller table was now the input. The only
// restore was `git checkout --` on the generated files themselves.
//
// # What changed
//
// tools/row-gen/ratified.json holds those 878 rows as an input no generator
// writes, proven byte-for-byte equal to what the committed table renders
// (ratified.go, TestRatifiedRendersTheCommittedIdentityTable), and -emit now
// reads it - along with [buildComparison] and [markerlessRoster], the two
// other reads that had to move with it. Deleting rows from
// table_generated.go and re-emitting restores them.
//
// So the unrecoverability this gate was named for is gone, and with it the
// argument that the gate must stop the WRITE rather than report after it.
// What is left is still worth stopping for: a type that leaves the table
// stops resolving for every configuration that names it, which is a support
// change, and a support change should be stated rather than noticed. The
// gate now says "confirm this", not "this cannot be undone".
//
// It remains deliberately not a judgement about whether a retraction is
// correct. Retractions are legitimate and have shipped: issue #249's
// markerless veto reaches backwards over rows admitted before the rule
// existed, and that is the design. -allow-retraction is how an operator says
// "yes, I mean to drop these".

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
//
// The message says what the recovery actually is. It used to say re-running
// CANNOT undo the drop, which was true while the corpus was -emit's own
// output and is false now that it is %[6]s; leaving that sentence in place
// would send an operator to `git checkout --` on four generated files when
// the thing to look at is one hand-owned input.
func retractionRefusal(retracted []string, allowFlag string) error {
	return fmt.Errorf(
		"row-gen -emit: %[1]d admitted type(s) would be retracted from %[2]s. Nothing has been written. "+
			"A retracted type stops resolving for every configuration that names it, so this has to be deliberate: "+
			"if it is, pass %[3]s; if it is not, fix the cause. The usual cause is a row deleted from %[6]s "+
			"or an evidence change that made markerless.go's veto reach further. Re-emitting after fixing the cause "+
			"DOES restore these rows - they are ratified in %[6]s, which no generator writes - and if a retraction has "+
			"already been written, `git checkout -- %[2]s %[4]s %[5]s %[7]s` restores the tables directly:\n  %[8]s",
		len(retracted), identityTableRel, allowFlag,
		lintTableRel, logicalTableRel, ratifiedJSONRel, markerlessTableRel,
		strings.Join(retracted, "\n  "))
}
