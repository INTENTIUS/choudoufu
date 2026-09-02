// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/registry"
)

// This file is issue #584's measurement: the sliced-versus-whole matrix,
// both binaries, on ONE estate rather than on two hand-written fixtures.
//
// It differs from sweep_split_bench_test.go (#581) in the two ways #582's
// open question 3 and #584 between them require:
//
//  1. **The estate is MIGRATED before anything is measured.** #581 deleted
//     the stock state file and measured a fixture whose live objects carry
//     no marker at all, so bound=0 and its read pass is a lower bound on
//     the day-2 case. Here the run goes through `live-import -approve`
//     first, exactly as live/live-cert/terralith-scale.sh's migrate stage
//     does, so every taggable instance carries tofu-estate/tofu-address
//     before any measurement is taken.
//
//  2. **The sweep is split into its two legs, not treated as one term.**
//     [sweepViaTagging] costs the estate-filtered GetResources call(s) and
//     nothing else - it is pure post-processing over [markerIndex]'s one
//     answer - while the native leg costs one list attempt per type
//     [arnJoinReaches] cannot reach. Those two legs have completely
//     different scaling laws and #579's proposed live-verify mode costs the
//     first alone, so reporting "sweep = 560" hides the whole question.
//
// Slicing is done by PARTITIONING the generator's output, never by
// hand-writing k fixtures: the comparison is only meaningful if both sides
// are the same estate. See [partitionByComponent].
//
// Not a ratchet. Nothing here asserts a threshold; the deliverable is the
// recorded matrix.
//
//	SLICE_SCALE=1 SLICE_K=2 SLICE_OUT=/tmp/k2.json TF_FLOCI_TEST=1 \
//	  env -u PWD go test ./internal/live/discovery/ -run TestSlicingMatrixAgainstFloci -v -timeout 60m
const slicingTerraformBin = "terraform"

// ---------------------------------------------------------------------------
// The report
// ---------------------------------------------------------------------------

type slicingReport struct {
	Scale         int    `json:"scale"`
	K             int    `json:"k"`
	Mode          string `json:"mode"`
	Commit        string `json:"commit"`
	Emulator      string `json:"emulator"`
	Prefix        string `json:"prefix"`
	WholeInstance int    `json:"whole_estate_state_instances"`

	// StockWholeApplySeconds is how long the one stock apply that created
	// every live object took. Reported for context only: it is paid once
	// per scenario regardless of k.
	StockWholeApplySeconds float64 `json:"stock_whole_apply_seconds"`

	Slices []*sliceRow `json:"slices"`

	// CrossSliceRefs is every reference this partition had to convert,
	// with what it became. Empty is a finding, not an omission.
	CrossSliceRefs []crossRef `json:"cross_slice_refs"`

	// LayerCutRefs is the same list for the alternative "slice by layer"
	// partition an operator is far more likely to actually write (one
	// slice per generated file: iam, network, ecs, dns, pods). Computed
	// statically whatever the measured mode is, because the conversion
	// cost is a property of where the cut falls, not of anything that has
	// to be applied.
	LayerCutRefs []crossRef `json:"layer_cut_refs"`
}

type crossRef struct {
	FromSlice string `json:"from_slice"`
	FromBlock string `json:"from_block"`
	Ref       string `json:"ref"`
	Became    string `json:"became"`
}

type sliceRow struct {
	Slice  string   `json:"slice"`
	Estate string   `json:"estate"`
	Groups []string `json:"groups"`

	StateInstances int `json:"state_instances"`

	StockPlanCalls   int            `json:"stock_plan_calls"`
	StockPlanSeconds float64        `json:"stock_plan_seconds"`
	StockPlanByAPI   map[string]int `json:"stock_plan_by_api"`
	StockPlanSummary string         `json:"stock_plan_summary"`

	ImportCalls   int     `json:"import_calls"`
	ImportSeconds float64 `json:"import_seconds"`
	ImportSummary string  `json:"import_summary"`

	HintApplySeconds float64 `json:"hint_apply_seconds,omitempty"`
	HintApplySummary string  `json:"hint_apply_summary,omitempty"`

	Plans []planRun `json:"plans"`

	Split *legSplit `json:"leg_split"`
}

type planRun struct {
	Variant  string         `json:"variant"`
	Pass     string         `json:"pass"` // "cold" (first plan of this variant) or "warm"
	Calls    int            `json:"calls"`
	Seconds  float64        `json:"seconds"`
	ByAPI    map[string]int `json:"by_api"`
	Summary  string         `json:"summary"`
	ExitCode int            `json:"exit_code"`

	// Proposed is every address the plan proposed to change, so a
	// non-empty plan on an estate that was migrated moments earlier is
	// visible as addresses rather than as a count.
	Proposed []string `json:"proposed,omitempty"`
}

