// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/backend"
	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/lint"
	"github.com/intentius/choudoufu/internal/live/policy"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/registry"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
	tfversion "github.com/intentius/choudoufu/version"
)

// live-plan -resource=ADDR (GitHub issue #2245, the engine tier's H2):
// plan one managed resource and its upstream cone - what it reads, its data
// sources, its identity - and report the cone's drift as chant's drift
// record v1, one record per resource instance, as NDJSON. See
// live/PLAN-RESOURCE.md for the contract.
//
// It is livePlan's pipeline cut down to the cone, and three things are
// different from a -target plan, which is the reason it is its own path:
//
//   - The record store is opened keyed ([projection.NewKeyedRecordStore]):
//     no handshake List, no bulk load, and the projection never lists the
//     namespace ([projection.Options.KeyedRecordsOnly]). Every read is a Get
//     of a key the cone names.
//   - Marker discovery runs for the cone's needs-discovery instances only,
//     with no sweep leg, no estate-wide tag-index call and no unclaimed
//     inventory ([liveResourceDiscover]). A -target plan keeps the sweep
//     estate-wide (live/PLAN-CONE.md).
//   - Nothing estate-wide that only a whole plan uses runs: the policy's
//     account reconciliation, the foreign classification, the guided-sweep
//     hint, root outputs, the lookalike guard and the Kubernetes dry run.
//
// The run never writes. It refuses -out, so nothing it plans can be
// applied, which is what makes leaving the removal legs out safe: a
// removal outside the cone is not this run's question.

// liveResourceRecordStoreOpener opens the record store for this path. A
// package variable so a test can hand the path a fake store and see what it
// reads; see [TestLivePlanResource_readsNoListAndSweepsNothing].
var liveResourceRecordStoreOpener recordStoreOpener = projection.NewKeyedRecordStore

// errLiveResourceList is what [liveResourceStore] answers a List with.
var errLiveResourceList = errors.New("live-plan -resource reads the record store by key only, and something on its path asked to list it; this is a defect, report it (GitHub issue #2245)")

// liveResourceStore is the record store as this path sees it: every keyed
// read counted, every List counted and refused, and every write refused,
// since a drift read writes nothing. The counts are the run's own read
// account in the drift-settle line.
type liveResourceStore struct {
	inner staterecord.Store

	mu    sync.Mutex
	gets  int
	keys  map[string]bool
	lists int
}

func newLiveResourceStore(inner staterecord.Store) *liveResourceStore {
	if inner == nil {
		return nil
	}
	return &liveResourceStore{inner: inner, keys: map[string]bool{}}
}

func (s *liveResourceStore) Get(ctx context.Context, key string) ([]byte, string, bool, error) {
	s.mu.Lock()
	s.gets++
	s.keys[key] = true
	s.mu.Unlock()
	return s.inner.Get(ctx, key)
}

func (s *liveResourceStore) PutIfVersion(context.Context, string, []byte, string) (string, error) {
	return "", errLiveResourceWrite
}

func (s *liveResourceStore) PutIfAbsent(context.Context, string, []byte) (string, error) {
	return "", errLiveResourceWrite
}

func (s *liveResourceStore) Delete(context.Context, string, string) error {
	return errLiveResourceWrite
}

func (s *liveResourceStore) List(context.Context, string) ([]string, error) {
	s.mu.Lock()
	s.lists++
	s.mu.Unlock()
	return nil, errLiveResourceList
}

var errLiveResourceWrite = errors.New("live-plan -resource writes nothing to the record store")

