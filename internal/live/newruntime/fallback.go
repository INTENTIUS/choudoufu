// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package newruntime

import (
	"fmt"
	"os"
	"strings"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// StrictVariable keeps the gap table's refusals (#2397). With the toggle on
// and this unset, a run the new runtime cannot handle falls back to the
// default runtime for the whole run, with [FallbackWarning]. With it set,
// such a run is refused, as it was before #2397. The experimental-runtime
// lane sets it, so the expected-failures list keeps measuring the new
// runtime.
const StrictVariable = "CHOUDOUFU_NEW_RUNTIME_STRICT"

// Strict reports whether [StrictVariable] is set to any non-empty value.
// This is the only place it is read.
func Strict() bool {
	return os.Getenv(StrictVariable) != ""
}

// GapRefusal is the extra information every refusal from this package
// carries: the id of the gap row that raised it. [RefusedRows] reads it.
type GapRefusal interface {
	GapRowID() string
}

type gapRefusal string

func (g gapRefusal) GapRowID() string { return string(g) }

// RefusedRows returns the rows behind every refusal in diags, each once, in
// table order. Diagnostics this package did not raise are ignored, so a
// caller can hand it everything a plan returned.
func RefusedRows(diags tfdiags.Diagnostics) []Row {
	ids := map[string]bool{}
	for _, d := range diags {
		if d.Severity() != tfdiags.Error {
			continue
		}
		if g := tfdiags.ExtraInfo[GapRefusal](d); g != nil {
			ids[g.GapRowID()] = true
		}
	}
	var out []Row
	for _, r := range rows {
		if ids[r.ID] {
			out = append(out, r)
		}
	}
	return out
}

// FallbackSummary is the summary of the warning a run that fell back to the
// default runtime prints in place of [WarningSummary]. Tests match on it.
const FallbackSummary = "Ran on the default runtime, not the new runtime"

// FallbackWarning is the warning for a run that fell back to the default
// runtime because of rows: the variable, each row with what it waits for,
// and how to get the refusal instead.
func FallbackWarning(rows []Row) tfdiags.Diagnostic {
	var b strings.Builder
	fmt.Fprintf(&b, "%s is set, but the new runtime cannot handle this configuration yet, so this whole run used the default runtime. Nothing in it ran on the new runtime. The gaps that caused the fallback (internal/live/newruntime/gaps.json):", Variable)
	for _, r := range rows {
		fmt.Fprintf(&b, "\n  - %s: %s; waits for %s", r.ID, r.Gap, r.WaitsFor())
	}
	fmt.Fprintf(&b, "\nSet %s=1 to refuse such a run instead of falling back.", StrictVariable)
	return tfdiags.Sourceless(tfdiags.Warning, FallbackSummary, b.String())
}
