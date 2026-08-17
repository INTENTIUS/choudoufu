// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configload"
	"github.com/intentius/choudoufu/internal/getmodules"
	"github.com/intentius/choudoufu/internal/httpclient"
	"github.com/intentius/choudoufu/internal/initwd"
	"github.com/intentius/choudoufu/internal/live/check"
	"github.com/intentius/choudoufu/internal/modsdir"
	"github.com/intentius/choudoufu/internal/registry"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// logf writes progress when a log destination was given.
func logf(out *os.File, format string, args ...any) {
	if out == nil {
		return
	}
	fmt.Fprintf(out, format, args...)
}

// # Why corpus-fetch installs modules at all (#254)
//
// internal/live/check.Load resolves a non-local module source through
// .terraform/modules, exactly as a real user's directory has it after
// "init". Nothing in this pipeline ever created that directory, so 58 of
// the corpus's 250 entries were measured with their modules missing: every
// resource inside a registry module was invisible to the ranking, and two
// refusal classes read as exactly zero because the code that would have
// tripped them was never loaded.
//
// The argument for leaving it alone was that a registry module is
// third-party code the estate does not own, so refusing to look inside it
// is the product's real behaviour. It is not. Load reads
// .terraform/modules whenever it is there and a user reaches live-plan
// only after init, so the uninstalled corpus was unrepresentative of the
// estates it exists to represent rather than conservative about them.
//
// # Why this fork's own installer, rather than "terraform get"
//
// Two reasons, one of them a measurement bias. Stock Terraform applies the
// configuration's required_version gate before it will install anything,
// and six corpus entries pin ranges no released Terraform binary satisfies
// - so a "terraform get" corpus would silently exclude exactly the entries
// whose authors pinned hardest, two of them currently counted as clean.
// This fork applies no core-version gate anywhere (there is no
// CheckCoreVersionRequirements call in the tree), so installing in-process
// measures every entry. The second reason is that it removes an external
// binary, and its version, from the reproducibility surface.
type installOptions struct {
	// Root is the repository root that manifest paths resolve against.
	Root string

	// PinsPath is the checked-in module lock, see [modulePins].
	PinsPath string

	// RemoteModules keeps go-getter module sources ("github.com/org/repo",
	// "git::https://...") in the installed manifest instead of dropping
	// them.
	//
	// It defaults off, and the default is the whole reproducibility
	// argument. 133 of this corpus's 134 go-getter module calls carry no
	// "?ref=", so go-getter clones a branch head: the corpus would then
	// float on six third-party repositories' default branches with nothing
	// to pin them to and no way to tell, which is the drift
	// live/corpus-manifest.json's commit pins exist to prevent. Registry
	// sources have no such problem - see [modulePins] - so they are always
	// installed.
	//
	// Turning this on measures a larger surface at the cost of a corpus
	// that can move under an unchanged tree. A run that used it says so.
	RemoteModules bool

	// Log receives progress, or nil for silence.
	Log *os.File
}

// installSummary is what one install pass did, for the caller to print and
// for a person to check a claim against.
type installSummary struct {
	Entries          int
	EntriesWithCalls int
	Installed        int
	Dropped          int
	Failed           int
	Packages         int
	NewlyPinned      []string
	FloatingPackages []string
	DroppedSources   map[string]int
	Errors           []string
}