// counts reads the store's account. A nil store (no record_store, or one
// that would not open) read nothing.
func (s *liveResourceStore) counts() (gets, keys, lists int) {
	if s == nil {
		return 0, 0, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gets, len(s.keys), s.lists
}

// asStore keeps a nil *liveResourceStore a nil interface, which is what
// every consumer downstream tests for.
func (s *liveResourceStore) asStore() staterecord.Store {
	if s == nil {
		return nil
	}
	return s
}

// liveResourceScan counts what the cone's discovery listed, from
// discovery's own progress events: one event per type a pass listed, with
// Sweep set when the estate-wide sweep listed it.
type liveResourceScan struct {
	mu     sync.Mutex
	sweeps int
	scans  int
	types  map[string]bool
}

func (s *liveResourceScan) progress(ev discovery.ProgressEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ev.Sweep {
		s.sweeps++
		return
	}
	s.scans++
	if s.types == nil {
		s.types = map[string]bool{}
	}
	s.types[ev.TypeName] = true
}

func (s *liveResourceScan) typeNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.types))
	for t := range s.types {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// liveResourceRejections refuses every option -resource cannot honour,
// rather than ignoring it.
func liveResourceRejections(args *arguments.LivePlan) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	reject := func(flag, detail string) {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, fmt.Sprintf("-resource cannot be combined with %s", flag), detail))
	}
	op := args.Operation
	if op != nil {
		if len(op.Targets) > 0 || len(op.Excludes) > 0 {
			reject("-target or -exclude", "-resource is the target: the run plans that resource and what it reads. Name one resource with -resource, or plan a set with -target and no -resource.")
		}
		if op.PlanMode == plans.DestroyMode {
			reject("-destroy", "A drift read compares the live resource with its configuration; a destroy plan has nothing to compare.")
		}
		if op.PlanMode == plans.RefreshOnlyMode {
			reject("-refresh-only", "Live mode plans normal plans only.")
		}
		if len(op.ForceReplace) > 0 {
			reject("-replace", "A forced replacement would read as drift that is not there.")
		}
	}
	if args.OutPath != "" {
		reject("-out", "A drift read is a report, and its plan is never saved or applied: it reads the cone alone and leaves the estate's removals out.")
	}
	if args.Cone != "" {
		reject("-cone", "-cone plans the cone of a change; -resource plans the cone of one resource.")
	}
	if args.AdoptionOnly {
		reject("-adoption-only", "The adoption ledger reads the estate-wide sweep, which -resource does not run.")
	}
	if args.Filter.Active() {
		reject("-filter", "-filter narrows the live-plan report; -resource prints drift records instead.")
	}
	return diags
}

// executeResource is Execute's -resource branch, taken once the estate
// name is settled. It prints the drift stream on stdout and every
// diagnostic on stderr.
func (c *LivePlanCommand) executeResource(ctx context.Context, args *arguments.LivePlan, estateFlag string, renderView *views.View, preDiags tfdiags.Diagnostics) int {
	diags := preDiags
	if moreDiags := liveResourceRejections(args); moreDiags.HasErrors() {
		renderView.Diagnostics(diags.Append(moreDiags))
		return 1
	}
	if moreDiags := liveRejections(surfaceEstateFlag, args.Operation, args.State, args.View, args.OutPath, args.GenerateConfigPath, ""); moreDiags.HasErrors() {
		renderView.Diagnostics(diags.Append(moreDiags))
		return 1
	}
	// Under -resource every word but the stream belongs on stderr, so the
	// plan view's hooks are built over renderView, whose stdout is stderr.
	renderOpts := *args.View
	renderOpts.ViewType = arguments.ViewHuman
	view := views.NewPlan(&renderOpts, renderView)
	drift := views.NewLiveDrift(c.View)

	code, moreDiags := c.livePlanResource(ctx, args, estateFlag, view, drift)
	diags = diags.Append(moreDiags)
	renderView.Diagnostics(diags)
	if diags.HasErrors() {
		return 1
	}
	return code
}

