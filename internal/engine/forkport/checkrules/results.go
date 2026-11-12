// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package checkrules

import (
	"context"
	"sync"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/checks"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/states"
)

// Results collects the check results of one plan or apply on the new
// runtime, for the plan's Checks and the state's CheckResults (C3 of
// #2410). The evaluator reports into the Results its context carries
// ([WithResults]) as it goes; the default runtime's graph nodes report into
// a checks.State the same way.
//
// The evaluator builds module instances lazily, so no single point knows
// every checkable object. A resource reports its instances when it decides
// them ([RecordObjects]); outputs, check blocks and variables are known by
// the results they report. Whatever the walk never reached is in a module
// with no instances, and [Results.CheckResults] reports it with none, as
// the default runtime does. A configuration object under an instance whose
// expansion is not known yet stays unknown.
//
// A destroy plan and its apply check only input variables in the default
// runtime, so a destroy Results records only those and leaves every other
// configuration object unknown.
type Results struct {
	destroy  bool
	mu       sync.Mutex
	objects  addrs.Map[addrs.ConfigCheckable, addrs.Set[addrs.Checkable]]
	unknown  addrs.Set[addrs.ConfigCheckable]
	statuses addrs.Map[addrs.CheckRule, result]
}

type result struct {
	status checks.Status
	msg    string
}

// NewResults returns an empty collector, for a destroy plan or its apply
// if destroy is true.
func NewResults(destroy bool) *Results {
	return &Results{
		destroy:  destroy,
		objects:  addrs.MakeMap[addrs.ConfigCheckable, addrs.Set[addrs.Checkable]](),
		unknown:  addrs.MakeSet[addrs.ConfigCheckable](),
		statuses: addrs.MakeMap[addrs.CheckRule, result](),
	}
}

type resultsKey struct{}

// WithResults returns ctx for a walk that reports into r. With the port
// off it returns ctx unchanged, and nothing is recorded.
func WithResults(ctx context.Context, r *Results) context.Context {
	if !Enabled() {
		return ctx
	}
	return context.WithValue(ctx, resultsKey{}, r)
}

func resultsOf(ctx context.Context) *Results {
	r, _ := ctx.Value(resultsKey{}).(*Results)
	return r
}

// RecordObjects records the instances a resource has, so that one whose
// rules never ran is reported with unknown results, and one with none is
// reported as having none. placeholder is true when the instances are not
// known yet.
func RecordObjects(ctx context.Context, configAddr addrs.ConfigCheckable, objs []addrs.Checkable, placeholder bool) {
	r := resultsOf(ctx)
	if r == nil || r.destroy {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if placeholder {
		r.unknown.Add(configAddr)
		return
	}
	set := r.objectSet(configAddr)
	for _, obj := range objs {
		set.Add(obj)
	}
}

// Record records the result of the index'th rule of type ruleType of obj.
// msg is the failure message, for a failure. A later report of the same
// rule, from a value evaluated twice, replaces the earlier one.
func Record(ctx context.Context, obj addrs.Checkable, ruleType addrs.CheckRuleType, index int, status checks.Status, msg string) {
	r := resultsOf(ctx)
	if r == nil || (r.destroy && ruleType != addrs.InputValidation) {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if placeholderCheckable(obj) {
		r.unknown.Add(obj.ConfigCheckable())
		return
	}
	r.objectSet(obj.ConfigCheckable()).Add(obj)
	r.statuses.Put(obj.CheckRule(ruleType, index), result{status, msg})
}

func (r *Results) objectSet(configAddr addrs.ConfigCheckable) addrs.Set[addrs.Checkable] {
	set, ok := r.objects.GetOk(configAddr)
	if !ok {
		set = addrs.MakeSet[addrs.Checkable]()
		r.objects.Put(configAddr, set)
	}
	return set
}

// CheckResults is what was recorded, as the default runtime's
// states.NewCheckResults gives it for config.
func (r *Results) CheckResults(config *configs.Config) *states.CheckResults {
	st := checks.NewState(config)
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, configAddr := range st.AllConfigAddrs() {
		objs, reported := r.objects.GetOk(configAddr)
		if r.unknown.Has(configAddr) || (r.destroy && !reported) {
			continue
		}
		// An object nothing reported is in a module with no instances.
		st.ReportCheckableObjects(configAddr, objs)
	}
	for _, elem := range r.statuses.Elems {
		rule, res := elem.Key, elem.Value
		configAddr := rule.Container.ConfigCheckable()
		if !st.ConfigHasChecks(configAddr) || r.unknown.Has(configAddr) || res.status == checks.StatusUnknown {
			continue
		}
		if res.status == checks.StatusFail {
			st.ReportCheckFailure(rule.Container, rule.Type, rule.Index, res.msg)
		} else {
			st.ReportCheckResult(rule.Container, rule.Type, rule.Index, res.status)
		}
	}
	return states.NewCheckResults(st)
}

func placeholderCheckable(obj addrs.Checkable) bool {
	switch a := obj.(type) {
	case addrs.AbsResourceInstance:
		return a.IsPlaceholder()
	case addrs.AbsOutputValue:
		return a.Module.IsPlaceholder()
	case addrs.AbsCheck:
		return a.Module.IsPlaceholder()
	case addrs.AbsInputVariableInstance:
		return a.Module.IsPlaceholder()
	}
	return false
}
