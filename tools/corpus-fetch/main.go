// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Command corpus-fetch materializes the external configurations
// live/corpus-manifest.json pins, so that tools/corpus-gen can measure them.
//
// It is the acquisition half of GitHub issue #102's corpus. The measurement
// half needs third-party configurations to be worth anything - a ranking over
// this project's own fixtures measures the fixtures - and this is what puts
// them on disk without checking several hundred files of somebody else's
// Terraform into this repository.
//
// # Why pinned commits, and why both a tag and a commit
//
// The artifact has to regenerate byte-identically, which is only true if the
// input cannot move. Each source therefore records both the tag a human
// recognizes and the commit that tag must resolve to; this checks out the
// tag and then verifies the commit. A tag that has been repointed makes the
// fetch fail rather than quietly changing every number in the artifact.
//
// The fetched trees are not committed. They are reproducible from the pins,
// and vendoring them would add weight and third-party licenses for no
// measurement value.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/check"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "corpus-fetch: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		manifestPath  = flag.String("manifest", "live/corpus-manifest.json", "corpus manifest to read")
		root          = flag.String("root", ".", "repository root that relative paths are resolved against")
		force         = flag.Bool("force", false, "re-clone even when the pinned commit is already checked out")
		modules       = flag.Bool("modules", true, "install each entry's registry modules into its .terraform/modules, the directory internal/live/check.Load reads (#254)")
		modulePinPath = flag.String("module-pins", "live/corpus-module-pins.json", "checked-in module lock: the frozen registry version listing a ranged version constraint resolves against, so an install does not float with whatever a module author published today")
		mirrorDir     = flag.String("module-mirrors", ".corpus/_modules", "where the manifest's pinned go-getter module sources are mirrored, so those calls install from a frozen commit instead of a branch head")
		remoteModules = flag.Bool("remote-modules", false, "also keep go-getter module records whose repository the manifest does not pin. Those clone a branch head over the network, so the corpus then floats on somebody's default branch and a run that uses this cannot be compared with one that does not")
		quiet         = flag.Bool("quiet", false, "suppress the per-entry module install log")
	)
	flag.Parse()

	manifest, err := check.ReadManifest(underRoot(*root, *manifestPath))
	if err != nil {
		return err
	}
	moduleSources, err := readModuleSources(underRoot(*root, *manifestPath))
	if err != nil {
		return err
	}

	var fetched, skipped int
	for _, source := range manifest.Sources {
		if source.Fetch == nil {
			continue
		}
		spec := *source.Fetch
		dir := underRoot(*root, spec.Dir)

		if !*force {
			if at, err := headCommit(dir); err == nil && at == spec.Commit {
				label := spec.Tag
				if label == "" {
					label = "commit-pinned"
				}
				fmt.Printf("corpus-fetch: %s already at %s (%s)\n", spec.Dir, short(spec.Commit), label)
				skipped++
				continue
			}
		}

		fmt.Printf("corpus-fetch: %s %s -> %s\n", spec.Repo, spec.Tag, spec.Dir)
		if err := fetchOne(dir, spec); err != nil {
			return fmt.Errorf("%s: %w", spec.Dir, err)
		}
		fetched++
	}

	if fetched == 0 && skipped == 0 {
		fmt.Println("corpus-fetch: the manifest pins no external sources; nothing to fetch")
	} else {
		fmt.Printf("corpus-fetch: %d fetched, %d already current\n", fetched, skipped)
	}

	if !*modules {
		return nil
	}
	logOut := os.Stderr
	if *quiet {
		logOut = nil
	}
	summary, err := installModules(context.Background(), manifest, installOptions{
		Root:          *root,
		PinsPath:      underRoot(*root, *modulePinPath),
		ModuleSources: moduleSources,
		MirrorDir:     *mirrorDir,
		RemoteModules: *remoteModules,
		Log:           logOut,
	})
	fmt.Print(renderInstallSummary(summary, *remoteModules))
	return err
}