// legSplit is the three-leg split. tagging + native + read is the whole of
// what a stateless plan's projection costs, apart from the config-driven
// scan (which stock's own refresh is the analogue of) and the post-sweep
// legs (bind, orphan classification, parent-read, fold-child).
type legSplit struct {
	ResolvedInstances   int `json:"resolved_instances"`
	DeclaredTypes       int `json:"declared_types"`
	TaggableInstances   int `json:"taggable_instances"`
	MarkerlessInstances int `json:"markerless_instances"`

	SweepUniverse   int `json:"sweep_universe"`
	TaggingUniverse int `json:"tagging_universe"`
	NativeUniverse  int `json:"native_universe"`

	DiscoverCalls int `json:"discover_calls"`
	BuildCalls    int `json:"build_calls"`

	TaggingLegCalls  int `json:"tagging_leg_calls"`
	ConfigScanCalls  int `json:"config_scan_calls"`
	BoundaryCalls    int `json:"boundary_calls"`
	NativeSweepCalls int `json:"native_sweep_calls"`
	PostSweepCalls   int `json:"post_sweep_calls"`

	DiscoverSeconds float64 `json:"discover_seconds"`
	BuildSeconds    float64 `json:"build_seconds"`

	DiscoverPages int `json:"discover_pages"`
	BuildPages    int `json:"build_pages"`

	Materialized int `json:"materialized"`
	Bound        int `json:"bound"`
	Unclaimed    int `json:"unclaimed"`

	DiscoverByAPI map[string]int `json:"discover_by_api"`
	BuildByAPI    map[string]int `json:"build_by_api"`
}

// ---------------------------------------------------------------------------
// The test
// ---------------------------------------------------------------------------

func TestSlicingMatrixAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "discovery/slicing-matrix")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, slicingTerraformBin)
	flocitest.RequireBinary(t, "go")

	scale := envInt(t, "SLICE_SCALE", 1)
	k := envInt(t, "SLICE_K", 1)
	mode := os.Getenv("SLICE_MODE")
	if mode == "" {
		mode = "component"
	}
	guidedMatrix := os.Getenv("SLICE_GUIDED_MATRIX") != ""

	root := flocitest.RepoRoot(t)
	work := t.TempDir()
	prefix := fmt.Sprintf("sl%d", os.Getpid()%100000)

	base := filepath.Join(work, "base")
	genCmd := exec.Command("go", "run", "./tools/terralith-gen", //nolint:gosec // fixed binary and args, test-only
		"-scale", strconv.Itoa(scale), "-prefix", prefix, "-out", base)
	genCmd.Dir = root
	if out, err := genCmd.CombinedOutput(); err != nil {
		t.Fatalf("terralith-gen: %v\n%s", err, out)
	} else {
		t.Logf("terralith-gen: %s", strings.TrimSpace(string(out)))
	}

	port := flocitest.StartFloci(t, "cdf-slicebench")
	proxy := flocitest.NewCountingProxy(t, flocitest.Endpoint(port))
	t.Setenv("AWS_ENDPOINT_URL", proxy.Endpoint())
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)
	flocitest.PluginCacheDir(t)

	report := &slicingReport{
		Scale:    scale,
		K:        k,
		Mode:     mode,
		Commit:   flocitest.HeadCommit(t),
		Emulator: flocitest.Image(),
		Prefix:   prefix,
	}

	// ---- 1. one stock apply of the WHOLE estate: this is the cloud.
	whole := filepath.Join(work, "whole")
	copyTree(t, base, whole)
	flocitest.Run(t, whole, slicingTerraformBin, "init", "-input=false", "-no-color")
	applyStart := time.Now()
	flocitest.Run(t, whole, slicingTerraformBin, "apply", "-auto-approve", "-input=false", "-no-color")
	report.StockWholeApplySeconds = time.Since(applyStart).Seconds()

	st := readState(t, filepath.Join(whole, "terraform.tfstate"))
	report.WholeInstance = st.instanceCount()
	t.Logf("stock apply: %d resource instances in %.1fs", report.WholeInstance, report.StockWholeApplySeconds)

	// ---- 2. partition the generated configuration.
	blocks := parseBlocks(t, base)
	groups := groupBlocks(blocks, mode)
	assignment := distribute(groups, k, st)
	report.LayerCutRefs = crossRefsFor(blocks, distribute(groupBlocks(blocks, "layer"), len(groupBlocks(blocks, "layer")), st))

	tofuBin := flocitest.BuildTofu(t)

	for i, sl := range assignment {
		estate := fmt.Sprintf("%s-s%d", prefix, i)
		row := &sliceRow{Slice: fmt.Sprintf("s%d", i), Estate: estate, Groups: sl.groupNames()}

		stockDir := filepath.Join(work, fmt.Sprintf("s%d-stock", i))
		chdfDir := filepath.Join(work, fmt.Sprintf("s%d-chdf", i))
		refs := writeSlice(t, base, stockDir, blocks, sl, st, "")
		_ = writeSlice(t, base, chdfDir, blocks, sl, st, estate)
		for _, r := range refs {
			r.FromSlice = row.Slice
			report.CrossSliceRefs = append(report.CrossSliceRefs, r)
		}

		sliceState := st.subset(sl)
		row.StateInstances = sliceState.instanceCount()
		writeState(t, filepath.Join(stockDir, "terraform.tfstate"), sliceState)

		// ---- 3. stock plan, on this slice's own state.
		flocitest.Run(t, stockDir, slicingTerraformBin, "init", "-input=false", "-no-color")
		proxy.Reset()
		start := time.Now()
		out, code := runCapture(t, stockDir, nil, slicingTerraformBin, "plan", "-input=false", "-no-color")
		row.StockPlanSeconds = time.Since(start).Seconds()
		row.StockPlanByAPI = proxy.Counts()
		row.StockPlanCalls = sumCounts(row.StockPlanByAPI)
		row.StockPlanSummary = lastLines(out, 3)
		if code != 0 {
			// A slice whose own configuration does not load is not a
			// finding about slicing, it is a broken partition, and it
			// silently poisons every number in the row. Loud, not logged:
			// the first version of this harness lost a whole run to a
			// reference pattern that missed indexed references.
			t.Errorf("slice %s stock plan exited %d - the partition produced a configuration stock cannot load:\n%s", row.Slice, code, lastLines(out, 20))
		}

		// ---- 4. migrate this slice: live-import -approve against its own
		// stock state. This is what #581 never did.
		flocitest.Run(t, chdfDir, tofuBin, "init", "-input=false", "-no-color")
		proxy.Reset()
		start = time.Now()
		out, code = runCapture(t, chdfDir, nil, tofuBin,
			"live-import", "-state="+filepath.Join(stockDir, "terraform.tfstate"), "-estate="+estate, "-approve")
		row.ImportSeconds = time.Since(start).Seconds()
		row.ImportCalls = proxy.Total()
		row.ImportSummary = grepLine(out, "newly stamped")
		if code != 0 {
			t.Fatalf("slice %s live-import -approve exited %d:\n%s", row.Slice, code, lastLines(out, 30))
		}
		t.Logf("slice %s migrated: %s (%d calls, %.1fs)", row.Slice, row.ImportSummary, row.ImportCalls, row.ImportSeconds)

		// ---- 4b. optional: one choudoufu apply, purely so guided
		// discovery has a hint to read. The hint is written by the
		// manager's final PersistState (projection.Manager.EnableHint,
		// issue #109) and "a plan never persists, so a plan never writes
		// one" - so without this step every "guided on" measurement is
		// really measuring guided's fallback path.
		if os.Getenv("SLICE_APPLY_FOR_HINT") != "" {
			start = time.Now()
			out, code = runCapture(t, chdfDir, nil, tofuBin, "apply", "-auto-approve", "-input=false", "-no-color")
			row.HintApplySeconds = time.Since(start).Seconds()
			row.HintApplySummary = grepLine(out, "Apply complete")
			if code != 0 {
				t.Errorf("slice %s apply-for-hint exited %d:\n%s", row.Slice, code, lastLines(out, 25))
			}
			t.Logf("slice %s apply-for-hint: %s (%.1fs)", row.Slice, row.HintApplySummary, row.HintApplySeconds)
		}

		// ---- 5. choudoufu plan, on the MIGRATED estate.
		variants := []struct {
			name string
			env  []string
		}{{"default", nil}}
		if guidedMatrix {
			variants = append(variants,
				struct {
					name string
					env  []string
				}{"guided-off", []string{"TOFU_DISABLE_GUIDED_DISCOVERY=1"}},
				struct {
					name string
					env  []string
				}{"cloudcontrol-off", []string{"TOFU_LIVE_CLOUDCONTROL=off"}},
				struct {
					name string
					env  []string
				}{"cloudcontrol-off+guided-off", []string{"TOFU_LIVE_CLOUDCONTROL=off", "TOFU_DISABLE_GUIDED_DISCOVERY=1"}},
			)
		}
		passes := []string{"cold", "warm"}
		if envInt(t, "SLICE_PLAN_PASSES", 2) == 1 {
			passes = passes[:1]
		}
		for _, v := range variants {
			for _, pass := range passes {
				proxy.Reset()
				start = time.Now()
				out, code = runCapture(t, chdfDir, v.env, tofuBin, "plan", "-input=false", "-no-color")
				run := planRun{
					Variant:  v.name,
					Pass:     pass,
					Seconds:  time.Since(start).Seconds(),
					ByAPI:    proxy.Counts(),
					Summary:  grepLine(out, "Plan:") + grepLine(out, "No changes"),
					ExitCode: code,
				}
				run.Calls = sumCounts(run.ByAPI)
				run.Proposed = proposedAddrs(out)
				row.Plans = append(row.Plans, run)
				if code != 0 {
					// Loud, and symmetrical with the stock-plan check above.
					// A refused plan's call count is not this plan's cost: it
					// is however far the run got before it gave up, and it is
					// not comparable with any other row. It was logged rather
					// than reported for the whole of issue #584's measurement,
					// the recorded exit_code was 1 in every CLI row, and the
					// numbers were quoted as clean ones anyway (issue #634).
					t.Errorf("slice %s plan[%s/%s] exited %d - %d calls recorded here are a REFUSED plan's cost, not a plan's cost:\n%s",
						row.Slice, v.name, pass, code, run.Calls, lastLines(out, 25))
				}
				t.Logf("slice %s plan[%s/%s]: %d calls in %.1fs", row.Slice, v.name, pass, run.Calls, run.Seconds)
			}
		}

		// ---- 6. the three-leg split, in process, on the same migrated estate.
		row.Split = measureLegs(t, chdfDir, estate, proxy)
		t.Logf("slice %s legs: tagging=%d native=%d read=%d (discover=%d build=%d, universe=%d/%d/%d, bound=%d)",
			row.Slice, row.Split.TaggingLegCalls, row.Split.NativeSweepCalls, row.Split.BuildCalls,
			row.Split.DiscoverCalls, row.Split.BuildCalls,
			row.Split.SweepUniverse, row.Split.TaggingUniverse, row.Split.NativeUniverse, row.Split.Bound)

		report.Slices = append(report.Slices, row)
	}

	if out := os.Getenv("SLICE_OUT"); out != "" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatalf("marshalling the report: %v", err)
		}
		if err := os.WriteFile(out, append(data, '\n'), 0o644); err != nil { //nolint:gosec // test artifact
			t.Fatalf("writing %s: %v", out, err)
		}
		t.Logf("wrote %s", out)
	}
}