// installModules installs every corpus entry's module tree into that
// entry's own .terraform/modules, the directory check.Load already reads.
func installModules(ctx context.Context, manifest check.Manifest, opts installOptions) (installSummary, error) {
	var summary installSummary
	summary.DroppedSources = map[string]int{}

	entries, err := manifest.Resolve(opts.Root)
	if err != nil {
		return summary, err
	}
	summary.Entries = len(entries)

	// Absolute before the first chdir: installOne moves the process into
	// each entry, so anything still relative here would be read or written
	// somewhere inside the corpus.
	if opts.PinsPath, err = filepath.Abs(opts.PinsPath); err != nil {
		return summary, err
	}

	pins, err := loadModulePins(opts.PinsPath)
	if err != nil {
		return summary, err
	}

	// One registry client and one package fetcher for the whole run. The
	// fetcher remembers packages it has already retrieved and copies them
	// locally on a repeat, which matters here: 117 of the corpus's module
	// calls name the same repository, and 21 registry packages are shared
	// across dozens of entries.
	httpClient := httpclient.NewForRegistryRequests(ctx, 2, 30*time.Second)
	interceptor := &pinnedVersions{
		base: httpClient.HTTPClient.Transport,
		pins: pins,
		seen: map[string]bool{},
	}
	if interceptor.base == nil {
		interceptor.base = http.DefaultTransport
	}
	httpClient.HTTPClient.Transport = interceptor
	reg := registry.NewClient(ctx, nil, httpClient)
	fetcher := getmodules.NewPackageFetcher(ctx, nil)

	resolved := map[string]map[string]bool{}

	for _, entry := range entries {
		logf(opts.Log, "corpus-fetch: modules %s\n", entry.Name)
		res, err := installOne(ctx, entry, reg, fetcher, opts)
		if err != nil {
			summary.Failed++
			summary.Errors = append(summary.Errors, fmt.Sprintf("%s: %v", entry.Name, err))
			continue
		}
		if res.calls > 0 {
			summary.EntriesWithCalls++
		}
		summary.Installed += res.installed
		summary.Dropped += res.dropped
		for source, n := range res.droppedSources {
			summary.DroppedSources[source] += n
		}
		for key, versions := range res.registryVersions {
			if resolved[key] == nil {
				resolved[key] = map[string]bool{}
			}
			for v := range versions {
				resolved[key][v] = true
			}
		}
		if len(res.errors) > 0 {
			summary.Errors = append(summary.Errors, fmt.Sprintf("%s: %s", entry.Name, strings.Join(res.errors, "; ")))
		}
	}

	// Fold what this run resolved back into the lock. An existing entry is
	// never rewritten, only extended, so a package already frozen stays
	// frozen and re-locking one means deleting its entry by hand - the same
	// deliberateness live/corpus-provider-pins.json already requires.
	for key, versions := range resolved {
		before := len(pins.Packages[key])
		have := map[string]bool{}
		for _, v := range pins.Packages[key] {
			have[v] = true
		}
		for v := range versions {
			if !have[v] {
				pins.Packages[key] = append(pins.Packages[key], v)
			}
		}
		sort.Strings(pins.Packages[key])
		if before == 0 {
			summary.NewlyPinned = append(summary.NewlyPinned, key)
		}
	}
	sort.Strings(summary.NewlyPinned)
	summary.Packages = len(pins.Packages)

	for key := range interceptor.seen {
		if len(pins.Packages[key]) == 0 {
			summary.FloatingPackages = append(summary.FloatingPackages, key)
		}
	}
	sort.Strings(summary.FloatingPackages)

	if err := writeModulePins(opts.PinsPath, pins); err != nil {
		return summary, err
	}
	return summary, nil
}

type entryResult struct {
	calls            int
	installed        int
	dropped          int
	droppedSources   map[string]int
	registryVersions map[string]map[string]bool
	errors           []string
}

