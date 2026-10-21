// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/instances"
	"github.com/intentius/choudoufu/internal/live/staticeval"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is the warning the Azure fence ruling asks for (GitHub issue
// #1122, 2026-10-19): on Azure an estate is a set of resource groups, and an
// estate that declares a resource in a group it does not own is told so by
// name.
//
// # What "the estate's groups" are
//
// The azurerm_resource_group blocks the configuration declares, and the
// azapi_resource blocks whose type is Microsoft.Resources/resourceGroups.
// Nothing else: the live block (internal/configs/live.go) has no place for
// per-platform settings, so there is no estate.chdf.hcl list of groups an
// estate owns without declaring them, and this check does not invent one.
// An estate that runs in a group someone else created gets the warning, and
// that is the point: the grant (live/azure/estate-grant.json) and the
// Policy guard (live/azure/estate-boundary.json) are applied at the groups
// an operator names, and a group the configuration does not declare is one
// nothing here can say was named.
//
// # Where it reads the group from
//
// azurerm's resource_group_name (and resource_group_id, an ARM id), and
// azapi_resource's parent_id. The expression is followed structurally
// before it is evaluated, because the common shapes are references that no
// static evaluation answers: azurerm_resource_group.this.name, a module
// input var.resource_group_name that the caller set to one, a local holding
// one, a module output, a data source. A reference to any managed resource
// the configuration declares is in the estate as far as this check goes:
// either it is one of the groups, or it is a resource whose own group this
// check reads separately (azurerm_virtual_network.this.resource_group_name,
// azapi_resource.vnet.id as a subnet's parent_id). A data source is
// followed into its own arguments, so data.azurerm_resource_group.x.name
// is the group its name argument names. What is left is evaluated
// statically and compared, case-insensitively, against the declared
// groups' names, because ARM resource group names are case-insensitive.
//
// It says nothing when it cannot tell: a value only known at apply, a
// declared group whose own name does not evaluate (any name could be that
// one), a parent_id with no resourceGroups segment (a subscription- or
// tenant-scoped resource, which is in no group at all). A warning that
// fired on what it could not read would fire on every module that takes
// its group as an input from an unread caller.
//
// A warning, not an error: the plan is correct and the resource is created
// where the configuration says. What is wrong is the fence around it.

// SummaryForeignResourceGroup is the warning [foreignResourceGroupWarnings]
// raises, one per resource group (or data source) the estate does not own.
const SummaryForeignResourceGroup = "Resource in a resource group this estate does not declare"

// azurermResourceGroupType is the azurerm type that declares a resource
// group, and the data source of the same name that reads one. Named once,
// here, because the ruling defines an estate by it (live/derivation_guard_test.go's
// typeLiteralSurfaces has the reason).
const azurermResourceGroupType = "azurerm_resource_group"

// azureGroupArgs are the arguments a declared resource names its resource
// group by, per provider. Each reads as a group name or as an ARM id.
var azureGroupArgs = []struct {
	prefix string
	arg    string
	isID   bool
}{
	{"azurerm_", "resource_group_name", false},
	{"azurerm_", "resource_group_id", true},
	{"azapi_resource", "parent_id", true},
}

// isAzureResourceGroupBlock reports whether rc declares a resource group:
// azurerm_resource_group, or an azapi_resource whose type is
// Microsoft.Resources/resourceGroups at any API version. ARM resource type
// names are case-insensitive, so the type is compared that way.
func isAzureResourceGroupBlock(ctx context.Context, mod *configs.Module, rc *configs.Resource) bool {
	switch rc.Type {
	case azurermResourceGroupType:
		return true
	case "azapi_resource":
		typ, reason := staticeval.Argument(ctx, mod, rc, "type")
		if reason != "" {
			return false
		}
		base, _, _ := strings.Cut(typ, "@")
		return strings.EqualFold(base, "Microsoft.Resources/resourceGroups")
	}
	return false
}

// groupVerdict is what following one expression established about the
// resource group it names.
type groupVerdict struct {
	// owned is true when the expression reaches a managed resource the
	// configuration declares.
	owned bool
	// name is the group's name when it evaluated to one; empty when it
	// did not.
	name string
	// via is the data source the name was read through, when there was
	// one, so the warning can say where to look.
	via string
}

// declaredGroups is every resource group name the configuration declares,
// lower-cased, and whether any declared group's name could not be read.
type declaredGroups struct {
	names   map[string]bool
	unknown bool
}

func (d *declaredGroups) has(name string) bool { return d.names[strings.ToLower(name)] }

// foreignResourceGroupWarnings walks the whole configuration tree and
// returns one [SummaryForeignResourceGroup] warning per resource group
// that a declared azurerm or azapi resource lives in and the configuration
// does not declare.
//
// scope is the run's -target / -exclude filter, nil for all; a resource
// outside it is not warned about, for the reason [Scope] gives.
func foreignResourceGroupWarnings(ctx context.Context, cfg *configs.Config, scope Scope) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if cfg == nil || cfg.Module == nil {
		return diags
	}

	groups := &declaredGroups{names: map[string]bool{}}
	var anyAzure bool
	walkConfigs(cfg, func(c *configs.Config) {
		for _, rc := range sortedResources(c.Module.ManagedResources) {
			if strings.HasPrefix(rc.Type, "azurerm_") || strings.HasPrefix(rc.Type, "azapi_") {
				anyAzure = true
			}
			if !isAzureResourceGroupBlock(ctx, c.Module, rc) {
				continue
			}
			names, ok := declaredNames(ctx, c.Module, rc)
			if !ok {
				groups.unknown = true
				continue
			}
			for _, n := range names {
				groups.names[strings.ToLower(n)] = true
			}
		}
	})
	if !anyAzure {
		return diags
	}

	// One finding per group, keyed case-insensitively, listing every
	// resource that lives there: a configuration built on a shared group
	// gets one warning naming it, not one per block.
	type finding struct {
		shown string
		via   string
		addrs []string
	}
	found := map[string]*finding{}
	walkConfigs(cfg, func(c *configs.Config) {
		for _, rc := range sortedResources(c.Module.ManagedResources) {
			if isAzureResourceGroupBlock(ctx, c.Module, rc) {
				continue
			}
			if scope != nil && !scope(addrs.ConfigResource{Module: c.Path, Resource: rc.Addr()}) {
				continue
			}
			for _, ga := range azureGroupArgs {
				if !strings.HasPrefix(rc.Type, ga.prefix) {
					continue
				}
				attr := blockAttr(rc.Config, ga.arg)
				if attr == nil {
					continue
				}
				v := followGroupExpr(ctx, c, attr.Expr, ga.isID, 0)
				if v.owned || v.name == "" {
					continue
				}
				if groups.has(v.name) || groups.unknown {
					continue
				}
				key := strings.ToLower(v.name)
				f := found[key]
				if f == nil {
					f = &finding{shown: v.name, via: v.via}
					found[key] = f
				}
				f.addrs = append(f.addrs, addrs.ConfigResource{Module: c.Path, Resource: rc.Addr()}.String())
				break
			}
		}
	})

	keys := make([]string, 0, len(found))
	for k := range found {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		f := found[k]
		sort.Strings(f.addrs)
		via := ""
		if f.via != "" {
			via = fmt.Sprintf(" (read through %s)", f.via)
		}
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Warning,
			SummaryForeignResourceGroup,
			fmt.Sprintf(
				"%s %s declared in resource group %q%s, and no azurerm_resource_group or azapi Microsoft.Resources/resourceGroups block in this configuration declares that group. On Azure an estate is the set of resource groups it declares: the estate's role assignment and its Policy guard are applied at those groups (live/azure/estate-grant.json, live/azure/estate-boundary.json), so a resource in another group is outside both. The estate's principal may be refused writing it, and anyone granted that group can change or delete it whatever its tofu-estate tag says. Declare the group in this configuration, or move the resource into one of the estate's groups. The plan is otherwise unaffected. See live/MARKERS.md, \"Granting an Azure estate\".",
				strings.Join(f.addrs, ", "), isOrAre(len(f.addrs)), f.shown, via,
			),
		))
	}
	return diags
}

func isOrAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// walkConfigs calls fn on cfg and every descendant, children in name order.
func walkConfigs(cfg *configs.Config, fn func(*configs.Config)) {
	if cfg == nil || cfg.Module == nil {
		return
	}
	fn(cfg)
	names := make([]string, 0, len(cfg.Children))
	for n := range cfg.Children {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		walkConfigs(cfg.Children[n], fn)
	}
}

// blockAttr reads one top-level argument out of a body, or nil.
func blockAttr(body hcl.Body, name string) *hcl.Attribute {
	if body == nil {
		return nil
	}
	content, _, diags := body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{{Name: name}},
	})
	if diags.HasErrors() || content == nil {
		return nil
	}
	return content.Attributes[name]
}

// declaredNames reads a declared group's name, once per instance when the
// block is expanded with count or for_each. ok is false when any instance's
// name cannot be read from configuration alone.
func declaredNames(ctx context.Context, mod *configs.Module, rc *configs.Resource) ([]string, bool) {
	switch {
	case rc.ForEach != nil:
		elems, ok := staticeval.ForEachElements(ctx, mod, rc.ForEach)
		if !ok {
			return nil, false
		}
		var out []string
		for k, v := range elems {
			n, reason := staticeval.ArgumentScoped(ctx, mod, rc, "name", instances.RepetitionData{
				EachKey:   cty.StringVal(k),
				EachValue: v,
			})
			if reason != "" {
				return nil, false
			}
			out = append(out, n)
		}
		return out, true
	case rc.Count != nil:
		n, ok := staticeval.Count(ctx, mod, rc.Count)
		if !ok {
			return nil, false
		}
		var out []string
		for i := 0; i < n; i++ {
			name, reason := staticeval.ArgumentScoped(ctx, mod, rc, "name", instances.RepetitionData{
				CountIndex: cty.NumberIntVal(int64(i)),
			})
			if reason != "" {
				return nil, false
			}
			out = append(out, name)
		}
		return out, true
	}
	n, reason := staticeval.Argument(ctx, mod, rc, "name")
	if reason != "" {
		return nil, false
	}
	return []string{n}, true
}

