// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"fmt"
	"strings"
)

// StatelessMvReport is one completed (or, with DryRun, one rehearsed) rename,
// in a form this package can render without importing the mv package.
//
// The fields correspond to mv.Result. They are all strings on purpose: what a
// rename produces is a set of labelled facts about one live resource, and the
// operator needs to be able to read and to grep them, not to be told a story
// about them.
type StatelessMvReport struct {
	// Estate is the estate the rename happened within - the destination, for
	// a cross-estate move.
	Estate string

	// FromEstate is the estate the resource left, for a cross-estate move,
	// and empty for a rename.
	FromEstate string

	// TypeName, LiveID and DisplayName identify the live resource that was
	// written to. DisplayName is empty for a type with no name to show.
	TypeName    string
	LiveID      string
	DisplayName string

	// OldAddr and NewAddr are the two addresses as the operator typed them,
	// and OldMarker and NewMarker are what the tofu-address tag held before
	// and after.
	OldAddr   string
	NewAddr   string
	OldMarker string
	NewMarker string

	// FoundBy names the path that located the resource - listing the type
	// and reading markers, or reading the identity the configuration names.
	FoundBy string

	// DryRun means nothing was written.
	DryRun bool
}

// StatelessMv renders the report "choudoufu live-mv" prints when a rename
// succeeds. Diagnostics do not come through here: they go to [View] and out
// to stderr, the way every other command's do.
type StatelessMv interface {
	Report(rep StatelessMvReport)
}

// NewStatelessMv returns the human-readable implementation. There is no JSON
// implementation, and live-mv accepts no -json flag to ask for one.
func NewStatelessMv(view *View) StatelessMv {
	return &StatelessMvHuman{view: view}
}

// StatelessMvHuman writes the report to the view's output stream, which is
// what makes live-mv's output land where every other command's does - and
// what makes it testable through terminal.StreamsForTesting rather than
// through an io.Writer bolted onto the command struct.
type StatelessMvHuman struct {
	view *View
}

var _ StatelessMv = (*StatelessMvHuman)(nil)

func (v *StatelessMvHuman) Report(rep StatelessMvReport) {
	headline := "Rewrote the ownership marker on one live resource. This was a cloud write."
	if rep.DryRun {
		headline = "Would rewrite the ownership marker on one live resource. Nothing was written (-dry-run)."
	}
	if rep.FromEstate != "" {
		headline = "Moved one live resource into this estate. This was a cloud write."
		if rep.DryRun {
			headline = "Would move one live resource into this estate. Nothing was written (-dry-run)."
		}
	}

	rows := [][2]string{
		{"estate", rep.Estate},
	}
	if rep.FromEstate != "" {
		rows = [][2]string{
			{"from estate", rep.FromEstate},
			{"to estate", rep.Estate},
			{"tofu-estate", fmt.Sprintf("%q -> %q", rep.FromEstate, rep.Estate)},
		}
	}
	rows = append(rows,
		[2]string{"resource type", rep.TypeName},
		[2]string{"live ID", rep.LiveID},
	)
	if rep.DisplayName != "" {
		rows = append(rows, [2]string{"live name", rep.DisplayName})
	}
	rows = append(rows,
		[2]string{"old address", rep.OldAddr},
		[2]string{"new address", rep.NewAddr},
		[2]string{"tofu-address", fmt.Sprintf("%q -> %q", rep.OldMarker, rep.NewMarker)},
		[2]string{"found by", rep.FoundBy},
	)

	var b strings.Builder
	b.WriteString("\n" + headline + "\n\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "  %-14s %s\n", row[0], row[1])
	}
	b.WriteString("\n")
	switch {
	case rep.DryRun:
		b.WriteString("Rerun without -dry-run to write it. Everything above was read from the live system; nothing was changed.\n")
	case rep.FromEstate != "":
		b.WriteString("The live resource's tofu-estate tag now names this estate, and nothing else about it was changed. The source estate no longer sees it and this one binds it on the next plan; its record in the source's store stays behind, and the first apply here records it afresh.\n")
	default:
		b.WriteString("The live resource's tofu-address tag now names the new address, and nothing else about it was changed. There is no state file to update: the old address is gone from the only place it was ever recorded.\n")
	}
	v.view.streams.Print(b.String())
}