func installOne(ctx context.Context, entry check.CorpusEntryRef, reg *registry.Client, fetcher *getmodules.PackageFetcher, opts installOptions) (entryResult, error) {
	res := entryResult{
		droppedSources:   map[string]int{},
		registryVersions: map[string]map[string]bool{},
	}

	// Two different path spellings, on purpose, because the installer and
	// the corpus want opposite things.
	//
	// internal/initwd stats a recorded Dir with no root joined onto it and
	// no working directory of its own, so a record it can re-use has to be
	// absolute here - stock "init" gets away with relative records only
	// because it runs with the config directory as its working directory,
	// which a program walking 250 of them cannot do without also giving up
	// the package fetcher's cross-entry cache (it remembers where it put a
	// package, and a relative memory read from the next entry is a
	// download failure, not a miss).
	//
	// What lands in modules.json is relative, though, because .corpus gets
	// copied aside for with/without measurements and absolute records would
	// make the copy read the original tree's modules. So the snapshot is
	// absolutized on the way in and relativized on the way out, and
	// [postProcess] owns the second half.
	entryDir, err := filepath.Abs(entry.Dir)
	if err != nil {
		return res, err
	}
	modsDir := filepath.Join(entryDir, ".terraform", "modules")
	if err := os.MkdirAll(modsDir, 0o755); err != nil { //nolint:gosec // a cache directory, mirroring what init creates
		return res, err
	}
	if err := absolutizeSnapshot(modsDir, entryDir); err != nil {
		return res, err
	}

	loader, err := configload.NewLoader(&configload.Config{ModulesDir: modsDir})
	if err != nil {
		return res, err
	}

	inst := initwd.NewModuleInstaller(modsDir, loader, reg, fetcher)
	call := configs.NewStaticModuleCall(addrs.RootModule, hcl.Range{}, corpusVariables, entryDir, "default")

	// installErrsOnly: a corpus configuration that does not build is an
	// ordinary outcome here (check.Load reports the same thing as a
	// measurement), and it must not stop this entry's modules being
	// installed or the next entry being reached.
	_, diags := inst.InstallModules(ctx, entryDir, "", false, true, nil, call)
	for _, d := range diags {
		if d.Severity() == tfdiags.Error {
			res.errors = append(res.errors, d.Description().Summary)
		}
	}
	if len(res.errors) > 4 {
		res.errors = append(res.errors[:4], fmt.Sprintf("and %d more", len(res.errors)-4))
	}

	if err := stripVCSDirs(modsDir); err != nil {
		return res, err
	}

	installed, err := modsdir.ReadManifestSnapshotForDir(modsDir)
	if err != nil {
		return res, err
	}
	pruned, dropped, droppedSources, registryVersions := postProcess(installed, entryDir, opts.RemoteModules)
	// The root's own record is not a module call, and a snapshot that was
	// never written has no root record either - hence the floor rather than
	// a bare subtraction.
	res.calls = max(0, len(installed)-1)
	res.installed = max(0, len(pruned)-1)
	res.dropped = dropped
	res.droppedSources = droppedSources
	res.registryVersions = registryVersions

	if err := pruned.WriteSnapshotToDir(modsDir); err != nil {
		return res, err
	}
	return res, nil
}

// postProcess rewrites what the installer recorded into what this corpus
// needs to be measurable and portable:
//
//   - Every Dir is made relative to the entry directory. The installer
//     records whatever root path it was handed, which here is absolute;
//     check.Load joins a relative Dir against the entry it is loading, so
//     relative records keep .corpus copyable to another path (the way a
//     with/without measurement has to copy it) instead of silently
//     resolving back to the original tree.
//   - A go-getter record is dropped unless the caller asked to keep it,
//     along with everything installed underneath it, which leaves those
//     calls exactly as unresolved as they were before this existed. See
//     [installOptions.RemoteModules].
//
// It also collects the (registry package, version) pairs the surviving
// records resolved to, which is what the lock freezes.
func postProcess(installed modsdir.Manifest, entryDir string, keepRemote bool) (modsdir.Manifest, int, map[string]int, map[string]map[string]bool) {
	out := make(modsdir.Manifest, len(installed))
	droppedSources := map[string]int{}
	registryVersions := map[string]map[string]bool{}

	dropPrefixes := []string{}
	if !keepRemote {
		keys := make([]string, 0, len(installed))
		for key := range installed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if key == "" {
				continue
			}
			if sourceKind(installed[key].SourceAddr) == sourceGoGetter {
				dropPrefixes = append(dropPrefixes, key)
			}
		}
	}

	dropped := 0
	for key, record := range installed {
		if key != "" && isUnder(key, dropPrefixes) {
			dropped++
			droppedSources[installed[key].SourceAddr]++
			continue
		}
		if record.Dir != "" && filepath.IsAbs(record.Dir) {
			if rel, err := filepath.Rel(entryDir, record.Dir); err == nil {
				record.Dir = filepath.ToSlash(rel)
			}
		}
		out[key] = record

		if key == "" {
			continue
		}
		if addr, err := addrs.ParseModuleSource(record.SourceAddr); err == nil {
			if regAddr, ok := addr.(addrs.ModuleSourceRegistry); ok && record.Version != nil {
				k := packageKey(regAddr.Package.Namespace, regAddr.Package.Name, regAddr.Package.TargetSystem)
				if registryVersions[k] == nil {
					registryVersions[k] = map[string]bool{}
				}
				registryVersions[k][record.Version.String()] = true
			}
		}
	}
	return out, dropped, droppedSources, registryVersions
}

