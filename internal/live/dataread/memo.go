// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package dataread

import (
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
)

// ReadMemo is GitHub issue #1537's cross-pass answer cache for one run's
// provider-configuration fixpoint (internal/command's
// statelessProviderDataReads). That fixpoint analyzes and reads, reads the
// managed values the analysis demanded, then analyzes and reads again with
// those values in hand, up to its pass cap. Before this type every pass
// re-read every source it classified, so a source the first pass had
// already answered - data.aws_region.current, whose arguments wait on
// nothing - was read once per pass: measured as aws_region=2 on
// internal/command/testdata/live-target-provider-work.
//
// # What invalidates an answer
//
// The issue asked the right question: a fixpoint exists because a later
// pass can know more than an earlier one, so can a later pass legitimately
// expect a DIFFERENT answer from a source an earlier pass already read?
//
// Only if it would send a different request. Within one run the provider
// process, its configuration and the live object are all the ones the
// earlier pass talked to; what a later pass changes is the managed values
// in scope ([Options.LiveManagedResults]), and those reach a read only
// through the source's own decoded arguments (or its expansion, which
// shows up as a different instance key). So an answer is keyed by the
// provider configuration, the source, and the instance keys one provider
// call answered, and it is reused only when the decoded configuration
// value this pass would send is [cty.Value.RawEquals] the one the earlier
// pass sent. A source whose arguments read a managed value that a later
// pass now answers from a live read rather than from the configuration
// sends a different request and is read again, which is the case a
// fixpoint has to keep. A source that never became readable on an earlier
// pass has no entry and is read the first time a pass can read it.
//
// Only answered reads are remembered. A read that failed is not an answer:
// a later pass retries it exactly as it did before this type existed,
// because a scoped failure is a warning and treating it as settled would
// turn a transient error into a silent omission for the rest of the run.
//
// # What it is not
//
// It is per run, built by the caller and dropped when the fixpoint
// returns. [Read]'s rule that values are never cached across runs is
// untouched: nothing here outlives the run that read it, so a stale value
// can never reach a marker from a previous run.
//
// A nil *ReadMemo is valid and remembers nothing.
type ReadMemo struct {
	answers map[string]memoAnswer
}

type memoAnswer struct {
	config cty.Value
	state  cty.Value
}

// NewReadMemo returns an empty memo for one run's fixpoint.
func NewReadMemo() *ReadMemo {
	return &ReadMemo{answers: make(map[string]memoAnswer)}
}

// Len is the number of provider calls the memo holds an answer for.
func (m *ReadMemo) Len() int {
	if m == nil {
		return 0
	}
	return len(m.answers)
}

// lookup returns the remembered state for key when the request config is
// the one that produced it.
func (m *ReadMemo) lookup(key string, config cty.Value) (cty.Value, bool) {
	if m == nil {
		return cty.NilVal, false
	}
	prev, ok := m.answers[key]
	if !ok || !prev.config.RawEquals(config) {
		return cty.NilVal, false
	}
	return prev.state, true
}

// remember records one answered provider call. A later answer for the same
// key with a different request replaces the earlier one, so the memo always
// holds the most recent request's answer.
func (m *ReadMemo) remember(key string, config, state cty.Value) {
	if m == nil {
		return
	}
	m.answers[key] = memoAnswer{config: config, state: state}
}

// memoKey names one provider call: the provider configuration that answers
// it, the source, and the instance keys the call's answer is stored under
// (every instance for a shared read, one for a [Source.PerInstance] read).
func memoKey(provider addrs.AbsProviderConfig, src *Source, keys []addrs.InstanceKey) string {
	var b strings.Builder
	b.WriteString(provider.String())
	b.WriteByte(0)
	b.WriteString(sourceKey(src.Module, src.Resource))
	for _, k := range keys {
		b.WriteByte(0)
		if k == addrs.NoKey {
			b.WriteString("<nokey>")
		} else {
			b.WriteString(k.String())
		}
	}
	return b.String()
}