// livePlanResource runs the cone pipeline and prints the drift stream. The
// exit code is 0 for a stream with no drifted record, and 2 under
// -detailed-exitcode when one is drifted, pending or missing.
func (c *LivePlanCommand) livePlanResource(ctx context.Context, args *arguments.LivePlan, estateFlag string, view views.Plan, drift *views.LiveDrift) (int, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	readPar, readParDiags := readParallelismSetting()
	diags = diags.Append(readParDiags)
	if readParDiags.HasErrors() {
		return 1, diags
	}

	config, cfgDiags := c.loadConfig(ctx, ".")
	diags = diags.Append(cfgDiags)
	if cfgDiags.HasErrors() {
		return 1, diags
	}

	enc, encDiags := c.Encryption(ctx)
	diags = diags.Append(encDiags)
	if encDiags.HasErrors() {
		return 1, diags
	}
	coreOpts, err := c.contextOpts(ctx)
	if err != nil {
		return 1, diags.Append(err)
	}
	coreOpts.Hooks = view.Hooks()
	coreOpts.Encryption = enc

	// The node seam, wired as livePlan wires it: the resolver is also the
	// marker writer, and without it every marker the configuration does not
	// spell out would read as a tag the plan removes - drift that is not
	// there.
	resolver := &projection.NodeResolver{}
	coreOpts.ConfigValueAdjuster = resolver
	if nodeResolveEnabled() {
		coreOpts.ResourceIdentityResolver = resolver
	}

	tfCtx, ctxDiags := tofu.NewContext(coreOpts)
	diags = diags.Append(ctxDiags)
	if ctxDiags.HasErrors() {
		return 1, diags
	}
	defer func() {
		if err := tfCtx.Close(context.WithoutCancel(ctx)); err != nil {
			log.Printf("[WARN] live-plan -resource: closing the provider plugins: %s", err)
		}
	}()

	// The cone: the plan graph's own answer to -target=ADDR, which keeps
	// the resource and everything it reads.
	targets := []addrs.Targetable{args.Resource}
	scope, scopeDiags := liveTargetScope(ctx, tfCtx, config, targets, nil)
	diags = diags.Append(scopeDiags)
	if scopeDiags.HasErrors() {
		return 1, diags
	}
	if scope == nil || !scope(liveResourceConfigAddr(args.Resource)) {
		return 1, diags.Append(tfdiags.Sourceless(tfdiags.Error, "Resource not declared",
			fmt.Sprintf("-resource=%s names no managed resource this configuration declares, so there is nothing to plan.", args.ResourceRaw)))
	}

	provs := newProjectionProviders(config, coreOpts.Plugins)
	resourceSchemas := provs.resourceSchemas(ctx)

	lctx := lint.Context{Schemas: resourceSchemas, Scope: scope}
	if issues := lint.CheckWith(ctx, config, lctx); len(issues) > 0 {
		diags = diags.Append(lint.Diagnostics(issues))
		if lint.HasErrors(issues) {
			return 1, diags.Append(provs.close(ctx))
		}
	}
	diags = diags.Append(lint.CheckResidueAttributes(config, lctx))

	estate, _, estateDiags := liveEstateFor(ctx, estateFlag, config)
	diags = diags.Append(estateDiags)
	if estate == "" {
		return 1, diags.Append(provs.close(ctx), tfdiags.Sourceless(tfdiags.Error, "No estate name",
			"-resource reads one resource's ownership markers, which name an estate. Name it with -estate, or in the configuration's live block."))
	}

	// The record store, keyed. A store that could not be reached is a
	// warning and the run goes on recordless, as live-plan does; a refusal
	// stops it.
	var store *liveResourceStore
	if config.Module != nil && config.Module.Live != nil {
		opened, storeDiags := openRecordStoreAsOneMoreSource(ctx, liveResourceRecordStoreOpener, config.Module.Live.RecordStore, config.Module.Live.Retry, estate, "live-plan -resource")
		diags = diags.Append(storeDiags)
		if storeDiags.HasErrors() {
			return 1, diags.Append(provs.close(ctx))
		}
		store = newLiveResourceStore(opened)
		unavailable := ""
		if opened == nil {
			unavailable = "this live-plan could not open the record store (see the warning about it)"
		}
		c.liveEstateOutputs().open(store.asStore(), config.Module.Live.RecordStore, estate, unavailable)
	}
	hintStore := store.asStore()

	dataResults, drDiags := liveDataReads(ctx, config, provs, resourceSchemas, scope)
	diags = diags.Append(drDiags)
	if drDiags.HasErrors() {
		return 1, diags.Append(provs.close(ctx))
	}

	resolutions, idDiags := liveResolve(ctx, config, provs, resourceSchemas, dataResults, scope)
	if nodeResolveEnabled() {
		resolver.StaticRefusals = identity.InstanceRefusals(idDiags)
		idDiags = identity.DowngradeForNodeResolution(idDiags)
	}
	diags = diags.Append(idDiags)
	if idDiags.HasErrors() {
		return 1, diags.Append(provs.close(ctx))
	}
	nodeRefused := nodeRefusedAddrs(resolver.StaticRefusals)

	expected := liveResourceExpected(args.Resource, scope, resolutions.All(), nodeRefused)
	member := estate
	observedFor := "live-plan -resource=" + args.ResourceRaw
	drift.Start(views.LiveDriftStart{
		Drift:    "v1",
		Member:   member,
		Engine:   "choudoufu",
		Read:     "plan",
		Resource: args.ResourceRaw,
		Expected: liveAddrStrings(expected),
	})

	recordStoreForReads := projection.NewRecordEnvelopeStore(hintStore, recordKeyPrefixFor(config, estate))

	var pdDiags tfdiags.Diagnostics
	provs.providerDataResults, provs.providerManagedResults, pdDiags = liveProviderDataReads(ctx, config, provs, resourceSchemas, resolutions, recordStoreForReads, readPar, scope, nil)
	diags = diags.Append(pdDiags)
	if pdDiags.HasErrors() {
		return 1, diags.Append(provs.close(ctx))
	}

	var pol *policy.Policy
	if config.Module != nil {
		pol = livePolicy(config.Module.Live, estate)
	}

	var recordShrinkStore *projection.RecordStore
	if nodeResolveEnabled() {
		recordShrinkStore = recordStoreForReads
	}

	inScope := liveInScopeResolutions(resolutions.All(), scope)
	deposedRecords := collectDeposedRecords(ctx, recordStoreForReads, config, inScope, nodeRefused)

	scan := &liveResourceScan{}
	merged := resolutions.All()
	disco, discoProvider, undeclaredProviders, discoDiags := liveResourceDiscover(ctx, config, resolutions, nodeRefused, estate, provs, pol, hintStore, recordShrinkStore, deposedRecords, scope, scan.progress)
	diags = diags.Append(discoDiags)
	if discoDiags.HasErrors() {
		return 1, diags.Append(provs.close(ctx))
	}
	if disco != nil {
		merged = disco.Resolutions
	}

	resolver.RecordStore = recordShrinkStore
	resolver.MarkerIndex = projection.NewMarkerIndex(merged)
	resolver.UnaddressedObjects = disco.UnaddressedAccount()
	resolver.NoSourceCreate = strict.CreatesFromNoSource(identity.NoSourceCreateFor(config))
	resolver.Estate = estate
	resolver.Selection = identity.SelectionFor(config)
	resolver.Config = config
	resolver.Slots = disco.SlotTable()
	resolver.Facts = markerFacts()
	resolver.MarkerWriter = provs.markerTagger

	projResult, projDiags := projection.BuildWith(ctx, config, merged, provs, projection.Options{
		UndeclaredProvider:       discoProvider,
		UndeclaredProviders:      undeclaredProviders,
		StrandedByProviderChange: disco.StrandedByProviderChange(),
		Ownership:                liveOwnershipWith(estate, disco, pol, nil),
		Scope:                    scope,
		RecordStore:              recordStoreForReads,
		KeyedRecordsOnly:         true,
		DataResults:              dataResults,
		DeposedBindings:          disco.DeposedBindingsList(),
		ReadParallelism:          readPar,
		ManifestOwnedKeys:        liveManifestOwnedKeys(config, provs),
	})
	diags = diags.Append(provs.close(ctx))
	diags = diags.Append(projDiags)
	if projDiags.HasErrors() {
		return 1, diags
	}
	resolver.Unowned = nodeResolverUnownedSet(projResult.Unowned)
	resolver.PolicyUntag = nodeResolverUntagMap(projResult.Policy, livePolicyTagKey(pol))

	rawVariables, varDiags := c.collectVariableValues()
	diags = diags.Append(varDiags)
	variables, parseDiags := backend.ParseVariableValues(rawVariables, config.Module.Variables)
	diags = diags.Append(parseDiags)
	if diags.HasErrors() {
		return 1, diags
	}

	schemas, schemaDiags := tfCtx.Schemas(ctx, config, projResult.State)
	diags = diags.Append(schemaDiags)
	if schemaDiags.HasErrors() {
		return 1, diags
	}

	diags = diags.Append(liveMarkerEstate(ctx, config, estate))
	if diags.HasErrors() {
		return 1, diags
	}
	gapDiags, _ := liveUnmarkedApplyGaps(ctx, config, resolutions, resourceSchemas, recordStoreForReads, estate, scope)
	diags = diags.Append(gapDiags)
	if diags.HasErrors() {
		return 1, diags
	}

	plan, planDiags := tfCtx.Plan(ctx, config, projResult.State, &tofu.PlanOpts{
		Mode:         plans.NormalMode,
		SkipRefresh:  true,
		SetVariables: variables,
		Targets:      targets,
	})
	diags = diags.Append(planDiags)
	if plan == nil {
		return 1, diags
	}

	bound := liveBoundReport(projResult, merged, liveNeedsDiscoverySet(resolutions))
	records := liveDriftRecords(liveDriftInput{
		Member:     member,
		Command:    observedFor,
		ObservedAt: time.Now().UTC(),
		Expected:   expected,
		Projection: projResult,
		Bound:      bound,
		Plan:       plan,
		Schemas:    schemas,
	})
	counts := map[string]int{}
	changed := false
	for _, r := range records {
		drift.Record(r)
		counts[r.Status]++
		switch r.Status {
		case "drifted", "pending", "missing":
			changed = true
		}
	}

	gets, keys, lists := store.counts()
	drift.Settle(views.LiveDriftSettle{
		Drift:  "v1",
		Member: member,
		Engine: "choudoufu",
		Read:   "plan",
		Counts: counts,
		Reads: views.LiveDriftReads{
			RecordsRead:     gets,
			RecordKeys:      keys,
			RecordListCalls: lists,
			SweepCalls:      scan.sweeps,
			ScanTypes:       scan.typeNames(),
			ScanListCalls:   scan.scans,
		},
	})
	if lists > 0 || scan.sweeps > 0 {
		// The settle line already says so; this makes the run fail too, so
		// a regression that lists or sweeps cannot pass as a clean read.
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "live-plan -resource read estate-wide",
			fmt.Sprintf("This run listed the record store %d time(s) and swept %d type(s), where it reads by key and sweeps nothing. The drift records above are still the plan's answer for the cone, but the read was not the one this path promises. Report it (GitHub issue #2245).", lists, scan.sweeps)))
	}
	if diags.HasErrors() {
		return 1, diags
	}
	if changed && args.DetailedExitCode {
		return 2, diags
	}
	return 0, diags
}