// ---------------------------------------------------------------------------
// The three-leg split
// ---------------------------------------------------------------------------

func measureLegs(t *testing.T, dir, estate string, proxy *flocitest.CountingProxy) *legSplit {
	t.Helper()

	provider := launchAWSProvider(t, dir)
	cfg := loadModuleConfig(t, dir)

	schema := provider.GetProviderSchema(context.Background())
	if schema.Diagnostics.HasErrors() {
		t.Fatalf("reading the AWS provider schema: %s", schema.Diagnostics.Err())
	}
	resolveResult, resolveDiags := identity.ResolveWith(context.Background(), cfg, identity.Context{Schemas: schema.ResourceTypes})
	if resolveDiags.HasErrors() {
		t.Logf("identity resolution diagnostics (%d), not fatal:\n%s", len(resolveDiags), renderDiags(resolveDiags))
	}
	resolutions := resolveResult.All()

	declaredTypes := map[string]int{}
	taggable, markerless := 0, 0
	for _, r := range resolutions {
		typeName := r.Addr.Resource.Resource.Type
		declaredTypes[typeName]++
		if s, ok := schema.ResourceTypes[typeName]; ok && markers.Taggable(s.Block) {
			taggable++
		}
		if _, ok := identity.MarkerlessTypes[typeName]; ok {
			markerless++
		}
	}

	roster, err := registry.Embedded()
	if err != nil {
		t.Fatalf("loading the embedded roster: %v", err)
	}
	ccCfg := cloudcontrol.Config{Endpoint: proxy.Endpoint(), Region: awsRegion}

	out := &legSplit{
		ResolvedInstances:   len(resolutions),
		DeclaredTypes:       len(declaredTypes),
		TaggableInstances:   taggable,
		MarkerlessInstances: markerless,
	}

	// The leg meter: snapshot the proxy at every progress event and bucket
	// the interval by which leg the event came from. A type whose scan
	// records nothing fires no event, so its (zero, in practice) calls fold
	// into the next event's interval - stated rather than hidden.
	last := 0
	firstSweepSeen := false
	meter := func(ev ProgressEvent) {
		now := proxy.Total()
		delta := now - last
		last = now
		switch {
		case !ev.Sweep:
			out.ConfigScanCalls += delta
		case !firstSweepSeen:
			firstSweepSeen = true
			out.BoundaryCalls += delta
		default:
			out.NativeSweepCalls += delta
		}
	}

	req := Request{
		Estate:           estate,
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
		Progress:         meter,
	}

	// The universes, computed exactly rather than inferred from the call
	// counts: this is what "does the sweep scale down with a slice's type
	// count" is actually asking about.
	decl, declDiags := declaredInstances(context.Background(), req)
	if declDiags.HasErrors() {
		t.Fatalf("building the declared set: %s", renderDiags(declDiags))
	}
	tagging, native := partitionSweepTypes(req, decl)
	out.SweepUniverse = len(sweepTypes(req, decl))
	out.TaggingUniverse = len(tagging)
	out.NativeUniverse = len(native)

	// Reset here and nowhere earlier: launching the provider, resolving
	// identities and building the declared set all happen above and none of
	// them is part of either leg. Nothing is in flight at this point, so a
	// reset cannot race a subprocess's own call.
	proxy.Reset()
	pagesBefore := proxy.PaginationTotal()
	discoverStart := time.Now()
	res, diags := Discover(context.Background(), req)
	out.DiscoverSeconds = time.Since(discoverStart).Seconds()
	if diags.HasErrors() {
		t.Logf("Discover diagnostics (%d), not fatal:\n%s", len(diags), renderDiags(diags))
	}
	afterDiscover := proxy.Counts()
	afterDiscoverPages := proxy.PaginationTotal()
	out.PostSweepCalls = proxy.Total() - last

	provs := projection.SingleProvider(addrs.AbsProviderConfig{
		Module:   addrs.RootModule,
		Provider: addrs.NewDefaultProvider("aws"),
	}, provider)
	buildStart := time.Now()
	proj, projDiags := projection.BuildFrom(context.Background(), cfg, res.Resolutions, provs)
	out.BuildSeconds = time.Since(buildStart).Seconds()
	if projDiags.HasErrors() {
		t.Logf("BuildFrom diagnostics (%d), not fatal:\n%s", len(projDiags), renderDiags(projDiags))
	}
	afterBuild := proxy.Counts()

	discoverByAPI := subtractCounts(afterDiscover, map[string]int{})
	out.DiscoverByAPI = discoverByAPI
	out.DiscoverCalls = sumCounts(discoverByAPI)
	out.BuildByAPI = subtractCounts(afterBuild, afterDiscover)
	out.BuildCalls = sumCounts(out.BuildByAPI)
	out.DiscoverPages = afterDiscoverPages - pagesBefore
	out.BuildPages = proxy.PaginationTotal() - afterDiscoverPages

	for action, n := range discoverByAPI {
		if strings.Contains(action, "GetResources") {
			out.TaggingLegCalls += n
		}
	}
	// The tagging leg's own calls landed in whichever bucket was open when
	// markerIndex fetched them; take them back out so the buckets sum to
	// DiscoverCalls with the tagging leg reported separately.
	switch {
	case out.BoundaryCalls >= out.TaggingLegCalls:
		out.BoundaryCalls -= out.TaggingLegCalls
	case out.ConfigScanCalls >= out.TaggingLegCalls:
		out.ConfigScanCalls -= out.TaggingLegCalls
	}

	out.Materialized = len(proj.Materialized)
	out.Bound = len(res.Bindings)
	out.Unclaimed = len(res.Unclaimed)
	return out
}

