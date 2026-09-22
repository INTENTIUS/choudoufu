// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// This file is GitHub issue #1258's instrument: how much PROVIDER WORK the
// two passes #1203's audit left unscoped do over blocks a -target run has
// removed from the plan graph.
//
// It counts, per resource type, the calls a fake provider receives from
//
//   - [projection.PlanInstances], reached through [statelessResolve]'s second
//     pass: one PlanResourceChange per plannable block; and
//   - [projection.ReadInstances] and [dataread.ReadProviderConfigs], reached
//     through [statelessProviderDataReads]: one ImportResourceState plus one
//     ReadResource per demanded managed instance, and one ReadDataSource per
//     provider-configuration data source.
//
// The two functions are called directly rather than through a whole
// live-plan, because a whole plan also makes PlanResourceChange and
// ReadResource calls of its own (the plan walk, the projection build) and a
// count taken there could not say which pass made which call.
//
// The scope is the plan graph's, computed by [statelessTargetScope] from a
// real [tofu.Context] over the same fake providers, and never hand-built
// except in the one test that says so. A hand-built scope can name a state
// targeting cannot produce - an in-scope block reading an out-of-scope one -
// and a count measured under it would be a count of nothing a user can run.

// targetWorkCloud is the two fake providers and every call they received.
type targetWorkCloud struct {
	mu sync.Mutex

	// plans, imports, reads and dataReads are keyed by resource type name.
	plans     map[string]int
	imports   map[string]int
	reads     map[string]int
	dataReads map[string]int

	aws, kubernetes *tofu.MockProvider
}

func targetWorkAttrs(names ...string) map[string]*configschema.Attribute {
	out := map[string]*configschema.Attribute{
		"id": {Type: cty.String, Computed: true},
	}
	for _, n := range names {
		out[n] = &configschema.Attribute{Type: cty.String, Optional: true}
	}
	return out
}

