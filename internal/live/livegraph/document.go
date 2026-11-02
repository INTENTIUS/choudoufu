// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package livegraph builds the estate graph "choudoufu live-graph -json"
// prints (GitHub issue #2285): one GraphIR version 1 document in the
// engine graph shape of INTENTIUS/chant#4520 (design section 3, ruled
// 2026-10-26), which chant's terraform lexicon reads for facts on its IR
// nodes and behold's graph pane loads as it is.
//
// The document is built from the configuration tree alone: one node per
// resource and data block, keyed by its address, one node per instance
// when a literal count or for_each (or a live-plan row) names it, and one
// edge per reference found by walking the configuration's expressions.
// The walk needs no provider schema, so no provider starts and nothing is
// read from a cloud (the #2275 ruling, Q1). Cross-estate reads are edges
// to one node per read estate (Q2). Live facts come only from a
// live-plan -json document handed in ([PlanDoc]); this package performs no
// live read of its own.
//
// The document carries addresses, types, the import ids a live plan bound
// and record version ids. It carries no attribute value, no record
// payload and nothing from a state file. [PlanDoc] decodes only the
// fields this package uses, so a value elsewhere in a live-plan document
// (a matched argument, an identity broken out by component, a diagnostic)
// is never even read into memory here.
package livegraph

// The JSON field names below are chant core's (packages/core/src/
// engine-facts.ts and graph-ir.ts, schema
// packages/core/src/graph-schema/graph.schema.json). Every object in that
// schema is closed, so a field added here that chant does not know fails
// the schema-decode test before it reaches a reader.

// Document is one engine graph document.
type Document struct {
	Version int      `json:"version"`
	Meta    Meta     `json:"meta"`
	Nodes   []Node   `json:"nodes"`
	Edges   []Edge   `json:"edges"`
	Groups  struct{} `json:"groups"`
}

// Meta is the document's meta bag.
type Meta struct {
	EngineGraph EngineGraph `json:"engineGraph"`
}

// EngineGraph is meta.engineGraph.
type EngineGraph struct {
	Format       int           `json:"format"`
	Engine       Engine        `json:"engine"`
	Scope        Scope         `json:"scope"`
	ObservedAt   string        `json:"observedAt,omitempty"`
	MarkerScheme *MarkerScheme `json:"markerScheme,omitempty"`
	Gaps         []Gap         `json:"gaps,omitempty"`
}

// Engine names what printed the document.
type Engine struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// Scope is the estate the graph covers.
type Scope struct {
	Kind   string `json:"kind"`
	Estate string `json:"estate"`
}

// MarkerScheme is the marker vocabulary choudoufu hands the public sweeps
// (pkg/markerkeys, #2281), printed once.
type MarkerScheme struct {
	OwnerKey         string         `json:"ownerKey"`
	AddressKey       string         `json:"addressKey"`
	ContinuationKeys []string       `json:"continuationKeys,omitempty"`
	Encoding         MarkerEncoding `json:"encoding"`
}

// MarkerEncoding is how marker values are written on tags and on labels.
type MarkerEncoding struct {
	Tags   string `json:"tags"`
	Labels string `json:"labels"`
}

// Gap is a block the graph could not describe in full, and why.
type Gap struct {
	Address string `json:"address"`
	Reason  string `json:"reason"`
	Detail  string `json:"detail,omitempty"`
}

// Gap reasons.
const (
	// GapJSONConfig: the block is in a .tf.json file, whose body cannot be
	// walked for references without the provider's schema. Its node is
	// there and its outgoing edges are not.
	GapJSONConfig = "json-config"
	// GapBodyUnread: the block's body is not native syntax for another
	// reason (an override merged into it), so its references were not
	// walked.
	GapBodyUnread = "references-unread"
	// GapCrossEstate: a data block reads another estate by a value that
	// is not a literal, so which estate cannot be told from configuration.
	GapCrossEstate = "cross-estate-read-unresolved"
	// GapNotDeclared: the live plan names an address that no block of
	// this configuration declares.
	GapNotDeclared = "not-in-configuration"
	// GapHistoryUnkept: -history was asked for, and the record store
	// replaces records in place.
	GapHistoryUnkept = "history-not-kept"
)

