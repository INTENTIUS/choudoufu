// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/instances"
	"github.com/intentius/choudoufu/internal/live/staticeval"
	"github.com/intentius/choudoufu/internal/live/substrate"
)

// gcpProjectSite is one block that names a GCP project: a google provider
// block's own project argument, or a google resource's.
type gcpProjectSite struct {
	project string
	block   string
	module  addrs.Module
	subject hcl.Range
}

// checkGCPMultiProject warns when the estate's google blocks name more than
// one GCP project (GitHub issue #1121's fence ruling). The fence is
// project-scoped, because an IAM condition cannot match a label: one estate
// is fenced by owning its project, and an estate spread over two projects
// cannot be fenced as one.
//
// It reads what configuration alone states: the project argument of every
// google and google-beta provider block, aliases included, and of every
// managed resource those providers serve. A resource with no project
// argument takes its provider block's, which is already counted.
//
// A resource's project is read per instance (GitHub issue #2145): on a
// count or for_each block whose collection is known from configuration, a
// project written with count.index, each.key or each.value is evaluated
// once for each instance. A project written as another resource's
// argument, such as a created project's project_id, is that argument when
// the other block's configuration states it.
//
// What still cannot be known before apply is not counted: a variable with
// no default, another resource's argument its block does not state or
// only apply fills, a data source or module output, a count or for_each collection that cannot be read, and a
// provider block with its own for_each, whose project may differ per key.
// The warning fails open on those, the default every lint rule gives an
// unknown, but when it does fire it names each one, so the reader knows
// the list of projects may be short. A project named only in the
// environment (GOOGLE_PROJECT and its siblings) is never seen here.
//
// One warning for the whole configuration, not one per block: the finding
// is about the estate, and every block it names is part of it. It points at
// the first block in source order.
//
// It takes no [Context.Scope]: a narrowed run plans fewer blocks, but the
// estate it belongs to spans the same projects whatever the run targets.
func checkGCPMultiProject(ctx context.Context, cfg *configs.Config, issues *[]Issue) {
	var sites []gcpProjectSite
	var unknown []gcpProjectUnknown
	cfg.DeepEach(func(c *configs.Config) {
		if c.Module == nil {
			return
		}
		ps, pu := gcpProviderProjects(ctx, c)
		rs, ru := gcpResourceProjects(ctx, c)
		sites = append(sites, ps...)
		sites = append(sites, rs...)
		unknown = append(unknown, pu...)
		unknown = append(unknown, ru...)
	})

	byProject := map[string][]gcpProjectSite{}
	for _, s := range sites {
		byProject[s.project] = append(byProject[s.project], s)
	}
	if len(byProject) < 2 {
		return
	}

	projects := make([]string, 0, len(byProject))
	for p := range byProject {
		projects = append(projects, p)
	}
	sort.Strings(projects)

	sort.SliceStable(sites, func(i, j int) bool { return rangeLess(sites[i].subject, sites[j].subject) })
	first := sites[0]

	parts := make([]string, 0, len(projects))
	for _, p := range projects {
		blocks := make([]string, 0, len(byProject[p]))
		for _, s := range byProject[p] {
			blocks = append(blocks, s.block)
		}
		sort.Strings(blocks)
		parts = append(parts, fmt.Sprintf("%q by %s", p, strings.Join(blocks, ", ")))
	}

	detail := fmt.Sprintf(
		"This configuration's google blocks name %d projects: %s. "+
			"An estate's fence is its project, because an IAM condition cannot match a label, so an estate spread over "+
			"two projects cannot be fenced as one. Point every google block at one project, or split the configuration into "+
			"one estate per project.",
		len(projects), strings.Join(parts, "; "),
	)
	if len(unknown) == 0 {
		detail += " A project this pass cannot read from configuration alone is not counted; this configuration has none."
	} else {
		sort.SliceStable(unknown, func(i, j int) bool { return unknown[i].block < unknown[j].block })
		named := make([]string, 0, len(unknown))
		for _, u := range unknown {
			named = append(named, fmt.Sprintf("%s (%s)", u.block, u.reason))
		}
		detail += fmt.Sprintf(" Not counted, because their project cannot be known before apply: %s. Each may name yet another project.", strings.Join(named, "; "))
	}

	*issues = append(*issues, Issue{
		Rule:      RuleGCPMultiProject,
		Construct: fmt.Sprintf("google projects %s", strings.Join(quoteAll(projects), ", ")),
		Module:    first.module,
		Detail:    detail,
		Subject:   first.subject,
	})
}

// gcpProjectUnknown is one block whose project argument is set and cannot
// be read before apply, and why.
type gcpProjectUnknown struct {
	block  string
	reason string
}

