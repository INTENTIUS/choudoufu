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

	// IdentitiesRecorded is [liveimport.StampReport.IdentitiesRecorded]:
	// GitHub issue #364 unit A2's count of instances that now carry a
	// kind=identity record, across every carrier that can hold one -
	// stamped, untaggable, and markers=record selected - and never a
	// record-backed instance's own kind=object value. Rendered as its own
	// sentence rather than folded into the outcome line below, so every
	// crossing script's existing grep against that exact line keeps
	// matching byte for byte.
	IdentitiesRecorded int
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

// statelessImportOutcomeOrder is the order outcome groups print in: what was
// written first, then what was already so, then what was not attempted. An
// outcome missing from this list renders nowhere at all, so a resource
// carrying it vanishes from the report rather than printing oddly -
// TestEveryStampOutcomePrintsAHeadline holds the list and the headline map
// against each other for that reason.
var statelessImportOutcomeOrder = []string{
	"STAMPED", "ALREADY_STAMPED", "RECORDED", "SENSITIVITY_RECORDED", "ALREADY_RECORDED", "FAILED", "SKIPPED",
}

// statelessImportOutcomeHeadline explains each outcome group once, above the
// group, rather than once per row.
//
// SENSITIVITY_RECORDED is a separate group from RECORDED on purpose, and the
// distinction is a count rather than a wording: a re-migrated estate's
// long-standing records are rewritten to carry the sensitivity a newer
// choudoufu persists, and calling that "newly recorded" tells an operator
// that fifty resources were just seeded into a store that has held them for
// weeks. See liveimport.OutcomeSensitivityRecorded.
var statelessImportOutcomeHeadline = map[string]string{
	"STAMPED":              "stamped",
	"ALREADY_STAMPED":      "already carried this estate's markers; no write made",
	"RECORDED":             "record-backed: its value was seeded into the estate's record store, which is where such a resource's identity lives",
	"SENSITIVITY_RECORDED": "record-backed, and the record store already held this exact value; the record was rewritten to carry which of its attributes are sensitive, and nothing else changed",
	"ALREADY_RECORDED":     "record-backed, and the record store already held exactly this object; no write made",
	"SKIPPED":              "not attempted (see the ratification report above)",
	"FAILED":               "a write was attempted and refused, or failed",
}

// pluralIdentitySuffix is "y" for exactly one identity and "ies" otherwise,
// so the sentence it feeds reads "1 identity recorded." rather than
// "1 identities recorded.".
func pluralIdentitySuffix(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

func (v *StatelessImportHuman) Stamped(rep StatelessImportStamped) {
	var b strings.Builder

	b.WriteString("\nApprove: stamping tofu-estate and tofu-address on every eligible resource, and seeding the record store for every record-backed one. This was a cloud write, one tags-only apply per stamped resource.\n\n")

	byOutcome := make(map[string][]StatelessImportOutcome)
	for _, o := range rep.Outcomes {
		byOutcome[o.Outcome] = append(byOutcome[o.Outcome], o)
	}

	for _, outcome := range statelessImportOutcomeOrder {
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
	// "newly recorded" counts RECORDED alone. A SENSITIVITY_RECORDED record
	// was already there and is counted in its own term, because the whole
	// point of the distinction is that a re-migration of an estate that has
	// been recorded for weeks must not print that number as new work.
	fmt.Fprintf(&b, "%d resource(s) newly stamped, %d already stamped, %d newly recorded, %d re-recorded for sensitivity only, %d already recorded, %d failed, %d skipped.\n",
		stamped, len(byOutcome["ALREADY_STAMPED"]), len(byOutcome["RECORDED"]), len(byOutcome["SENSITIVITY_RECORDED"]), len(byOutcome["ALREADY_RECORDED"]), failed, len(byOutcome["SKIPPED"]))
	// GitHub issue #364 unit A2. A separate sentence, deliberately: the line
	// above is what every live/e2e crossing script's own grep matches by
	// exact substring, and appending a clause to it - even after its own
	// final period - would still risk a script anchored on "... skipped."
	// meaning "and nothing after". This line is new territory instead.
	fmt.Fprintf(&b, "%d identit%s recorded.\n", rep.IdentitiesRecorded, pluralIdentitySuffix(rep.IdentitiesRecorded))
	b.WriteString("The tfstate file was not touched: it was read once, at the start of this run, and never opened again.\n")
	if failed > 0 {
		b.WriteString("Nothing about a FAILED resource's live tags was changed; it is exactly as it was before this run. Re-running live-import is safe: STAMPED and ALREADY_STAMPED resources are no-ops the second time.\n")
	}

	v.view.streams.Print(b.String())
}
