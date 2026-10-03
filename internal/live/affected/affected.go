// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package affected answers "which estate roots does this git range touch"
// (GitHub issue #1751, part of epic #1749; it takes over section 4 of
// #1106). It reads each root's module graph at both ends of the range,
// walks every changed file to the roots that reach it, follows cross-estate
// reads (#561) to dependents, and answers indeterminate, with the reason,
// for a change it cannot place.
//
// # Rules
//
// A changed file is read at its base path (deleted, modified, or the old
// side of a rename) against the base revision's graphs, and at its head
// path against the head revision's. Its module is the nearest directory at
// or above it that holds configuration (.tf, .tofu, .tf.json, .tofu.json)
// at that revision, so a template beside a module's .tf files belongs to
// that module.
//
//   - A file in a root's own directory names that root: "changed".
//   - A file in a module a root reaches by local paths, directly or through
//     nested modules, names that root: "uses <module dir>".
//   - A module call outside the working tree (oci://, registry, git) whose
//     source or version changes names the roots whose graph holds the call:
//     "pin <call> <from> -> <to>". A change to the module's own directory
//     names no root, since no root reads the working tree for it.
//   - A root that reads a named root's estate through a cross-estate
//     reference is named too, transitively: "reads <estate>".
//
// Indeterminate, with the reason; the consumer plans every root:
//
//   - a changed .terraform.lock.hcl anywhere;
//   - a required_providers constraint that differs between the revisions in
//     any directory a root reaches;
//   - a change in a module directory other than a root's own while some
//     root calls a module by a floating source (a registry source with no
//     exact version, an OCI source with no tag or digest, a git ref that is
//     neither a commit nor a version tag, any other remote kind): the change
//     may reach that root through a published version. This command does
//     not read the installed version (.terraform/modules/modules.json),
//     which is a property of a working directory's last init, not of the
//     range;
//   - a file in no module directory that is not documentation: a plan can
//     read it (a -var-file, a file() call, a wrapper's configuration) and
//     nothing in the configuration says which;
//   - a root whose module graph does not load at either revision.
//
// Documentation outside every module directory (.md, .markdown, .rst,
// .adoc, LICENSE, COPYING, NOTICE) and paths matching an -ignore pattern
// are listed as unplaced and name nothing. Module code that no root reaches
// by a local path is listed as unplaced too: that is the pinned case.
//
// # Bounds
//
// A cross-estate read inside a module called from outside the working tree
// is not seen, since that module is not descended into. Reads are found by
// [check.EstatesRead]; internal/live/waves (#1754) carries a stricter
// reader that replaces it when it lands.
package affected

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/check"
)

// Schema is the -json document's version. A field's meaning never changes
// under one value; a new field may be added.
const Schema = 1

// Outcome is the answer's kind.
type Outcome string

const (
	// Determinate means Roots is the whole affected set.
	Determinate Outcome = "determinate"
	// Indeterminate means some change could not be attributed; plan every
	// root. Roots still lists what was attributed.
	Indeterminate Outcome = "indeterminate"
)

// Reason kinds.
const (
	KindChanged = "changed"
	KindUses    = "uses"
	KindPin     = "pin"
	KindReads   = "reads"
)

// Result is the -json document. Every slice is non-nil, so a consumer
// reads an empty array rather than null.
type Result struct {
	Schema int `json:"schema"`
	// Range is the range as given and the two commits it resolved to.
	Range Range `json:"range"`
	// Outcome is "determinate" or "indeterminate".
	Outcome Outcome `json:"outcome"`
	// RootsTotal is the number of estate roots at either end of the range.
	RootsTotal int `json:"roots_total"`
	// Roots are the affected roots, sorted by directory.
	Roots []Root `json:"roots"`
	// Indeterminate are the reasons the answer is not the whole set.
	Indeterminate []Indeterminacy `json:"indeterminate"`
	// Unplaced are changed paths that name no root and do not make the
	// answer indeterminate.
	Unplaced []Unplaced `json:"unplaced"`
}

// Range is the range a result answers.
type Range struct {
	Spec string `json:"spec"`
	Base string `json:"base"`
	Head string `json:"head"`
}

// Root is one affected estate root.
type Root struct {
	// Dir is the root's directory relative to the repository top.
	Dir string `json:"dir"`
	// Estate is the estate the root declares.
	Estate string `json:"estate"`
	// Removed is true for a root that exists at the base revision only.
	Removed bool `json:"removed,omitempty"`
	// Reasons say why the root is named; at least one.
	Reasons []Reason `json:"reasons"`
}

// Reason is one cause for naming a root.
type Reason struct {
	// Kind is "changed", "uses", "pin" or "reads".
	Kind string `json:"kind"`
	// Module is the module directory for "uses" and the module call path
	// for "pin".
	Module string `json:"module,omitempty"`
	// From and To are the pin's two sides for "pin".
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// Estate is the estate read, for "reads".
	Estate string `json:"estate,omitempty"`
	// Paths are the changed files behind "changed" and "uses", sorted.
	Paths []string `json:"paths,omitempty"`
	// Text is the reason as the text output prints it.
	Text string `json:"text"`
}