// liveResourceConfigAddr is -resource's block, for the scope test.
func liveResourceConfigAddr(t addrs.Targetable) addrs.ConfigResource {
	switch a := t.(type) {
	case addrs.AbsResourceInstance:
		return a.ContainingResource().Config()
	case addrs.AbsResource:
		return a.Config()
	}
	return addrs.ConfigResource{}
}

// liveResourceExpected is the cone's managed instances, in address order:
// every declared instance whose block the plan graph keeps, the node-refused
// ones included (they are declared and absent from resolutions). When
// -resource names one instance, that block's other instances are left out,
// since the plan does not reach them.
func liveResourceExpected(target addrs.Targetable, scope identity.Scope, all []identity.Resolution, nodeRefused map[string]bool) []addrs.AbsResourceInstance {
	seen := map[string]addrs.AbsResourceInstance{}
	keep := func(addr addrs.AbsResourceInstance) {
		if addr.Resource.Resource.Mode != addrs.ManagedResourceMode || !scope(addr.ConfigResource()) {
			return
		}
		if inst, ok := target.(addrs.AbsResourceInstance); ok {
			if addr.ContainingResource().Equal(inst.ContainingResource()) && !addr.Equal(inst) {
				return
			}
		}
		seen[addr.String()] = addr
	}
	for _, r := range all {
		if r.Undeclared {
			continue
		}
		keep(r.Addr)
	}
	for key := range nodeRefused {
		if addr, diags := addrs.ParseAbsResourceInstanceStr(key); !diags.HasErrors() {
			keep(addr)
		}
	}
	out := make([]addrs.AbsResourceInstance, 0, len(seen))
	for _, a := range seen {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Less(out[j]) })
	return out
}

