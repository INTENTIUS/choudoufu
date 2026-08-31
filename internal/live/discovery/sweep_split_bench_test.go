// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/registry"
)

// This is issue #579's measurement: of a full projection's API cost, how
// much is the estate-wide sweep (the O(types) discovery pass, plus the one
// Resource Groups Tagging API GetResources call the marker index makes) and
// how much is the per-instance read pass ([projection.BuildFrom])?
//
// terralith_ceiling_bench_test.go (#565) already drives the same
// Discover+BuildFrom pipeline against the same fixture and reports one
// api_calls_total for both phases together. That total cannot answer #579,
// whose whole proposition is that one of the two terms can be dropped. This
// file changes exactly one thing: it snapshots the [flocitest.CountingProxy]
// BETWEEN the two phases, so the two terms are separately reported. Nothing
// else about the run differs, deliberately, so a number here is comparable
// to a number there.
//
// It also reports the population split the sweep's reach actually depends
// on, per INSTANCE rather than per type: an instance whose type carries a
// settable top-level tags argument ([markers.Taggable], read off the
// provider's own schema - the same predicate the run itself applies) can
// appear in the tag index; one whose type does not, structurally cannot,
// however many pages the sweep reads.
//
// # Not a ratchet
//
// Like the ceiling benchmark it borrows from, this asserts no threshold.
// The deliverable is the ratio at named scales, recorded in
// rulings/20260830-marker-verified-fast-projection.md, not a number a future
// run could regress.
const sweepSplitEstate = "sweep-split-cohort"

// TestSweepSplitAgainstFloci measures the two terms.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/discovery/ -run TestSweepSplitAgainstFloci -v -timeout 20m
//	SWEEP_SPLIT_SCALE=4 TF_FLOCI_TEST=1 go test ./internal/live/discovery/ -run TestSweepSplitAgainstFloci -v -timeout 20m
func TestSweepSplitAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "discovery/sweep-split")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, terraformBin)
	flocitest.RequireBinary(t, "go")

	scale := sweepSplitScale(t)
	report := runSweepSplitBenchmark(t, scale)
	t.Logf("%s", report)
	for _, line := range report.PerType {
		t.Logf("  %s", line)
	}
	for _, line := range report.sweepLegLines() {
		t.Logf("  %s", line)
	}
	for _, line := range report.byAPILines() {
		t.Logf("  %s", line)
	}
}

func sweepSplitScale(t *testing.T) int {
	t.Helper()
	v := os.Getenv("SWEEP_SPLIT_SCALE")
	if v == "" {
		return 1
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		t.Fatalf("SWEEP_SPLIT_SCALE=%q is not a positive integer", v)
	}
	return n
}

type sweepSplitReport struct {
	Scale     int
	Instances int
	Types     int

	// TaggableInstances is how many resolved instances have a type the
	// provider schema gives a settable top-level tags argument, i.e. how
	// many the estate-wide marker sweep could ever see. The complement is
	// what no number of sweep pages reaches.
	TaggableInstances   int
	MarkerlessInstances int

	DiscoverElapsed time.Duration
	BuildElapsed    time.Duration

	DiscoverCalls int
	BuildCalls    int
	SweepCalls    int // GetResources against the Resource Groups Tagging API

	DiscoverPages int
	BuildPages    int

	DiscoverByAPI map[string]int
	BuildByAPI    map[string]int

	Materialized int
	Bound        int
	Unclaimed    int

	// PerType is one line per declared type: instance count and whether
	// the sweep could ever see it. Reported rather than summarized because
	// a bare "48% taggable" is exactly the shape of number this repository
	// has had to re-derive before.
	PerType []string

	// SweepScansBySource is how many types the SWEEP itself scanned through
	// each enumeration source, counted off [Result.Scans] rather than
	// inferred from the proxy's API names (#586). It is the attribution the
	// by-API breakdown cannot give: "CloudApiService.ListResources=435" says
	// which wire call was made, not which leg of [partitionSweepTypes] made
	// it, and the config-driven scan uses the same transport as the sweep.
	SweepScansBySource map[EnumerationSource]int

	// SweepGapsByReason is the same attribution for the types the sweep
	// visited and could not enumerate at all - a type reported here cost no
	// list call, so it is the difference between the native leg's 992 types
	// and the far smaller number of calls it actually issues.
	SweepGapsByReason map[SweepGapReason]int

	// TaggingLegTypes and NativeLegTypes are [partitionSweepTypes]' own two
	// answers for this run, so the routing is reported next to its cost.
	TaggingLegTypes int
	NativeLegTypes  int
}

