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
// argument takes its provider block's, which is already counted. A value
// this pass cannot evaluate statically (a variable with no default, a
// reference to another resource, each.value) is skipped silently, the
// default every lint rule gives an unknown, and so is a provider block with
// its own for_each, whose project may differ per key. A project named only
// in the environment (GOOGLE_PROJECT and its siblings) is never seen here.
//
// One warning for the whole configuration, not one per block: the finding
// is about the estate, and every block it names is part of it. It points at
// the first block in source order.
//
// It takes no [Context.Scope]: a narrowed run plans fewer blocks, but the
// estate it belongs to spans the same projects whatever the run targets.
func checkGCPMultiProject(ctx context.Context, cfg *configs.Config, issues *[]Issue) {
	var sites []gcpProjectSite
	cfg.DeepEach(func(c *configs.Config) {
		if c.Module == nil {
			return
		}
		sites = append(sites, gcpProviderProjects(ctx, c)...)
		sites = append(sites, gcpResourceProjects(ctx, c)...)
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

	*issues = append(*issues, Issue{
		Rule:      RuleGCPMultiProject,
		Construct: fmt.Sprintf("google projects %s", strings.Join(quoteAll(projects), ", ")),
		Module:    first.module,
		Detail: fmt.Sprintf(
			"This configuration's google blocks name %d projects: %s. "+
				"An estate's fence is its project, because an IAM condition cannot match a label, so an estate spread over "+
				"two projects cannot be fenced as one. Point every google block at one project, or split the configuration into "+
				"one estate per project. A project this pass cannot read from configuration alone is not counted.",
			len(projects), strings.Join(parts, "; "),
		),
		Subject: first.subject,
	})
}

// gcpProviderProjects is every google provider block in c's own module
// whose project argument evaluates statically.
func gcpProviderProjects(ctx context.Context, c *configs.Config) []gcpProjectSite {
	var out []gcpProjectSite
	for _, pc := range c.Module.ProviderConfigs {
		fqn := c.Module.ProviderForLocalConfig(addrs.LocalProviderConfig{LocalName: pc.Name})
		if !isGoogleProvider(fqn) || pc.ForEach != nil || pc.Config == nil {
			continue
		}
		project, ok := gcpStaticProject(ctx, c.Module, pc.Config, "provider."+pc.Name+".project")
		if !ok {
			continue
		}
		block := fmt.Sprintf("provider %q", pc.Name)
		if pc.Alias != "" {
			block = fmt.Sprintf("provider %q (alias %q)", pc.Name, pc.Alias)
		}
		if !c.Path.IsRoot() {
			block = c.Path.String() + " " + block
		}
		out = append(out, gcpProjectSite{project: project, block: block, module: c.Path, subject: pc.DeclRange})
	}
	return out
}

// gcpResourceProjects is every google managed resource in c's own module
// whose own project argument evaluates statically.
func gcpResourceProjects(ctx context.Context, c *configs.Config) []gcpProjectSite {
	var out []gcpProjectSite
	for _, rc := range c.Module.ManagedResources {
		if !isGoogleProvider(rc.Provider) || rc.Config == nil {
			continue
		}
		addr := addrs.ConfigResource{Module: c.Path, Resource: rc.Addr()}.String()
		project, ok := gcpStaticProject(ctx, c.Module, rc.Config, addr+".project")
		if !ok {
			continue
		}
		out = append(out, gcpProjectSite{project: project, block: addr, module: c.Path, subject: rc.DeclRange})
	}
	return out
}

// isGoogleProvider reports whether fqn is served by [substrate.Google].
func isGoogleProvider(fqn addrs.Provider) bool {
	sub, ok := substrate.ForProvider(fqn.Type)
	return ok && sub == substrate.Google
}

// gcpStaticProject reads body's project argument from configuration alone,
// through the module's pure static evaluator: constants, variables with
// defaults, locals and functions. ok is false for an absent argument and
// for anything it cannot read as a known, non-empty string.
func gcpStaticProject(ctx context.Context, mod *configs.Module, body hcl.Body, subject string) (string, bool) {
	content, _, diags := body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{{Name: "project"}},
	})
	if diags.HasErrors() {
		return "", false
	}
	attr, ok := content.Attributes["project"]
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
