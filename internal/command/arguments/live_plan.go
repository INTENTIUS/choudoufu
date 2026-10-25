// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// LivePlan represents the command-line arguments for the live-plan command,
// which is the plan command's whole option set plus -estate.
type LivePlan struct {
	// Plan is the stock plan argument set, embedded so that -target, -var,
	// -var-file, -refresh and the rest parse and behave identically. Anything
	// live-plan cannot honor is rejected by the command after parsing, rather
	// than removed from the flag set here, so that an operator who passes one
	// is told so instead of being ignored.
	*Plan

	// Estate names the estate whose ownership markers this run looks for. An
	// empty value means the flag was absent, which is not an error: the name
	// is then derived from the configuration, and a run that cannot derive it
	// degrades rather than fails. It is an error only when the configuration
	// has a live block, since the block names the estate itself; the command
	// makes that check, because it needs a loaded configuration to make it.
	Estate string

	// Resource is -resource=ADDR (GitHub issue #2245): plan one managed
	// resource, or one of its instances, and its upstream cone, and print
	// one drift record per instance as NDJSON instead of a plan. Nil when
	// the flag was absent. ResourceRaw is the flag's text, for messages.
	Resource    addrs.Targetable
	ResourceRaw string
}

// BindLivePlan registers the live-plan command's options on cli: the plan
// command's whole option set, from [BindPlan], plus -estate.
//
// -estate is registered here rather than added to [Plan], which would make it
// an option of "choudoufu plan" as well: a flag naming a live-mode concept on
// the command that has state files. Registering it on live-plan's own
// CommandLine keeps the stock plan surface exactly as it was, and still gets
// -estate the parsing every other option has - end-of-flags handling, the
// -estate=NAME and -estate NAME forms, and a real error for a flag that is
// not one.
func BindLivePlan(cli *CommandLine) *LivePlan {
	livePlan := &LivePlan{Plan: BindPlan(cli)}
	cli.StringVar(&livePlan.Estate, "estate", "", "The estate whose ownership markers this run looks for. Refused when the configuration has a live block, which names the estate itself.").SetDisplay("=name")
	cli.StringVar(&livePlan.ResourceRaw, "resource", "", "Plan one managed resource or resource instance and what it reads, with no estate-wide marker sweep and no record-store listing, and print one drift record per instance as NDJSON.").SetDisplay("=address")
	cli.PreHook(func() tfdiags.Diagnostics {
		var diags tfdiags.Diagnostics
		if livePlan.ResourceRaw == "" {
			return diags
		}
		target, moreDiags := parseLivePlanResource(livePlan.ResourceRaw)
		diags = diags.Append(moreDiags)
		livePlan.Resource = target
		return diags
	})
	return livePlan
}

// parseLivePlanResource parses -resource's value: a managed resource or a
// managed resource instance, in any module. A module, a data source or a
// whole-module path is refused by name, since none of them is one
// resource's drift.
func parseLivePlanResource(raw string) (addrs.Targetable, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	traversal, syntaxDiags := hclsyntax.ParseTraversalAbs([]byte(raw), "", hcl.Pos{Line: 1, Column: 1})
	if syntaxDiags.HasErrors() {
		return nil, diags.Append(tfdiags.Sourceless(tfdiags.Error, fmt.Sprintf("Invalid -resource %q", raw), syntaxDiags[0].Detail))
	}
	target, targetDiags := addrs.ParseTarget(traversal)
	if targetDiags.HasErrors() {
		return nil, diags.Append(tfdiags.Sourceless(tfdiags.Error, fmt.Sprintf("Invalid -resource %q", raw), targetDiags[0].Description().Detail))
	}
	var mode addrs.ResourceMode
	switch subject := target.Subject.(type) {
	case addrs.AbsResource:
		mode = subject.Resource.Mode
	case addrs.AbsResourceInstance:
		mode = subject.Resource.Resource.Mode
	default:
		return nil, diags.Append(tfdiags.Sourceless(tfdiags.Error, fmt.Sprintf("Invalid -resource %q", raw), "-resource names one managed resource or one of its instances, such as aws_s3_bucket.data or module.net.aws_subnet.private[0]. A module is a set of resources; use -target for that."))
	}
	if mode != addrs.ManagedResourceMode {
		return nil, diags.Append(tfdiags.Sourceless(tfdiags.Error, fmt.Sprintf("Invalid -resource %q", raw), "-resource names a managed resource. A data source has no drift of its own; it is read as part of the cone of the resource that uses it."))
	}
	return target.Subject, diags
}

// ParseLivePlan processes CLI arguments, returning a LivePlan value, a closer
// function, and errors. If errors are encountered, a LivePlan value is still
// returned representing the best effort interpretation of the arguments.
func ParseLivePlan(args []string) (*LivePlan, func(), tfdiags.Diagnostics) {
	cli := new(CommandLine)
	livePlan := BindLivePlan(cli)
	closer, diags := cli.parseWithHooks("live-plan", args)
	return livePlan, closer, diags
}