// maxGroupFollow bounds how many references [followGroupExpr] chases, so a
// local that refers to itself through a module ends rather than recursing.
const maxGroupFollow = 16

// followGroupExpr establishes what resource group expr, written in module
// c, names. isID says the argument is an ARM id rather than a group name.
func followGroupExpr(ctx context.Context, c *configs.Config, expr hcl.Expression, isID bool, depth int) groupVerdict {
	if depth > maxGroupFollow || expr == nil {
		return groupVerdict{}
	}
	if wrap, ok := expr.(*hclsyntax.TemplateWrapExpr); ok {
		expr = wrap.Wrapped
	}

	if trav, diags := hcl.AbsTraversalForExpr(expr); !diags.HasErrors() {
		if v, handled := followGroupTraversal(ctx, c, trav, isID, depth); handled {
			return v
		}
	}

	// Anything else: a literal, a template, a function over variables.
	// Evaluated statically, and read as a name or as an ARM id.
	if c.Module.StaticEvaluator == nil {
		return groupVerdict{}
	}
	if _, bad := staticeval.FirstDisallowed(expr); bad {
		return groupVerdict{}
	}
	val, hdiags, recovered := staticeval.Evaluate(ctx, c.Module.StaticEvaluator, expr, configs.StaticIdentifier{
		Module:    c.Path,
		Subject:   "resource group",
		DeclRange: expr.Range(),
	})
	if recovered != nil || hdiags.HasErrors() || val.IsNull() || !val.IsWhollyKnown() || val.IsMarked() {
		return groupVerdict{}
	}
	str, err := convert.Convert(val, cty.String)
	if err != nil {
		return groupVerdict{}
	}
	s := str.AsString()
	if isID {
		return groupVerdict{name: groupFromARMID(s)}
	}
	return groupVerdict{name: s}
}

