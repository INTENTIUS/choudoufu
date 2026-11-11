// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package newruntime holds choudoufu's gap table for OpenTofu's new runtime
// (#2256) and the refusals generated from it.
//
// gaps.json is the only copy of the table: #2256 links to it. Each row names
// a gap between the new runtime (internal/engine) and what choudoufu needs,
// how it is handled while TOFU_X_EXPERIMENTAL_RUNTIME is on, and what closes
// it. The checks in this package read their rows from it, so a row's state
// changes in one place.
package newruntime

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// The check kinds a row can carry.
const (
	CheckLive      = "live"
	CheckPreflight = "preflight"
	CheckPostPlan  = "post_plan"
	CheckEngine    = "engine"
	CheckAccepted  = "accepted"
	CheckNone      = "none"
)

// The scopes a post-plan row can carry.
const (
	ScopeLive    = "live"
	ScopeNonLive = "non_live"
	ScopeAll     = "all"
)

// Variable is the opt-in that switches plan and apply onto the new runtime.
// It is upstream's own name (#2256 ruling 4).
const Variable = "TOFU_X_EXPERIMENTAL_RUNTIME"

// Row is one gap.
type Row struct {
	ID string `json:"id"`
	// Gap names what the new runtime cannot do yet.
	Gap string `json:"gap"`
	// Check is how the gap is handled under the toggle.
	Check string `json:"check"`
	// Construct names the detector a preflight, post-plan or engine row
	// uses. Every such row must name one this package implements.
	Construct string `json:"construct,omitempty"`
	// Scope, for a post-plan row, is "live" (live mode only), "non_live"
	// (outside live mode only) or "all".
	Scope string `json:"scope,omitempty"`
	// Interim says what happens meanwhile.
	Interim string `json:"interim,omitempty"`
	// Warning is the text the toggle's warning carries for this row.
	Warning string `json:"warning,omitempty"`
	// ClosedBy says what closes the gap.
	ClosedBy string `json:"closed_by"`
	// Upstream lists opentofu/opentofu issues and PRs as "#NNNN".
	Upstream []string `json:"upstream"`
	// ForkPort, on a row choudoufu closes for now with its own code in
	// place of upstream's, names the fork-owned package that holds it.
	// Removing the port is deleting that package, its call sites (each
	// commented with the row's unit) and the row.
	ForkPort string `json:"fork_port,omitempty"`
	// Switch is the environment setting that turns a fork port off. With
	// it set, what the port covers is refused again by the row that
	// refused it before.
	Switch string `json:"switch,omitempty"`
}

// WaitsFor is how a refusal names what it is waiting for.
func (r Row) WaitsFor() string {
	s := r.ClosedBy
	if len(r.Upstream) > 0 && !strings.Contains(s, r.Upstream[0]) {
		s += " (upstream opentofu/opentofu " + strings.Join(r.Upstream, ", ") + ")"
	}
	return s
}

type table struct {
	Comment string `json:"comment"`
	Rows    []Row  `json:"rows"`
}

//go:embed gaps.json
var gapsJSON []byte

var rows = mustParse(gapsJSON)

func mustParse(b []byte) []Row {
	rows, err := parse(b)
	if err != nil {
		panic(err)
	}
	return rows
}

func parse(b []byte) ([]Row, error) {
	var t table
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return nil, fmt.Errorf("internal/live/newruntime/gaps.json: %w", err)
	}
	seen := map[string]bool{}
	for _, r := range t.Rows {
		if r.ID == "" || seen[r.ID] {
			return nil, fmt.Errorf("internal/live/newruntime/gaps.json: missing or duplicate row id %q", r.ID)
		}
		seen[r.ID] = true
		switch r.Check {
		case CheckLive, CheckPreflight, CheckPostPlan, CheckEngine, CheckAccepted, CheckNone:
		default:
			return nil, fmt.Errorf("internal/live/newruntime/gaps.json: row %q has unknown check %q", r.ID, r.Check)
		}
		if (r.ForkPort == "") != (r.Switch == "") {
			return nil, fmt.Errorf("internal/live/newruntime/gaps.json: row %q must name both a fork_port and its switch, or neither", r.ID)
		}
		if r.Check == CheckPostPlan {
			switch r.Scope {
			case ScopeLive, ScopeNonLive, ScopeAll:
			default:
				return nil, fmt.Errorf("internal/live/newruntime/gaps.json: post-plan row %q has unknown scope %q", r.ID, r.Scope)
			}
		}
	}
	return t.Rows, nil
}

// Rows returns the table, in file order.
func Rows() []Row {
	return append([]Row(nil), rows...)
}

// RowsFor returns the rows of one check kind, in file order.
func RowsFor(check string) []Row {
	var out []Row
	for _, r := range rows {
		if r.Check == check {
			out = append(out, r)
		}
	}
	return out
}

// RowByID returns the row with id, and whether there is one.
func RowByID(id string) (Row, bool) {
	for _, r := range rows {
		if r.ID == id {
			return r, true
		}
	}
	return Row{}, false
}

// UpstreamIssues returns every upstream issue and PR the table names,
// sorted, for scripts/upstream-sync.sh's report.
func UpstreamIssues() []string {
	set := map[string]bool{}
	for _, r := range rows {
		for _, u := range r.Upstream {
			set[u] = true
		}
	}
	out := make([]string, 0, len(set))
	for u := range set {
		out = append(out, u)
	}
	sort.Strings(out)
	return out
}

// WarningSummary is the summary of the warning every run that reaches the
// engine prints. Tests match on it.
const WarningSummary = "Running on OpenTofu's experimental new runtime"

// WarningDetail is the toggle's warning: the variable, and every accepted
// risk in the table.
func WarningDetail() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s is set, so this plan or apply runs on OpenTofu's new runtime (internal/engine), which upstream has not yet called stable. Constructs it cannot handle are refused before planning. These known differences are accepted, not refused:", Variable)
	for _, r := range RowsFor(CheckAccepted) {
		fmt.Fprintf(&b, "\n  - %s", r.Warning)
	}
	for _, r := range rows {
		if r.Check != CheckAccepted && r.Warning != "" {
			fmt.Fprintf(&b, "\n  - %s", r.Warning)
		}
	}
	b.WriteString("\nUnset the variable to use the default runtime. The full gap table is internal/live/newruntime/gaps.json (#2256).")
	return b.String()
}
