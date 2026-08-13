// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// recordBackedTable holds the identity table entries for GitHub issue #73's
// RECORD_ADMITTED logical types (internal/live/lint's ClassifyLogicalType):
// null_resource, terraform_data, the four time_* resources, and the four
// non-sensitive random_* resources hand-verified there. Every entry sets
// only [TypeIdentity.RecordBacked] - no Components, because a record-backed
// instance's identity is the persisted micro-state record itself, never a
// concatenation of configuration arguments the way every other row in
// [DefaultTable] is.
//
// Kept in its own file rather than folded into table.go's huge hand-written
// literal because these rows are not part of that AWS provider survey: they
// are the identity package's half of a classification internal/live/lint
// already owns and verifies (logical_type.go's logicalTypes table is the
// citation-bearing source of truth for "why is this type's output not a
// secret"). This file only has to agree with that table on which type names
// are RECORD_ADMITTED, which init below does once at package load rather
// than on every resolution.
var recordBackedTable = buildTable(
	TypeIdentity{Type: "null_resource", RecordBacked: true},
	TypeIdentity{Type: "terraform_data", RecordBacked: true},
	TypeIdentity{Type: "time_static", RecordBacked: true},
	TypeIdentity{Type: "time_offset", RecordBacked: true},
	TypeIdentity{Type: "time_rotating", RecordBacked: true},
	TypeIdentity{Type: "time_sleep", RecordBacked: true},
	TypeIdentity{Type: "random_id", RecordBacked: true},
	TypeIdentity{Type: "random_pet", RecordBacked: true},
	TypeIdentity{Type: "random_shuffle", RecordBacked: true},
	TypeIdentity{Type: "random_integer", RecordBacked: true},
)

// init merges recordBackedTable into DefaultTable once, after both package
// vars have their own initializers run (Go runs every package-level var
// initializer before any init() func, regardless of file order), so this
// never races DefaultTable's own construction in table.go.
//
// Merging into DefaultTable itself - rather than having [LookupType] consult
// two maps - means every existing caller (AdmittedTypes, the schema-fallback
// memo, resolve.go's r.lookupType) sees these ten types with no change of
// its own: they are simply ten more rows, distinguished by RecordBacked
// rather than by Components. Unconditionally merging them is safe even for
// a run with no record store configured, because internal/live/lint refuses
// every RECORD_ADMITTED type before resolution ever runs unless a
// record_store block is present (internal/live/lint/lint.go's
// checkManagedResources); a caller that reaches resolve.go with one of
// these types in its configuration has already passed that gate.
func init() { registerCohortTable(recordBackedTable) }