func (r sweepSplitReport) String() string {
	total := r.DiscoverCalls + r.BuildCalls
	share := 0.0
	if total > 0 {
		share = 100 * float64(r.BuildCalls) / float64(total)
	}
	return fmt.Sprintf(
		"SWEEP SPLIT: scale=%d instances=%d types=%d taggable_instances=%d markerless_instances=%d "+
			"discover_calls=%d build_calls=%d total=%d read_pass_share=%.1f%% sweep_getresources_calls=%d "+
			"discover_pages=%d build_pages=%d discover=%s build=%s "+
			"materialized=%d bound=%d unclaimed=%d [emulator=floci]",
		r.Scale, r.Instances, r.Types, r.TaggableInstances, r.MarkerlessInstances,
		r.DiscoverCalls, r.BuildCalls, total, share, r.SweepCalls,
		r.DiscoverPages, r.BuildPages, r.DiscoverElapsed, r.BuildElapsed,
		r.Materialized, r.Bound, r.Unclaimed,
	)
}

// sweepLegLines reports which leg of [partitionSweepTypes] paid for the
// discovery calls above, counted off [Result.Scans] and [Result.SweepGaps]
// rather than read off the wire.
func (r sweepSplitReport) sweepLegLines() []string {
	lines := []string{fmt.Sprintf("partition: tagging_leg=%d native_leg=%d", r.TaggingLegTypes, r.NativeLegTypes)}

	sources := make([]string, 0, len(r.SweepScansBySource))
	for s := range r.SweepScansBySource {
		sources = append(sources, string(s))
	}
	sort.Strings(sources)
	for _, s := range sources {
		lines = append(lines, fmt.Sprintf("sweep scans via %-14s %d types", s, r.SweepScansBySource[EnumerationSource(s)]))
	}

	reasons := make([]string, 0, len(r.SweepGapsByReason))
	for g := range r.SweepGapsByReason {
		reasons = append(reasons, string(g))
	}
	sort.Strings(reasons)
	for _, g := range reasons {
		lines = append(lines, fmt.Sprintf("sweep gap    %-22s %d types (no list call issued)", g, r.SweepGapsByReason[SweepGapReason(g)]))
	}
	return lines
}

