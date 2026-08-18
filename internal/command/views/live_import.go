// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"fmt"
	"sort"
	"strings"
)

// StatelessImportEntry is one resource instance's ratification verdict, in a
// form this package can render without importing the liveimport package.
// The fields correspond to liveimport.Entry.
type StatelessImportEntry struct {
	Addr     string
	TypeName string
	Status   string
	Detail   string
	LiveID   string
	Drifted  []string
}

// StatelessImportReport is the whole ratification report "choudoufu
// live-import" prints before any tag is written.
type StatelessImportReport struct {
	Estate    string
	StatePath string
	Entries   []StatelessImportEntry
}

// StatelessImportOutcome is one resource instance's stamp outcome, in a form
// this package can render without importing the liveimport package. The
// fields correspond to liveimport.StampOutcome.
type StatelessImportOutcome struct {
	Addr     string
	TypeName string
	Outcome  string
	Detail   string
}

// StatelessImportStamped is what one -approve run did.
type StatelessImportStamped struct {
	Estate   string
	Outcomes []StatelessImportOutcome
}

// StatelessImport renders what "choudoufu live-import" produces: the
// ratification report first, always, and the stamp report only on a run
// given -approve. Diagnostics do not come through here: they go to [View]
// and out to stderr, the way every other command's do.
type StatelessImport interface {
	Ratification(rep StatelessImportReport)
	Stamped(rep StatelessImportStamped)
}

// NewStatelessImport returns the human-readable implementation. There is no
// JSON implementation, matching live-mv.
func NewStatelessImport(view *View) StatelessImport {
	return &StatelessImportHuman{view: view}
}

// StatelessImportHuman writes both reports to the view's output stream.
type StatelessImportHuman struct {
	view *View
}

var _ StatelessImport = (*StatelessImportHuman)(nil)

// statelessImportStatusOrder is the order statuses print in: the two
// something-to-see-here statuses first, then the three that a run cannot
// act on, each explained once above its group rather than once per row.
var statelessImportStatusOrder = []string{"VERIFIED", "DRIFTED", "MISSING", "UNTAGGABLE", "UNADMITTED_TYPE"}

var statelessImportStatusHeadline = map[string]string{
	"VERIFIED":        "verified against the live system",
	"DRIFTED":         "verified, but drifted from the live system",
	"MISSING":         "could not be verified",
	"UNTAGGABLE":      "outside the taggable subset",
	"UNADMITTED_TYPE": "outside the admitted type set",
}

func (v *StatelessImportHuman) Ratification(rep StatelessImportReport) {
	var b strings.Builder

	fmt.Fprintf(&b, "\nRatifying %s for estate %q against the live system. This was read-only: nothing was written.\n\n", rep.StatePath, rep.Estate)

	byStatus := make(map[string][]StatelessImportEntry)
	for _, e := range rep.Entries {
		byStatus[e.Status] = append(byStatus[e.Status], e)
	}

	if len(rep.Entries) == 0 {
		b.WriteString("The state file names no managed resource instances. Nothing to ratify.\n")
	}

	for _, status := range statelessImportStatusOrder {
		entries := byStatus[status]
		if len(entries) == 0 {
			continue
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Addr < entries[j].Addr })
		fmt.Fprintf(&b, "%s (%d) - %s:\n", status, len(entries), statelessImportStatusHeadline[status])
		for _, e := range entries {
			liveID := e.LiveID
			if liveID == "" {
				liveID = "-"
			}
			fmt.Fprintf(&b, "  %-42s %-24s live id: %s\n", e.Addr, e.TypeName, liveID)
			fmt.Fprintf(&b, "    %s\n", e.Detail)
		}
		b.WriteString("\n")
	}

	verified := len(byStatus["VERIFIED"]) + len(byStatus["DRIFTED"])
	fmt.Fprintf(&b, "%d of %d resource instance(s) are eligible for stamping (VERIFIED or DRIFTED).\n", verified, len(rep.Entries))
	b.WriteString(fmt.Sprintf("\n%s was opened once, read-only, and will not be opened again by this run - not to write it, and not to read it a second time.\n", rep.StatePath))
	if verified > 0 {
		b.WriteString("No tag has been written. Rerun with -approve to stamp tofu-estate and tofu-address onto every eligible resource above.\n")
	} else {
		b.WriteString("No tag has been written, and none would be: nothing above is eligible for stamping.\n")
	}

	v.view.streams.Print(b.String())
}

var statelessImportOutcomeHeadline = map[string]string{
	"STAMPED":         "stamped",
	"ALREADY_STAMPED": "already carried this estate's markers; no write made",
	"SKIPPED":         "not attempted (see the ratification report above)",
	"FAILED":          "a write was attempted and refused, or failed",
}

func (v *StatelessImportHuman) Stamped(rep StatelessImportStamped) {
	var b strings.Builder

	b.WriteString("\nApprove: stamping tofu-estate and tofu-address on every eligible resource. This was a cloud write, one tags-only apply per resource.\n\n")

	byOutcome := make(map[string][]StatelessImportOutcome)
	for _, o := range rep.Outcomes {
		byOutcome[o.Outcome] = append(byOutcome[o.Outcome], o)
	}

	for _, outcome := range []string{"STAMPED", "ALREADY_STAMPED", "FAILED", "SKIPPED"} {
		outcomes := byOutcome[outcome]
		if len(outcomes) == 0 {
			continue
		}
		sort.Slice(outcomes, func(i, j int) bool { return outcomes[i].Addr < outcomes[j].Addr })
		fmt.Fprintf(&b, "%s (%d) - %s:\n", outcome, len(outcomes), statelessImportOutcomeHeadline[outcome])
		for _, o := range outcomes {
			fmt.Fprintf(&b, "  %-42s %-24s %s\n", o.Addr, o.TypeName, o.Detail)
		}
		b.WriteString("\n")
	}

	stamped := len(byOutcome["STAMPED"])
	failed := len(byOutcome["FAILED"])
	fmt.Fprintf(&b, "%d resource(s) newly stamped, %d already stamped, %d failed, %d skipped.\n", stamped, len(byOutcome["ALREADY_STAMPED"]), failed, len(byOutcome["SKIPPED"]))
	b.WriteString("The tfstate file was not touched: it was read once, at the start of this run, and never opened again.\n")
	if failed > 0 {
		b.WriteString("Nothing about a FAILED resource's live tags was changed; it is exactly as it was before this run. Re-running live-import is safe: STAMPED and ALREADY_STAMPED resources are no-ops the second time.\n")
	}

	v.view.streams.Print(b.String())
}
