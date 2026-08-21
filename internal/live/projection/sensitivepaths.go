// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is the record store's half of the one thing a state file does
// for a value that a bare ctyjson encoding cannot: remember WHICH parts of
// it were sensitive.
//
// A record-backed resource has no state file - the record IS its state - so
// [states.ResourceInstanceObjectSrc.AttrSensitivePaths], which the state
// file persists as "sensitive_attributes", has to be persisted here too or
// it is lost at the boundary. Losing it is not cosmetic. `live-plan` runs
// with SkipRefresh, so a projected object's marks are the ONLY marks the
// plan's "before" side ever has, while the "after" side is re-marked from
// the configuration and the provider schema every run
// (node_resource_abstract_instance.go's plan, which combines the config's
// own paths with schema.Block.ValueMarks). An unmarked before against a
// marked after is a difference, so every migrated estate holding a
// sensitive attribute proposed a perpetual sensitivity-only in-place update
// that OpenTofu's own renderer annotated "The value is unchanged".
//
// The CONCRETE-CLOUD path answers the same question in one line inside
// [importAndRead], where the provider's unmarked wire answer is marked from
// the schema the moment it arrives. GitHub issue #343 was filed on the
// reading that builder.materialize applied no schema marks; it applies them
// through importAndRead, and the issue closed on that finding. What was
// genuinely missing was a test, and
// TestMaterializeMarksASensitiveAttributeFromTheSchema is it.
//
// What is left for [markSchemaSensitive], at the bottom of this file, is the
// case where BOTH sources exist and have to be composed: a record carries the
// sensitivity it was written with, the schema carries the sensitivity of
// today, and the two are not the same set for a record written before an
// attribute became sensitive - or before this fork persisted paths at all.
// Composing them is what upstream's own refresh does
// (combinePathValueMarks(priorPaths, schema.Block.ValueMarks(...))); the
// record is that path's priorPaths.
//
// The encoding is deliberately the state file's own
// (internal/states/statefile/version4.go's marshalPaths/unmarshalPaths):
// a JSON array of paths, each an array of {"type","value"} steps. Those are
// unexported there and this fork does not widen an upstream package's API
// for its own use, so the shape is reproduced rather than imported - which
// is checked, not asserted, by TestSensitivePathsUseTheStateFilesOwnShape.

// pathStepJSON is one step of a cty.Path as the state file writes it. Value
// is a JSON string for an attribute step and a ctyjson-encoded dynamic value
// for an index step.
type pathStepJSON struct {
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value"`
}

const (
	indexPathStepType   = "index"
	getAttrPathStepType = "get_attr"
)

// splitSensitiveMarks separates a value from its sensitivity: the value with
// every mark stripped, and the paths that carried one.
//
// A mark that is not [marks.Sensitive] is REFUSED rather than dropped. The
// record format can carry exactly the one kind the state file carries, and
// silently discarding an ephemeral mark on the way into a persisted store is
// the shape of defect this repository's rulings say never to write: cty
// marks cannot carry provenance, so a dropped mark is unrecoverable and
// invisible. The caller turns the error into a refusal to record, which
// leaves the resource proposed for creation - loud, and correctable.
func splitSensitiveMarks(val cty.Value) (cty.Value, []cty.Path, error) {
	if val == cty.NilVal {
		return val, nil, nil
	}
	unmarked, pvms := val.UnmarkDeepWithPaths()
	if len(pvms) == 0 {
		return unmarked, nil, nil
	}
	paths := make([]cty.Path, 0, len(pvms))
	for _, pvm := range pvms {
		for mark := range pvm.Marks {
			if mark != marks.Sensitive {
				return cty.NilVal, nil, fmt.Errorf(
					"the value at %s carries a %v mark, and a persisted record can only carry sensitivity",
					tfdiags.FormatCtyPath(pvm.Path), mark)
			}
		}
		paths = append(paths, pvm.Path)
	}
	return unmarked, paths, nil
}