// absolutizeSnapshot rewrites an already-written modules.json so the
// installer can stat what a previous run recorded. See [installOne] for why
// the file on disk holds relative paths that this has to undo first: without
// it every run reinstalls everything, which is correct but pays the whole
// download again and hides a genuine install failure among the noise.
func absolutizeSnapshot(modsDir, entryDir string) error {
	snapshot, err := modsdir.ReadManifestSnapshotForDir(modsDir)
	if err != nil {
		return err
	}
	if len(snapshot) == 0 {
		return nil
	}
	for key, record := range snapshot {
		if record.Dir != "" && !filepath.IsAbs(record.Dir) {
			record.Dir = filepath.Join(entryDir, record.Dir)
			snapshot[key] = record
		}
	}
	return snapshot.WriteSnapshotToDir(modsDir)
}

// stripVCSDirs removes the .git directory go-getter leaves behind in every
// package it clones - which is every remote package, because the OpenTofu
// registry answers a module download with a "git::https://...?ref=<commit>"
// location rather than an archive.
//
// It walks only modsDir, never the entry directory, because a local module
// record points back into the checked-out corpus source tree and removing
// .git there would destroy the pin corpus-fetch just verified.
func stripVCSDirs(modsDir string) error {
	return filepath.WalkDir(modsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() || d.Name() != ".git" {
			return nil
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		return filepath.SkipDir
	})
}

func isUnder(key string, prefixes []string) bool {
	for _, p := range prefixes {
		if key == p || strings.HasPrefix(key, p+".") {
			return true
		}
	}
	return false
}

type moduleSourceKind int

const (
	sourceLocal moduleSourceKind = iota
	sourceRegistry
	sourceGoGetter
	sourceUnparseable
)

// sourceKind classifies a recorded source address through the same parser
// the installer used to produce it, rather than by matching prefixes: which
// of the three installation paths a source takes is addrs.ParseModuleSource's
// answer, not this file's.
func sourceKind(source string) moduleSourceKind {
	addr, err := addrs.ParseModuleSource(source)
	if err != nil {
		return sourceUnparseable
	}
	switch addr.(type) {
	case addrs.ModuleSourceLocal:
		return sourceLocal
	case addrs.ModuleSourceRegistry:
		return sourceRegistry
	default:
		return sourceGoGetter
	}
}

// corpusVariables answers the installer's static-evaluation questions about
// root input variables the same way internal/live/check does: a declared
// default when there is one, and an unknown of the declared type otherwise.
// Unknown rather than a placeholder, because a placeholder would make an
// expression look statically evaluable that is not - and because a module
// source address has to be a literal anyway, so this affects only how far
// the installer can see, never which version it picks.
func corpusVariables(v *configs.Variable) (cty.Value, hcl.Diagnostics) {
	if v.Default != cty.NilVal {
		return v.Default, nil
	}
	if v.Type == cty.NilType {
		return cty.DynamicVal, nil
	}
	return cty.UnknownVal(v.Type), nil
}