// renderInstallSummary prints what the module pass did. Every line is a
// count this run computed, not a claim about the corpus: an entry whose
// modules failed to install is measured with a hole in it, and the ranking
// that follows has to be read knowing which ones.
func renderInstallSummary(s installSummary, remote bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\ncorpus-fetch: module pass ran over %d of %d entries (%d module records kept, %d entries carry calls).\n",
		s.Entries-s.Failed, s.Entries, s.Installed, s.EntriesWithCalls)
	fmt.Fprintf(&b, "corpus-fetch: %d registry package(s) pinned in the module lock", s.Packages)
	if len(s.NewlyPinned) > 0 {
		fmt.Fprintf(&b, "; %d newly locked this run: %s", len(s.NewlyPinned), strings.Join(s.NewlyPinned, ", "))
	}
	b.WriteString(".\n")
	if len(s.FloatingPackages) > 0 {
		fmt.Fprintf(&b, "corpus-fetch: %d package(s) were queried but resolved to nothing installable, so they are unpinned: %s\n",
			len(s.FloatingPackages), strings.Join(s.FloatingPackages, ", "))
	}
	fmt.Fprintf(&b, "corpus-fetch: %d pinned go-getter module source(s) served from a local mirror.\n", s.Mirrors)
	if len(s.MirrorErrors) > 0 {
		fmt.Fprintf(&b, "corpus-fetch: %d mirror(s) could not be built. Every call to them fails below rather than\n"+
			"  reaching the network, so no entry is measured against an unpinned tree:\n", len(s.MirrorErrors))
		for _, e := range s.MirrorErrors {
			fmt.Fprintf(&b, "    %s\n", e)
		}
	}
	if len(s.UnpinnedPackages) > 0 {
		fmt.Fprintf(&b, "corpus-fetch: %d go-getter repositor(ies) are not pinned by live/corpus-manifest.json's\n"+
			"  module_sources. Add them there to measure the calls that reach them: %s\n",
			len(s.UnpinnedPackages), strings.Join(s.UnpinnedPackages, ", "))
	}
	switch {
	case remote:
		b.WriteString("corpus-fetch: unpinned go-getter sources were KEPT (-remote-modules). This corpus is not\n" +
			"  reproducible: a branch head can move under it with no change to this repository.\n")
	case s.Dropped > 0:
		sources := make([]string, 0, len(s.DroppedSources))
		for source, n := range s.DroppedSources {
			sources = append(sources, fmt.Sprintf("%s (%d)", source, n))
		}
		sort.Strings(sources)
		fmt.Fprintf(&b, "corpus-fetch: %d go-getter module record(s) dropped, so those calls stay unresolved and the\n"+
			"  corpus stays pinned. Sources: %s\n", s.Dropped, strings.Join(sources, ", "))
	}
	if len(s.Errors) > 0 {
		fmt.Fprintf(&b, "corpus-fetch: %d entr(ies) reported install errors:\n", len(s.Errors))
		for _, e := range s.Errors {
			fmt.Fprintf(&b, "    %s\n", e)
		}
	}
	return b.String()
}

// fetchOne clones one pinned source, shallowly, and verifies the tag resolves
// to the commit the manifest names.
func fetchOne(dir string, spec check.ManifestFetch) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// A shallow single-ref clone: the corpus needs the tree at one commit,
	// not the history that led to it. A source with no tag is fetched by
	// its pinned commit directly; GitHub serves reachable SHAs to shallow
	// fetches, and the verification below is then an identity check.
	ref := spec.Commit
	if spec.Tag != "" {
		ref = "refs/tags/" + spec.Tag
	}
	steps := [][]string{
		{"init", "--quiet"},
		{"remote", "add", "origin", spec.Repo},
		{"fetch", "--quiet", "--depth", "1", "origin", ref},
		{"checkout", "--quiet", "FETCH_HEAD"},
	}
	for _, args := range steps {
		cmd := exec.Command("git", args...) //nolint:gosec // arguments come from the checked-in manifest
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
		}
	}

	at, err := headCommit(dir)
	if err != nil {
		return err
	}
	if at != spec.Commit {
		// Deliberately fatal. The manifest's promise is that this corpus
		// is the one the artifact was computed from; a tag pointing
		// somewhere new breaks that promise, and measuring it anyway
		// would produce numbers nobody could reproduce or trust.
		return fmt.Errorf("tag %s resolves to %s, but the manifest pins %s. "+
			"The tag has been moved. Verify what changed before updating the pin",
			spec.Tag, at, spec.Commit)
	}
	return nil
}

func headCommit(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func short(commit string) string {
	if len(commit) > 9 {
		return commit[:9]
	}
	return commit
}

func underRoot(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}