// gcpProviderProjects is every google provider block in c's own module
// whose project argument evaluates statically, and every one that sets a
// project this pass cannot read.
func gcpProviderProjects(ctx context.Context, c *configs.Config) ([]gcpProjectSite, []gcpProjectUnknown) {
	var out []gcpProjectSite
	var unknown []gcpProjectUnknown
	for _, pc := range c.Module.ProviderConfigs {
		fqn := c.Module.ProviderForLocalConfig(addrs.LocalProviderConfig{LocalName: pc.Name})
		if !isGoogleProvider(fqn) || pc.Config == nil {
			continue
		}
		block := fmt.Sprintf("provider %q", pc.Name)
		if pc.Alias != "" {
			block = fmt.Sprintf("provider %q (alias %q)", pc.Name, pc.Alias)
		}
		if !c.Path.IsRoot() {
			block = c.Path.String() + " " + block
		}
		attr, ok := gcpProjectAttr(pc.Config)
		if !ok {
			continue
		}
		if pc.ForEach != nil {
			unknown = append(unknown, gcpProjectUnknown{block: block, reason: "a provider block with its own for_each, whose project may differ per key"})
			continue
		}
		project, reason := gcpScopedProject(ctx, c.Module, attr, c.Path, "provider."+pc.Name+".project", instances.RepetitionData{})
		if reason != "" {
			unknown = append(unknown, gcpProjectUnknown{block: block, reason: reason})
			continue
		}
		out = append(out, gcpProjectSite{project: project, block: block, module: c.Path, subject: pc.DeclRange})
	}
	return out, unknown
}

// gcpResourceProjects is every google managed resource instance in c's own
// module whose project argument can be read before apply, and every block
// whose project cannot. See [checkGCPMultiProject] for what is read.
func gcpResourceProjects(ctx context.Context, c *configs.Config) ([]gcpProjectSite, []gcpProjectUnknown) {
	var out []gcpProjectSite
	var unknown []gcpProjectUnknown
	for _, rc := range c.Module.ManagedResources {
		if !isGoogleProvider(rc.Provider) || rc.Config == nil {
			continue
		}
		attr, ok := gcpProjectAttr(rc.Config)
		if !ok {
			continue
		}
		addr := addrs.ConfigResource{Module: c.Path, Resource: rc.Addr()}.String()
		site := func(project, block string) gcpProjectSite {
			return gcpProjectSite{project: project, block: block, module: c.Path, subject: rc.DeclRange}
		}

		if project, reason, ok := gcpReferencedProject(ctx, c.Module, attr.Expr); ok {
			if reason != "" {
				unknown = append(unknown, gcpProjectUnknown{block: addr, reason: reason})
			} else {
				out = append(out, site(project, addr))
			}
			continue
		}

		if root, bad := staticeval.FirstDisallowedScoped(attr.Expr); bad {
			unknown = append(unknown, gcpProjectUnknown{block: addr, reason: fmt.Sprintf("its project refers to %s, which is known only after apply", root)})
			continue
		}

		reps, keys, reason := gcpInstanceReps(ctx, c.Module, rc, attr.Expr)
		if reason != "" {
			unknown = append(unknown, gcpProjectUnknown{block: addr, reason: reason})
			continue
		}
		for i, rep := range reps {
			block := addr + keys[i]
			project, reason := gcpScopedProject(ctx, c.Module, attr, c.Path, addr+".project", rep)
			if reason != "" {
				unknown = append(unknown, gcpProjectUnknown{block: block, reason: reason})
				continue
			}
			out = append(out, site(project, block))
		}
	}
	return out, unknown
}