func liveAddrStrings(in []addrs.AbsResourceInstance) []string {
	out := make([]string, len(in))
	for i, a := range in {
		out[i] = a.String()
	}
	return out
}

// liveResourceDiscover is marker discovery for the cone: the binding demand
// of the cone's needs-discovery instances, and nothing else. Against
// [liveDiscover] it drops
//
//   - the sweep (Request.Sweep off, an empty Sweepers list, no record
//     removal leg), so no type the cone does not wait on is listed;
//   - the tag-index client (Request.Tagging), whose one GetResources call is
//     filtered by estate and so reads the whole estate;
//   - the unclaimed inventory (CollectUnclaimed off), so each list stays
//     filtered to this estate's marker where the provider can filter;
//   - the guided-sweep hint, which only narrows a sweep.
//
// The Cloud Control fallback and the per-service tag reader stay: they
// answer for one type the cone waits on, not for the estate.
//
// A cone with nothing waiting on discovery runs no pass at all and lists
// nothing. Every pass is scoped to the run's [identity.Scope], which is
// what keeps the demand to the cone while the declared set stays whole, so
// a listed object another block declares is never read as an orphan.
func liveResourceDiscover(ctx context.Context, config *configs.Config, resolutions *identity.Result, nodeRefused map[string]bool, estate string, provs *projectionProviders, pol *policy.Policy, hintStore staterecord.Store, recordShrinkStore *projection.RecordStore, deposedRecords map[string]map[string]projection.DeposedRecord, scope identity.Scope, progress discovery.ProgressFunc) (*discovery.Result, addrs.AbsProviderConfig, map[string]addrs.AbsProviderConfig, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	var noProvider addrs.AbsProviderConfig

	needs := liveInScopeResolutions(resolutions.NeedsDiscovery(), scope)
	if len(needs) == 0 {
		return nil, noProvider, nil, diags
	}
	recordBacked, recordDiags := liveRecordBackedNeedsDiscoveryAddrs(ctx, recordShrinkStore, needs)
	diags = diags.Append(recordDiags)
	if recordDiags.HasErrors() {
		return nil, noProvider, nil, diags
	}
	passProviders, needsDiags := liveNeedsDiscoveryProviders(config, needs)
	diags = diags.Append(needsDiags)
	if needsDiags.HasErrors() || len(passProviders) == 0 {
		return nil, noProvider, nil, diags
	}

	one := func(providerAddr, scopeProvider addrs.AbsProviderConfig) (*discovery.Result, tfdiags.Diagnostics) {
		var diags tfdiags.Diagnostics
		provider, err := provs.ConfiguredProvider(ctx, providerAddr)
		if err != nil {
			return nil, diags.Append(tfdiags.Sourceless(tfdiags.Error, "Provider unavailable for marker discovery",
				fmt.Sprintf("Finding this resource's live object needs provider %s, which could not be used: %s.", providerAddr, err)))
		}
		req := discovery.Request{
			Estate:                  estate,
			Config:                  config,
			RecordBackedAddrs:       recordBacked,
			DeposedRecords:          deposedRecords,
			Resolutions:             resolutions.All(),
			NodeRefused:             nodeRefused,
			Scope:                   scope,
			Provider:                provider,
			Region:                  provs.region(providerAddr),
			VouchProvider:           providerAddr,
			DeferDeniedSweepWarning: false,
			Sweep:                   false,
			Sweepers:                []discovery.Sweeper{},
			Policy:                  pol,
			ScopeProvider:           scopeProvider,
			Progress:                progress,
			HintStore:               hintStore,
			KeyPrefix:               recordKeyPrefixFor(config, estate),
		}
		sub, known := substrate.ForProvider(providerAddr.Provider.Type)
		if ep, on := cloudControlTarget(); on && known && sub.Sweep() == substrate.SweepTaggingIndex {
			if roster, err := registry.Embedded(); err == nil {
				req.Roster = roster
				creds, explicit := provs.credentials(providerAddr, ep)
				cfg := cloudcontrol.Config{
					Endpoint:             ep,
					Region:               provs.region(providerAddr),
					Credentials:          creds,
					SignEndpointOverride: explicit,
				}
				req.CloudControl = cloudcontrol.New(cfg)
				svc := newServiceTagsReader(cfg.Region, ep, creds)
				req.ServiceTags = svc
				req.ServiceList = svc
			}
		}
		res, discoDiags := discovery.Discover(ctx, req)
		return res, diags.Append(discoDiags)
	}

	if len(passProviders) == 1 {
		res, discoDiags := one(passProviders[0], addrs.AbsProviderConfig{})
		diags = diags.Append(discoDiags)
		if discoDiags.HasErrors() {
			return nil, noProvider, nil, diags
		}
		res.AttributeOrphans(passProviders[0])
		return res, passProviders[0], nil, diags
	}
	passes := make([]discovery.Pass, 0, len(passProviders))
	for _, providerAddr := range passProviders {
		res, discoDiags := one(providerAddr, providerAddr)
		diags = diags.Append(discoDiags)
		if discoDiags.HasErrors() {
			return nil, noProvider, nil, diags
		}
		passes = append(passes, discovery.Pass{Provider: providerAddr, Region: provs.region(providerAddr), Result: res})
	}
	merged, providerOf, mergeDiags := discovery.Merge(estate, passes, strict.RecreatesOnProviderChange(identity.ProviderChangeFor(config)))
	diags = diags.Append(mergeDiags)
	return merged, passProviders[0], providerOf, diags
}