// Node is one resource or data block, one instance of one, or one estate
// this configuration reads.
type Node struct {
	ID            string     `json:"id"`
	Kind          string     `json:"kind"`
	Lexicon       string     `json:"lexicon"`
	Attrs         NodeAttrs  `json:"attrs"`
	SourceLoc     *SourceLoc `json:"sourceLoc,omitempty"`
	PhysicalID    string     `json:"physicalId,omitempty"`
	InstanceOf    string     `json:"instanceOf,omitempty"`
	InstanceKey   string     `json:"instanceKey,omitempty"`
	InstanceScope string     `json:"instanceScope,omitempty"`
	Facts         *Facts     `json:"facts,omitempty"`
}

// NodeAttrs is a node's attrs: its address, or for an estate node the
// estate's name. Never a resource argument's value.
type NodeAttrs struct {
	Address string `json:"address,omitempty"`
	Estate  string `json:"estate,omitempty"`
}

// SourceLoc is where a block is declared, relative to the root directory.
type SourceLoc struct {
	File string `json:"file"`
	Line int    `json:"line,omitempty"`
}

// Facts is chant#4520's EngineFacts. Every field is absent when this run
// did not observe it.
type Facts struct {
	Ownership      string          `json:"ownership,omitempty"`
	Markers        *Markers        `json:"markers,omitempty"`
	OwnedBy        string          `json:"ownedBy,omitempty"`
	CloudIDs       []CloudID       `json:"cloudIds,omitempty"`
	RecordVersions *RecordVersions `json:"recordVersions,omitempty"`
	ObservedAt     string          `json:"observedAt,omitempty"`
}

func (f *Facts) empty() bool {
	return f == nil || (f.Ownership == "" && f.Markers == nil && f.OwnedBy == "" && len(f.CloudIDs) == 0 && f.RecordVersions == nil && f.ObservedAt == "")
}

// Markers are the marker values the live plan read off the object.
type Markers struct {
	Owner    string `json:"owner,omitempty"`
	Address  string `json:"address,omitempty"`
	Encoding string `json:"encoding"`
	Source   string `json:"source"`
}

// CloudID is one cloud identifier of a node.
type CloudID struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// RecordVersions are a record's versions, newest first, with no values.
type RecordVersions struct {
	Kept     bool            `json:"kept"`
	Versions []RecordVersion `json:"versions"`
}

// RecordVersion is one version of a record.
type RecordVersion struct {
	ID        string `json:"id"`
	WrittenAt string `json:"writtenAt"`
	Current   bool   `json:"current"`
	Deleted   bool   `json:"deleted"`
}

// Edge is a reference from one node to another.
type Edge struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Kind    string `json:"kind"`
	ViaAttr string `json:"viaAttr,omitempty"`
	Grain   string `json:"grain"`
}

// GrainBlock is every edge's grain until instance edges land (#2256): the
// reference was found between two blocks, and an instance's edge points at
// the block it reads, never at that block's instances.
const GrainBlock = "block"

// EstateNodeKind is the kind of a node that stands for a read estate.
const EstateNodeKind = "estate"

// EstateNodeID is the id of the node standing for estate.
func EstateNodeID(estate string) string { return "estate:" + estate }

// PlanDoc is the part of a "choudoufu live-plan -json" document
// (views.LivePlanDocument) the graph takes facts from. Nothing else in
// that document is decoded.
type PlanDoc struct {
	Estate     string          `json:"estate"`
	ObservedAt string          `json:"observed_at"`
	Bound      []PlanBound     `json:"bound"`
	Unowned    []PlanUnowned   `json:"unowned"`
	Adoptable  []PlanAdoptable `json:"adoptable"`
	Omissions  []PlanOmission  `json:"omissions"`
}

// PlanBound is a bound[] row.
type PlanBound struct {
	Addr     string `json:"addr"`
	Type     string `json:"type"`
	Identity string `json:"identity"`
	Source   string `json:"source"`
}

// PlanUnowned is an unowned[] row.
type PlanUnowned struct {
	Addr     string `json:"addr"`
	Type     string `json:"type"`
	Identity string `json:"identity"`
	HeldBy   string `json:"tofu_estate"`
}

// PlanAdoptable is an adoptable[] row.
type PlanAdoptable struct {
	Addr     string `json:"addr"`
	Type     string `json:"type"`
	Identity string `json:"identity"`
}

// PlanOmission is an omissions[] row. Its detail is not decoded.
type PlanOmission struct {
	Addr   string `json:"addr"`
	Reason string `json:"reason"`
}
