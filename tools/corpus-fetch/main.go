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
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
		manifestPath = flag.String("manifest", "live/corpus-manifest.json", "corpus manifest to read")
		root         = flag.String("root", ".", "repository root that relative paths are resolved against")
		force        = flag.Bool("force", false, "re-clone even when the pinned commit is already checked out")
	)
	flag.Parse()

	manifest, err := check.ReadManifest(underRoot(*root, *manifestPath))
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
		return nil
	}
	fmt.Printf("corpus-fetch: %d fetched, %d already current\n", fetched, skipped)
	return nil
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
