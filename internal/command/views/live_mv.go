// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"encoding/json"
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

// NewStatelessMv returns the human-readable implementation. See
// [NewStatelessMvJSON] for -json's own, which live-mv's Run calls instead of
// this one rather than through it: the two reports diverge (a refusal is
// worth a JSON document too, which [StatelessMvHuman] never renders one
// for), so there is no single call site that decides between them by
// swapping this function's return value alone.
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

// ---------------------------------------------------------------------------
// -json (GitHub issue #791)
// ---------------------------------------------------------------------------

// StatelessMvJSONReport is one live-mv move, or one refusal, as the single
// document -json prints instead of [StatelessMvReport]'s labelled rows.
//
// It exists so that anything reconstructing a move from live-mv's output -
// the workbench's preview phase, which used to re-derive the map from the
// text a -dry-run printed (examples/live-mv-workbench/README.md, "The
// projection is the page's arithmetic over the dry-run reports"), or
// behold, which never writes to a cloud and would render carve.json's plan
// as cards gliding between estate boxes and then read this document to
// confirm what a human ran - has one parse target instead of a prose
// reconstruction. See the issue's "Ask" for the field-by-field reasoning;
// this struct is that list, not a superset invented for convenience.
type StatelessMvJSONReport struct {
	// Resource is the live object: its type and its import identity - an
	// ARN for the types that have one, the provider's own identity string
	// otherwise.
	Resource StatelessMvJSONResource `json:"resource"`

	// From and To are the address on each side of the move: the unescaped
	// address an operator would type, the estate it belongs to, and the
	// escaped tofu-address value that names it as a tag. From's Estate is
	// the source estate for a cross-estate move (Request.FromEstate) and
	// the same estate as To's otherwise - a rename never leaves the address
	// without an estate to be found under.
	From StatelessMvJSONEndpoint `json:"from"`
	To   StatelessMvJSONEndpoint `json:"to"`

	// Followers are the declared instances that move along with Resource
	// without a marker write of their own - mv.Result.Followers, unpacked
	// into the two facts a reader needs to draw them moving too. Omitted
	// entirely, not printed as [], when Resource has none: the zero value a
	// reader gets by leaving out a JSON key it never asked about.
	Followers []StatelessMvJSONFollower `json:"followers,omitempty"`

	// DryRun echoes -dry-run: true means nothing below was written, whether
	// or not Refusal is set - a dry run still refuses exactly what a real
	// run would have.
	DryRun bool `json:"dry_run"`

	// Written is true once ApplyResourceChange completed - never true
	// alongside DryRun, and never true alongside Refusal.
	Written bool `json:"written"`

	// Verified is true when the object the provider returned from the apply
	// was read back carrying the new marker - mv.Result.Verified's own doc
	// comment names the providers that do not serve tags back on that read,
	// which is why this can be false on a write that still succeeded. This
	// is literally the answer to the Why section's "what proved it": Written
	// says the apply returned no error, Verified says the marker was seen.
	Verified bool `json:"verified"`

	// FoundBy is mv.Path's own stable value - "LIST" or "IDENTITY" - naming
	// which admission rule located the live resource. Empty when nothing was
	// found at all (a refusal before Move ever got there).
	FoundBy string `json:"found_by,omitempty"`

	// RequestID would be whatever the provider's own write returned that a
	// CloudTrail row could be matched on - the Why section's "so the receipt
	// phase's join is by id rather than by time window". It is always empty
	// today: the plugin protocol's ApplyResourceChangeResponse
	// (internal/providers/provider.go) carries NewState, Private and
	// NewIdentity and nothing resembling a wire-level request id, and no
	// provider this repository talks to surfaces one through any other RPC
	// either. Threading one through would mean widening the provider
	// protocol itself - a protobuf and SDK change reaching past this
	// repository into the providers it drives, which is a different piece
	// of work than this issue's field, so the field is kept, empty and
	// documented, rather than dropped: a future protocol change has
	// somewhere to land its value without another round of API design.
	RequestID string `json:"request_id,omitempty"`

	// Refusal is set whenever this run did not complete - present with an
	// empty Code for a refusal outside the five stable shapes [mv.RefusalCode]
	// names (a malformed marker, a provider error, an argument problem
	// raised before Move ever ran), and nil on an ordinary success.
	Refusal *StatelessMvJSONRefusal `json:"refusal,omitempty"`
}

// StatelessMvJSONResource is the "the resource: ARN or identity, type" half
// of the Ask, plus DisplayName where the list path supplied one - the same
// three facts [StatelessMvReport] already carries as TypeName/LiveID/
// DisplayName, renamed to what a JSON reader expects them called.
type StatelessMvJSONResource struct {
	TypeName    string `json:"type"`
	LiveID      string `json:"live_id"`
	DisplayName string `json:"display_name,omitempty"`
}

// StatelessMvJSONEndpoint is one side of a move: the address as an operator
// would type it, unescaped, and the estate and escaped tag value that
// address means as a marker.
type StatelessMvJSONEndpoint struct {
	Estate  string `json:"estate,omitempty"`
	Address string `json:"address"`
	Marker  string `json:"marker,omitempty"`
}

// StatelessMvJSONFollower is one instance that follows Resource with no
// write of its own - see mv.Result.Followers and mv.Follower's own doc
// comments for what "follows" means here.
type StatelessMvJSONFollower struct {
	Address  string `json:"address"`
	TypeName string `json:"type"`
}

// StatelessMvJSONRefusal is the "reason as a stable code plus the text" the
// Ask names: Code is empty for a refusal outside the five [mv.RefusalCode]
// shapes, and Summary/Detail are the same two halves [StatelessMvHuman]'s
// underlying diagnostic would otherwise only reach a reader as formatted
// prose on stderr.
type StatelessMvJSONRefusal struct {
	Code    string `json:"code,omitempty"`
	Summary string `json:"summary"`
	Detail  string `json:"detail"`
}

// StatelessMvJSON renders the -json report: exactly one document, on both a
// completed move and a refused one, which is what makes it usable as a
// preview (the workbench, over -dry-run) and as a receipt (the smoke's own
// carve-by-retag assertion, comparing a dry run's document against a real
// run's) alike - [StatelessMv]'s human report only ever renders a success.
type StatelessMvJSON interface {
	Report(rep StatelessMvJSONReport)
}

// NewStatelessMvJSON returns the JSON implementation of the -json report.
func NewStatelessMvJSON(view *View) StatelessMvJSON {
	return &StatelessMvJSONHuman{view: view}
}

// StatelessMvJSONHuman is named to match [StatelessMvHuman] - "human" here
// means "the process talking to a human's terminal or a script's pipe",
// [View]'s own streams, the same meaning [StatelessMvHuman] gives it, not a
// claim about the output being prose. There is exactly one JSON rendering;
// this is it.
type StatelessMvJSONHuman struct {
	view *View
}

var _ StatelessMvJSON = (*StatelessMvJSONHuman)(nil)

func (v *StatelessMvJSONHuman) Report(rep StatelessMvJSONReport) {
	// MarshalIndent, not Marshal: this is a document a human reads while
	// building a preview or a receipt reader, same as every other -json
	// command in this codebase's tree prints one JSON value per line rather
	// than one packed object - see internal/command/providers_schema.go's
	// own jsonprovider.Marshal for the sibling convention this follows.
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		// Every field above is a plain string, bool or slice of those - this
		// can only fail if a future field breaks that, which is a bug in
		// this file rather than something a caller did.
		panic(fmt.Sprintf("live-mv -json: report could not be marshalled: %s", err))
	}
	v.view.streams.Println(string(b))
}