// liveDriftInput is what [liveDriftRecords] reads.
type liveDriftInput struct {
	Member     string
	Command    string
	ObservedAt time.Time
	Expected   []addrs.AbsResourceInstance
	Projection *projection.Result
	Bound      []views.LivePlanBound
	Plan       *plans.Plan
	Schemas    *tofu.Schemas
}

// liveDriftRecords maps one cone plan onto drift records, one per expected
// instance, following chant's table (INTENTIUS/chant#4450) for choudoufu
// live+plan, with the ownership facet from live-plan's own sections:
//
//   - an UNOWNED omission is unclaimed, read "ownership": the object at the
//     declared identity carries no marker of this estate, or another's;
//   - an ABSENT omission is pending: nothing is there and the plan creates it;
//   - any other omission is not-observed, with the omission's reason;
//   - an instance the plan leaves unchanged is in-sync, and one it updates
//     or replaces is drifted, with the top-level attributes the plan writes;
//   - a create with no omission (an instance the node resolver found nothing
//     for) is pending;
//   - an expected instance the plan never reached is not-observed,
//     not-reached, which is what drift-settle owes every silent address.
func liveDriftRecords(in liveDriftInput) []views.LiveDriftRecord {
	observedAt := in.ObservedAt.Format(time.RFC3339)
	omitted := map[string]projection.Omission{}
	unowned := map[string]projection.Unowned{}
	if in.Projection != nil {
		for _, o := range in.Projection.Omitted {
			omitted[o.Addr.String()] = o
		}
		for _, u := range in.Projection.Unowned {
			unowned[u.Addr.String()] = u
		}
	}
	bound := map[string]views.LivePlanBound{}
	for _, b := range in.Bound {
		bound[b.Addr] = b
	}

	out := make([]views.LiveDriftRecord, 0, len(in.Expected))
	for _, addr := range in.Expected {
		key := addr.String()
		rec := views.LiveDriftRecord{
			Member:     in.Member,
			Engine:     "choudoufu",
			Address:    key,
			ObservedAt: observedAt,
			Read:       "plan",
			Evidence:   views.LiveDriftEvidence{Tool: "choudoufu", Version: tfversion.Fork, Command: in.Command},
		}
		if b, ok := bound[key]; ok {
			rec.Ownership = &views.LiveDriftOwnership{Verdict: "owned", By: string(b.Source)}
		}

		if o, ok := omitted[key]; ok {
			rec.Evidence.EngineCode = string(o.Reason)
			switch o.Reason {
			case projection.ReasonUnowned:
				rec.Read = "ownership"
				rec.Status = "unclaimed"
				own := &views.LiveDriftOwnership{Verdict: "adoptable", MarkerOnObject: boolPtr(false)}
				if u, ok := unowned[key]; ok && u.Estate != "" {
					own = &views.LiveDriftOwnership{Verdict: "foreign", By: "marker", MarkerOnObject: boolPtr(true), OwnedBy: u.Estate}
				}
				rec.Ownership = own
			case projection.ReasonAbsent:
				rec.Status = "pending"
			default:
				rec.Status = "not-observed"
				rec.Unobserved = &views.LiveDriftUnobserved{Reason: liveDriftUnobservedReason(o.Reason), Detail: o.Detail}
			}
			out = append(out, rec)
			continue
		}

		var change *plans.ResourceInstanceChangeSrc
		if in.Plan != nil && in.Plan.Changes != nil {
			change = in.Plan.Changes.ResourceInstance(addr)
		}
		if change == nil {
			rec.Status = "not-observed"
			rec.Unobserved = &views.LiveDriftUnobserved{Reason: "not-reached", Detail: "the plan of this cone holds no change for this instance"}
			out = append(out, rec)
			continue
		}
		rec.Evidence.EngineCode = strings.ToLower(change.Action.String())
		switch change.Action {
		case plans.NoOp:
			rec.Status = "in-sync"
		case plans.Create:
			rec.Status = "pending"
		case plans.Delete:
			rec.Status = "drifted"
		default:
			rec.Status = "drifted"
			rec.Attributes = liveDriftAttributes(change, in.Schemas)
		}
		out = append(out, rec)
	}
	return out
}

