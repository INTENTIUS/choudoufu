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

	// ModuleSources are the pinned go-getter repositories from
	// live/corpus-manifest.json, mirrored locally so those calls install
	// reproducibly. See tools/corpus-fetch/gitmirror.go.
	ModuleSources []moduleSourcePin

	// MirrorDir is where those mirrors are built, relative to Root.
	MirrorDir string

	// RemoteModules keeps EVERY go-getter module record in the installed
	// manifest, including one whose repository no module_sources entry
	// pins.
	//
	// It defaults off, and the default is the reproducibility argument.
	// A pinned repository is installed from its local mirror and kept
	// either way - that is what gitmirror.go exists for. What this flag
	// admits is the rest: a go-getter source with no pin behind it clones
	// a branch head over the network, so the corpus would float on
	// somebody's default branch with no way to tell. Dropping those
	// records leaves such calls exactly as unresolved as they were before
	// module installation existed, and [installSummary.UnpinnedPackages]
	// names them so the fix is to add the pin.
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

	// Mirrors is how many pinned go-getter repositories were served from a
	// local mirror, and MirrorErrors names any that could not be built.
	// A failed mirror still has its rewrite registered, so its calls fail
	// loudly per entry rather than silently reaching the network.
	Mirrors      int
	MirrorErrors []string

	// UnpinnedPackages are the go-getter clone URLs a module record
	// resolved to that no module_sources entry pins. Each is a repository
	// this corpus reaches over the network at whatever its branch head is
	// today, so each is a reason a number computed here may not reproduce.
	UnpinnedPackages []string
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

	// Mirrors before the rewrite is installed, because building one clones
	// from the very URL the rewrite redirects.
	mirrorBase, err := filepath.Abs(underRoot(opts.Root, opts.MirrorDir))
	if err != nil {
		return summary, err
	}
	mirrors := prepareMirrors(ctx, opts.ModuleSources, mirrorBase, &pins, opts.Log)
	mirrored := map[string]bool{}
	rebuilt := map[string]bool{}
	for _, m := range mirrors {
		if m.Rebuilt {
			rebuilt[m.Pin.Repo] = true
		}
		if m.Err != nil {
			summary.MirrorErrors = append(summary.MirrorErrors, fmt.Sprintf("%s: %v", m.Pin.Repo, m.Err))
			continue
		}
		summary.Mirrors++
		mirrored[m.Pin.Repo] = true
	}
	sort.Strings(summary.MirrorErrors)
	defer withEnv(gitRewriteEnv(os.Getenv, mirrors))()

	resolved := map[string]map[string]bool{}
	unpinned := map[string]bool{}

	for _, entry := range entries {
		logf(opts.Log, "corpus-fetch: modules %s\n", entry.Name)
		res, err := installOne(ctx, entry, reg, fetcher, mirrored, rebuilt, opts)
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
		for _, repo := range res.unpinnedGit {
			unpinned[repo] = true
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

	for repo := range unpinned {
		summary.UnpinnedPackages = append(summary.UnpinnedPackages, repo)
	}
	sort.Strings(summary.UnpinnedPackages)

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
	unpinnedGit      []string
	errors           []string
}

func installOne(ctx context.Context, entry check.CorpusEntryRef, reg *registry.Client, fetcher *getmodules.PackageFetcher, mirrored, rebuilt map[string]bool, opts installOptions) (entryResult, error) {
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
	if err := absolutizeSnapshot(modsDir, entryDir, rebuilt); err != nil {
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
	out := postProcess(installed, entryDir, mirrored, opts.RemoteModules)
	// The root's own record is not a module call, and a snapshot that was
	// never written has no root record either - hence the floor rather than
	// a bare subtraction.
	res.calls = max(0, len(installed)-1)
	res.installed = max(0, len(out.manifest)-1)
	res.dropped = out.dropped
	res.droppedSources = out.droppedSources
	res.registryVersions = out.registryVersions
	res.unpinnedGit = out.unpinnedGit
	pruned := out.manifest

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
//   - A go-getter record whose repository no module_sources entry pins is
//     dropped unless the caller asked to keep it, along with everything
//     installed underneath it, which leaves those calls exactly as
//     unresolved as they were before this existed. See
//     [installOptions.RemoteModules]. A PINNED go-getter record is kept:
//     it came from a local mirror at a frozen commit, so it is as
//     reproducible as a registry record.
//
// It also collects the (registry package, version) pairs the surviving
// records resolved to, which is what the lock freezes, and the clone URLs of
// any unpinned go-getter record it dropped, which is what tells a maintainer
// which repository to pin next.
type postProcessed struct {
	manifest         modsdir.Manifest
	dropped          int
	droppedSources   map[string]int
	registryVersions map[string]map[string]bool
	unpinnedGit      []string
}

func postProcess(installed modsdir.Manifest, entryDir string, mirrored map[string]bool, keepRemote bool) postProcessed {
	out := postProcessed{
		manifest:         make(modsdir.Manifest, len(installed)),
		droppedSources:   map[string]int{},
		registryVersions: map[string]map[string]bool{},
	}

	keys := make([]string, 0, len(installed))
	for key := range installed {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	dropPrefixes := []string{}
	seenUnpinned := map[string]bool{}
	for _, key := range keys {
		if key == "" || sourceKind(installed[key].SourceAddr) != sourceGoGetter {
			continue
		}
		repo := gitCloneURL(installed[key].SourceAddr)
		if mirrored[repo] {
			continue
		}
		if repo != "" && !seenUnpinned[repo] {
			seenUnpinned[repo] = true
			out.unpinnedGit = append(out.unpinnedGit, repo)
		}
		if !keepRemote {
			dropPrefixes = append(dropPrefixes, key)
		}
	}

	for key, record := range installed {
		if key != "" && isUnder(key, dropPrefixes) {
			out.dropped++
			out.droppedSources[installed[key].SourceAddr]++
			continue
		}
		if record.Dir != "" && filepath.IsAbs(record.Dir) {
			if rel, err := filepath.Rel(entryDir, record.Dir); err == nil {
				record.Dir = filepath.ToSlash(rel)
			}
		}
		out.manifest[key] = record

		if key == "" {
			continue
		}
		if addr, err := addrs.ParseModuleSource(record.SourceAddr); err == nil {
			if regAddr, ok := addr.(addrs.ModuleSourceRegistry); ok && record.Version != nil {
				k := packageKey(regAddr.Package.Namespace, regAddr.Package.Name, regAddr.Package.TargetSystem)
				if out.registryVersions[k] == nil {
					out.registryVersions[k] = map[string]bool{}
				}
				out.registryVersions[k][record.Version.String()] = true
			}
		}
	}
	return out
}

// absolutizeSnapshot rewrites an already-written modules.json so the
// installer can stat what a previous run recorded. See [installOne] for why
// the file on disk holds relative paths that this has to undo first: without
// it every run reinstalls everything, which is correct but pays the whole
// download again and hides a genuine install failure among the noise.
//
// It also evicts what a changed pin invalidated. A module record carries the
// source address and, for a registry module, the resolved version - but a
// go-getter record carries neither a version nor a commit, so editing
// live/corpus-manifest.json's commit for a mirrored repository changes
// nothing the reuse check can see. Measured before this eviction existed:
// the mirror moved to the new commit and the installed tree stayed on the
// old one, silently, which is the exact failure the pins are here to
// prevent. A repository whose mirror was rebuilt this run therefore has its
// records dropped and its installed directories removed, so the entry
// reinstalls from the mirror that is actually there now.
func absolutizeSnapshot(modsDir, entryDir string, rebuilt map[string]bool) error {
	snapshot, err := modsdir.ReadManifestSnapshotForDir(modsDir)
	if err != nil {
		return err
	}
	if len(snapshot) == 0 {
		return nil
	}

	keys := make([]string, 0, len(snapshot))
	for key := range snapshot {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var stalePrefixes []string
	for _, key := range keys {
		if key == "" {
			continue
		}
		if repo := gitCloneURL(snapshot[key].SourceAddr); repo != "" && rebuilt[repo] {
			stalePrefixes = append(stalePrefixes, key)
		}
	}

	for key, record := range snapshot {
		if record.Dir != "" && !filepath.IsAbs(record.Dir) {
			record.Dir = filepath.Join(entryDir, record.Dir)
			snapshot[key] = record
		}
		if key == "" || !isUnder(key, stalePrefixes) {
			continue
		}
		// Only ever under .terraform/modules. A local module's record
		// points back into the checked-out corpus source tree, and
		// removing that would destroy the pin corpus-fetch just verified -
		// the same rule stripVCSDirs holds.
		if dir := snapshot[key].Dir; dir != "" && isWithin(modsDir, dir) {
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
		}
		delete(snapshot, key)
	}
	return snapshot.WriteSnapshotToDir(modsDir)
}

// isWithin reports whether path sits inside root.
func isWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
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
