// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package affected

import (
	"context"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	version "github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// snapshot is the repository's configuration at one revision: which
// directories are modules, which of them are estate roots, and each root's
// module graph as far as the working tree holds it.
type snapshot struct {
	// moduleDirs are the directories holding configuration files, slash
	// separated and relative to the repository ("." is its top).
	moduleDirs map[string]bool
	// roots are the estate roots by directory.
	roots map[string]*rootGraph
	// providers is each reached module directory's required_providers:
	// provider source to version constraint.
	providers map[string]map[string]string
}

// rootGraph is one root's module graph at one revision.
type rootGraph struct {
	Dir    string
	Estate string
	// Dirs are the module directories the root reaches by local paths,
	// itself included.
	Dirs map[string]bool
	// Calls are every module call in the graph by its path from the root
	// ("shared", "wrapper.inner").
	Calls map[string]moduleCall
	// Reads are the estates the root's configuration reads.
	Reads []string
	// LoadErr is why the graph could not be read whole, empty when it was.
	LoadErr string
	// ReadsErr is why the estates the root reads cannot be told (a read
	// whose estate is not a literal), empty when they can.
	ReadsErr string
}

// moduleCall is one module block as the graph records it.
type moduleCall struct {
	Source  string
	Version string
	// Local is a call by a relative path, read from the working tree.
	Local bool
	// Floating is a call outside the working tree whose content can change
	// with no change to this repository: no exact version, tag, digest or
	// commit.
	Floating bool
}

// ReadsFunc names the estates a root's configuration reads through a
// cross-estate reference (#561). [waves.EstatesRead] is the default.
type ReadsFunc func(cfg *configs.Config) ([]string, error)

// isConfigFile is the loader's own set of configuration file suffixes.
func isConfigFile(name string) bool {
	for _, s := range []string{".tf", ".tofu", ".tf.json", ".tofu.json"} {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

// loadSnapshot reads the tree extracted at tree. selected, when non-empty,
// are the root directories to read; otherwise every directory declaring an
// estate (a live block or an estate.chdf.hcl sidecar) is a root.
func loadSnapshot(ctx context.Context, tree string, selected []string, reads ReadsFunc) (*snapshot, error) {
	s := &snapshot{
		moduleDirs: map[string]bool{},
		roots:      map[string]*rootGraph{},
		providers:  map[string]map[string]string{},
	}
	err := filepath.WalkDir(tree, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != tree && (d.Name() == ".git" || d.Name() == ".terraform") {
				return filepath.SkipDir
			}
			return nil
		}
		if isConfigFile(d.Name()) {
			s.moduleDirs[rel(tree, filepath.Dir(p))] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	parser := configs.NewParser(nil)
	var rootDirs []string
	if len(selected) > 0 {
		for _, d := range selected {
			if s.moduleDirs[d] {
				rootDirs = append(rootDirs, d)
			}
		}
	} else {
		// A sidecar alone makes a root, so a root whose configuration
		// stops parsing is still a root, and reads as one that does not
		// load rather than as one that vanished.
		for d := range s.moduleDirs {
			abs := filepath.Join(tree, filepath.FromSlash(d))
			if _, err := os.Stat(filepath.Join(abs, configs.LiveSidecarFilename)); err == nil {
				rootDirs = append(rootDirs, d)
				continue
			}
			mod, _ := parser.LoadConfigDir(abs)
			if mod != nil && mod.Live != nil && mod.Live.Estate != "" {
				rootDirs = append(rootDirs, d)
			}
		}
	}
	sort.Strings(rootDirs)
	for _, d := range rootDirs {
		g := loadRoot(ctx, parser, tree, d, reads)
		// A diagnostic names files by their extracted path; a reader
		// knows them by the path in the repository.
		g.LoadErr = strings.ReplaceAll(g.LoadErr, tree+string(filepath.Separator), "")
		s.roots[d] = g.rootGraph
		for dir, reqs := range g.providers {
			s.providers[dir] = reqs
		}
	}
	return s, nil
}

// graphWithProviders carries the per-directory provider requirements out
// of loadRoot alongside the graph.
type graphWithProviders struct {
	*rootGraph
	providers map[string]map[string]string
}

func rel(tree, p string) string {
	r, err := filepath.Rel(tree, p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(r)
}

// loadRoot builds one root's static module tree. A module called by a
// local path is read from the extracted tree; any other is recorded as a
// call and not descended into, since its content is not in the working
// tree at all.
func loadRoot(ctx context.Context, parser *configs.Parser, tree, dir string, reads ReadsFunc) graphWithProviders {
	g := &rootGraph{Dir: dir, Dirs: map[string]bool{}, Calls: map[string]moduleCall{}}
	out := graphWithProviders{rootGraph: g, providers: map[string]map[string]string{}}
	abs := filepath.Join(tree, filepath.FromSlash(dir))

	mod, diags := parser.LoadConfigDir(abs)
	if mod == nil || diags.HasErrors() {
		g.LoadErr = firstError(diags, "the configuration does not load")
		return out
	}
	if mod.Live != nil {
		g.Estate = mod.Live.Estate
	}

	call := configs.NewStaticModuleCall(addrs.RootModule, hcl.Range{},
		func(v *configs.Variable) (cty.Value, hcl.Diagnostics) {
			if v.Required() {
				return cty.NilVal, hcl.Diagnostics{{
					Severity: hcl.DiagError,
					Summary:  "No value for required variable",
					Detail:   fmt.Sprintf("The root module input variable %q has no default, and live-affected reads configuration without variable values.", v.Name),
					Subject:  v.DeclRange.Ptr(),
				}}
			}
			return v.Default, nil
		}, abs, "default")

	dirs := map[string]string{"": abs}
	var walkErr string
	cfg, cfgDiags := configs.BuildConfig(ctx, mod, call, configs.ModuleWalkerFunc(
		func(_ context.Context, req *configs.ModuleRequest) (*configs.Module, *version.Version, hcl.Diagnostics) {
			name := strings.Join(req.Path, ".")
			mc := moduleCall{Version: constraintString(req.VersionConstraint)}
			if req.SourceAddr != nil {
				mc.Source = req.SourceAddr.String()
			}
			local, isLocal := req.SourceAddr.(addrs.ModuleSourceLocal)
			if !isLocal {
				mc.Floating = floating(req.SourceAddr, req.VersionConstraint)
				g.Calls[name] = mc
				return nil, nil, nil
			}
			mc.Local = true
			g.Calls[name] = mc
			where := filepath.Join(dirs[req.Parent.Path.String()], filepath.FromSlash(string(local)))
			if r, err := filepath.Rel(tree, where); err != nil || !filepath.IsLocal(r) {
				if walkErr == "" {
					walkErr = fmt.Sprintf("module.%s is called by %q, a path outside the repository", name, string(local))
				}
				return nil, nil, nil
			}
			dirs[req.Path.String()] = where
			child, modDiags := parser.LoadConfigDir(where)
			return child, nil, modDiags
		}, parser.LoadSymbolFilesInDir,
	))
	if cfgDiags.HasErrors() {
		g.LoadErr = firstError(cfgDiags, "the module tree does not load")
		return out
	}
	if walkErr != "" {
		g.LoadErr = walkErr
		return out
	}

	var visit func(c *configs.Config)
	visit = func(c *configs.Config) {
		d := rel(tree, c.Module.SourceDir)
		g.Dirs[d] = true
		if c.Module.ProviderRequirements != nil {
			reqs := out.providers[d]
			if reqs == nil {
				reqs = map[string]string{}
				out.providers[d] = reqs
			}
			for _, rp := range c.Module.ProviderRequirements.RequiredProviders {
				reqs[rp.Type.String()] = constraintString(rp.Requirement)
			}
		}
		for _, child := range c.Children {
			visit(child)
		}
	}
	visit(cfg)
	// A directory reached with no required_providers still compares as
	// "none", so adding a block is seen.
	for d := range g.Dirs {
		if out.providers[d] == nil {
			out.providers[d] = map[string]string{}
		}
	}

	if reads != nil {
		r, err := reads(cfg)
		if err != nil {
			// The graph loaded; only who it reads is unknown. Attribution
			// still runs on it, and the answer is indeterminate.
			g.ReadsErr = err.Error()
			return out
		}
		g.Reads = r
	}
	return out
}

func firstError(diags hcl.Diagnostics, fallback string) string {
	for _, d := range diags {
		if d.Severity == hcl.DiagError {
			if d.Subject != nil {
				return fmt.Sprintf("%s: %s (%s)", d.Summary, d.Detail, d.Subject.String())
			}
			return fmt.Sprintf("%s: %s", d.Summary, d.Detail)
		}
	}
	return fallback
}

func constraintString(vc configs.VersionConstraint) string {
	if len(vc.Required) == 0 {
		return ""
	}
	return vc.Required.String()
}

// exactConstraint is a version constraint that admits one version: a bare
// version or "= v".
func exactConstraint(vc configs.VersionConstraint) bool {
	if len(vc.Required) != 1 {
		return false
	}
	c := strings.TrimSpace(vc.Required[0].String())
	if strings.HasPrefix(c, "=") {
		c = strings.TrimSpace(strings.TrimPrefix(c, "="))
	}
	_, err := version.NewVersion(c)
	return err == nil
}

var (
	fullSHA    = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
	versionTag = regexp.MustCompile(`^v?[0-9]+(\.[0-9]+)*([-+][0-9A-Za-z.\-+]*)?$`)
)

// floating reports whether a call outside the working tree can resolve to
// different content with no change to the call: a registry source with no
// exact version, an OCI source with no tag or digest (or the tag
// "latest"), a git source with no ref or a ref that is neither a commit nor
// a version tag, and every other remote kind (an archive URL, a bucket),
// whose content this command has no way to see pinned.
func floating(src addrs.ModuleSource, vc configs.VersionConstraint) bool {
	switch s := src.(type) {
	case addrs.ModuleSourceRegistry:
		return !exactConstraint(vc)
	case addrs.ModuleSourceRemote:
		raw := s.String()
		u, err := url.Parse(strings.TrimPrefix(raw, "git::"))
		if err != nil {
			return true
		}
		q := u.Query()
		switch {
		case strings.HasPrefix(raw, "oci://"):
			if q.Get("digest") != "" {
				return false
			}
			tag := q.Get("tag")
			return tag == "" || tag == "latest"
		case strings.HasPrefix(raw, "git::"):
			ref := q.Get("ref")
			return !(fullSHA.MatchString(ref) || versionTag.MatchString(ref))
		}
		return true
	}
	return true
}

// pinLabel is the short form of a pinned call for a reason string: the tag,
// digest, ref or version alone when both sides share an address, else the
// whole source.
func pinLabels(a, b moduleCall) (string, string) {
	if a.Source == "" {
		return "(none)", full(b)
	}
	if b.Source == "" {
		return full(a), "(none)"
	}
	baseA, qa := splitQuery(a.Source)
	baseB, qb := splitQuery(b.Source)
	if baseA == baseB {
		for _, k := range []string{"tag", "digest", "ref"} {
			if qa.Get(k) != "" || qb.Get(k) != "" {
				if otherQueryEqual(qa, qb, k) && a.Version == b.Version {
					return orNone(qa.Get(k)), orNone(qb.Get(k))
				}
			}
		}
		if a.Source == b.Source && a.Version != b.Version {
			return orNone(trimEq(a.Version)), orNone(trimEq(b.Version))
		}
	}
	return full(a), full(b)
}

func full(c moduleCall) string {
	if c.Version != "" {
		return c.Source + " " + c.Version
	}
	return c.Source
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func trimEq(v string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "="))
}

func splitQuery(src string) (string, url.Values) {
	base, q, _ := strings.Cut(src, "?")
	v, _ := url.ParseQuery(q)
	return base, v
}

func otherQueryEqual(a, b url.Values, skip string) bool {
	for k := range a {
		if k != skip && a.Get(k) != b.Get(k) {
			return false
		}
	}
	for k := range b {
		if k != skip && a.Get(k) != b.Get(k) {
			return false
		}
	}
	return true
}

// nearestModuleDir is the closest directory at or above file's own that
// holds configuration at this revision, "" when none does.
func (s *snapshot) nearestModuleDir(file string) (string, bool) {
	d := path.Dir(file)
	for {
		if s.moduleDirs[d] {
			return d, true
		}
		if d == "." || d == "/" || d == "" {
			return "", false
		}
		d = path.Dir(d)
	}
}

// users are the roots that reach dir, by root directory.
func (s *snapshot) users(dir string) []*rootGraph {
	var out []*rootGraph
	for _, g := range s.roots {
		if g.Dirs[dir] {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dir < out[j].Dir })
	return out
}

// tempTree makes a directory for one revision's tree, with symlinks in its
// own path resolved so every path derived from it is spelled one way.
func tempTree(parent, name string) (string, error) {
	d, err := os.MkdirTemp(parent, name)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(d)
}