// ---------------------------------------------------------------------------
// Partitioning
// ---------------------------------------------------------------------------

// tfBlock is one top-level block of the generated configuration.
type tfBlock struct {
	File string // "iam.tf"
	Kind string // "resource", "module", "locals", "terraform", "provider"
	Addr string // "aws_iam_role.team_0000_role", "module.team_pod", "" for locals
	Src  string // the block's own source text
	Refs []string
}

// refPattern matches a reference to another resource's attribute. The
// optional bracket group is load-bearing: `aws_iam_role.count_team[count.
// index].name` is how every count-expanded block in this fixture refers to
// its own role, and a pattern requiring three bare dot-separated segments
// misses it silently - which split a coupled component across two slices
// and produced a slice whose configuration did not load at all.
var refPattern = regexp.MustCompile(`\baws_[a-z0-9_]+\.[A-Za-z_][A-Za-z0-9_-]*(\[[^\]]*\])?\.[A-Za-z_][A-Za-z0-9_.\-]*\b`)

// refTarget is the "type.name" a matched reference points at, index
// expression stripped.
func refTarget(ref string) (target, attr string) {
	rest := ref
	if i := strings.Index(rest, "["); i >= 0 {
		j := strings.Index(rest, "]")
		target = rest[:i]
		attr = strings.TrimPrefix(rest[j+1:], ".")
		return target, attr
	}
	parts := strings.SplitN(rest, ".", 3)
	if len(parts) < 3 {
		return rest, ""
	}
	return parts[0] + "." + parts[1], parts[2]
}

