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
// This fixes the RECORD-BACKED half only. builder.materialize, one path
// over, builds its object from the provider's own unmarked wire answer and
// applies no schema.Block.ValueMarks before encoding it, so a concrete cloud
// object with a Sensitive attribute has the identical perpetual diff. That
// is GitHub issue #343, and it is separate because it changes what b.live
// means for identity composition, not only what is persisted.
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
