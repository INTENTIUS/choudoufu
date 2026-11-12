// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
	"github.com/intentius/choudoufu/pkg/identity"
	"github.com/intentius/choudoufu/pkg/staterecord"
)

// This file is the plan's half of the capacity check (GitHub issue #2393,
// follow-up 1). Without it the store checked, at its first parameter
// create, the parameters the records it had read already reference plus
// that one write's own: every later create in the same run was invisible,
// so a greenfield apply of thousands of record-backed secrets passed the
// check at its first write and met SSM's ceiling partway through.
//
// With it, the count is the estate after this plan: every record the store
// holds now, less the ones the plan deletes, with each record the plan
// writes counted at the larger of what it references now and what the plan
// says it will hold.

// PlannedSecretWrite is one planned change as the capacity count sees it.
type PlannedSecretWrite struct {
	Addr addrs.AbsResourceInstance
	// PrevAddr is the address the record was under before a moved block,
	// or the zero value. Its record moves to Addr.
	PrevAddr addrs.AbsResourceInstance
	// Delete is a planned destroy: the record goes, and its parameters.
	Delete bool
	// Count is the parameters the plan's value for the instance will put
	// in SSM, as far as a plan can know them: one per sensitive path whose
	// planned value is not null, an unknown counted as present. A value's
	// size is unknown before it exists, so each is counted standard.
	Count SecretParameterCount
}

// PlannedSecretWrites reads the plan's resource changes into the writes
// [SSMSecretsStore.PlanCount] counts.
//
// Only a record-backed instance (random_password, tls_private_key: the
// record is the whole object) is counted from the plan's value. An
// ordinary resource's record holds the residue, which is decided after the
// apply against what the provider returns, and its provider private bytes,
// which exist only after the apply; for those the count keeps what the
// record references now. Counting every sensitive argument of an ordinary
// resource instead would refuse estates that fit, which #1309's bands
// exist to avoid.
func PlannedSecretWrites(changes *plans.Changes, schemas *tofu.Schemas) ([]PlannedSecretWrite, error) {
	if changes == nil {
		return nil, nil
	}
	var out []PlannedSecretWrite
	for _, rc := range changes.Resources {
		if rc == nil || rc.Addr.Resource.Resource.Mode != addrs.ManagedResourceMode || rc.DeposedKey != states.NotDeposed {
			continue
		}
		w := PlannedSecretWrite{Addr: rc.Addr}
		if rc.PrevRunAddr.Resource.Resource.Type != "" && !rc.PrevRunAddr.Equal(rc.Addr) {
			w.PrevAddr = rc.PrevRunAddr
		}
		switch rc.Action {
		case plans.Delete:
			w.Delete = true
			out = append(out, w)
			continue
		case plans.Create, plans.Update, plans.DeleteThenCreate, plans.CreateThenDelete:
		default:
			if w.PrevAddr.Resource.Resource.Type != "" {
				out = append(out, w)
			}
			continue
		}
		typeName := rc.Addr.Resource.Resource.Type
		if ti, ok := identity.LookupType(typeName); ok && ti.RecordBacked && schemas != nil {
			schema, _ := schemas.ResourceTypeConfig(rc.ProviderAddr.Provider, addrs.ManagedResourceMode, typeName)
			if schema == nil || schema.Block == nil {
				return nil, fmt.Errorf("counting secret parameters: no schema for %s", typeName)
			}
			change, err := rc.Decode(schema)
			if err != nil {
				return nil, fmt.Errorf("counting secret parameters for %s: %w", rc.Addr, err)
			}
			w.Count.Standard = countSensitivePaths(change.After, schema.Block.ValueMarks)
		}
		out = append(out, w)
	}
	return out, nil
}

