// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"log"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// The stock hand-over read (GitHub issue #1863, the second of PR #1828's
// follow-ups to #1191).
//
// A field-granular block applied by stock OpenTofu wrote its fields under
// the provider's default field manager, "Terraform"
// ([kubesweep.DefaultFieldManager]). Under a live block the marker is the
// estate's own manager, "choudoufu:<estate>", and the read is made under
// it ([fieldGranularSeed]), so the first live plan after a migration found
// nothing the estate owns and proposed the write as a create - and the
// apply of that create then shared every field with "Terraform" rather
// than taking it over, so the stock manager kept a claim on fields the
// estate now writes.
//
// So when the estate's read comes back holding nothing AND the estate's
// record says the instance was migrated off a stock state file
// (live-import writes the stock manager into it, fieldgranular_record.go),
// the instance is read a second time under that manager - "Terraform"
// unless the stock block named another - and what that manager owns of the
// keys this configuration declares is the prior. Without that evidence
// nothing is taken: a block this estate never migrated is planned as a
// create, because "Terraform" fields on its object may be another stock
// configuration's, and the plan-time boundary warns that the write will
// share them (discovery.SummaryFieldSharedWithStock). The plan is then an
// update - field_manager from "Terraform" to the estate's - rather than a
// create, which is what stock would plan for the same block with its state
// file in hand. The apply's half is internal/command's: before the first
// change, ownership of exactly those fields moves from "Terraform" to the
// estate's manager on the live object (kubesweep.TransferFieldOwnership),
// so the provider's write lands on fields the estate already owns. Only
// "Terraform" is read and only "Terraform" is handed over: a field another
// estate's manager owns is that estate's, and the plan-time boundary
// (discovery.CheckKubernetesFieldOwners) says so whatever happens here.
//
// Keys "Terraform" owns that this configuration does not declare are left
// out of the prior. They are not this block's - another stock
// configuration sharing the default manager could have written them - and
// a prior holding them would plan their removal, which the apply would not
// carry out because the hand-over leaves them with "Terraform".

// fieldGranularDeclaredMaps copies the written maps out of a seed before
// [fieldGranularSeed] removes them.
func fieldGranularDeclaredMaps(seed map[string]cty.Value) map[string]cty.Value {
	var out map[string]cty.Value
	for _, name := range fieldGranularWrittenMaps {
		v, ok := seed[name]
		if !ok {
			continue
		}
		if out == nil {
			out = map[string]cty.Value{}
		}
		out[name] = v
	}
	return out
}

// fieldGranularHandover is the second read; see this file's doc comment.
// ok is false - and the instance stays absent, a create - when the read
// fails, finds nothing, or what "Terraform" owns is none of what the
// configuration declares.
func (b *builder) fieldGranularHandover(ctx context.Context, w wanted, p readPrep) (*states.ResourceInstanceObject, cty.Value, tfdiags.Diagnostics, bool) {
	if p.entry == nil || p.schema.Block == nil {
		return nil, cty.NilVal, nil, false
	}
	// Migration evidence first: only an instance live-import recorded as
	// migrated off a stock state file is read under the stock manager. A
	// block that was never migrated is planned as the create it is, and
	// the plan-time boundary names the stock manager's shared fields.
	rec, found, err := b.opts.RecordStore.GetFieldGranular(ctx, w.addr)
	if err != nil || !found || rec.HandoverFrom == "" {
		return nil, cty.NilVal, nil, false
	}
	if _, isEstate := markers.EstateOfFieldManager(rec.HandoverFrom); isEstate {
		return nil, cty.NilVal, nil, false
	}
	seed := make(map[string]cty.Value, len(p.attrsSeed))
	for k, v := range p.attrsSeed {
		seed[k] = v
	}
	seed[substrate.FieldManagerAttr] = cty.StringVal(rec.HandoverFrom)

	obj, stub, status, diags := importAndRead(ctx, p.entry.provider, p.schema, w.addr.Resource.Resource.Type, p.target, w.importID, w.values, seed, p.attrsSeedMarks, p.manifestKeys)
	if status != statusMaterialized || obj == nil {
		return nil, cty.NilVal, nil, false
	}
	kept := fieldGranularKeepDeclared(obj.Value, p.schema, p.fieldGranularDeclared)
	if !fieldGranularHoldsFields(kept, p.schema) {
		return nil, cty.NilVal, nil, false
	}
	log.Printf("[TRACE] projection: %s has no fields under this estate's field manager, its record says it was migrated from %q, and %q owns some it declares; planning the hand-over as an update", w.addr, rec.HandoverFrom, rec.HandoverFrom)
	obj.Value = kept
	return obj, stub, diags, true
}

// fieldGranularKeepDeclared returns obj with each written map cut to the
// keys declared names for it. A map declared names nothing for - not set,
// or not statically known - is emptied: no key of it can be shown to be
// this block's. The written blocks (env, taint) are left as read, since
// the provider's own read already keys them by what the prior names.
func fieldGranularKeepDeclared(obj cty.Value, schema providers.Schema, declared map[string]cty.Value) cty.Value {
	obj, topMarks := obj.Unmark()
	if obj.IsNull() || !obj.IsKnown() || !obj.Type().IsObjectType() {
		return obj.WithMarks(topMarks)
	}
	attrs := obj.AsValueMap()
	for _, name := range fieldGranularWrittenMaps {
		v, has := attrs[name]
		if !has {
			continue
		}
		raw, marks := v.Unmark()
		if raw.IsNull() || !raw.IsKnown() || !raw.CanIterateElements() {
			continue
		}
		want := map[string]bool{}
		if d, ok := declared[name]; ok {
			d, _ = d.Unmark()
			if !d.IsNull() && d.IsKnown() && d.CanIterateElements() {
				for it := d.ElementIterator(); it.Next(); {
					k, _ := it.Element()
					if k.IsKnown() && !k.IsNull() {
						want[k.AsString()] = true
					}
				}
			}
		}
		keep := map[string]cty.Value{}
		for it := raw.ElementIterator(); it.Next(); {
			k, e := it.Element()
			if want[k.AsString()] {
				keep[k.AsString()] = e
			}
		}
		ety := raw.Type().ElementType()
		if len(keep) == 0 {
			attrs[name] = cty.MapValEmpty(ety).WithMarks(marks)
			continue
		}
		attrs[name] = cty.MapVal(keep).WithMarks(marks)
	}
	return cty.ObjectVal(attrs).WithMarks(topMarks)
}