func boolPtr(b bool) *bool { return &b }

// liveDriftUnobservedReason maps an omission reason onto chant's
// UnobservedReason. The source token stays in evidence.engineCode.
func liveDriftUnobservedReason(r projection.Reason) string {
	switch r {
	case projection.ReasonNeedsDiscovery:
		return "no-binding"
	case projection.ReasonOutOfScope:
		return "filtered"
	default:
		return "read-failed"
	}
}

// liveDriftAttributes is the top-level attributes a planned update writes:
// each one whose planned value is unknown or differs from the live value
// the projection read. Sensitive is the planner's own mark on either side,
// or the schema's.
func liveDriftAttributes(change *plans.ResourceInstanceChangeSrc, schemas *tofu.Schemas) []views.LiveDriftAttribute {
	if schemas == nil {
		return nil
	}
	schema, _ := schemas.ResourceTypeConfig(change.ProviderAddr.Provider, addrs.ManagedResourceMode, change.Addr.Resource.Resource.Type)
	if schema == nil || schema.Block == nil {
		return nil
	}
	decoded, err := change.Decode(schema)
	if err != nil {
		log.Printf("[WARN] live-plan -resource: decoding the planned change of %s: %s", change.Addr, err)
		return nil
	}
	before, beforeMarks := decoded.Before.UnmarkDeepWithPaths()
	after, afterMarks := decoded.After.UnmarkDeepWithPaths()
	if before.IsNull() || !before.IsKnown() || after.IsNull() || !after.IsKnown() || !before.Type().IsObjectType() || !after.Type().IsObjectType() {
		return nil
	}
	sensitive := map[string]bool{}
	for _, pvm := range append(beforeMarks, afterMarks...) {
		if _, ok := pvm.Marks[marks.Sensitive]; !ok || len(pvm.Path) == 0 {
			continue
		}
		if step, ok := pvm.Path[0].(cty.GetAttrStep); ok {
			sensitive[step.Name] = true
		}
	}
	for name, attr := range schema.Block.Attributes {
		if attr.Sensitive {
			sensitive[name] = true
		}
	}

	names := make([]string, 0, len(after.Type().AttributeTypes()))
	for name := range after.Type().AttributeTypes() {
		if before.Type().HasAttribute(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var out []views.LiveDriftAttribute
	for _, name := range names {
		b, a := before.GetAttr(name), after.GetAttr(name)
		if a.IsWhollyKnown() && b.RawEquals(a) {
			continue
		}
		out = append(out, views.LiveDriftAttribute{Path: name, Sensitive: sensitive[name]})
	}
	return out
}
