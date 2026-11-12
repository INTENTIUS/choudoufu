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
	"github.com/intentius/choudoufu/internal/tfdiags"
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

	// checkData and checkDeps are #2418's: see [RecordCheckDataRead] and
	// [RecordCheckDependencies].
	checkData addrs.Map[addrs.AbsResourceInstance, checkData]
	checkDeps addrs.Set[addrs.AbsResourceInstance]
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

		checkData: addrs.MakeMap[addrs.AbsResourceInstance, checkData](),
		checkDeps: addrs.MakeSet[addrs.AbsResourceInstance](),
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

// checkData is what reading a data resource instance declared in a check
// block came to (#2418): whether it failed and, if so, the failure message.
type checkData struct {
	failed bool
	msg    string
}

// RecordCheckDataRead records how the read of addr, a data resource
// instance declared in a check block, went: diags are the read's
// diagnostics, before they are made warnings. The check block reports it as
// its own result, as the default runtime's planDataSource and
// readDataSource do: the rule of type CheckDataResource fails with the
// read's error, or passes.
func RecordCheckDataRead(ctx context.Context, addr addrs.AbsResourceInstance, diags tfdiags.Diagnostics) {
	r := resultsOf(ctx)
	if r == nil {
		return
	}
	res := checkData{}
	if diags.HasErrors() {
		res = checkData{failed: true, msg: diags.Err().Error()}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checkData.Put(addr, res)
}

// CheckDataFailure returns the failure message [RecordCheckDataRead]
// recorded for addr, and whether its read failed. A read that never
// happened, because it was deferred to the apply, did not fail.
func CheckDataFailure(ctx context.Context, addr addrs.AbsResourceInstance) (string, bool) {
	r := resultsOf(ctx)
	if r == nil {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	res, ok := r.checkData.GetOk(addr)
	return res.msg, ok && res.failed
}

// RecordCheckDependencies records resource instances a check block's
// assertions read (#2418). The default runtime's apply checks them against
// the state, so the apply of the new runtime needs each in its execution
// graph, as it does for what a root output reads, even when it has no
// change: otherwise an assertion on a data source read during the plan
// cannot be decided during the apply.
func RecordCheckDependencies(ctx context.Context, deps ...addrs.AbsResourceInstance) {
	r := resultsOf(ctx)
	if r == nil || len(deps) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, dep := range deps {
		r.checkDeps.Add(dep)
	}
}

// CheckDependencies is what [RecordCheckDependencies] recorded under ctx,
// or nothing with the port off.
func CheckDependencies(ctx context.Context) addrs.Set[addrs.AbsResourceInstance] {
	ret := addrs.MakeSet[addrs.AbsResourceInstance]()
	r := resultsOf(ctx)
	if r == nil {
		return ret
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for dep := range r.checkDeps.All() {
		ret.Add(dep)
	}
	return ret
}