// asSensitiveMarks is [splitSensitiveMarks]'s inverse half: the
// [cty.PathValueMarks] form MarkWithPaths takes.
func asSensitiveMarks(paths []cty.Path) []cty.PathValueMarks {
	if len(paths) == 0 {
		return nil
	}
	pvms := make([]cty.PathValueMarks, 0, len(paths))
	for _, p := range paths {
		pvms = append(pvms, cty.PathValueMarks{Path: p, Marks: cty.NewValueMarks(marks.Sensitive)})
	}
	return pvms
}

// marshalSensitivePaths encodes paths for [recordPayload.SensitiveAttrs].
// nil is returned for an empty set, so a record for a value with no
// sensitivity is byte-identical to one written before this field existed -
// which matters because [SeedRecordForInstance] treats a byte-different
// record as a conflict rather than an update.
//
// The output is sorted by its own encoded form. cty's element iterators
// already walk object attributes in name order, but a persisted record is
// compared byte-for-byte by two separate callers and an ordering that
// depends on a library's traversal is not a property to lean on.
func marshalSensitivePaths(paths []cty.Path) (json.RawMessage, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	encoded := make([]json.RawMessage, 0, len(paths))
	for _, path := range paths {
		steps := make([]pathStepJSON, 0, len(path))
		for _, step := range path {
			switch s := step.(type) {
			case cty.GetAttrStep:
				name, err := json.Marshal(s.Name)
				if err != nil {
					return nil, fmt.Errorf("encoding the attribute step %q of a sensitive path: %w", s.Name, err)
				}
				steps = append(steps, pathStepJSON{Type: getAttrPathStepType, Value: name})
			case cty.IndexStep:
				key, err := ctyjson.Marshal(s.Key, cty.DynamicPseudoType)
				if err != nil {
					return nil, fmt.Errorf("encoding an index step of a sensitive path: %w", err)
				}
				steps = append(steps, pathStepJSON{Type: indexPathStepType, Value: key})
			default:
				return nil, fmt.Errorf("a sensitive path contains a %T step, which cannot be persisted", step)
			}
		}
		one, err := json.Marshal(steps)
		if err != nil {
			return nil, fmt.Errorf("encoding a sensitive path: %w", err)
		}
		encoded = append(encoded, one)
	}
	sort.Slice(encoded, func(i, j int) bool { return string(encoded[i]) < string(encoded[j]) })
	out, err := json.Marshal(encoded)
	if err != nil {
		return nil, fmt.Errorf("encoding the record's sensitive paths: %w", err)
	}
	return out, nil
}