func newTargetWorkCloud() *targetWorkCloud {
	c := &targetWorkCloud{
		plans:     map[string]int{},
		imports:   map[string]int{},
		reads:     map[string]int{},
		dataReads: map[string]int{},
	}

	optionType := cty.Object(map[string]cty.Type{
		"domain_name":           cty.String,
		"resource_record_name":  cty.String,
		"resource_record_type":  cty.String,
		"resource_record_value": cty.String,
	})
	metadata := map[string]*configschema.NestedBlock{
		"metadata": {
			Nesting:  configschema.NestingList,
			MaxItems: 1,
			Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
				"name":      {Type: cty.String, Optional: true},
				"namespace": {Type: cty.String, Optional: true},
			}},
		},
	}

	awsTypes := map[string]providers.Schema{
		"aws_acm_certificate": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":                        {Type: cty.String, Computed: true},
			"domain_name":               {Type: cty.String, Optional: true},
			"validation_method":         {Type: cty.String, Optional: true},
			"arn":                       {Type: cty.String, Computed: true},
			"domain_validation_options": {Type: cty.Set(optionType), Computed: true},
		}}},
		"aws_route53_record": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":      {Type: cty.String, Computed: true},
			"zone_id": {Type: cty.String, Optional: true},
			"name":    {Type: cty.String, Optional: true},
			"type":    {Type: cty.String, Optional: true},
			"records": {Type: cty.List(cty.String), Optional: true},
			"ttl":     {Type: cty.Number, Optional: true},
		}}},
		"aws_eks_cluster": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":       {Type: cty.String, Computed: true},
			"name":     {Type: cty.String, Optional: true},
			"endpoint": {Type: cty.String, Computed: true},
		}}},
		"aws_s3_bucket":            {Block: &configschema.Block{Attributes: targetWorkAttrs("bucket")}},
		"aws_sqs_queue":            {Block: &configschema.Block{Attributes: targetWorkAttrs("name")}},
		"aws_sns_topic":            {Block: &configschema.Block{Attributes: targetWorkAttrs("name")}},
		"aws_cloudwatch_log_group": {Block: &configschema.Block{Attributes: targetWorkAttrs("name")}},
	}
	c.aws = &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{"region": {Type: cty.String, Optional: true}}}},
			ResourceTypes: statelessTestIdentitySchemasFrom(awsTypes),
			DataSources: map[string]providers.Schema{
				"aws_eks_cluster": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
					"id":       {Type: cty.String, Computed: true},
					"name":     {Type: cty.String, Required: true},
					"endpoint": {Type: cty.String, Computed: true},
				}}},
				// The fixture's second provider-configuration source, with
				// no argument at all: readable on the first pass, so the
				// only thing that can stop it being read is the scope.
				"aws_region": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
					"id":   {Type: cty.String, Computed: true},
					"name": {Type: cty.String, Computed: true},
				}}},
			},
		},
	}
	c.kubernetes = &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider: providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
				"host":  {Type: cty.String, Optional: true},
				"token": {Type: cty.String, Optional: true},
			}}},
			ResourceTypes: statelessTestIdentitySchemasFrom(map[string]providers.Schema{
				"kubernetes_namespace":  {Block: &configschema.Block{Attributes: targetWorkAttrs(), BlockTypes: metadata}},
				"kubernetes_config_map": {Block: &configschema.Block{Attributes: targetWorkAttrs(), BlockTypes: metadata}},
			}),
		},
	}

	for _, p := range []*tofu.MockProvider{c.aws, c.kubernetes} {
		// The seam below hands these back as already configured, and the
		// mock declines every call until it believes that.
		p.ConfigureProviderCalled = true
		p.PlanResourceChangeFn = func(req providers.PlanResourceChangeRequest) (resp providers.PlanResourceChangeResponse) {
			c.count(c.plans, req.TypeName)
			obj := req.ProposedNewState.AsValueMap()
			if req.TypeName == "aws_acm_certificate" {
				// The real provider's behaviour on the one attribute the
				// second pass turns on - see certPlanningProvider.
				obj["domain_validation_options"] = cty.SetVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
					"domain_name":           obj["domain_name"],
					"resource_record_name":  cty.UnknownVal(cty.String),
					"resource_record_type":  cty.UnknownVal(cty.String),
					"resource_record_value": cty.UnknownVal(cty.String),
				})})
			}
			resp.PlannedState = cty.ObjectVal(obj)
			return resp
		}
		p.ImportResourceStateFn = func(req providers.ImportResourceStateRequest) (resp providers.ImportResourceStateResponse) {
			c.count(c.imports, req.TypeName)
			id := req.Target.ID
			if req.Target.IsIdentityBased() {
				id = req.Target.Identity.GetAttr("id").AsString()
			}
			schema := p.GetProviderSchemaResponse.ResourceTypes[req.TypeName]
			resp.ImportedResources = []providers.ImportedResource{{
				TypeName: req.TypeName,
				State:    statelessTestObject(schema, map[string]string{"id": id}),
			}}
			return resp
		}
		p.ReadResourceFn = func(req providers.ReadResourceRequest) (resp providers.ReadResourceResponse) {
			c.count(c.reads, req.TypeName)
			schema := p.GetProviderSchemaResponse.ResourceTypes[req.TypeName]
			id := req.PriorState.GetAttr("id").AsString()
			resp.NewState = statelessTestObject(schema, map[string]string{
				"id": id, "name": id, "endpoint": "https://" + id + ".example",
			})
			return resp
		}
		p.ReadDataSourceFn = func(req providers.ReadDataSourceRequest) (resp providers.ReadDataSourceResponse) {
			c.count(c.dataReads, req.TypeName)
			if req.TypeName == "aws_region" {
				resp.State = cty.ObjectVal(map[string]cty.Value{
					"id":   cty.StringVal("us-east-1"),
					"name": cty.StringVal("us-east-1"),
				})
				return resp
			}
			name := req.Config.GetAttr("name")
			resp.State = cty.ObjectVal(map[string]cty.Value{
				"id":       name,
				"name":     name,
				"endpoint": cty.StringVal("https://" + name.AsString() + ".example"),
			})
			return resp
		}
	}
	return c
}

func (c *targetWorkCloud) count(m map[string]int, typeName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m[typeName]++
}

// ConfiguredProvider hands back the fake for whichever provider the block
// names. It evaluates no provider block: whether the kubernetes provider
// CAN be configured is [statelessProviders]' business, and what this
// instrument counts is the work done once something has been.
func (c *targetWorkCloud) ConfiguredProvider(_ context.Context, addr addrs.AbsProviderConfig) (providers.Interface, error) {
	switch addr.Provider.Type {
	case "aws":
		return c.aws, nil
	case "kubernetes":
		return c.kubernetes, nil
	}
	return nil, fmt.Errorf("no fake for %s", addr)
}

