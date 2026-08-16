// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Manifest is the corpus definition: which configurations get measured, where
// each came from, and - for the ones that are not in this repository - the
// exact commit they were taken at.
//
// It lives here rather than in either tool because two programs read it:
// tools/corpus-fetch materializes the sources it pins, and tools/corpus-gen
// measures whatever the globs then match. One definition, so a source cannot
// be fetched under one description and measured under another.
type Manifest struct {
	Sources []ManifestSource `json:"sources"`
}

// ManifestSource is one glob and its provenance.
type ManifestSource struct {
	// Glob is a filepath.Glob pattern, relative to the repository root
	// unless absolute. Globs rather than a list of paths, because a
	// hand-maintained path list is the kind of manual wiring this
	// repository's charter is against: adding a configuration to the
	// corpus should mean adding the configuration.
	Glob string `json:"glob"`

	// Origin says where these configurations came from, in words a reader
	// can weigh: "in-repo fixture", "terraform-aws-modules", "internal
	// estate". It is copied into the artifact and never interpreted.
	//
	// It is the field that decides what a ranking is worth. Configurations
	// this project wrote measure this project's own idea of what works;
	// only third-party ones measure the product promise. An artifact that
	// did not carry this would let the two be confused later, which is how
	// a fixture count becomes a compatibility claim.
	Origin string `json:"origin"`

	// Fetch, when set, is how a source outside this repository is
	// obtained. Absent for in-repo sources.
	Fetch *ManifestFetch `json:"fetch,omitempty"`

	// VarFile, when set, names a tfvars file - relative to the repository
	// root, like Glob - applied on top of whatever the matched directory
	// itself holds, the corpus runner's equivalent of "-var-file" (#183).
	//
	// It is this project's own measurement input, not the estate's: #178's
	// rule-2 scoping found that some estates carry no real language-wall
	// blocker at all, only refusals that are artifacts of an unset root
	// variable nobody supplied. A var file here answers "does the language
	// subset accept this once the variable has a value", never "what was
	// production's value" - see the comment at the top of each file this
	// manifest points to for the value policy that keeps that distinction
	// honest.
	//
	// Scoped to the one directory the source names, not to every directory
	// its Glob matches: a wildcard source's var file would silently reach
	// every sibling directory it happens to expand to. To aim a var file at
	// one estate under a wildcarded source, add a second, narrower source
	// for that one directory (Glob with no wildcard, same Origin, no
	// Fetch) ahead of the wildcard source in this list - [Manifest.Resolve]
	// dedupes by matched path in list order, so the narrow source's entry
	// wins and the wildcard source fills in everything else unchanged.
	VarFile string `json:"var_file,omitempty"`
}

// ManifestFetch pins one external source to an exact commit.
//
// When the source tags releases, both the tag and the commit are recorded,
// and tools/corpus-fetch checks out the tag and then verifies it resolves to
// the commit. That is deliberate redundancy: the tag is what a human
// recognizes and the commit is what makes a run reproducible, and a tag that
// has been moved to a different commit is something the fetch should refuse
// rather than silently measure. A corpus that changed under the artifact
// would turn every number in it into a claim about an unknown input.
//
// A source that publishes no tags at all - true of every repository in the
// published-deployment population (#147) - is pinned by commit alone, and
// the fetch checks the commit out directly. The reproducibility property
// lives entirely in the commit either way; the tag only ever added the
// human-readable name and the moved-tag tripwire.
type ManifestFetch struct {
	// Dir is where the source is materialized, relative to the repository
	// root. It is expected to be ignored by git: the corpus is pinned by
	// commit, so checking the contents in as well would only add weight
	// and third-party licenses.
	Dir string `json:"dir"`

	// Repo is the clone URL.
	Repo string `json:"repo"`

	// Tag is the human-recognizable version, e.g. "v6.6.1". Empty for a
	// source that publishes no tags; the commit alone pins it then.
	Tag string `json:"tag,omitempty"`

	// Commit is the exact object fetched: what Tag must resolve to when
	// one is recorded, or the checkout target itself when there is none.
	Commit string `json:"commit"`
}

// CorpusEntryRef is one directory the corpus runner will measure.
type CorpusEntryRef struct {
	// Name identifies the configuration in the artifact, relative to the
	// repository root and slash-separated so it reads the same on every
	// platform.
	Name string

	// Dir is where to read it from.
	Dir string

	// Origin is its source's origin string.
	Origin string

	// VarFile is its source's [ManifestSource.VarFile], repository-root
	// relative and unresolved - the caller joins it against whatever root
	// it is itself running under, the same way it already does for Name.
	// Empty when the source named none.
	VarFile string
}

// ReadManifest reads and validates a corpus manifest.
func ReadManifest(path string) (Manifest, error) {
	var manifest Manifest

	src, err := os.ReadFile(path)
	if err != nil {
		return manifest, fmt.Errorf("reading the corpus manifest: %w", err)
	}
	if err := json.Unmarshal(src, &manifest); err != nil {
		return manifest, fmt.Errorf("parsing %s: %w", path, err)
	}
	if len(manifest.Sources) == 0 {
		return manifest, fmt.Errorf("%s names no sources", path)
	}
	for i, source := range manifest.Sources {
		if source.Glob == "" {
			return manifest, fmt.Errorf("%s: source %d has no glob", path, i)
		}
		if source.Origin == "" {
			// Refused rather than defaulted. An origin-less source would
			// land in the artifact as an unlabelled row, and the whole
			// point of the field is that nobody can later mistake a
			// fixture for a third-party configuration.
			return manifest, fmt.Errorf("%s: source %q has no origin", path, source.Glob)
		}
		if f := source.Fetch; f != nil {
			if f.Dir == "" || f.Repo == "" || f.Commit == "" {
				return manifest, fmt.Errorf("%s: source %q has an incomplete fetch block; dir, repo and commit are all required (tag only when the source publishes one)", path, source.Glob)
			}
		}
	}
	return manifest, nil
}

// Resolve expands every glob into the directories that hold a configuration.
//
// A matched path that is not a directory, or that holds no .tf files, is
// skipped rather than counted: an empty directory in the ranking would read
// as a configuration nothing refused, which is a lie about coverage.
func (m Manifest) Resolve(root string) ([]CorpusEntryRef, error) {
	var entries []CorpusEntryRef
	seen := map[string]bool{}

	for _, source := range m.Sources {
		pattern := source.Glob
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(root, pattern)
		}
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("bad glob %q: %w", source.Glob, err)
		}
		sort.Strings(matches)

		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil || !info.IsDir() {
				continue
			}
			if !HasConfigFiles(match) {
				continue
			}
			if seen[match] {
				continue
			}
			seen[match] = true

			name, err := filepath.Rel(root, match)
			if err != nil {
				name = match
			}
			entries = append(entries, CorpusEntryRef{
				Name:    filepath.ToSlash(name),
				Dir:     match,
				Origin:  source.Origin,
				VarFile: source.VarFile,
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

// HasConfigFiles reports whether a directory holds any OpenTofu
// configuration files directly.
func HasConfigFiles(dir string) bool {
	files, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		name := file.Name()
		if strings.HasSuffix(name, ".tf") || strings.HasSuffix(name, ".tf.json") {
			return true
		}
	}
	return false
}