// Indeterminate kinds.
const (
	IndetLock     = "lock-file"
	IndetProvider = "provider-version"
	IndetFloating = "floating-module"
	IndetUnplaced = "unplaced-file"
	IndetLoad     = "load-error"
)

// Indeterminacy is one reason the answer is indeterminate.
type Indeterminacy struct {
	// Kind is "lock-file", "provider-version", "floating-module",
	// "unplaced-file" or "load-error".
	Kind string `json:"kind"`
	// Path is the changed file or root directory it concerns.
	Path string `json:"path"`
	Text string `json:"text"`
}

// Unplaced kinds.
const (
	UnplacedDocs    = "documentation"
	UnplacedIgnored = "ignored"
	UnplacedUnread  = "unread-module"
)

// Unplaced is a changed path that names no root.
type Unplaced struct {
	Path string `json:"path"`
	// Why is "documentation", "ignored" or "unread-module".
	Why  string `json:"why"`
	Text string `json:"text"`
}

// Options are Compute's inputs.
type Options struct {
	// RepoDir is any directory inside the repository.
	RepoDir string
	// Spec is the range: "A..B", "A...B" or "A".
	Spec string
	// Roots, when non-empty, are the root directories to consider,
	// relative to RepoDir; otherwise every directory declaring an estate.
	Roots []string
	// Ignore are path patterns (doublestar, against the path from the
	// repository top) whose changes name nothing.
	Ignore []string
	// Reads finds a root's cross-estate reads; nil is [check.EstatesRead].
	Reads ReadsFunc
	// TempDir is where the two revisions are extracted; "" is os.TempDir.
	TempDir string
}

// Compute answers the range.
func Compute(ctx context.Context, o Options) (*Result, error) {
	if o.Reads == nil {
		o.Reads = func(cfg *configs.Config) ([]string, error) { return check.EstatesRead(cfg), nil }
	}
	for _, p := range o.Ignore {
		if !doublestar.ValidatePattern(p) {
			return nil, fmt.Errorf("-ignore %q is not a valid pattern", p)
		}
	}
	top, err := git(ctx, o.RepoDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%s is not inside a git repository", o.RepoDir)
	}
	repo := strings.TrimSpace(string(top))
	base, head, err := resolveRange(ctx, repo, o.Spec)
	if err != nil {
		return nil, err
	}
	selected, err := selectedRoots(repo, o.RepoDir, o.Roots)
	if err != nil {
		return nil, err
	}
	changes, err := diff(ctx, repo, base, head)
	if err != nil {
		return nil, err
	}

	work, err := os.MkdirTemp(o.TempDir, "live-affected-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	snaps := [2]*snapshot{}
	for i, rev := range []string{base, head} {
		tree, err := tempTree(work, fmt.Sprintf("rev%d-", i))
		if err != nil {
			return nil, err
		}
		if err := extract(ctx, repo, rev, tree); err != nil {
			return nil, err
		}
		if snaps[i], err = loadSnapshot(ctx, tree, selected, o.Reads); err != nil {
			return nil, err
		}
	}

	for _, d := range selected {
		if !snaps[0].moduleDirs[d] && !snaps[1].moduleDirs[d] {
			return nil, fmt.Errorf("-root %s holds no configuration at %s or at %s", d, short(base), short(head))
		}
	}

	res := attribute(snaps[0], snaps[1], changes, o.Ignore)
	res.Range = Range{Spec: o.Spec, Base: base, Head: head}
	return res, nil
}

// selectedRoots turns -root directories, relative to the working
// directory, into paths relative to the repository top.
func selectedRoots(repo, wd string, roots []string) ([]string, error) {
	var out []string
	realRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		return nil, err
	}
	// The working directory resolved once, so a -root that does not exist
	// is still compared in the same spelling as the repository.
	if real, err := filepath.EvalSymlinks(wd); err == nil {
		wd = real
	}
	for _, r := range roots {
		abs := r
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(wd, r)
		}
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			abs = real
		}
		rp, err := filepath.Rel(realRepo, abs)
		if err != nil || !(rp == "." || filepath.IsLocal(rp)) {
			return nil, fmt.Errorf("-root %s is not inside the repository %s", r, repo)
		}
		out = append(out, filepath.ToSlash(rp))
	}
	sort.Strings(out)
	return out, nil
}

// isDocumentation is a file outside every module that no plan reads.
func isDocumentation(p string) bool {
	base := path.Base(p)
	for _, prefix := range []string{"LICENSE", "COPYING", "NOTICE"} {
		if strings.HasPrefix(base, prefix) {
			return true
		}
	}
	switch strings.ToLower(path.Ext(base)) {
	case ".md", ".markdown", ".rst", ".adoc", ".asciidoc":
		return true
	}
	return false
}

// JSON is the result as the -json document.
func (r *Result) JSON() (string, error) {
	// No HTML escaping: a reason's "->" reads as itself.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return "", err
	}
	return buf.String(), nil
}
