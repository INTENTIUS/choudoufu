// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package waves

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Options are one wave planning's inputs.
type Options struct {
	// Roots are the root directories as given, relative to BaseDir or
	// absolute. Empty means the roots of Set.
	Roots []string
	// BaseDir is what a relative root is relative to.
	BaseDir string
	// Canaries name roots by directory or estate.
	Canaries []string
	// Set, when non-nil, is the set plan document whose plans give every
	// wave its digest. It must name exactly the roots planned.
	Set *SetDocument
}

// RootName is how a set names a root directory: cleaned, with forward
// slashes, the spelling #1752's set plan document uses.
func RootName(dir string) string {
	return filepath.ToSlash(filepath.Clean(dir))
}

// Build reads every root's configuration, splits the set into waves and,
// when a set plan document is given, gives the set and each wave its
// digest.
func Build(ctx context.Context, opts Options) (*Document, error) {
	names := make([]string, 0, len(opts.Roots))
	for _, r := range opts.Roots {
		names = append(names, RootName(r))
	}
	if len(names) == 0 && opts.Set != nil {
		for _, r := range opts.Set.Roots {
			names = append(names, r.Root)
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no roots to split")
	}

	var docEstate map[string]string
	if opts.Set != nil {
		docEstate = make(map[string]string, len(opts.Set.Roots))
		for _, r := range opts.Set.Roots {
			docEstate[r.Root] = r.Estate
		}
		if missing, extra := setDifference(names, docEstate); len(missing)+len(extra) > 0 {
			var parts []string
			if len(missing) > 0 {
				parts = append(parts, "not in the set plan document: "+strings.Join(missing, ", "))
			}
			if len(extra) > 0 {
				parts = append(parts, "in the document but not given: "+strings.Join(extra, ", "))
			}
			return nil, fmt.Errorf("the roots given and the set plan document's roots differ (%s); a digest covers exactly the roots planned", strings.Join(parts, "; "))
		}
	}

	roots := make([]Root, 0, len(names))
	for _, n := range names {
		dir := filepath.FromSlash(n)
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(opts.BaseDir, dir)
		}
		r, err := LoadRoot(ctx, n, dir)
		if err != nil {
			return nil, err
		}
		if e := docEstate[n]; e != "" && e != r.Estate {
			return nil, fmt.Errorf("root %s owns estate %q by its configuration, but the set plan document planned it as %q", n, r.Estate, e)
		}
		roots = append(roots, r)
	}

	w, err := Split(roots, opts.Canaries)
	if err != nil {
		return nil, err
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].Root < roots[j].Root })
	doc := &Document{
		FormatVersion: FormatVersion,
		Roots:         roots,
		Waves:         w.Waves,
		Edges:         w.Edges,
		ExternalReads: w.External,
	}
	if opts.Set != nil {
		byRoot, set, err := DocumentDigests(opts.Set)
		if err != nil {
			return nil, err
		}
		if err := w.AttachDigests(byRoot); err != nil {
			return nil, err
		}
		doc.Waves = w.Waves
		doc.Digest = set
		doc.RootDigests = byRoot
	}
	return doc, nil
}

func setDifference(names []string, doc map[string]string) (missing, extra []string) {
	given := make(map[string]bool, len(names))
	for _, n := range names {
		given[n] = true
		if _, ok := doc[n]; !ok {
			missing = append(missing, n)
		}
	}
	for n := range doc {
		if !given[n] {
			extra = append(extra, n)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}