func (r sweepSplitReport) byAPILines() []string {
	keys := map[string]bool{}
	for k := range r.DiscoverByAPI {
		keys[k] = true
	}
	for k := range r.BuildByAPI {
		keys[k] = true
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	lines := make([]string, 0, len(names))
	for _, k := range names {
		lines = append(lines, fmt.Sprintf("%-60s discover=%-6d build=%-6d", k, r.DiscoverByAPI[k], r.BuildByAPI[k]))
	}
	return lines
}

func subtractCounts(after, before map[string]int) map[string]int {
	out := map[string]int{}
	for k, v := range after {
		if d := v - before[k]; d != 0 {
			out[k] = d
		}
	}
	return out
}

func sumCounts(m map[string]int) int {
	total := 0
	for _, v := range m {
		total += v
	}
	return total
}

// runSweepSplitBenchmark is runTerralithCeilingBenchmark's shape with the
// proxy read between the two phases and the memory sampling dropped (#579
// asks for calls, not bytes).
func runSweepSplitBenchmark(t *testing.T, scale int) sweepSplitReport {
	t.Helper()

	root := flocitest.RepoRoot(t)
	dir := t.TempDir()
	prefix := fmt.Sprintf("ss%d", os.Getpid()%100000)

	genCmd := exec.Command("go", "run", "./tools/terralith-gen", //nolint:gosec // fixed binary and args, test-only
		"-scale", strconv.Itoa(scale), "-prefix", prefix, "-out", dir)
	genCmd.Dir = root
	genOut, err := genCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run ./tools/terralith-gen -scale %d: %v\n%s", scale, err, genOut)
	}
	t.Logf("terralith-gen: %s", strings.TrimSpace(string(genOut)))

	flociPort := flocitest.StartFloci(t, "cdf-sweepsplit")
	proxy := flocitest.NewCountingProxy(t, flocitest.Endpoint(flociPort))

	t.Setenv("AWS_ENDPOINT_URL", proxy.Endpoint())
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)

	flocitest.PluginCacheDir(t)
	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")
	flocitest.Run(t, dir, terraformBin, "apply", "-auto-approve", "-input=false", "-no-color")

	proxy.Reset()

	stateFile := filepath.Join(dir, "terraform.tfstate")
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("apply left no state file: %v", err)
	}
	if err := os.Remove(stateFile); err != nil {
		t.Fatalf("removing the state file: %v", err)
	}
	_ = os.Remove(stateFile + ".backup")

	provider := launchAWSProvider(t, dir)
	// loadModuleConfig, not loadConfig: since #574 tools/terralith-gen
	// emits a child module (modules/team_pod) to exercise module-nested
	// expansion, and loadConfig's walker fails the test on any module call.
	cfg := loadModuleConfig(t, dir)

	schema := provider.GetProviderSchema(context.Background())
	if schema.Diagnostics.HasErrors() {
		t.Fatalf("reading the AWS provider schema: %s", schema.Diagnostics.Err())
	}
	resolveResult, resolveDiags := identity.ResolveWith(context.Background(), cfg, identity.Context{Schemas: schema.ResourceTypes})
	if resolveDiags.HasErrors() {
		t.Logf("identity resolution diagnostics (%d), not fatal to this benchmark:\n%s", len(resolveDiags), renderDiags(resolveDiags))
	}
	resolutions := resolveResult.All()

	declaredTypes := map[string]int{}
	taggableInstances, markerlessInstances := 0, 0
	for _, r := range resolutions {
		typeName := r.Addr.Resource.Resource.Type
		declaredTypes[typeName]++
		if s, ok := schema.ResourceTypes[typeName]; ok && markers.Taggable(s.Block) {
			taggableInstances++
		}
		if _, ok := identity.MarkerlessTypes[typeName]; ok {
			markerlessInstances++
		}
	}
	perType := make([]string, 0, len(declaredTypes))
	for typeName, n := range declaredTypes {
		taggable := false
		if s, ok := schema.ResourceTypes[typeName]; ok {
			taggable = markers.Taggable(s.Block)
		}
		_, markerless := identity.MarkerlessTypes[typeName]
		perType = append(perType, fmt.Sprintf("%-40s instances=%-5d taggable=%-6v markerless=%v", typeName, n, taggable, markerless))
	}
	sort.Strings(perType)

	// The production Request shape, not the ceiling benchmark's narrower
	// one: internal/command/live_plan.go's statelessDiscoverOne sets
	// Sweep, CollectUnclaimed, and - whenever the run names an endpoint
	// override, which every emulator run does - CloudControl, Roster,
	// Tagging and TaggingSweep. Without those last four, Sweep=true would
	// fall back to per-type listing over the whole admission table and
	// the estate-wide sweep's cost would be a thousand list calls rather
	// than the one GetResources #579 is about.
	roster, rosterErr := registry.Embedded()
	if rosterErr != nil {
		t.Fatalf("loading the embedded roster: %v", rosterErr)
	}
	ccCfg := cloudcontrol.Config{Endpoint: proxy.Endpoint(), Region: awsRegion}

	req := Request{
		Estate:           sweepSplitEstate,
		Config:           cfg,
		Resolutions:      resolutions,
		Provider:         provider,
		Region:           awsRegion,
		CollectUnclaimed: true,
		Sweep:            true,
		Roster:           roster,
		CloudControl:     cloudcontrol.New(ccCfg),
		Tagging:          cloudcontrol.NewTagging(ccCfg),
		TaggingSweep:     true,
	}

	discoverStart := time.Now()
	res, diags := Discover(context.Background(), req)
	discoverElapsed := time.Since(discoverStart)
	if diags.HasErrors() {
		t.Logf("Discover diagnostics (%d), not fatal to this benchmark:\n%s", len(diags), renderDiags(diags))
	}

	afterDiscover := proxy.Counts()
	afterDiscoverPages := proxy.PaginationTotal()

	provs := projection.SingleProvider(addrs.AbsProviderConfig{
		Module:   addrs.RootModule,
		Provider: addrs.NewDefaultProvider("aws"),
	}, provider)
	buildStart := time.Now()
	proj, projDiags := projection.BuildFrom(context.Background(), cfg, res.Resolutions, provs)
	buildElapsed := time.Since(buildStart)
	if projDiags.HasErrors() {
		t.Logf("BuildFrom diagnostics (%d), not fatal to this benchmark:\n%s", len(projDiags), renderDiags(projDiags))
	}

	afterBuild := proxy.Counts()
	afterBuildPages := proxy.PaginationTotal()

	discoverByAPI := subtractCounts(afterDiscover, map[string]int{})
	buildByAPI := subtractCounts(afterBuild, afterDiscover)

	sweepCalls := 0
	for action, n := range afterBuild {
		if strings.Contains(action, "GetResources") {
			sweepCalls += n
		}
	}

	// #586's attribution. partitionSweepTypes is re-run against the same
	// Request so the routing is reported next to the cost it produced;
	// Discover's own call is pure over (req, decl), so this reproduces it
	// rather than re-deciding it.
	scansBySource := map[EnumerationSource]int{}
	for _, s := range res.Scans {
		if s.Sweep {
			scansBySource[s.Source]++
		}
	}
	gapsByReason := map[SweepGapReason]int{}
	for _, g := range res.SweepGaps {
		gapsByReason[g.Reason]++
	}
	taggingLeg, nativeLeg := 0, 0
	if decl, declDiags := declaredInstances(context.Background(), req); !declDiags.HasErrors() {
		tagging, native := partitionSweepTypes(req, decl)
		taggingLeg, nativeLeg = len(tagging), len(native)
	}

	return sweepSplitReport{
		Scale:               scale,
		Instances:           len(resolutions),
		Types:               len(declaredTypes),
		TaggableInstances:   taggableInstances,
		MarkerlessInstances: markerlessInstances,
		DiscoverElapsed:     discoverElapsed,
		BuildElapsed:        buildElapsed,
		DiscoverCalls:       sumCounts(discoverByAPI),
		BuildCalls:          sumCounts(buildByAPI),
		SweepCalls:          sweepCalls,
		DiscoverPages:       afterDiscoverPages,
		BuildPages:          afterBuildPages - afterDiscoverPages,
		DiscoverByAPI:       discoverByAPI,
		BuildByAPI:          buildByAPI,
		Materialized:        len(proj.Materialized),
		Bound:               len(res.Bindings),
		Unclaimed:           len(res.Unclaimed),
		PerType:             perType,
		SweepScansBySource:  scansBySource,
		SweepGapsByReason:   gapsByReason,
		TaggingLegTypes:     taggingLeg,
		NativeLegTypes:      nativeLeg,
	}
}

