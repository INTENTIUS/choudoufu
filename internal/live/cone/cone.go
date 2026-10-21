// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package cone computes a change's cone within one estate (GitHub issue
// #1956): the blocks a git diff changes, deletes or refactors, and every
// block that reads one of them in the head configuration. A plan given the
// cone as targets lets the plan graph add the ancestors itself, so this
// package writes no second targeting rule.
//
// The diff is block-level over the root module: two blocks with the same
// type and labels are the same node, and a node changed when its source
// text, whitespace aside, differs. A local module call changed when any
// file under its source directory did. tfvars change the variables they
// set. A guard names the conditions under which a cone cannot be trusted,
// and a caller that sees one plans the whole estate instead.
package cone

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultShare is the largest share of the estate's blocks a cone may hold
// before the guard plans everything instead (the research's 30%).
const DefaultShare = 0.30

// Guard reasons, as the JSON document and the plan note name them.
const (
	GuardNoBase       = "no-base"
	GuardProvider     = "provider-changed"
	GuardLockFile     = "lock-file-changed"
	GuardLiveBlock    = "live-block-changed"
	GuardTerraform    = "terraform-block-changed"
	GuardShare        = "cone-too-large"
	GuardUnplaced     = "refactor-unplaced"
	GuardEmpty        = "empty-cone"
	GuardParse        = "config-unreadable"
	GuardSweep        = "sweep-outside-cone"
	GuardNonNative    = "json-config"
	GuardModuleRemote = "remote-module-changed"
	GuardNewRoot      = "root-new-at-base"
)

// Options is one cone computation.
type Options struct {
	// Dir is the root module's directory, inside a git working tree. The
	// head side is the working tree as it is now, uncommitted edits included,
	// because that is what a plan in Dir reads.
	Dir string
	// Base is the git revision the change is measured against.
	Base string
	// Share overrides DefaultShare when positive.
	Share float64
}