func (c *targetWorkCloud) managedTypesByProvider(context.Context) map[addrs.Provider]map[string]bool {
	out := map[addrs.Provider]map[string]bool{}
	for addr, p := range map[addrs.Provider]*tofu.MockProvider{
		addrs.NewDefaultProvider("aws"):        c.aws,
		addrs.NewDefaultProvider("kubernetes"): c.kubernetes,
	} {
		types := map[string]bool{}
		for name := range p.GetProviderSchemaResponse.ResourceTypes {
			types[name] = true
		}
		out[addr] = types
	}
	return out
}

// scopeFor is [statelessTargetScope] over a real plan graph built from the
// same fakes, so the scope is the one a run with these flags would compute.
func (c *targetWorkCloud) scopeFor(t *testing.T, cfg *configs.Config, targets ...string) identity.Scope {
	t.Helper()
	if len(targets) == 0 {
		return nil
	}
	tfCtx, ctxDiags := tofu.NewContext(&tofu.ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("aws"):        providers.FactoryFixed(c.aws),
			addrs.NewDefaultProvider("kubernetes"): providers.FactoryFixed(c.kubernetes),
		}, nil),
	})
	if ctxDiags.HasErrors() {
		t.Fatalf("tofu.NewContext: %s", ctxDiags.Err())
	}
	var parsed []addrs.Targetable
	for _, s := range targets {
		target, diags := addrs.ParseTargetStr(s)
		if diags.HasErrors() {
			t.Fatalf("parsing -target=%s: %s", s, diags.Err())
		}
		parsed = append(parsed, target.Subject)
	}
	scope, diags := statelessTargetScope(t.Context(), tfCtx, cfg, parsed, nil)
	if diags.HasErrors() {
		t.Fatalf("statelessTargetScope: %s", diags.Err())
	}
	if scope == nil {
		t.Fatal("statelessTargetScope returned no scope for a targeted run")
	}
	return scope
}

func renderCounts(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	total := 0
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
		total += m[k]
	}
	return fmt.Sprintf("%d [%s]", total, strings.Join(parts, " "))
}

func renderResolutions(result *identity.Result) []string {
	var out []string
	if result == nil {
		return out
	}
	for _, res := range result.All() {
		formula := ""
		if res.Formula != nil {
			formula = res.Formula.String()
		}
		out = append(out, fmt.Sprintf("%s %s %q formula=%q cause=%s", res.Addr, res.Class, res.ImportID, formula, res.Cause))
	}
	sort.Strings(out)
	return out
}

// targetWorkFixture is the one configuration every reading below is taken on.
const targetWorkFixture = "live-target-provider-work"

// plannableInScope is how many of the configuration's managed blocks
// [projection.PlanInstances] can plan at all (no for_each; the fixture has no
// count) AND the scope keeps. It is computed from the configuration and the
// graph's own scope, so "calls made for excluded blocks" below is a
// subtraction over two independent sources and never a number typed in to
// match the provider's count.
func plannableInScope(cfg *configs.Config, scope identity.Scope) int {
	n := 0
	for _, rc := range cfg.Module.ManagedResources {
		if rc.ForEach != nil {
			continue
		}
		if scope == nil || scope(addrs.ConfigResource{Module: addrs.RootModule, Resource: rc.Addr()}) {
			n++
		}
	}
	return n
}

// TestTargetWorkScopeIsThePlanGraphs pins the two scopes the readings below
// are taken under, because every one of them is only as good as the scope.
//
// The second case is the one that matters for the provider-configuration
// fixpoint: targeting a kubernetes block keeps aws_eks_cluster.this, which no
// argument of that block names. The edge runs through the PROVIDER - the
// namespace needs provider "kubernetes", whose host reads
// data.aws_eks_cluster.cluster, whose name reads the cluster - and the plan
// graph follows it. So a scope from [statelessTargetScope] already keeps
// whatever a still-needed provider's configuration reads, and drops it only
// when nothing in the run uses that provider.
func TestTargetWorkScopeIsThePlanGraphs(t *testing.T) {
	cfg := statelessTestLoadConfig(t, filepath.Join("testdata", targetWorkFixture))

	for _, tc := range []struct {
		target string
		want   string
	}{
		{"aws_route53_record.cert_validation", "aws_acm_certificate.cert aws_route53_record.cert_validation"},
		{"kubernetes_namespace.app", "aws_eks_cluster.this kubernetes_namespace.app"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			scope := newTargetWorkCloud().scopeFor(t, cfg, tc.target)
			var kept []string
			for _, rc := range cfg.Module.ManagedResources {
				if scope(addrs.ConfigResource{Module: addrs.RootModule, Resource: rc.Addr()}) {
					kept = append(kept, rc.Addr().String())
				}
			}
			sort.Strings(kept)
			if got := strings.Join(kept, " "); got != tc.want {
				t.Errorf("-target=%s keeps [%s], want [%s]", tc.target, got, tc.want)
			}
		})
	}
}

