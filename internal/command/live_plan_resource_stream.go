// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"encoding/json"
	"sync"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// liveDriftStream writes live-plan -resource's drift records as each one is
// known, rather than all of them after the plan (GitHub issue #2260):
//
//   - once the projection is built, every expected instance it already
//     answered for (unclaimed, pending, not observed), through
//     [liveDriftStream.emitSettled];
//   - while the plan walks the cone, each planned instance as its PostDiff
//     hook fires, from the action and the two values the hook is handed;
//   - after the plan, [liveDriftStream.finish] writes every record
//     [liveDriftRecords] computes from the finished plan that differs from
//     the one already written for its address, and every one not yet
//     written (an instance the plan never reached).
//
// chant reads the stream that way: "a later line for the same (member,
// address, read) replaces it" (DriftRecordLine, packages/core/src/
// drift-record.ts), so a record the final pass corrects is a second line,
// never a contradiction.
type liveDriftStream struct {
	tofu.NilHook

	drift *views.LiveDrift

	mu      sync.Mutex
	ready   bool
	in      liveDriftInput
	ix      liveDriftIndex
	want    map[string]addrs.AbsResourceInstance
	written map[string]views.LiveDriftRecord
}

var _ tofu.Hook = (*liveDriftStream)(nil)

func newLiveDriftStream(drift *views.LiveDrift) *liveDriftStream {
	return &liveDriftStream{drift: drift, written: map[string]views.LiveDriftRecord{}}
}

// begin is called once the projection is built and the plan's schemas are
// known. Until it is called the hook writes nothing.
func (s *liveDriftStream) begin(in liveDriftInput) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.in = in
	s.ix = newLiveDriftIndex(in)
	s.want = make(map[string]addrs.AbsResourceInstance, len(in.Expected))
	for _, a := range in.Expected {
		s.want[a.String()] = a
	}
	s.ready = true
}

// emitSettled writes every expected instance the projection answered for.
func (s *liveDriftStream) emitSettled() {
	s.mu.Lock()
	in, ix := s.in, s.ix
	s.mu.Unlock()
	for _, addr := range in.Expected {
		if rec, ok := ix.settled(in, addr); ok {
			s.write(rec)
		}
	}
}

// PostDiff writes the record of one planned instance of the cone.
func (s *liveDriftStream) PostDiff(addr addrs.AbsResourceInstance, gen states.Generation, action plans.Action, priorState, plannedNewState cty.Value) (tofu.HookAction, error) {
	if gen != states.CurrentGen {
		return tofu.HookActionContinue, nil
	}
	s.mu.Lock()
	ready, in, ix := s.ready, s.in, s.ix
	_, want := s.want[addr.String()]
	s.mu.Unlock()
	if !ready || !want {
		return tofu.HookActionContinue, nil
	}
	if _, settled := ix.settled(in, addr); settled {
		return tofu.HookActionContinue, nil
	}
	rec := ix.base(in, addr)
	liveDriftApplyAction(&rec, action, func() []views.LiveDriftAttribute {
		return liveDriftAttributesOf(priorState, plannedNewState, liveDriftSchemaBlock(in.Schemas, addr.Resource.Resource.Type))
	})
	s.write(rec)
	return tofu.HookActionContinue, nil
}

// finish writes what the finished plan says wherever it differs from, or
// is missing in, what was streamed.
func (s *liveDriftStream) finish(records []views.LiveDriftRecord) {
	for _, r := range records {
		s.write(r)
	}
}

// write writes r unless the same record is already the last one written
// for its address.
func (s *liveDriftStream) write(r views.LiveDriftRecord) {
	s.mu.Lock()
	if prev, ok := s.written[r.Address]; ok && sameDriftRecord(prev, r) {
		s.mu.Unlock()
		return
	}
	s.written[r.Address] = r
	s.drift.Record(r)
	s.mu.Unlock()
	if hook := liveDriftRecordWritten; hook != nil {
		hook(r.Address)
	}
}

// sameDriftRecord compares two records of one address, observedAt aside.
func sameDriftRecord(a, b views.LiveDriftRecord) bool {
	a.ObservedAt, b.ObservedAt = "", ""
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(ja) == string(jb)
}

// liveDriftSchemaBlock is typeName's managed-resource schema among the
// plan's providers, or nil when no single provider serves it.
func liveDriftSchemaBlock(schemas *tofu.Schemas, typeName string) *configschema.Block {
	if schemas == nil {
		return nil
	}
	var found *configschema.Block
	for _, ps := range schemas.Providers {
		rs, ok := ps.ResourceTypes[typeName]
		if !ok || rs.Block == nil {
			continue
		}
		if found != nil {
			return nil
		}
		found = rs.Block
	}
	return found
}