// TestSweepUniversePartitionIsMostlyNative is the other half of #579's
// measurement, and it needs no emulator: of the admitted types the
// estate-wide sweep would list, how many does the one estate-filtered
// GetResources call actually cover?
//
// This matters because #579's own asymmetry table costs the sweep at "1
// paginated call". That is [sweepViaTagging] in isolation. What
// Request.Sweep costs is [partitionSweepTypes]' two legs together, and the
// second leg is a per-type list attempt for every type the ARN join cannot
// reach ([arnJoinReaches]: the type must be in the roster AND its CFN type
// covered by arnJoinTable, a hand-curated table).
//
// The numbers this logs are recorded in
// rulings/20260830-marker-verified-fast-projection.md against the commit that
// produced them. This test asserts only the partition invariant - every
// swept type lands in exactly one leg, and neither leg is empty - because
// a threshold on the ratio would be a ratchet on a hand-curated table
// somebody is meant to be able to grow.
func TestSweepUniversePartitionIsMostlyNative(t *testing.T) {
	roster, err := registry.Embedded()
	if err != nil {
		t.Fatalf("loading the embedded roster: %v", err)
	}
	req := Request{Roster: roster, TaggingSweep: true}
	decl, diags := declaredInstances(context.Background(), req)
	if diags.HasErrors() {
		t.Fatalf("building an empty declared set: %s", renderDiags(diags))
	}

	universe := sweepTypes(req, decl)
	tagging, native := partitionSweepTypes(req, decl)

	t.Logf("sweep universe=%d tagging_leg=%d native_leg=%d", len(universe), len(tagging), len(native))

	if len(tagging)+len(native) != len(universe) {
		t.Errorf("partitionSweepTypes returned %d+%d types for a %d-type universe; a type has been dropped from or duplicated across the two legs",
			len(tagging), len(native), len(universe))
	}
	if len(tagging) == 0 {
		t.Error("the tagging leg is empty, so Request.TaggingSweep buys nothing and every swept type costs a list call. If this is deliberate, sweepViaTagging is dead code and #51 has been reverted.")
	}
	if len(native) == 0 {
		t.Error("the native leg is empty, which would mean the one GetResources call covers the whole admitted universe. That would be excellent news and it contradicts arnJoinTable being hand-curated - check arnJoinReaches before believing it.")
	}
}