// Guard is the verdict: Full means plan everything, for Reason.
type Guard struct {
	Full   bool   `json:"full"`
	Kind   string `json:"kind,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Result is the cone.
type Result struct {
	Schema  int    `json:"schema"`
	Base    string `json:"base"`
	BaseSHA string `json:"base_sha,omitempty"`
	// Changed are the nodes whose source differs, in head-graph terms
	// (resource, data.x.y, module.m, var.v, local.l, output.o).
	Changed []string `json:"changed"`
	// Deleted are block addresses present at Base and absent now.
	Deleted []string `json:"deleted"`
	// Refactor are moved from/to, removed from and import to addresses.
	Refactor []string `json:"refactor"`
	// Widened are blocks added by the co-managed type-pair table.
	Widened []string `json:"widened"`
	// Reach is every node in the cone: Changed plus descendants plus
	// Widened, of every kind.
	Reach []string `json:"reach"`
	// Targets are the resource and module addresses to pass as -target:
	// Reach's targetable members, Deleted and Refactor.
	Targets []string `json:"targets"`
	// Blocks and ConeBlocks size the estate and the cone for the share.
	Blocks     int   `json:"blocks"`
	ConeBlocks int   `json:"cone_blocks"`
	Guard      Guard `json:"guard"`
}

// JSON renders the document. Every list is an array, never null.
func (r *Result) JSON() (string, error) {
	c := *r
	for _, p := range []*[]string{&c.Changed, &c.Deleted, &c.Refactor, &c.Widened, &c.Reach, &c.Targets} {
		if *p == nil {
			*p = []string{}
		}
	}
	b, err := json.MarshalIndent(&c, "", "  ")
	return string(b), err
}

// Text renders the targets one per line, as -target-file reads them,
// followed by the guard as a comment line.
func (r *Result) Text() string {
	var b strings.Builder
	if r.Guard.Full {
		fmt.Fprintf(&b, "# full plan: %s\n", r.Guard.Reason)
		return b.String()
	}
	for _, t := range r.Targets {
		b.WriteString(t + "\n")
	}
	fmt.Fprintf(&b, "# cone: %d of %d blocks\n", r.ConeBlocks, r.Blocks)
	return b.String()
}

func (r *Result) fall(kind, reason string) *Result {
	if !r.Guard.Full {
		r.Guard = Guard{Full: true, Kind: kind, Reason: reason}
	}
	return r
}

// Compute reads the diff and returns the cone. An error is returned only
// when Dir cannot be read at all; everything else that defeats a cone is a
// guard verdict on the result.
func Compute(ctx context.Context, o Options) (*Result, error) {
	res := &Result{Schema: 1, Base: o.Base}
	dir, err := filepath.Abs(o.Dir)
	if err != nil {
		return nil, err
	}
	share := o.Share
	if share <= 0 {
		share = DefaultShare
	}
	if strings.TrimSpace(o.Base) == "" {
		return res.fall(GuardNoBase, "no base ref was given"), nil
	}
	sha, err := gitOut(ctx, dir, "rev-parse", "--verify", "--quiet", o.Base+"^{commit}")
	if err != nil {
		return res.fall(GuardNoBase, fmt.Sprintf("%q is not a commit in this repository", o.Base)), nil
	}
	res.BaseSHA = strings.TrimSpace(sha)
	prefix, err := gitOut(ctx, dir, "rev-parse", "--show-prefix")
	if err != nil {
		return nil, err
	}
	prefix = strings.TrimSpace(prefix)
	top, err := gitOut(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	top = strings.TrimSpace(top)

	changedFiles, err := changedPaths(ctx, dir, res.BaseSHA)
	if err != nil {
		return nil, err
	}
	for _, p := range changedFiles {
		if path.Base(p) == ".terraform.lock.hcl" && strings.HasPrefix(p, prefix) {
			res.fall(GuardLockFile, "the provider lock file changed")
		}
	}

	baseFiles, baseExists, err := baseConfigFiles(ctx, dir, res.BaseSHA, prefix)
	if err != nil {
		return nil, err
	}
	// A root with nothing to compare against at base is new there: every
	// block would read as added, and the first one to trip a guard (a
	// terraform or provider block, or the share) would name the wrong
	// cause.
	where := strings.TrimSuffix(prefix, "/")
	if where == "" {
		where = "the repository top"
	}
	switch {
	case !baseExists:
		res.fall(GuardNewRoot, fmt.Sprintf("the root's directory (%s) did not exist at %s, so there is no base configuration to diff", where, o.Base))
	case len(baseFiles) == 0:
		res.fall(GuardNewRoot, fmt.Sprintf("the root's directory (%s) held no configuration at %s, so there is no base configuration to diff", where, o.Base))
	}
	headFiles, err := headConfigFiles(dir)
	if err != nil {
		return nil, err
	}
	base, bdiags := parseModule(baseFiles)
	head, hdiags := parseModule(headFiles)
	if bdiags != "" {
		res.fall(GuardParse, "the base configuration does not parse: "+bdiags)
	}
	if hdiags != "" {
		res.fall(GuardParse, "the head configuration does not parse: "+hdiags)
	}
	if base.nonNative || head.nonNative {
		res.fall(GuardNonNative, "the root module has JSON configuration, which this diff does not read")
	}

	changed := map[string]bool{}
	for key, hb := range head.blocks {
		bb, ok := base.blocks[key]
		if ok && bb.text == hb.text {
			continue
		}
		switch {
		case hb.kind == kindProvider && !ok:
			res.fall(GuardProvider, "a provider block was added: "+key)
		case hb.kind == kindProvider:
			res.fall(GuardProvider, "a provider block changed: "+key)
		case hb.kind == kindLive && !ok:
			res.fall(GuardLiveBlock, "the live block was added")
		case hb.kind == kindLive:
			res.fall(GuardLiveBlock, "the live block changed")
		case hb.kind == kindTerraform && !ok:
			res.fall(GuardTerraform, "the terraform block was added")
		case hb.kind == kindTerraform:
			res.fall(GuardTerraform, "the terraform block changed (required_providers, backend or settings)")
		case hb.kind == kindRefactor:
			// Handled below, from both sides.
		default:
			changed[key] = true
		}
	}
	for key, bb := range base.blocks {
		if _, ok := head.blocks[key]; ok {
			continue
		}
		switch bb.kind {
		case kindProvider:
			res.fall(GuardProvider, "a provider block was removed: "+key)
		case kindLive:
			res.fall(GuardLiveBlock, "the live block was removed")
		case kindTerraform:
			res.fall(GuardTerraform, "the terraform block was removed")
		case kindResource, kindData, kindModule:
			res.Deleted = append(res.Deleted, key)
		case kindVariable, kindLocal, kindOutput:
			// A removed value's readers changed too, so they are in the
			// head diff already; nothing to target by name.
		}
	}

	// tfvars: a variable whose assigned value differs is changed.
	for name := range diffVars(base.vars, head.vars) {
		changed["var."+name] = true
	}

	// Local module calls: any file under the source directory changed.
	for key, hb := range head.blocks {
		if hb.kind != kindModule {
			continue
		}
		src := hb.source
		if !strings.HasPrefix(src, "./") && !strings.HasPrefix(src, "../") {
			continue
		}
		modDir := path.Clean(path.Join(prefix, src))
		if rel, err := filepath.Rel(top, filepath.Join(top, filepath.FromSlash(modDir))); err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		for _, p := range changedFiles {
			if p == modDir || strings.HasPrefix(p, modDir+"/") {
				changed[key] = true
				break
			}
		}
	}

	// Refactor blocks added or changed, read from the head side; a removed
	// refactor block changes nothing a plan does.
	headRefactor := map[string]bool{}
	for _, rb := range head.refactors {
		if base.hasRefactor(rb) {
			continue
		}
		for _, a := range rb.addrs {
			headRefactor[a] = true
		}
		for _, a := range rb.mustPlace {
			if !head.places(a) {
				res.fall(GuardUnplaced, fmt.Sprintf("a %s block names %s, which no block in the head configuration declares", rb.kind, a))
			}
		}
	}
	res.Refactor = sortedKeys(headRefactor)

	// Reach: descendants in the head graph.
	reach := head.descendants(changed)
	for _, a := range res.Refactor {
		if _, ok := head.blocks[a]; ok {
			for k := range head.descendants(map[string]bool{a: true}) {
				reach[k] = true
			}
		}
	}

	// Co-managed type pairs.
	widened := map[string]bool{}
	for k := range reach {
		hb, ok := head.blocks[k]
		if !ok || hb.kind != kindResource {
			continue
		}
		for _, other := range coManaged[hb.typ] {
			for k2, b2 := range head.blocks {
				if b2.kind == kindResource && b2.typ == other && !reach[k2] {
					widened[k2] = true
				}
			}
		}
	}
	for k := range widened {
		reach[k] = true
	}
	res.Widened = sortedKeys(widened)
	res.Changed = sortedKeys(changed)
	sort.Strings(res.Deleted)
	res.Reach = sortedKeys(reach)

	targets := map[string]bool{}
	for k := range reach {
		if hb, ok := head.blocks[k]; ok && hb.targetable() {
			targets[k] = true
		}
	}
	for _, d := range res.Deleted {
		targets[d] = true
	}
	for _, a := range res.Refactor {
		targets[a] = true
	}
	res.Targets = sortedKeys(targets)

	for _, b := range head.blocks {
		if b.targetable() {
			res.Blocks++
		}
	}
	res.ConeBlocks = len(targets)
	switch {
	case len(res.Targets) == 0:
		res.fall(GuardEmpty, "the diff reaches no resource, data source or module call")
	case res.Blocks > 0 && float64(res.ConeBlocks) > share*float64(res.Blocks):
		res.fall(GuardShare, fmt.Sprintf("the cone holds %d of the estate's %d blocks, over the %.0f%% limit", res.ConeBlocks, res.Blocks, share*100))
	}
	return res, nil
}

// coManaged is the type-pair table: a type whose live state another type
// also writes (inline versus separate rules), so a change to one widens the
// cone to every block of the other. Symmetric by construction below.
var coManagedPairs = [][2]string{
	{"aws_security_group", "aws_security_group_rule"},
	{"aws_security_group", "aws_vpc_security_group_ingress_rule"},
	{"aws_security_group", "aws_vpc_security_group_egress_rule"},
	{"aws_route_table", "aws_route"},
	{"aws_default_route_table", "aws_route"},
	{"aws_network_acl", "aws_network_acl_rule"},
	{"aws_default_network_acl", "aws_network_acl_rule"},
	{"aws_iam_role", "aws_iam_role_policy"},
	{"aws_iam_role", "aws_iam_role_policy_attachment"},
	{"aws_iam_role", "aws_iam_role_policies_exclusive"},
	{"aws_iam_user", "aws_iam_user_policy"},
	{"aws_iam_group", "aws_iam_group_policy"},
	{"aws_s3_bucket", "aws_s3_bucket_policy"},
	{"aws_lb_listener", "aws_lb_listener_rule"},
	{"kubernetes_manifest", "kubernetes_labels"},
	{"kubernetes_manifest", "kubernetes_annotations"},
}

var coManaged = func() map[string][]string {
	m := map[string][]string{}
	for _, p := range coManagedPairs {
		m[p[0]] = append(m[p[0]], p[1])
		m[p[1]] = append(m[p[1]], p[0])
	}
	return m
}()

// CoManaged exposes the table for tests and docs.
func CoManaged(typ string) []string { return coManaged[typ] }

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir, "-c", "core.quotepath=off"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

// changedPaths lists the repository paths (from the top) that differ
// between base and the working tree, untracked files included.
func changedPaths(ctx context.Context, dir, base string) ([]string, error) {
	top, err := gitOut(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	top = strings.TrimSpace(top)
	diff, err := gitOut(ctx, top, "diff", "--no-ext-diff", "--name-only", "-z", base)
	if err != nil {
		return nil, err
	}
	untracked, err := gitOut(ctx, top, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, s := range strings.Split(diff+untracked, "\x00") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

func isConfigName(name string) bool {
	for _, ext := range []string{".tf", ".tofu", ".tf.json", ".tofu.json", ".tfvars", ".tfvars.json"} {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

type file struct {
	name string
	src  []byte
}

// baseConfigFiles reads the root's configuration files at base. prefix is
// the root's path from the repository top (rev-parse --show-prefix, which
// git computes from the real path, so a symlinked working directory names
// the same subdirectory), and both reads name it that way. Without
// --full-tree, ls-tree run from a subdirectory filters the listing by the
// working directory as well, so a root below the repository top listed
// nothing and read as an empty base module (#2062). exists is false when
// the root's path is not a directory at base.
func baseConfigFiles(ctx context.Context, dir, base, prefix string) (files []file, exists bool, err error) {
	if _, err := gitOut(ctx, dir, "cat-file", "-e", base+":"+prefix); err != nil {
		return nil, false, nil
	}
	list, err := gitOut(ctx, dir, "ls-tree", "--full-tree", "--name-only", "-z", base+":"+prefix)
	if err != nil {
		// The path is a file at base, not a directory.
		return nil, false, nil
	}
	var out []file
	for _, name := range strings.Split(list, "\x00") {
		if name == "" || !isConfigName(name) {
			continue
		}
		src, err := gitOut(ctx, dir, "show", base+":"+prefix+name)
		if err != nil {
			return nil, true, err
		}
		out = append(out, file{name: name, src: []byte(src)})
	}
	return out, true, nil
}

func headConfigFiles(dir string) ([]file, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []file
	for _, e := range entries {
		if e.IsDir() || !isConfigName(e.Name()) {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, file{name: e.Name(), src: src})
	}
	return out, nil
}

// The tfvars a plan reads without being told: terraform.tfvars and
// *.auto.tfvars. A -var-file is not seen, which the docs say.
func autoVars(name string) bool {
	return name == "terraform.tfvars" || strings.HasSuffix(name, ".auto.tfvars")
}

func diffVars(a, b map[string]string) map[string]bool {
	out := map[string]bool{}
	for k, v := range a {
		if b[k] != v {
			out[k] = true
		}
	}
	for k, v := range b {
		if a[k] != v {
			out[k] = true
		}
	}
	return out
}

// MissedUpdates is the claim's check (GitHub issue #1956): given the
// addresses a full plan changes and the cone's targets, it returns the
// changes no cone target covers. A cone that drops a dependent leaves that
// dependent's update here; an empty answer is the cone holding.
func MissedUpdates(fullChanges, targets []string) []string {
	in := map[string]bool{}
	for _, t := range targets {
		in[stripKeys(t)] = true
	}
	var out []string
	for _, c := range fullChanges {
		if !in[stripKeys(c)] {
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}
