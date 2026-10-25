// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"encoding/json"
	"fmt"
)

// The drift stream is live-plan -resource's output (GitHub issue #2245):
// one plan of one resource's cone, reported as chant's drift record v1
// (INTENTIUS/chant#4450's design note, typed by chant C1, INTENTIUS/chant#4451).
// It is NDJSON on stdout, three kinds of line, each carrying a "type":
//
//	{"type":"drift-start", ...}   once, before any record
//	{"type":"drift-record", ...}  one per resource instance in the cone
//	{"type":"drift-settle", ...}  once, last
//
// A record line is the DriftRecord interface's fields under their own
// names, plus "type". "type" is this stream's envelope, not part of the
// record: chant's design note fixes the record and the three line kinds
// but not the discriminator, and this is the one choudoufu picked. A
// reader that wants the bare record drops it.

// LiveDriftStart is the drift-start line. Expected is every instance
// address a record will follow for, so a renderer can show each card as
// "reading" until its record arrives.
type LiveDriftStart struct {
	Type     string   `json:"type"`
	Drift    string   `json:"drift"`
	Member   string   `json:"member"`
	Engine   string   `json:"engine"`
	Read     string   `json:"read"`
	Resource string   `json:"resource"`
	Expected []string `json:"expected"`
}

// LiveDriftRecord is one drift-record line: chant's DriftRecord, field for
// field, with Type in front. Node is always JSON null from choudoufu: it is
// chant's block-level graph id, and the change-set mapping rules that
// derive it are chant's.
type LiveDriftRecord struct {
	Type         string               `json:"type"`
	Member       string               `json:"member"`
	Engine       string               `json:"engine"`
	Address      string               `json:"address"`
	Node         *string              `json:"node"`
	ObservedAt   string               `json:"observedAt"`
	Read         string               `json:"read"`
	FellBackFrom string               `json:"fellBackFrom,omitempty"`
	Status       string               `json:"status"`
	Unobserved   *LiveDriftUnobserved `json:"unobserved,omitempty"`
	Attributes   []LiveDriftAttribute `json:"attributes,omitempty"`
	Ownership    *LiveDriftOwnership  `json:"ownership,omitempty"`
	Evidence     LiveDriftEvidence    `json:"evidence"`
}

// LiveDriftUnobserved is DriftRecord.unobserved. Reason is one of chant's
// UnobservedReason values or "not-reached".
type LiveDriftUnobserved struct {
	Reason  string `json:"reason"`
	Detail  string `json:"detail,omitempty"`
	Queried string `json:"queried,omitempty"`
}

// LiveDriftAttribute is one entry of DriftRecord.attributes. choudoufu
// reports top-level attribute names only and leaves the optional values
// out, so a sensitive value is never one redaction away from the stream;
// Sensitive still says which of the names the planner marked.
type LiveDriftAttribute struct {
	Path      string `json:"path"`
	Before    any    `json:"before,omitempty"`
	After     any    `json:"after,omitempty"`
	Sensitive bool   `json:"sensitive,omitempty"`
}

// LiveDriftOwnership is DriftRecord.ownership.
type LiveDriftOwnership struct {
	Verdict        string `json:"verdict"`
	By             string `json:"by,omitempty"`
	MarkerOnObject *bool  `json:"markerOnObject,omitempty"`
	OwnedBy        string `json:"ownedBy,omitempty"`
	Adopt          string `json:"adopt,omitempty"`
}

// LiveDriftEvidence is DriftRecord.evidence.
type LiveDriftEvidence struct {
	Tool       string `json:"tool"`
	Version    string `json:"version,omitempty"`
	Command    string `json:"command,omitempty"`
	EngineCode string `json:"engineCode,omitempty"`
}

// LiveDriftSettle is the drift-settle line: the per-status counts, and
// Reads, the run's own account of what it read. Reads is what shows a
// reader that no estate-wide sweep and no record listing happened.
type LiveDriftSettle struct {
	Type   string         `json:"type"`
	Drift  string         `json:"drift"`
	Member string         `json:"member"`
	Engine string         `json:"engine"`
	Read   string         `json:"read"`
	Counts map[string]int `json:"counts"`
	Reads  LiveDriftReads `json:"reads"`
}

// LiveDriftReads counts what one live-plan -resource run read.
//
// RecordsRead is every keyed read that reached the record store, and
// RecordKeys how many distinct keys those were. RecordListCalls is every
// List or bulk GetAll that reached it, which this path never makes; a
// non-zero value is a defect, and the run refuses it as one. SweepCalls is
// every type the estate-wide sweep listed, which is zero on this path for
// the same reason. ScanTypes names the types the cone's own marker
// discovery listed, one provider list call each; it is empty when nothing
// in the cone waits on discovery.
type LiveDriftReads struct {
	RecordsRead     int      `json:"recordsRead"`
	RecordKeys      int      `json:"recordKeys"`
	RecordListCalls int      `json:"recordListCalls"`
	SweepCalls      int      `json:"sweepCalls"`
	ScanTypes       []string `json:"scanTypes"`
	ScanListCalls   int      `json:"scanListCalls"`
}

// LiveDrift renders the drift stream. It satisfies [LivePlan] so the
// pipeline's section calls have somewhere to go, and every one of them is
// a no-op: the stream carries the cone's data, and prose beside NDJSON
// would leave stdout neither one thing nor the other.
type LiveDrift struct {
	view *View
}

var _ LivePlan = (*LiveDrift)(nil)

// NewLiveDrift returns the drift-stream renderer, writing to view's stdout.
func NewLiveDrift(view *View) *LiveDrift {
	return &LiveDrift{view: view}
}

func (v *LiveDrift) Progress(LiveProgress)                   {}
func (v *LiveDrift) Omissions([]LiveOmission)                {}
func (v *LiveDrift) Unowned([]LiveUnowned)                   {}
func (v *LiveDrift) Foreign(LiveForeign)                     {}
func (v *LiveDrift) Policy(LivePolicyReport)                 {}
func (v *LiveDrift) GuidedFallback(string)                   {}
func (v *LiveDrift) Lookalikes([]LiveLookalike)              {}
func (v *LiveDrift) KubernetesDryRun([]LiveKubernetesDryRun) {}
func (v *LiveDrift) Adoption(LiveAdoption)                   {}
func (v *LiveDrift) Document(LivePlanDocument) bool          { return false }

// Start prints the drift-start line.
func (v *LiveDrift) Start(s LiveDriftStart) bool {
	s.Type = "drift-start"
	return v.line(s)
}

// Record prints one drift-record line.
func (v *LiveDrift) Record(r LiveDriftRecord) bool {
	r.Type = "drift-record"
	return v.line(r)
}

// Settle prints the drift-settle line.
func (v *LiveDrift) Settle(s LiveDriftSettle) bool {
	s.Type = "drift-settle"
	return v.line(s)
}

func (v *LiveDrift) line(x any) bool {
	b, err := json.Marshal(x)
	if err != nil {
		_, _ = v.view.streams.Eprintln(fmt.Sprintf("\nError marshalling JSON: %s", err))
		return false
	}
	_, _ = v.view.streams.Println(string(b))
	return true
}