func parseBlocks(t *testing.T, dir string) []tfBlock {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".tf") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var out []tfBlock
	for _, name := range names {
		if name == "versions.tf" {
			continue // provider wiring, rewritten per slice
		}
		src, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // a generated fixture
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		f, diags := hclsyntax.ParseConfig(src, name, hcl.Pos{Line: 1, Column: 1})
		if diags.HasErrors() {
			t.Fatalf("parsing %s: %s", name, diags.Error())
		}
		body, ok := f.Body.(*hclsyntax.Body)
		if !ok {
			t.Fatalf("%s: body is not hclsyntax", name)
		}
		for _, b := range body.Blocks {
			start := b.TypeRange.Start.Byte
			end := b.Body.SrcRange.End.Byte
			text := string(src[start:end])
			blk := tfBlock{File: name, Kind: b.Type, Src: text}
			switch b.Type {
			case "resource":
				blk.Addr = b.Labels[0] + "." + b.Labels[1]
			case "module":
				blk.Addr = "module." + b.Labels[0]
			}
			// References read off the block's own text rather than off the
			// HCL AST's traversals: the text is what the slice writer has
			// to rewrite, so reading both from the same source keeps the
			// two from disagreeing. The pattern only matches a
			// three-segment aws_*.name.attr traversal, which is exactly the
			// shape a cross-block reference takes here.
			seen := map[string]bool{}
			for _, m := range refPattern.FindAllString(text, -1) {
				target, _ := refTarget(m)
				if target == blk.Addr {
					continue // a self-reference inside a count/for_each body
				}
				if !seen[m] {
					seen[m] = true
					blk.Refs = append(blk.Refs, m)
				}
			}
			out = append(out, blk)
		}
	}
	return out
}

// group is a set of blocks that move together.
type group struct {
	Name  string
	Addrs []string
}

// groupBlocks partitions the root module's resource and module blocks into
// groups. "component" takes the weakly-connected components of the
// reference graph, which is the only partition that needs no cross-slice
// conversion at all; "layer" groups by the generator's own file, which is
// how a human would actually cut it and which does cut a component.
func groupBlocks(blocks []tfBlock, mode string) []group {
	addrOf := map[string]tfBlock{}
	var order []string
	for _, b := range blocks {
		if b.Addr == "" {
			continue
		}
		addrOf[b.Addr] = b
		order = append(order, b.Addr)
	}

	if mode == "layer" {
		byFile := map[string][]string{}
		var files []string
		for _, a := range order {
			f := addrOf[a].File
			if _, ok := byFile[f]; !ok {
				files = append(files, f)
			}
			byFile[f] = append(byFile[f], a)
		}
		sort.Strings(files)
		out := make([]group, 0, len(files))
		for _, f := range files {
			out = append(out, group{Name: strings.TrimSuffix(f, ".tf"), Addrs: byFile[f]})
		}
		return out
	}

	parent := map[string]string{}
	var find func(string) string
	find = func(a string) string {
		if parent[a] == "" || parent[a] == a {
			parent[a] = a
			return a
		}
		r := find(parent[a])
		parent[a] = r
		return r
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}
	for _, a := range order {
		find(a)
	}
	for _, a := range order {
		for _, ref := range addrOf[a].Refs {
			target, _ := refTarget(ref)
			if _, ok := addrOf[target]; ok {
				union(a, target)
			}
		}
	}
	byRoot := map[string][]string{}
	var roots []string
	for _, a := range order {
		r := find(a)
		if _, ok := byRoot[r]; !ok {
			roots = append(roots, r)
		}
		byRoot[r] = append(byRoot[r], a)
	}
	out := make([]group, 0, len(roots))
	for _, r := range roots {
		out = append(out, group{Name: r, Addrs: byRoot[r]})
	}
	return out
}

type sliceAssign struct {
	Groups []group
	Addrs  map[string]bool
}

func (s sliceAssign) groupNames() []string {
	out := make([]string, 0, len(s.Groups))
	for _, g := range s.Groups {
		out = append(out, g.Name)
	}
	return out
}

// distribute spreads groups over k slices largest-first, so the slices are
// as even as the estate's own coupling allows.
func distribute(groups []group, k int, st *stateFile) []sliceAssign {
	if k < 1 {
		k = 1
	}
	weight := func(g group) int {
		n := 0
		for _, a := range g.Addrs {
			n += st.instancesOf(a)
		}
		return n
	}
	sorted := append([]group(nil), groups...)
	sort.SliceStable(sorted, func(i, j int) bool { return weight(sorted[i]) > weight(sorted[j]) })

	out := make([]sliceAssign, k)
	for i := range out {
		out[i].Addrs = map[string]bool{}
	}
	load := make([]int, k)
	for _, g := range sorted {
		best := 0
		for i := 1; i < k; i++ {
			if load[i] < load[best] {
				best = i
			}
		}
		out[best].Groups = append(out[best].Groups, g)
		load[best] += weight(g)
		for _, a := range g.Addrs {
			out[best].Addrs[a] = true
		}
	}
	// Drop empties: k larger than the component count is not an error, it
	// just means the estate does not decompose that far.
	var kept []sliceAssign
	for _, s := range out {
		if len(s.Groups) > 0 {
			kept = append(kept, s)
		}
	}
	return kept
}