// TestProviderWorkOverTargetExcludedBlocks is the measurement GitHub issue
// #1258 asked for before anything is narrowed, asserted by value.
//
// First read at fa890883e9, on this fixture (ten managed blocks, nine of them
// plannable, two providers):
//
//	                                   PlanInstances' own    live managed reads       data reads
//	                                   PlanResourceChange    import+read+normalize
//	                                   total   excluded      total      excluded      total
//	untargeted                           9        0          1+1+1         0            1
//	-target=aws_route53_record.cert_..   9        8          1+1+1       1+1+1          1
//	-target=kubernetes_namespace.app     9        7          1+1+1         0            1
//
// Re-read after GitHub issue #1470 scoped [resolver.collectSignal]'s
// diagnostics and #1258's first leg narrowed [projection.PlanInstancesIn]
// to the target set:
//
//	                                   PlanInstances' own    live managed reads       data reads
//	                                   PlanResourceChange    import+read+normalize
//	                                   total   excluded      total      excluded      total
//	untargeted                           9        0          1+1+1         0            1
//	-target=aws_route53_record.cert_..   1        0          1+1+1       1+1+1          1
//	-target=kubernetes_namespace.app     0        0          1+1+1         0            1
//
// The kubernetes row is #1470's: that run's first resolution pass no longer
// carries the excluded record's for_each refusal, so it has no demand, and
// a first pass with no demand never runs PlanInstances at all (9 -> 0). The
// record row is the narrowing's: the record is IN scope there, its refusal
// stands, and the second pass now plans the one plannable block the scope
// keeps - the certificate it reads - rather than all nine (9 -> 1, excluded
// 8 -> 0). The untargeted row does not move. errorCount is 0 and
// downgradedToDiscovery is empty on every row, asserted below. The live
// reads and data reads are the fixpoint's and are unchanged; that leg
// stays where this function's last paragraph leaves it.
//
// "normalize" is the one PlanResourceChange [builder.normalizeIdentityAttrs]
// makes for each instance it reads (GitHub issue #281). It is a provider-
// process call like PlanInstances' own, and it is counted with the read
// because it happens only when the read does.
//
// So the issue's premise holds: a targeted run makes exactly the calls an
// untargeted one does. What it costs is bounded, and the bound is the
// reason this file lands a measurement and not a fix:
//
//   - PlanResourceChange here is a provider-process call with a null prior
//     state; it reaches no cloud. And it is made only when a first
//     resolution pass refuses with a managed demand (the ACM/Route53 shape).
//     A configuration without one pays nothing, targeted or not - see
//     TestStatelessResolveNeverConfiguresAProviderWithNothingToGain.
//   - The live reads are one import, one read and one normalizing plan per
//     managed instance a PROVIDER BLOCK's data source demands, which is one cluster here and in
//     corpus-eks-basic, the shape the fixpoint exists for.
//
// Narrowing either leg was tried against the first readings and both were
// found to turn a targeted run that works today into one that does not; see
// TestATargetedRunIsNotRefusedByAnExcludedForEach for the first, and this
// function's last paragraph for the second. #1470 removed the first
// obstacle and the first leg is narrowed; the second stands.
//
// These are today's numbers, pinned so that the change which moves them has
// to say so. The PlanInstances excluded column is at zero; the read column
// is not, and the untargeted row must not move with either.
//
// Re-read after GitHub issue #1514 scoped [statelessDiscover]'s
// needs-discovery set and #1258's second leg passed the run's scope into
// [statelessProviderDataReads]:
//
//	                                   PlanInstances' own    live managed reads       data reads
//	                                   PlanResourceChange    import+read+normalize
//	                                   total   excluded      total      excluded      total
//	untargeted                           9        0          1+1+1         0            3
//	-target=aws_route53_record.cert_..   1        0          0+0+0         0            0
//	-target=kubernetes_namespace.app     0        0          1+1+1         0            3
//
// The data-read column is against a fixture that gained a second provider-
// configuration source in the same commit, data.aws_region.current, and the
// two readings above it are not comparable to it for that column alone.
// The second source is what makes the record row's zeros mean anything.
// The cluster source is refused for a managed value it cannot have before
// the plan, so its own read stops the moment the demand underneath it does,
// for reasons that have nothing to do with a scope. data.aws_region.current
// has no managed demand at all: it is readable on the first pass, so the
// only thing that can stop it being read is [dataread.Options.Scope], and
// its zero on the record row is that option's and nothing else's. Take the
// option out and this test goes red on that one count. It is read twice on
// the rows that read it at all, once per analysis pass, which is what a
// second pass costs a source the first pass already answered.
//
// Only the record row moves, and it moves to zero. Targeting the record
// drops data.aws_eks_cluster.cluster from the plan graph, so
// [dataread.AnalyzeProviderConfigs] marks that source
// [dataread.SummaryOutOfScope] and never demands the cluster, which is the
// whole of that row's remaining provider work: the import, the read, the
// normalizing plan and the data read were all four for a block the run
// excludes. The kubernetes row does not move, and must not: targeting the
// namespace keeps provider "kubernetes", so the plan graph keeps the data
// source and the cluster with it (TestTargetWorkScopeIsThePlanGraphs). The
// untargeted row does not move because a nil scope admits every block.
//
// What the record row gives up is stated rather than hidden: provider
// "kubernetes" is now unconfigurable for the rest of that run, because the
// value its host argument needs was deliberately not read. Nothing in the
// plan graph wants it - targeting dropped every kubernetes block too - but
// [statelessManagedResourceProviders] is still read off the whole
// configuration, so the estate-wide sweep still tries a pass through it and
// [statelessDiscoverProviderUnavailable] downgrades that pass to the
// "Provider unavailable for the estate-wide sweep" warning GitHub issue
// #1514 built for exactly this. Fatal is reachable only when a
// needs-discovery instance THIS RUN ACTS ON uses that provider, and such an
// instance is in scope by construction, which is what makes the pass above
// safe to narrow. The end-to-end shape of that warning is pinned by
// TestLivePlan_targetIsNotRefusedByAnExcludedBlocksDiscoveryProvider; this
// file cannot reach it, because its harness registers no second provider
// process.
func TestProviderWorkOverTargetExcludedBlocks(t *testing.T) {
	cfg := statelessTestLoadConfig(t, filepath.Join("testdata", targetWorkFixture))

	const allNinePlans = "9 [aws_acm_certificate=1 aws_cloudwatch_log_group=1 aws_eks_cluster=1 aws_s3_bucket=2 aws_sns_topic=1 aws_sqs_queue=1 kubernetes_config_map=1 kubernetes_namespace=1]"
	const oneCluster = "1 [aws_eks_cluster=1]"
	// Both provider-configuration sources, and aws_region twice: the first
	// analysis pass reads it (nothing in its arguments waits on a managed
	// value) and the pass that follows the cluster read re-reads every
	// source it classifies, the cluster included. So the cluster's own read
	// is what a pass BUYS and aws_region's second is what a pass COSTS -
	// both are the fixpoint's, and this counts them rather than quietly
	// choosing one.
	const bothSources = "3 [aws_eks_cluster=1 aws_region=2]"

	for _, tc := range []struct {
		name   string
		target string

		plans, imports, reads, dataReads string

		// readPlans is the PlanResourceChange calls the READ makes, one
		// per instance [builder.normalizeIdentityAttrs] normalizes, and so
		// moves with reads rather than with plans.
		readPlans string

		// plansForExcluded is PlanResourceChange calls minus the plannable
		// blocks the scope keeps. readsForExcluded is the import/read pairs
		// made for a managed block the scope drops.
		plansForExcluded int
		readsForExcluded int

		// noSecondPass is a row whose first resolution pass is clean, so
		// statelessResolve returns before PlanInstances and the subtraction
		// above has nothing to measure: zero calls, in scope or out.
		noSecondPass bool

		// sourceOutOfScope is a row whose provider-configuration data
		// source the run's own -target drops, so the fixpoint reads
		// nothing and the control below inverts: the absence IS the
		// measurement, and what it costs is the paragraph above.
		sourceOutOfScope bool
	}{
		{
			name:  "untargeted",
			plans: allNinePlans, imports: oneCluster, reads: oneCluster, dataReads: bothSources, readPlans: oneCluster,
		},
		{
			// The certificate alone since #1258's first leg: the one
			// plannable block the scope keeps, and the one the record's
			// for_each reads. Nothing at all since its second: this run's
			// plan graph has no kubernetes block, so it has no
			// data.aws_eks_cluster.cluster and no cluster read either.
			name: "targeting the record", target: "aws_route53_record.cert_validation",
			plans: "1 [aws_acm_certificate=1]", imports: "0 []", reads: "0 []", dataReads: "0 []", readPlans: "0 []",
			plansForExcluded: 0, readsForExcluded: 0, sourceOutOfScope: true,
		},
		{
			// No plan call at all since #1470: with the excluded record's
			// refusal rolled back, this run's first pass is clean and
			// statelessResolve returns before PlanInstances. The reads
			// stand: targeting the namespace keeps its provider, and the
			// plan graph keeps that provider's data source and the cluster
			// the data source names.
			name: "targeting a kubernetes block", target: "kubernetes_namespace.app",
			plans: "0 []", imports: oneCluster, reads: oneCluster, dataReads: bothSources, readPlans: oneCluster,
			noSecondPass: true, readsForExcluded: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var targets []string
			if tc.target != "" {
				targets = []string{tc.target}
			}

			// Leg one: statelessResolve's second pass.
			resolveCloud := newTargetWorkCloud()
			scope := resolveCloud.scopeFor(t, cfg, targets...)
			resolutions, resolveDiags := statelessResolve(t.Context(), cfg, resolveCloud, nil, nil, scope)
			if got := renderCounts(resolveCloud.plans); got != tc.plans {
				t.Errorf("PlanResourceChange calls:\n got %s\nwant %s", got, tc.plans)
			}
			// The two ratchets statelessResolve carries, read per row: a
			// narrowing that traded the excluded calls for a refusal, or for
			// a demotion to discovery, would show here and nowhere else.
			if n := errorCount(resolveDiags); n != 0 {
				t.Errorf("statelessResolve refused with %d error(s): %v", n, renderDiags(resolveDiags))
			}
			first, _ := identity.ResolveWith(t.Context(), cfg, identity.Context{Scope: scope})
			if downgraded := downgradedToDiscovery(first, resolutions); downgraded != "" {
				t.Errorf("the pass statelessResolve kept demoted %s to needs-discovery against the first pass", downgraded)
			}
			total := 0
			for _, n := range resolveCloud.plans {
				total += n
			}
			if tc.noSecondPass {
				if total != 0 {
					t.Errorf("PlanInstances made %d PlanResourceChange call(s) on a run whose first pass has been clean since #1470", total)
				}
			} else if got := total - plannableInScope(cfg, scope); got != tc.plansForExcluded {
				t.Errorf("%d PlanResourceChange call(s) were made for blocks the scope excludes, want %d", got, tc.plansForExcluded)
			}
			if n := len(resolveCloud.imports) + len(resolveCloud.reads) + len(resolveCloud.dataReads); n != 0 {
				t.Errorf("statelessResolve reached the cloud: imports %s, reads %s, data reads %s",
					renderCounts(resolveCloud.imports), renderCounts(resolveCloud.reads), renderCounts(resolveCloud.dataReads))
			}

			// Leg two: the provider-configuration fixpoint, on a fresh cloud
			// so the two legs' calls cannot be confused.
			readCloud := newTargetWorkCloud()
			results := statelessProviderDataReads(t.Context(), cfg, readCloud, nil, resolutions, nil, 1, scope)
			if got := renderCounts(readCloud.imports); got != tc.imports {
				t.Errorf("ImportResourceState calls: got %s, want %s", got, tc.imports)
			}
			if got := renderCounts(readCloud.reads); got != tc.reads {
				t.Errorf("ReadResource calls: got %s, want %s", got, tc.reads)
			}
			if got := renderCounts(readCloud.dataReads); got != tc.dataReads {
				t.Errorf("ReadDataSource calls: got %s, want %s", got, tc.dataReads)
			}
			clusterInScope := scope == nil || scope(addrs.ConfigResource{
				Module:   addrs.RootModule,
				Resource: addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_eks_cluster", Name: "this"},
			})
			forExcluded := 0
			if !clusterInScope {
				forExcluded = readCloud.reads["aws_eks_cluster"]
			}
			if forExcluded != tc.readsForExcluded {
				t.Errorf("%d live read(s) were made for a managed block the scope excludes, want %d", forExcluded, tc.readsForExcluded)
			}
			// Not PlanInstances: this is the one PlanResourceChange
			// [builder.normalizeIdentityAttrs] makes per instance it reads
			// (GitHub issue #281), so it moves with the read and is part of
			// what a read of an excluded block costs.
			if got := renderCounts(readCloud.plans); got != tc.readPlans {
				t.Errorf("PlanResourceChange calls made by the read itself: got %s, want %s", got, tc.readPlans)
			}

			// The control: without it every count above could be zero
			// because the fixpoint never closed, not because it was narrow.
			got, ok := results["data.aws_eks_cluster.cluster"]
			if tc.sourceOutOfScope {
				// The inverse control. This row's zeros are only worth
				// something if the source is absent for the stated reason -
				// the scope dropped it - rather than because the whole
				// fixpoint stopped working, which the two rows either side
				// of it would catch.
				if ok {
					t.Errorf("the fixpoint read data.aws_eks_cluster.cluster on a run whose -target drops it: %#v", got)
				}
				if len(results) != 0 {
					t.Errorf("the fixpoint returned %d source(s) on a run whose only provider-config source is out of scope", len(results))
				}
				return
			}
			if !ok {
				t.Fatalf("the fixpoint did not read data.aws_eks_cluster.cluster; results: %d", len(results))
			}
			if want := cty.StringVal("https://demo.example"); !got.GetAttr("endpoint").RawEquals(want) {
				t.Errorf("endpoint is %#v, want %#v - the cluster's live id carried into the data source", got.GetAttr("endpoint"), want)
			}
		})
	}
}

