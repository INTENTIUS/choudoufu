// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
)

// fieldGranularFields is what the record store keeps about a
// field-granular Kubernetes instance (GitHub issue #1863). Two questions
// need an answer the cloud cannot give:
//
//   - Does this estate have field-granular instances at all, including
//     ones whose block the configuration has since dropped, and which
//     kinds do they patch? The field-manager orphan sweep
//     (internal/live/discovery's kubernetes_fieldorphans.go) costs one
//     unselected list per kind, so it runs only for an estate that does,
//     and only over those kinds. Written by every apply's write-back.
//   - Was this instance migrated off a stock state file, and under which
//     manager did the stock apply write? Only then may a plan read its
//     fields under that manager and hand them over (fieldgranular_handover.go);
//     a block that was never migrated must not take another stock
//     configuration's fields. Written by live-import -approve, and cleared
//     by the first apply that writes the instance under the estate's own
//     manager.
type fieldGranularFields struct {
	APIVersion   string `json:"api_version,omitempty"`
	Kind         string `json:"kind,omitempty"`
	HandoverFrom string `json:"handover_from,omitempty"`
}

// FieldGranularRecord is one instance's [fieldGranularFields], as a reader
// outside this package sees it.
type FieldGranularRecord struct {
	Addr         addrs.AbsResourceInstance
	APIVersion   string
	Kind         string
	HandoverFrom string
}

// RecordFieldGranular writes addr's field-granular record: the object's
// apiVersion and kind, and handoverFrom, the stock field manager a
// migration found the fields under. An empty handoverFrom keeps whatever
// is recorded. live-import is the caller; write-back keeps the record
// current after an apply on its own.
func (s *RecordStore) RecordFieldGranular(ctx context.Context, addr addrs.AbsResourceInstance, apiVersion, kind, handoverFrom string) error {
	if s == nil {
		return fmt.Errorf("no record store is configured, so %s's migration cannot be recorded", addr)
	}
	expected, err := s.currentVersion(ctx, addr)
	if err != nil {
		return err
	}
	_, err = s.mergeEnvelope(ctx, addr, expected, func(env *recordEnvelope) {
		next := fieldGranularFields{APIVersion: apiVersion, Kind: kind, HandoverFrom: handoverFrom}
		if next.HandoverFrom == "" && env.FieldGranular != nil {
			next.HandoverFrom = env.FieldGranular.HandoverFrom
		}
		env.FieldGranular = &next
	})
	return err
}

// GetFieldGranular reads addr's field-granular record. found is false for
// a key that does not exist or holds none. Only a store error is an error.
func (s *RecordStore) GetFieldGranular(ctx context.Context, addr addrs.AbsResourceInstance) (FieldGranularRecord, bool, error) {
	if s == nil {
		return FieldGranularRecord{}, false, nil
	}
	env, _, exists, err := s.getEnvelope(ctx, addr, false)
	if err != nil || !exists || env.FieldGranular == nil {
		return FieldGranularRecord{}, false, err
	}
	f := env.FieldGranular
	return FieldGranularRecord{Addr: addr, APIVersion: f.APIVersion, Kind: f.Kind, HandoverFrom: f.HandoverFrom}, true, nil
}

// ListFieldGranular is every field-granular record the store holds for an
// instance of one of types, the configuration's or not. Only the keys
// whose address names one of types are read, so an estate with none costs
// one key listing and no reads.
func (s *RecordStore) ListFieldGranular(ctx context.Context, types map[string]bool) ([]FieldGranularRecord, error) {
	if s == nil || len(types) == 0 {
		return nil, nil
	}
	keys, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []FieldGranularRecord
	for _, key := range keys {
		addr, ok := RecordAddr(s.prefix, key)
		if !ok || !types[addr.Resource.Resource.Type] {
			continue
		}
		rec, found, err := s.GetFieldGranular(ctx, addr)
		if err != nil {
			return nil, err
		}
		if found {
			out = append(out, rec)
		}
	}
	return out, nil
}

// fieldGranularRecordFor is write-back's reading of one applied instance:
// the record it should hold, and whether the apply wrote it under an
// estate's own manager (which ends a migration's hand-over). nil for an
// instance that is not field-granular or whose object cannot be named.
func fieldGranularRecordFor(schema *providers.Schema, typeName string, ri *states.ResourceInstance) (rec *fieldGranularFields, estateOwned bool) {
	if schema == nil || schema.Block == nil || ri == nil || ri.Current == nil || !fieldGranularOwned("", *schema) {
		return nil, false
	}
	obj, err := ri.Current.Decode(schema.Block.ImpliedType())
	if err != nil {
		return nil, false
	}
	v, _ := obj.Value.UnmarkDeep()
	if v.IsNull() || !v.IsKnown() || !v.Type().IsObjectType() {
		return nil, false
	}
	str := func(name string) string {
		if !v.Type().HasAttribute(name) {
			return ""
		}
		a := v.GetAttr(name)
		if a.IsNull() || !a.IsKnown() || a.Type() != cty.String {
			return ""
		}
		return a.AsString()
	}
	rec = &fieldGranularFields{}
	if substrate.FieldGranularNamesKind(schema.Block) {
		rec.APIVersion, rec.Kind = str("api_version"), str("kind")
	} else {
		rec.APIVersion, rec.Kind = kubesweep.FieldGranularFixedKind(typeName)
	}
	if rec.Kind == "" {
		return nil, false
	}
	return rec, strings.HasPrefix(str(substrate.FieldManagerAttr), markers.FieldManagerPrefix)
}

// fieldGranularMerged is what write-back leaves recorded: rec's kind,
// and the recorded hand-over unless the apply ended it.
func fieldGranularMerged(prev *fieldGranularFields, rec *fieldGranularFields, estateOwned bool) *fieldGranularFields {
	next := *rec
	if !estateOwned && prev != nil {
		next.HandoverFrom = prev.HandoverFrom
	}
	return &next
}