// TestNativeSweepLegRoutingIsExhaustive states the routing rule #586 asked
// for, in executable form, and separates the part of it that is a
// correctness requirement from the part that is a property of a
// hand-curated table.
//
// [partitionSweepTypes] sends a type to the native per-type leg for exactly
// three reasons, and this asserts that those three account for every type
// that lands there - so a fourth clause cannot appear without saying so.
// The interesting fact is the SIZE of each: measured at 5dbe452a1e against
// live/registry.json's embedded roster, of 992 native types only 6 are
// issue #394's carve-out ([typeNeedsResourceObjectToRecompose], the
// companion pairs that genuinely need a native list call's own resource
// object) and the other 986 are there solely because [arnJoinTable] has no
// row to place their ARNs through.
//
// # Why that does not make the 986 removable
//
// #586's premise is that [markerTFType] reads the TF type off the marker,
// so the join table is not needed to place a tagged object. That premise is
// TRUE, and it is not what routes these types. [arnJoinReaches] is a proxy
// for a different question - "can the one estate-wide GetResources answer
// FIND this type's objects" - and the marker answers placement, not
// enumeration. Moving a type off the native leg swaps a second, independent
// enumeration (Cloud Control ListResources, or the provider's own list
// resource) for the Resource Groups Tagging API alone, whose index both
// lags writes and covers a different, partial set of services. #394's own
// doc comment records what that costs: before the !arnJoinReaches clause
// existed, a deleted block's live object was "silently never destroyed and
// never even diagnosed".
//
// The second assertion is the one that would catch a regression widening
// the native leg by accident: a type the ARN join CAN reach, and which is
// not a #394 companion, must be in the tagging leg.
func TestNativeSweepLegRoutingIsExhaustive(t *testing.T) {
	roster, err := registry.Embedded()
	if err != nil {
		t.Fatalf("loading the embedded roster: %v", err)
	}
	req := Request{Roster: roster, TaggingSweep: true}
	decl, diags := declaredInstances(context.Background(), req)
	if diags.HasErrors() {
		t.Fatalf("building an empty declared set: %s", renderDiags(diags))
	}

	tagging, native := partitionSweepTypes(req, decl)

	var carveOut, noARNJoin, both, unexplained int
	for _, typeName := range native {
		needsObject := typeNeedsResourceObjectToRecompose(typeName)
		noJoin := !arnJoinReaches(req, typeName)
		switch {
		case needsObject && noJoin:
			both++
		case needsObject:
			carveOut++
		case noJoin:
			noARNJoin++
		default:
			unexplained++
			t.Errorf("%s is in the native sweep leg but is neither a #394 companion pair nor outside arnJoinTable's coverage; partitionSweepTypes has grown a clause this test does not know about", typeName)
		}
	}
	t.Logf("native leg=%d: needs_resource_object_only=%d no_arn_join_only=%d both=%d unexplained=%d",
		len(native), carveOut, noARNJoin, both, unexplained)

	// What the native leg actually COSTS is not its type count: a type the
	// provider cannot list and Cloud Control cannot enumerate reports a
	// sweep gap without issuing a call at all. This is the population that
	// converts to wire calls, and it is the number #586 is really about.
	var enumerableTaggable int
	for _, typeName := range native {
		cfnType, mapped := roster.CloudControlType(typeName)
		if !mapped {
			continue
		}
		if _, ok := roster.EnumerationSource(typeName); !ok {
			continue
		}
		if taggable, _ := roster.TaggableKnown(cfnType); taggable {
			enumerableTaggable++
		}
	}
	t.Logf("native leg types Cloud Control can enumerate AND the registry calls taggable: %d (the ones that cost a ListResources)", enumerableTaggable)

	inTagging := make(map[string]bool, len(tagging))
	for _, typeName := range tagging {
		inTagging[typeName] = true
	}
	for _, typeName := range sweepTypes(req, decl) {
		if typeNeedsResourceObjectToRecompose(typeName) || !arnJoinReaches(req, typeName) {
			continue
		}
		if !inTagging[typeName] {
			t.Errorf("%s can be placed by the ARN join and is not a #394 companion pair, so the one estate-wide GetResources call covers it - but partitionSweepTypes routed it to the native per-type leg, which costs a list call for nothing", typeName)
		}
	}
}