// countSensitivePaths is how many distinct paths of val are sensitive, by
// its own marks or by the schema's, and hold something: a known null moves
// nothing (splitSecrets skips it), and an unknown will hold a value.
func countSensitivePaths(val cty.Value, schemaMarks func(cty.Value, cty.Path, *addrs.AbsResourceInstance) []cty.PathValueMarks) int {
	if val == cty.NilVal {
		return 0
	}
	unmarked, pvms := val.UnmarkDeepWithPaths()
	if !unmarked.IsKnown() || unmarked.IsNull() {
		return 0
	}
	pvms = append(pvms, schemaMarks(unmarked, nil, nil)...)
	var seen []cty.Path
	n := 0
	for _, pvm := range pvms {
		if _, ok := pvm.Marks[marks.Sensitive]; !ok {
			continue
		}
		dup := false
		for _, p := range seen {
			if p.Equals(pvm.Path) {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		seen = append(seen, pvm.Path)
		v, err := pvm.Path.Apply(unmarked)
		if err == nil && v.IsKnown() && v.IsNull() {
			continue
		}
		n++
	}
	return n
}

// PlanCount is the number of parameters the estate's records will
// reference once writes are applied: what every record under this store's
// namespace references now, read from this run's own reads where it has
// them and from the bucket where it does not, with writes applied over it.
// A record a write changes counts at the larger of the two, per tier, since
// the plan cannot see the parameters an apply adds beside the ones it
// counts.
func (s *SSMSecretsStore) PlanCount(ctx context.Context, writes []PlannedSecretWrite) (SecretParameterCount, error) {
	var total SecretParameterCount
	keys, err := s.inner.List(ctx, s.cfg.RecordPrefix)
	if err != nil {
		return total, fmt.Errorf("counting secret parameters: listing the records: %w", err)
	}
	counts := map[string]SecretParameterCount{}
	for _, key := range keys {
		if !s.isRecordKey(key) {
			continue
		}
		s.mu.Lock()
		n, seen := s.perKey[key]
		s.mu.Unlock()
		if !seen {
			payload, version, exists, err := s.inner.Get(ctx, key)
			if err != nil {
				return total, fmt.Errorf("counting secret parameters: reading the record at %q: %w", key, err)
			}
			if !exists {
				continue
			}
			refs, err := rawRefs(payload)
			if err != nil {
				return total, fmt.Errorf("counting secret parameters: the record at %q: %w", key, err)
			}
			s.noteBase(key, version, refs)
			n = countRefs(refs)
		}
		counts[key] = n
	}
	for _, w := range writes {
		key := RecordKey(s.cfg.RecordPrefix, w.Addr)
		cur := counts[key]
		if w.PrevAddr.Resource.Resource.Type != "" {
			prev := RecordKey(s.cfg.RecordPrefix, w.PrevAddr)
			cur = maxCount(cur, counts[prev])
			delete(counts, prev)
		}
		if w.Delete {
			delete(counts, key)
			continue
		}
		counts[key] = maxCount(cur, w.Count)
	}
	for _, n := range counts {
		total.Standard += n.Standard
		total.Advanced += n.Advanced
	}
	return total, nil
}

func maxCount(a, b SecretParameterCount) SecretParameterCount {
	return SecretParameterCount{Standard: max(a.Standard, b.Standard), Advanced: max(a.Advanced, b.Advanced)}
}

// CheckPlannedSecretsCapacity is the post-plan step: for a store that keeps
// secrets in SSM, count what the estate will reference once plan is
// applied, hand the count to the store for its first write, and return
// #1309's diagnostics for it - an error over a ceiling, which stops a plan
// and an apply before anything is written, and a warning in the band below.
// Any other store gets nothing.
func CheckPlannedSecretsCapacity(ctx context.Context, store staterecord.Store, plan *plans.Plan, schemas *tofu.Schemas) tfdiags.Diagnostics {
	ss, ok := AsSSMSecretsStore(store)
	if !ok || plan == nil {
		return nil
	}
	var diags tfdiags.Diagnostics
	writes, err := PlannedSecretWrites(plan.Changes, schemas)
	if err == nil {
		var n SecretParameterCount
		if n, err = ss.PlanCount(ctx, writes); err == nil {
			ss.PlanSecretParameters(n)
			return CheckSecretsCapacity(n)
		}
	}
	// The count could not be made. The store's own check still runs at the
	// first write, on its lower bound, so this says what was lost rather
	// than refusing a plan over it.
	return diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummarySecretsCapacityUncounted, fmt.Sprintf(
		"The plan's count of secret parameters could not be made (%s). The first write still checks SSM's ceiling, on the parameters this run can see, which may be fewer than the apply will create.", err)))
}

// SummarySecretsCapacityUncounted is [CheckPlannedSecretsCapacity]'s
// warning when the count fails.
const SummarySecretsCapacityUncounted = "Secret parameters not counted from the plan"