// followGroupTraversal answers the reference shapes followGroupExpr
// follows structurally. handled is false for a traversal it does not
// recognise, which the caller then evaluates statically.
func followGroupTraversal(ctx context.Context, c *configs.Config, trav hcl.Traversal, isID bool, depth int) (groupVerdict, bool) {
	ref, diags := addrs.ParseRef(trav)
	if diags.HasErrors() {
		return groupVerdict{}, false
	}
	switch subject := ref.Subject.(type) {
	case addrs.InputVariable:
		if c.Parent == nil || len(c.Path) == 0 {
			// A root input: its value comes from the caller, which the
			// static evaluator already sees.
			return groupVerdict{}, false
		}
		call := c.Parent.Module.ModuleCalls[c.Path[len(c.Path)-1]]
		if call == nil {
			return groupVerdict{}, false
		}
		attr := blockAttr(call.Config, subject.Name)
		if attr == nil {
			// Unset by the caller: the variable's default, which the
			// static evaluator reads.
			return groupVerdict{}, false
		}
		if len(ref.Remaining) != 0 {
			return groupVerdict{}, true
		}
		return followGroupExpr(ctx, c.Parent, attr.Expr, isID, depth+1), true

	case addrs.LocalValue:
		local := c.Module.Locals[subject.Name]
		if local == nil || len(ref.Remaining) != 0 {
			return groupVerdict{}, false
		}
		return followGroupExpr(ctx, c, local.Expr, isID, depth+1), true

	case addrs.ModuleCallInstanceOutput:
		child := c.Children[subject.Call.Call.Name]
		if child == nil || child.Module == nil {
			return groupVerdict{}, true
		}
		out := child.Module.Outputs[subject.Name]
		if out == nil || len(ref.Remaining) != 0 {
			return groupVerdict{}, true
		}
		return followGroupExpr(ctx, child, out.Expr, isID, depth+1), true

	case addrs.Resource:
		return followResourceRef(ctx, c, subject, ref.Remaining, isID, depth), true
	case addrs.ResourceInstance:
		return followResourceRef(ctx, c, subject.Resource, ref.Remaining, isID, depth), true
	}
	return groupVerdict{}, false
}

// followResourceRef answers a reference to a resource: a managed one the
// module declares is owned, a data source is followed into the argument
// that names its group.
func followResourceRef(ctx context.Context, c *configs.Config, res addrs.Resource, remaining hcl.Traversal, isID bool, depth int) groupVerdict {
	switch res.Mode {
	case addrs.ManagedResourceMode:
		if c.Module.ManagedResources[res.String()] != nil {
			return groupVerdict{owned: true}
		}
		return groupVerdict{}
	case addrs.DataResourceMode:
		rc := c.Module.DataResources[res.String()]
		if rc == nil || len(remaining) == 0 {
			return groupVerdict{}
		}
		step, ok := remaining[0].(hcl.TraverseAttr)
		if !ok {
			return groupVerdict{}
		}
		// Which of the data source's own arguments names the group the
		// read attribute describes. A resource group data source's name
		// and id are both the group itself; any other azurerm data
		// source's resource_group_name is the group it was read from.
		var arg string
		var argIsID bool
		switch {
		case rc.Type == azurermResourceGroupType && (step.Name == "name" || step.Name == "id"):
			arg = "name"
		case strings.HasPrefix(rc.Type, "azurerm_") && step.Name == "resource_group_name":
			arg = "resource_group_name"
		case rc.Type == "azapi_resource" && step.Name == "id":
			// data "azapi_resource" reads one object by type, parent_id and
			// name; when that object is a group, its name is the group.
			if isAzureResourceGroupBlock(ctx, c.Module, rc) {
				arg = "name"
			} else {
				arg, argIsID = "parent_id", true
			}
		default:
			return groupVerdict{}
		}
		attr := blockAttr(rc.Config, arg)
		if attr == nil {
			return groupVerdict{}
		}
		v := followGroupExpr(ctx, c, attr.Expr, argIsID, depth+1)
		if v.via == "" {
			v.via = addrs.ConfigResource{Module: c.Path, Resource: res}.String()
		}
		return v
	}
	return groupVerdict{}
}

// groupFromARMID returns the resource group an ARM id lives in, or "" when
// it names none (a subscription, tenant or management-group scope). The
// segment name is case-insensitive in ARM ids.
func groupFromARMID(id string) string {
	segs := strings.Split(strings.Trim(id, "/"), "/")
	for i := 0; i+1 < len(segs); i++ {
		if strings.EqualFold(segs[i], "resourceGroups") {
			return segs[i+1]
		}
	}
	return ""
}