func crossRefsFor(blocks []tfBlock, assignment []sliceAssign) []crossRef {
	owner := map[string]int{}
	for i, s := range assignment {
		for a := range s.Addrs {
			owner[a] = i
		}
	}
	var out []crossRef
	for _, b := range blocks {
		if b.Addr == "" {
			continue
		}
		mine, ok := owner[b.Addr]
		if !ok {
			continue
		}
		for _, ref := range b.Refs {
			target, _ := refTarget(ref)
			if o, ok := owner[target]; ok && o != mine {
				out = append(out, crossRef{
					FromSlice: fmt.Sprintf("s%d", mine),
					FromBlock: b.Addr,
					Ref:       ref,
					Became:    "hardcoded literal read from the whole-estate state",
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FromBlock != out[j].FromBlock {
			return out[i].FromBlock < out[j].FromBlock
		}
		return out[i].Ref < out[j].Ref
	})
	return out
}

// writeSlice materializes one slice as a runnable root module. estate is
// empty for the stock copy and names the estate for the choudoufu copy,
// which also gets a live block with a record store (so guided discovery is
// eligible - see internal/command/statelessApplyGuidedDiscovery).
func writeSlice(t *testing.T, base, dir string, blocks []tfBlock, sl sliceAssign, st *stateFile, estate string) []crossRef {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	copyTree(t, filepath.Join(base, "modules"), filepath.Join(dir, "modules"))

	var b strings.Builder
	b.WriteString("# sliced by internal/live/discovery/slicing_bench_test.go (issue #584).\n\n")
	var refs []crossRef
	for _, blk := range blocks {
		switch {
		case blk.Addr == "":
			// locals and anything else shared: every slice keeps it. An
			// unreferenced local costs nothing and keeps the slice's own
			// blocks byte-identical to the whole estate's.
			b.WriteString(blk.Src)
			b.WriteString("\n\n")
		case sl.Addrs[blk.Addr]:
			src := blk.Src
			for _, ref := range blk.Refs {
				target, attr := refTarget(ref)
				if sl.Addrs[target] {
					continue
				}
				if strings.Contains(ref, "[") {
					// A cross-slice reference into an EXPANDED resource
					// cannot be flattened to one literal, and the
					// component partition never produces one (an indexed
					// reference is a reference, so both ends land in the
					// same component). Refuse rather than emit a slice
					// whose configuration is silently wrong.
					t.Fatalf("cross-slice reference %s carries an index expression; this partition cannot be materialized", ref)
				}
				lit, ok := st.attr(target, attr)
				if !ok {
					t.Fatalf("slice needs %s but the whole-estate state has no such attribute", ref)
				}
				src = strings.ReplaceAll(src, ref, strconv.Quote(lit))
				refs = append(refs, crossRef{FromBlock: blk.Addr, Ref: ref, Became: strconv.Quote(lit)})
			}
			b.WriteString(src)
			b.WriteString("\n\n")
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "sliced.tf"), []byte(b.String()), 0o644); err != nil { //nolint:gosec // test artifact
		t.Fatalf("writing sliced.tf: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "versions.tf"), []byte(versionsFor(t, base, estate)), 0o644); err != nil { //nolint:gosec // test artifact
		t.Fatalf("writing versions.tf: %v", err)
	}
	return refs
}

// versionsFor is one slice's provider wiring: terralith-gen's own versions.tf,
// read from the generated estate, with a live block spliced into its terraform
// block when estate names one.
//
// It used to be a second copy of the provider block, written out here rather
// than read, and that cost this document its headline numbers. The copy still
// set skip_requesting_account_id long after the generator's own template
// dropped it (issue #628, fixed in #633), so every CLI plan this bench timed
// was resolving ECS identities against a provider with no account id: the plan
// exited 1 on "Live resource listed but not importable", and its call count -
// 744 at k=1 - was a refused plan's cost quoted as a clean one (issue #634).
// The three other benches in this package read the generator's output and were
// fixed by #633 alone; this one was not, because it did not read anything.
//
// So: read, never re-declare. A slice's provider configuration differs from
// the whole estate's in exactly one respect, the live block, and that is the
// only thing this function adds.
func versionsFor(t *testing.T, base, estate string) string {
	t.Helper()

	path := filepath.Join(base, "versions.tf")
	data, err := os.ReadFile(path) //nolint:gosec // a path terralith-gen just wrote
	if err != nil {
		t.Fatalf("reading the generated %s: %v", path, err)
	}
	src := string(data)
	if estate == "" {
		return src
	}
	// The generator's own required_version line. A missing anchor fails loudly
	// rather than producing a choudoufu slice that is silently still stateful,
	// which would make the migrated column measure the stock one over again.
	const anchor = `required_version = ">= 1.5.0"`
	if !strings.Contains(src, anchor) {
		t.Fatalf("%s does not contain the expected anchor %q; terralith-gen's versions.tf template changed", path, anchor)
	}
	block := anchor + fmt.Sprintf("\n\n  live {\n    estate = %q\n\n    record_store \"local\" {\n      path = \".tofu-records\"\n    }\n  }", estate)
	return strings.Replace(src, anchor, block, 1)
}

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

type stateFile struct {
	top       map[string]json.RawMessage
	resources []json.RawMessage
	headers   []stateResourceHeader
}

type stateResourceHeader struct {
	Module    string `json:"module"`
	Mode      string `json:"mode"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	Instances []struct {
		Attributes map[string]any `json:"attributes"`
	} `json:"instances"`
}

var moduleCallPattern = regexp.MustCompile(`^module\.([A-Za-z_][A-Za-z0-9_-]*)`)

func (h stateResourceHeader) group() string {
	if h.Module != "" {
		if m := moduleCallPattern.FindStringSubmatch(h.Module); m != nil {
			return "module." + m[1]
		}
	}
	return h.Type + "." + h.Name
}

func readState(t *testing.T, path string) *stateFile {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // a path this test wrote
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(top["resources"], &raw); err != nil {
		t.Fatalf("parsing %s resources: %v", path, err)
	}
	st := &stateFile{top: top, resources: raw}
	for _, r := range raw {
		var h stateResourceHeader
		if err := json.Unmarshal(r, &h); err != nil {
			t.Fatalf("parsing a state resource: %v", err)
		}
		st.headers = append(st.headers, h)
	}
	return st
}

func (s *stateFile) instanceCount() int {
	n := 0
	for _, h := range s.headers {
		n += len(h.Instances)
	}
	return n
}

func (s *stateFile) instancesOf(addr string) int {
	n := 0
	for _, h := range s.headers {
		if h.group() == addr {
			n += len(h.Instances)
		}
	}
	return n
}

func (s *stateFile) attr(addr, name string) (string, bool) {
	for _, h := range s.headers {
		if h.Module != "" || h.Type+"."+h.Name != addr {
			continue
		}
		if len(h.Instances) == 0 {
			return "", false
		}
		v, ok := h.Instances[0].Attributes[name]
		if !ok {
			return "", false
		}
		str, ok := v.(string)
		return str, ok
	}
	return "", false
}

func (s *stateFile) subset(sl sliceAssign) *stateFile {
	out := &stateFile{top: map[string]json.RawMessage{}}
	for k, v := range s.top {
		out.top[k] = v
	}
	for i, h := range s.headers {
		if sl.Addrs[h.group()] {
			out.resources = append(out.resources, s.resources[i])
			out.headers = append(out.headers, h)
		}
	}
	return out
}

func writeState(t *testing.T, path string, s *stateFile) {
	t.Helper()

	res := s.resources
	if res == nil {
		res = []json.RawMessage{}
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshalling resources: %v", err)
	}
	top := map[string]json.RawMessage{}
	for k, v := range s.top {
		top[k] = v
	}
	top["resources"] = raw
	data, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		t.Fatalf("marshalling state: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil { //nolint:gosec // test artifact
		t.Fatalf("writing %s: %v", path, err)
	}
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

func envInt(t *testing.T, name string, def int) int {
	t.Helper()

	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		t.Fatalf("%s=%q is not a positive integer", name, v)
	}
	return n
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()

	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("stat %s: %v", src, err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", src)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dst, err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("reading %s: %v", src, err)
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			copyTree(t, s, d)
			continue
		}
		data, err := os.ReadFile(s) //nolint:gosec // generated fixture
		if err != nil {
			t.Fatalf("reading %s: %v", s, err)
		}
		if err := os.WriteFile(d, data, 0o644); err != nil { //nolint:gosec // test artifact
			t.Fatalf("writing %s: %v", d, err)
		}
	}
}

func runCapture(t *testing.T, dir string, extraEnv []string, name string, args ...string) (string, int) {
	t.Helper()

	cmd := exec.Command(name, args...) //nolint:gosec // fixed binaries, test-only
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if ok := asExitError(err, &exit); ok {
			code = exit.ExitCode()
		} else {
			t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
		}
	}
	return string(out), code
}

func asExitError(err error, target **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError) //nolint:errorlint // exact type is what is wanted
	if ok {
		*target = e
	}
	return ok
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// proposedAddrs pulls the "# aws_x.y will be created" headers out of a
// rendered plan.
func proposedAddrs(out string) []string {
	var addrs []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") && strings.Contains(line, " will be ") {
			addrs = append(addrs, strings.TrimPrefix(line, "# "))
		}
	}
	return addrs
}

func grepLine(s, needle string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, needle) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}
