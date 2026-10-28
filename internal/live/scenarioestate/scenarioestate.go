// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package scenarioestate renders a stock estate of any size from a
// hand-written scenario tree already in this repository, for the tests and
// smoke scenarios that need an estate of N resources to run against
// (choudoufu#2303, ruling A on INTENTIUS/chant#4586).
//
// Those tests used to generate a synthetic terralith with tools/terralith-gen.
// The ruling keeps the terralith for the scale ladder only (live/gauntlet-
// scale.json, run when a scale figure is re-quoted). Tests run against a
// scenario estate: a tree somebody wrote to look like a real estate, copied
// scale times.
//
// The default tree is live/e2e/estate, the flagship fixture: VPC, subnets,
// routes, security group rules, EC2, an ALB, ACM, Route 53, IAM, ECS, KMS,
// S3 with its children, DynamoDB, SQS-free messaging, SNS, Step Functions,
// CloudWatch and SSM receipts, with count, for_each and a conditional. The
// tree carries its own markers; a render strips them, so the output is the
// stock estate a stranger would have before adoption, the same as the
// terralith was.
//
// A render of scale N writes, for each original file, one file holding N
// copies of its resources. Copy k (0000, 0001, ...) has every resource
// label and local suffixed "_k", every reference rewritten to match, and
// the tree's estate name ("live-e2e") replaced by "<prefix>-<k>" in every
// string, so two renders with different prefixes coexist in one account.
// variables.tf is written once. versions.tf is written fresh: the terraform
// block first (with the `required_version = ">= 1.5.0"` line the tests
// anchor a live block on) and the provider block last, in the shape
// tools/terralith-gen used, so a caller that rewrote terralith-gen's
// provider block rewrites this one the same way. The tree's
// .terraform.lock.hcl is copied beside it.
package scenarioestate

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// DefaultTree is the scenario tree a render copies unless told otherwise,
// relative to the repository root.
const DefaultTree = "live/e2e/estate"

// TreeEstateName is the token the default tree spells its own name with in
// every client-chosen name ("tofu-live-e2e-app", "live-e2e.example.com").
// A render replaces it with "<prefix>-<k>".
const TreeEstateName = "live-e2e"

// Options says what to render.
type Options struct {
	// Tree is the scenario tree's directory. Empty means DefaultTree under
	// RepoRoot.
	Tree     string
	RepoRoot string
	// Scale is how many copies of the tree to write; at least 1.
	Scale int
	// Prefix replaces TreeEstateName in copy k as "<Prefix>-<k>". Lowercase
	// letters, digits and dashes, starting with a letter: it lands in
	// bucket names and a Route 53 zone name.
	Prefix string
	// Out is the directory to write; it is created and must be empty.
	Out string
}

// Summary is what a render wrote.
type Summary struct {
	Tree  string `json:"tree"`
	Scale int    `json:"scale"`
	// Resources is the number of resource instances the estate declares
	// (count and for_each expanded), PerCopy times Scale.
	Resources int `json:"resources"`
	PerCopy   int `json:"per_copy"`
	// Taggable is how many of those instances carry a tags argument, the
	// ones adoption stamps a marker on; the rest compose their identity
	// from a parent.
	Taggable int `json:"taggable"`
}

func (s Summary) String() string {
	return fmt.Sprintf("scenario estate %s x%d: %d resources (%d per copy), %d taggable", s.Tree, s.Scale, s.Resources, s.PerCopy, s.Taggable)
}

var prefixPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,15}$`)

// markerLine is one ownership marker in a tags object, which a stock
// estate never carries.
var markerLine = regexp.MustCompile(`(?m)^[ \t]*tofu-(estate|address)[ \t]*=.*\n`)

// CopyLabel is the label copy k gives a block labelled name.
func CopyLabel(name string, k int) string { return fmt.Sprintf("%s_%04d", name, k) }

// CopyName is the estate name copy k writes in place of TreeEstateName.
func CopyName(prefix string, k int) string { return fmt.Sprintf("%s-%04d", prefix, k) }

// Render writes the estate and says what it wrote.
func Render(o Options) (Summary, error) {
	var none Summary
	if o.Scale < 1 {
		return none, fmt.Errorf("scale %d: a scenario estate has at least one copy", o.Scale)
	}
	if !prefixPattern.MatchString(o.Prefix) {
		return none, fmt.Errorf("prefix %q: lowercase letters, digits and dashes, starting with a letter, at most 16 characters (it lands in bucket names and a Route 53 zone name, and the provider lowercases a record's name)", o.Prefix)
	}
	tree := o.Tree
	if tree == "" {
		tree = filepath.Join(o.RepoRoot, filepath.FromSlash(DefaultTree))
	}
	files, err := readTree(tree)
	if err != nil {
		return none, err
	}
	perCopy, taggable, err := countInstances(files)
	if err != nil {
		return none, err
	}
	if err := os.MkdirAll(o.Out, 0o755); err != nil {
		return none, err
	}
	if entries, err := os.ReadDir(o.Out); err != nil {
		return none, err
	} else if len(entries) > 0 {
		return none, fmt.Errorf("%s is not empty; a render writes into an empty directory", o.Out)
	}

	renames := renamesOf(files)
	names := sortedNames(files)
	for _, name := range names {
		src := files[name]
		var out []byte
		switch name {
		case "versions.tf":
			out, err = versionsFile(src)
		case "variables.tf":
			out = markerLine.ReplaceAll(src, nil)
		default:
			out, err = copiesOf(name, src, renames, o.Prefix, o.Scale)
		}
		if err != nil {
			return none, err
		}
		if err := os.WriteFile(filepath.Join(o.Out, name), out, 0o600); err != nil {
			return none, err
		}
	}
	if lock, err := os.ReadFile(filepath.Join(tree, ".terraform.lock.hcl")); err == nil { //nolint:gosec // a tree in the checkout
		if err := os.WriteFile(filepath.Join(o.Out, ".terraform.lock.hcl"), lock, 0o600); err != nil {
			return none, err
		}
	}
	return Summary{
		Tree: filepath.ToSlash(relOrSelf(o.RepoRoot, tree)), Scale: o.Scale,
		Resources: perCopy * o.Scale, PerCopy: perCopy, Taggable: taggable * o.Scale,
	}, nil
}

// Count says what a render at scale would declare, without writing it.
func Count(repoRoot, tree string, scale int) (Summary, error) {
	if tree == "" {
		tree = filepath.Join(repoRoot, filepath.FromSlash(DefaultTree))
	}
	files, err := readTree(tree)
	if err != nil {
		return Summary{}, err
	}
	perCopy, taggable, err := countInstances(files)
	if err != nil {
		return Summary{}, err
	}
	return Summary{Tree: filepath.ToSlash(relOrSelf(repoRoot, tree)), Scale: scale, Resources: perCopy * scale, PerCopy: perCopy, Taggable: taggable * scale}, nil
}

func relOrSelf(root, p string) string {
	if root == "" {
		return p
	}
	if r, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return p
}

func readTree(tree string) (map[string][]byte, error) {
	entries, err := os.ReadDir(tree)
	if err != nil {
		return nil, fmt.Errorf("reading the scenario tree: %w", err)
	}
	files := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() {
			return nil, fmt.Errorf("the scenario tree %s holds a directory (%s); a render copies a flat root and has no module source to rewrite", tree, e.Name())
		}
		if !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(tree, e.Name())) //nolint:gosec // a tree in the checkout
		if err != nil {
			return nil, err
		}
		files[e.Name()] = b
	}
	if _, ok := files["versions.tf"]; !ok {
		return nil, fmt.Errorf("the scenario tree %s has no versions.tf", tree)
	}
	return files, nil
}

func sortedNames(files map[string][]byte) []string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// renames is every name a copy suffixes: [type, name] for each resource and
// data source, and ["local", name] for each local.
func renamesOf(files map[string][]byte) [][]string {
	var out [][]string
	for _, name := range sortedNames(files) {
		if name == "versions.tf" || name == "variables.tf" {
			continue
		}
		f, diags := hclwrite.ParseConfig(files[name], name, hcl.InitialPos)
		if diags.HasErrors() {
			continue // copiesOf reports it
		}
		for _, b := range f.Body().Blocks() {
			switch b.Type() {
			case "resource":
				out = append(out, b.Labels())
			case "data":
				out = append(out, append([]string{"data"}, b.Labels()...))
			case "locals":
				for n := range b.Body().Attributes() {
					out = append(out, []string{"local", n})
				}
			}
		}
	}
	return out
}

// copiesOf is one file's resources, scale times over.
func copiesOf(name string, src []byte, renames [][]string, prefix string, scale int) ([]byte, error) {
	stripped := markerLine.ReplaceAll(src, nil)
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# Rendered by internal/live/scenarioestate from the scenario tree's %s, %d copies.\n# Do not edit; render again.\n\n", name, scale)
	for k := 0; k < scale; k++ {
		text := bytes.ReplaceAll(stripped, []byte(TreeEstateName), []byte(CopyName(prefix, k)))
		f, diags := hclwrite.ParseConfig(text, name, hcl.InitialPos)
		if diags.HasErrors() {
			return nil, fmt.Errorf("%s: %s", name, diags.Error())
		}
		for _, b := range f.Body().Blocks() {
			switch b.Type() {
			case "resource", "data":
				labels := b.Labels()
				labels[len(labels)-1] = CopyLabel(labels[len(labels)-1], k)
				b.SetLabels(labels)
			case "locals":
				// Relabelled after renameIn below: an attribute set from raw
				// tokens carries no traversals for RenameVariablePrefix to find.
			case "terraform", "provider", "variable", "output", "module", "moved", "import", "removed", "check":
				return nil, fmt.Errorf("%s holds a %s block; a scenario tree keeps those in versions.tf or variables.tf, and a render copies resources, data sources and locals only", name, b.Type())
			}
		}
		renameIn(f.Body(), renames, k)
		for _, b := range f.Body().Blocks() {
			if b.Type() != "locals" {
				continue
			}
			attrs := b.Body().Attributes()
			names := make([]string, 0, len(attrs))
			for n := range attrs {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				b.Body().SetAttributeRaw(CopyLabel(n, k), attrs[n].Expr().BuildTokens(nil))
				b.Body().RemoveAttribute(n)
			}
		}
		buf.Write(hclwrite.Format(f.Bytes()))
		buf.WriteString("\n")
	}
	return buf.Bytes(), nil
}

// renameIn rewrites every reference in body, nested blocks included.
func renameIn(body *hclwrite.Body, renames [][]string, k int) {
	for _, a := range body.Attributes() {
		for _, r := range renames {
			to := append([]string(nil), r...)
			to[len(to)-1] = CopyLabel(to[len(to)-1], k)
			a.Expr().RenameVariablePrefix(r, to)
		}
	}
	for _, b := range body.Blocks() {
		renameIn(b.Body(), renames, k)
	}
}

// versionsFile is the render's versions.tf: the tree's required_providers,
// a required_version line, then a provider block.
func versionsFile(treeVersions []byte) ([]byte, error) {
	f, diags := hclwrite.ParseConfig(treeVersions, "versions.tf", hcl.InitialPos)
	if diags.HasErrors() {
		return nil, fmt.Errorf("versions.tf: %s", diags.Error())
	}
	var awsVersion string
	for _, b := range f.Body().Blocks() {
		if b.Type() != "terraform" {
			continue
		}
		for _, rp := range b.Body().Blocks() {
			if rp.Type() != "required_providers" {
				continue
			}
			if a := rp.Body().GetAttribute("aws"); a != nil {
				m := regexp.MustCompile(`version\s*=\s*"([^"]+)"`).FindStringSubmatch(string(a.Expr().BuildTokens(nil).Bytes()))
				if m != nil {
					awsVersion = m[1]
				}
			}
		}
	}
	if awsVersion == "" {
		return nil, fmt.Errorf("versions.tf: no hashicorp/aws version in required_providers")
	}
	return []byte(fmt.Sprintf(`# Rendered by internal/live/scenarioestate. The terraform block comes
# first and the provider block last, the layout tools/terralith-gen used,
# so a caller can put a live block after required_version and replace the
# provider block from its first line to the end of the file.

terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = %q
    }
  }
}

provider "aws" {
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}
`, awsVersion)), nil
}