// gcpInstanceReps is one repetition per instance whose project must be read
// separately, with the instance key each one prints as. A project that
// reads no count or each value is one value for every instance, so it is
// read once, unkeyed. reason is non-empty when the instances cannot be
// known before apply.
func gcpInstanceReps(ctx context.Context, mod *configs.Module, rc *configs.Resource, expr hcl.Expression) ([]instances.RepetitionData, []string, string) {
	perInstance := false
	for _, trav := range expr.Variables() {
		if root := trav.RootName(); root == "count" || root == "each" {
			perInstance = true
		}
	}
	if !perInstance {
		return []instances.RepetitionData{{}}, []string{""}, ""
	}
	switch {
	case rc.ForEach != nil:
		elems, ok := staticeval.ForEachElements(ctx, mod, rc.ForEach)
		if !ok {
			return nil, nil, "its project is read per instance, and its for_each collection cannot be read before apply"
		}
		keys := make([]string, 0, len(elems))
		for k := range elems {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		reps := make([]instances.RepetitionData, 0, len(keys))
		printed := make([]string, 0, len(keys))
		for _, k := range keys {
			reps = append(reps, instances.RepetitionData{EachKey: cty.StringVal(k), EachValue: elems[k]})
			printed = append(printed, fmt.Sprintf("[%q]", k))
		}
		return reps, printed, ""
	case rc.Count != nil:
		n, ok := staticeval.Count(ctx, mod, rc.Count)
		if !ok {
			return nil, nil, "its project is read per instance, and its count cannot be read before apply"
		}
		reps := make([]instances.RepetitionData, 0, n)
		printed := make([]string, 0, n)
		for i := 0; i < n; i++ {
			reps = append(reps, instances.RepetitionData{CountIndex: cty.NumberIntVal(int64(i))})
			printed = append(printed, fmt.Sprintf("[%d]", i))
		}
		return reps, printed, ""
	default:
		return nil, nil, "its project refers to count or each on a block with neither"
	}
}

// gcpReferencedProject reads a project written as a reference to another
// managed resource's own argument in the same module, TYPE.NAME.ARG, from
// what that block's configuration states: google_project.p.project_id is
// p's project_id argument when configuration gives it. ok is false for any
// other expression. reason is non-empty when the referenced argument is not
// known before apply: the block has count or for_each, does not set it, or
// sets it to something this pass cannot read. The provider fills such an
// attribute at apply, so it is named, not guessed.
func gcpReferencedProject(ctx context.Context, mod *configs.Module, expr hcl.Expression) (project, reason string, ok bool) {
	trav, diags := hcl.AbsTraversalForExpr(expr)
	if diags.HasErrors() || len(trav) != 3 {
		return "", "", false
	}
	name, nameOK := trav[1].(hcl.TraverseAttr)
	arg, argOK := trav[2].(hcl.TraverseAttr)
	if !nameOK || !argOK {
		return "", "", false
	}
	key := trav.RootName() + "." + name.Name
	ref := mod.ManagedResources[key]
	if ref == nil || ref.Config == nil {
		return "", "", false
	}
	unknown := fmt.Sprintf("its project is %s.%s, which is known only after apply", key, arg.Name)
	if ref.Count != nil || ref.ForEach != nil {
		return "", unknown, true
	}
	if v, ok := gcpStaticArg(ctx, mod, ref.Config, arg.Name, key+"."+arg.Name); ok {
		return v, "", true
	}
	return "", unknown, true
}

// gcpProjectAttr is body's project argument, when it sets one.
func gcpProjectAttr(body hcl.Body) (*hcl.Attribute, bool) {
	content, _, diags := body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{{Name: "project"}},
	})
	if diags.HasErrors() {
		return nil, false
	}
	attr, ok := content.Attributes["project"]
	return attr, ok
}

// gcpScopedProject evaluates a project argument for one instance, bound to
// rep, through the module's pure static evaluator. reason is non-empty
// when it cannot be read as a known, non-empty string before apply.
func gcpScopedProject(ctx context.Context, mod *configs.Module, attr *hcl.Attribute, path addrs.Module, subject string, rep instances.RepetitionData) (string, string) {
	if root, bad := staticeval.FirstDisallowedScoped(attr.Expr); bad {
		return "", fmt.Sprintf("its project refers to %s, which is known only after apply", root)
	}
	if mod.StaticEvaluator == nil {
		return "", "the configuration carries no static evaluator"
	}
	ident := configs.StaticIdentifier{Module: path, Subject: subject, DeclRange: attr.Range}
	val, diags, recovered := staticeval.Scoped(ctx, mod.StaticEvaluator.Pure(), attr.Expr, ident, rep, nil)
	if recovered != nil || diags.HasErrors() {
		return "", "its project cannot be read from configuration alone, such as a variable with no default"
	}
	if val.IsMarked() || val.IsNull() || !val.IsWhollyKnown() {
		return "", "its project is not a plain value known before apply"
	}
	str, err := convert.Convert(val, cty.String)
	if err != nil || str.AsString() == "" {
		return "", "its project is not a non-empty string"
	}
	return str.AsString(), ""
}

// isGoogleProvider reports whether fqn is served by [substrate.Google].
func isGoogleProvider(fqn addrs.Provider) bool {
	sub, ok := substrate.ForProvider(fqn.Type)
	return ok && sub == substrate.Google
}

// gcpStaticArg reads body's argument name from configuration alone,
// through the module's pure static evaluator: constants, variables with
// defaults, locals and functions. ok is false for an absent argument and
// for anything it cannot read as a known, non-empty string.
func gcpStaticArg(ctx context.Context, mod *configs.Module, body hcl.Body, name, subject string) (string, bool) {
	content, _, diags := body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{{Name: name}},
	})
	if diags.HasErrors() {
		return "", false
	}
	attr, ok := content.Attributes[name]
	if !ok || mod.StaticEvaluator == nil {
		return "", false
	}
	if _, bad := staticeval.FirstDisallowed(attr.Expr); bad {
		return "", false
	}
	val, ok := staticeval.EvaluateOK(ctx, mod.StaticEvaluator.Pure(), attr.Expr, subject)
	if !ok || val.IsMarked() || val.IsNull() || !val.IsWhollyKnown() {
		return "", false
	}
	str, err := convert.Convert(val, cty.String)
	if err != nil || str.AsString() == "" {
		return "", false
	}
	return str.AsString(), true
}

// rangeLess orders two source ranges by file, then position.
func rangeLess(a, b hcl.Range) bool {
	if a.Filename != b.Filename {
		return a.Filename < b.Filename
	}
	return a.Start.Byte < b.Start.Byte
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}