// TestATargetedRunIsNotRefusedByAnExcludedForEach was, until GitHub issue
// #1470, why [projection.PlanInstances] could not be narrowed to the target
// set; it now guards the fix that let it be.
//
// -target=kubernetes_namespace.app drops the certificate and the record from
// the plan graph. [resolver.walkOutOfScope] promises such a block "cannot
// refuse the run" and rolls back whatever its own attempt raised. Before
// #1470 it did not get the chance: [resolver.collectSignal] runs before the
// walk and called expansionFor on EVERY block with no scope at all, so the
// record's "Non-static for_each expression" was raised there and memoized,
// ahead of any mark walkOutOfScope could roll back to. The first
// resolution pass of this targeted run carried one error for a block the
// run excluded (measured at fa890883e9: errorCount 1), and what cleared it
// was the second pass - only because PlanInstances, being unscoped, planned
// the excluded certificate and handed the record its key set. Measured
// then, with both legs experimentally narrowed:
//
//	                                   errorCount            downgradedToDiscovery
//	                                   first  final  final
//	                                          today  narrowed
//	untargeted                           1      0      0        "" both
//	-target=aws_route53_record...        1      0      0        "" both
//	-target=kubernetes_namespace.app     1      0      1        "" both
//
// Since #1470 the collection routes an out-of-scope block through
// [resolver.expansionOutOfScope], the first pass of this run is clean, and
// no second pass runs at all (TestProviderWorkOverTargetExcludedBlocks'
// kubernetes row: 0 PlanResourceChange calls). With that in place #1258's
// first leg narrowed [projection.PlanInstancesIn] to the target set, which
// is what this test was holding back. It stays as the guard on the
// collection: a regression there would present exactly as it did before,
// now with nothing left to mask it.
func TestATargetedRunIsNotRefusedByAnExcludedForEach(t *testing.T) {
	cfg := statelessTestLoadConfig(t, filepath.Join("testdata", targetWorkFixture))
	cloud := newTargetWorkCloud()
	scope := cloud.scopeFor(t, cfg, "kubernetes_namespace.app")

	_, diags := statelessResolve(t.Context(), cfg, cloud, nil, nil, scope)
	if n := errorCount(diags); n != 0 {
		t.Errorf("-target=kubernetes_namespace.app refused with %d error(s), for blocks the run excludes: %v\n"+
			"PlanResourceChange calls were %s. The excluded record's for_each refusal reached the caller; "+
			"see resolver.expansionOutOfScope and this test's doc comment.",
			n, renderDiags(diags), renderCounts(cloud.plans))
	}
}