// unmarshalSensitivePaths reverses [marshalSensitivePaths]. An absent field -
// every record written before this existed - is no paths and no error, the
// same "no sensitivity recorded" default the state file's own absent
// sensitive_attributes has. A field this package did not write is an error
// rather than a guess: a record whose marks cannot be read is a record whose
// value must not be trusted into a plan.
func unmarshalSensitivePaths(raw json.RawMessage) ([]cty.Path, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var encoded [][]pathStepJSON
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, fmt.Errorf("the stored sensitive paths are not valid JSON: %w", err)
	}
	paths := make([]cty.Path, 0, len(encoded))
	for _, steps := range encoded {
		var path cty.Path
		for _, step := range steps {
			switch step.Type {
			case getAttrPathStepType:
				var name string
				if err := json.Unmarshal(step.Value, &name); err != nil {
					return nil, fmt.Errorf("a stored sensitive path's attribute step could not be read: %w", err)
				}
				path = append(path, cty.GetAttrStep{Name: name})
			case indexPathStepType:
				key, err := ctyjson.Unmarshal(step.Value, cty.DynamicPseudoType)
				if err != nil {
					return nil, fmt.Errorf("a stored sensitive path's index step could not be read: %w", err)
				}
				path = append(path, cty.IndexStep{Key: key})
			default:
				return nil, fmt.Errorf("a stored sensitive path contains an unsupported step type %q", step.Type)
			}
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// markSchemaSensitive returns val carrying the sensitivity marks a real
// refresh would have put on it, derived from the provider's own schema and
// from nothing else.
//
// It is one call because upstream makes it one call: `refresh` ends with
//
//	marks := combinePathValueMarks(priorPaths, schema.Block.ValueMarks(ret.Value, nil, nil))
//
// (internal/tofu/node_resource_abstract_instance.go). `live-plan` runs the
// plan graph with SkipRefresh, so that line is never reached, and the
// projection is standing in for the refresh it deliberately skips - which
// means it owes the plan the same two sources of marks: whatever paths the
// value already carries, and whatever the schema says is Sensitive.
//
// The caller that needs both sources is builder.materializeRecord: the paths
// come out of the record and the schema is read fresh, and a record written
// before an attribute was Sensitive carries strictly fewer. The concrete
// path has no second source - a value off the wire carries no marks - so it
// marks in place inside [importAndRead] instead of calling this.
//
// Deriving it from the schema is the whole point. Every Sensitive attribute
// of every type is reached, including ones inside nested blocks and nested
// object attributes, because [configschema.Block.ValueMarks] descends them -
// and no provider type name appears anywhere in the control flow.
//
// The subject argument is nil, exactly as upstream's refresh passes it, so
// deprecation marks are NOT added. A deprecation mark carries a diagnostic
// the plan raises against a configuration; putting one on prior state read
// back out of the cloud would raise it against an argument nobody wrote.
//
// The unmark-first is not defensive tidying. [configschema.Block.ValueMarks]
// descends with GetAttr and ElementIterator, both of which panic on a marked
// receiver, so the schema walk has to run on a provably unmarked value -
// which UnmarkDeepWithPaths is what produces. The paths it strips are then
// combined back in rather than dropped, because a value reaching here can
// already carry marks that did not come from the schema (residue filled from
// a record, for one), and losing those would be the same defect in the other
// direction.
func markSchemaSensitive(val cty.Value, block *configschema.Block) cty.Value {
	if val == cty.NilVal || block == nil {
		return val
	}
	unmarked, priorPaths := val.UnmarkDeepWithPaths()
	schemaPaths := block.ValueMarks(unmarked, nil, nil)
	if len(schemaPaths) == 0 {
		// Nothing in this type's schema is sensitive, so the value is
		// returned exactly as it came in - marks, sharing and all. The
		// overwhelmingly common case, and the one where doing nothing is
		// visibly doing nothing.
		return val
	}
	combined := combineValueMarks(priorPaths, schemaPaths)
	return unmarked.MarkWithPaths(combined)
}

// combineValueMarks is internal/tofu's own combinePathValueMarks, which is
// unexported there and is the exact operation upstream's refresh performs on
// these two lists. Two marks at the same path merge into one entry rather
// than producing two, so that a value the record store already marked
// Sensitive and the schema also marks Sensitive comes out with one mark and
// one path, not a duplicate pair that [states.ResourceInstanceObject.Encode]
// would then write twice into AttrSensitivePaths.
//
// Reproduced rather than imported for sensitivepaths.go's own stated reason:
// this fork does not widen an upstream package's API for its own use.
// [TestCombineValueMarksMergesOnePathOnce] is the check that the
// reproduction still has the property it was reproduced for.
func combineValueMarks(a, b []cty.PathValueMarks) []cty.PathValueMarks {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	combined := make([]cty.PathValueMarks, 0, len(a)+len(b))
	combined = append(combined, a...)
	for _, add := range b {
		merged := false
		for i, existing := range combined {
			if !add.Path.Equals(existing.Path) {
				continue
			}
			// Copy before mutating: the receiver's ValueMarks map can be
			// shared with the value the caller still holds.
			dupe := make(cty.ValueMarks, len(existing.Marks)+len(add.Marks))
			for k, v := range existing.Marks {
				dupe[k] = v
			}
			for k, v := range add.Marks {
				dupe[k] = v
			}
			combined[i] = cty.PathValueMarks{Path: existing.Path, Marks: dupe}
			merged = true
			break
		}
		if !merged {
			combined = append(combined, add)
		}
	}
	return combined
}