// countInstances counts one copy's resource instances, count and for_each
// expanded, and how many of them carry a tags argument. It evaluates count
// and for_each against the variables' defaults and the locals; a for_each
// over another resource takes that resource's own instance count.
func countInstances(files map[string][]byte) (instances, taggable int, err error) {
	type block struct {
		addr string
		body *hclsyntax.Body
	}
	var resources []block
	vars := map[string]cty.Value{}
	localExprs := map[string]hclsyntax.Expression{}
	for _, name := range sortedNames(files) {
		f, diags := hclsyntax.ParseConfig(files[name], name, hcl.InitialPos)
		if diags.HasErrors() {
			return 0, 0, fmt.Errorf("%s: %s", name, diags.Error())
		}
		for _, b := range f.Body.(*hclsyntax.Body).Blocks {
			switch b.Type {
			case "resource":
				resources = append(resources, block{addr: b.Labels[0] + "." + b.Labels[1], body: b.Body})
			case "variable":
				if a, ok := b.Body.Attributes["default"]; ok {
					v, d := a.Expr.Value(nil)
					if d.HasErrors() {
						return 0, 0, fmt.Errorf("variable %s default: %s", b.Labels[0], d.Error())
					}
					vars[b.Labels[0]] = v
				}
			case "locals":
				for n, a := range b.Body.Attributes {
					localExprs[n] = a.Expr
				}
			}
		}
	}
	ctx := &hcl.EvalContext{Variables: map[string]cty.Value{"var": cty.ObjectVal(vars)}}
	locals := map[string]cty.Value{}
	for n, e := range localExprs {
		v, d := e.Value(ctx)
		if d.HasErrors() {
			continue // a local the counts do not need, such as one that reads a resource
		}
		locals[n] = v
	}
	ctx.Variables["local"] = cty.ObjectVal(locals)

	counts := map[string]int{}
	var resolve func(b block, depth int) (int, error)
	byAddr := map[string]block{}
	for _, b := range resources {
		byAddr[b.addr] = b
	}
	resolve = func(b block, depth int) (int, error) {
		if n, ok := counts[b.addr]; ok {
			return n, nil
		}
		if depth > len(resources) {
			return 0, fmt.Errorf("%s: for_each chain does not end", b.addr)
		}
		n := 1
		if a, ok := b.body.Attributes["count"]; ok {
			v, d := a.Expr.Value(ctx)
			if d.HasErrors() || v.IsMarked() || !v.IsKnown() || v.IsNull() || !v.Type().Equals(cty.Number) {
				return 0, fmt.Errorf("%s: count is not a number this render can evaluate", b.addr)
			}
			bf := v.AsBigFloat()
			i, _ := bf.Int64()
			n = int(i)
		} else if a, ok := b.body.Attributes["for_each"]; ok {
			if st, isTrav := a.Expr.(*hclsyntax.ScopeTraversalExpr); isTrav && len(st.Traversal) == 2 && strings.HasPrefix(st.Traversal.RootName(), "aws_") {
				attr, _ := st.Traversal[1].(hcl.TraverseAttr)
				target, ok := byAddr[st.Traversal.RootName()+"."+attr.Name]
				if !ok {
					return 0, fmt.Errorf("%s: for_each names a resource the tree does not declare", b.addr)
				}
				m, err := resolve(target, depth+1)
				if err != nil {
					return 0, err
				}
				n = m
			} else {
				v, d := a.Expr.Value(ctx)
				if d.HasErrors() || v.IsMarked() || !v.IsKnown() || v.IsNull() || !v.CanIterateElements() {
					return 0, fmt.Errorf("%s: for_each is not a collection this render can evaluate", b.addr)
				}
				n = v.LengthInt()
			}
		}
		counts[b.addr] = n
		return n, nil
	}
	for _, b := range resources {
		n, err := resolve(b, 0)
		if err != nil {
			return 0, 0, err
		}
		instances += n
		if _, ok := b.body.Attributes["tags"]; ok {
			taggable += n
		}
	}
	return instances, taggable, nil
}
