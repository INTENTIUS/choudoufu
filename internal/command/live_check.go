// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"
	"strings"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/live/check"
	"github.com/intentius/choudoufu/internal/live/lint"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// LiveCheckCommand answers "will my configuration work under live resource
// markers" from a directory, with no cloud, no state, and no live block
// required in the configuration under test.
//
// It is GitHub issue #114, and it is deliberately a thin renderer over
// internal/live/check rather than an analysis of its own. That package is
// also what tools/corpus-gen runs across a corpus for #102, so the
// compatibility claim this project publishes and the verdict a user gets on
// their own repository are computed by the same code. Two implementations
// would eventually disagree, and the user's would be the one that mattered.
//
// Three things are answered, in the order someone reads them: whether it can
// move at all, what stops it, and what to do about each. The report then
// names the stages it did not check - stamping, discovery and projection all
// need a cloud - because a clean verdict that read as "this ships" while
// three stages went unexamined would be the same overstatement #101 spent a
// campaign removing.
type LiveCheckCommand struct {
	Meta
}

func (c *LiveCheckCommand) Run(rawArgs []string) int {
	ctx := c.CommandContext()

	common, rawArgs := arguments.ParseView(rawArgs)
	c.View.Configure(common)

	dir, jsonOutput, diags := parseLiveCheckArgs(rawArgs)
	if diags.HasErrors() {
		c.View.Diagnostics(diags)
		return 1
	}

	// Nothing here prompts: this command's audience is someone evaluating
	// the mode on a repository they may not have written, and a prompt is
	// the wrong thing to meet them with. A required variable with no value
	// is reported in the verdict instead.
	c.Meta.input = false

	report := c.liveCheck(ctx, dir)

	if !report.Readable() {
		// A directory that will not parse has no findings either, and
		// printing the clean verdict for it would be a lie of exactly the
		// shape this command exists to stop telling. -json gets the same
		// treatment: a document with an empty roster would read as "zero
		// instances declared" rather than "could not be read", which is a
		// worse lie than printing nothing at all.
		diags = diags.Append(report.Load.Diags)
		c.View.Diagnostics(diags)
		return 1
	}

	rep := liveCheckReport(dir, report)
	if jsonOutput {
		// GitHub issue #790: the declared roster, for a caller like behold
		// that parses no HCL of its own. See views.NewLiveCheckJSON's own
		// doc comment for why this did not exist before #790.
		views.NewLiveCheckJSON(c.View).Report(rep)
	} else {
		views.NewLiveCheck(c.View).Report(rep)
	}
	c.View.Diagnostics(diags)

	// Non-zero on "cannot move", so this can gate CI. The verdict is the
	// primary product and it has already been printed either way.
	if report.Blocked() {
		return 1
	}
	return 0
}

// parseLiveCheckArgs reads live-check's own flag set: -json (GitHub issue
// #790) and, unchanged since #114, at most one positional directory
// argument. -json is special-cased rather than folded into the "no options"
// refusal below it precedes, and every other flag still refuses exactly as
// it always has - this command's whole premise is a verdict a human or a
// machine can trust, and a silently-ignored typo'd flag would undermine
// that for either reader.
func parseLiveCheckArgs(rawArgs []string) (dir string, jsonOutput bool, diags tfdiags.Diagnostics) {
	var positional []string
	for _, arg := range rawArgs {
		if arg == "-json" {
			jsonOutput = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			diags = diags.Append(fmt.Errorf("live-check accepts only -json; got %q", arg))
			continue
		}
		positional = append(positional, arg)
	}

	switch len(positional) {
	case 0:
		return ".", jsonOutput, diags
	case 1:
		return positional[0], jsonOutput, diags
	default:
		diags = diags.Append(fmt.Errorf("live-check takes at most one argument, the directory to check; got %d", len(positional)))
		return ".", jsonOutput, diags
	}
}

// liveCheck runs the shared analysis over one directory.
//
// Provider schemas are read first when they can be, from the ordinary plugin
// library "choudoufu init" populates, because the analysis is materially
// more accurate with them: a resource type absent from the generated
// admission table is admitted anyway when the provider's own identity schema
// settles it, and without schemas every such type reads as refused. That is
// the one error this instrument most has to avoid, since it would put a
// false result at the top of the ranking. A directory that was never
// initialized simply has no schemas, the run continues, and the report says
// they were absent rather than presenting the worse answer as the answer.
func (c *LiveCheckCommand) liveCheck(ctx context.Context, dir string) check.Report {
	load := check.Load(ctx, dir)
	if load.Config == nil {
		return check.Report{Load: load}
	}

	var schemas map[string]providers.Schema
	// managedTypes is the same read, attributed per provider rather than
	// merged: it is what lets this instrument draw the data-read phase's
	// provider boundary where a real live-plan draws it. See
	// [check.Context.ProviderManagedTypes].
	var managedTypes map[addrs.Provider]map[string]bool
	if coreOpts, err := c.contextOpts(ctx); err == nil {
		provs := newStatelessProviders(load.Config, coreOpts.Plugins)
		schemas = provs.resourceSchemas(ctx)
		managedTypes = provs.managedTypesByProvider(ctx)
		// A close failure is not this command's news: it read schemas and
		// is done with the plugins. A provider that would not launch
		// already shows up as absent schemas, which the report states.
		_ = provs.close(ctx)
	}

	report := check.Analyze(ctx, load.Config, check.Context{Schemas: schemas, ProviderManagedTypes: managedTypes})
	report.Load = load
	// After Analyze, not inside it: the static evaluator is lazy, so most
	// variables are first read during identity resolution and the unset set
	// is not complete until now (issue #161).
	report.AttributeUnsetVariables(load.UnsetVariables(), load.Sources())
	return report
}

// liveCheckReport converts the analysis into what the view prints. Every
// decision here is presentational - how many positions to show, how long a
// remedy line runs. What is true was decided in internal/live/check.
func liveCheckReport(dir string, report check.Report) views.LiveCheckReport {
	out := views.LiveCheckReport{
		Dir:            dir,
		Blocked:        report.Blocked(),
		Instances:      report.Instances,
		Sites:          report.Sites(),
		Schemas:        report.Schemas,
		Checked:        layerNames(report.Checked),
		Partial:        partialLayerNames(report.Partial),
		Unchecked:      layerNames(report.Unchecked),
		UnsetVariables: report.Load.UnsetVariables(),
		Estate:         report.Estate,
		InstanceRoster: liveCheckInstances(report.Roster),
		References:     liveCheckReferences(report.References),
	}
	for _, finding := range report.Findings {
		if finding.UnsetVarSites > 0 {
			out.VariableDependentFindings++
			if finding.UnsetVarSites == len(finding.Sites) {
				out.FullyVariableDependent++
			}
		}
	}

	// The one-remaining-item case (#175, revised by #215 after #214 demoted
	// state-backend from a fatal finding to a warning): the state-backend
	// rule can no longer reach Findings at all, so it can no longer be what
	// blocks a configuration. What it can still do is be the ONLY warning on
	// an otherwise-clean estate, which is worth naming explicitly rather
	// than folding into an unqualified "nothing is refused" - the deletion
	// is still the recommended edit, and not required for THIS check, which
	// runs with no live block by design (see LiveCheckCommand's doc
	// comment). It stops being optional the moment a live block is added,
	// which plan and apply both need and which live-check does not exercise
	// (GitHub issue #268: internal/configs/module.go refuses to load a
	// module carrying both). Computed here, where the warning IDs are, per
	// this function's own rule that what is true is decided below the view.
	out.OnlyBackendRemains = !report.Blocked() && len(report.Warnings) > 0
	for _, warning := range report.Warnings {
		if warning.ID != string(lint.RuleStateBackend) {
			out.OnlyBackendRemains = false
			break
		}
	}

	for _, finding := range report.Findings {
		out.Findings = append(out.Findings, liveCheckFinding(finding))
	}
	for _, warning := range report.Warnings {
		out.Warnings = append(out.Warnings, views.LiveCheckCount{
			Label: warning.Title,
			Count: len(warning.Sites),
		})
	}
	for _, mod := range report.Load.UnresolvedModules {
		out.UnresolvedModules = append(out.UnresolvedModules, views.LiveCheckModule{
			Path:   mod.Path,
			Source: mod.Source,
		})
	}
	return out
}

func liveCheckFinding(finding check.Finding) views.LiveCheckFinding {
	out := views.LiveCheckFinding{
		Title:     finding.Title,
		Layer:     string(finding.Layer),
		SiteCount: len(finding.Sites),
		Remedy:    condense(finding.Remedy()),
		DocsRef:   finding.DocsRef,

		UnsetVarRefs:  finding.UnsetVarRefs,
		UnsetVarSites: finding.UnsetVarSites,
	}

	// A type-shaped rule is summarized by type rather than enumerated by
	// site. #114 asks for this by name, because the identity layer's
	// unadmitted-type refusal once interpolated its whole 800-plus-row admitted
	// list into a single 25KB error, and a report listing every site would
	// be that mistake with more steps.
	if types := finding.Types(); len(types) > 0 {
		for _, tc := range types {
			out.Types = append(out.Types, views.LiveCheckCount{Label: tc.Type, Count: tc.Sites})
		}
		return out
	}

	shown := finding.Sites
	if len(shown) > liveCheckMaxSites {
		shown = shown[:liveCheckMaxSites]
		out.MoreSites = len(finding.Sites) - len(shown)
	}
	for _, site := range shown {
		out.Examples = append(out.Examples, views.LiveCheckSite{
			Location: site.Location(),
			Address:  site.Address,
		})
	}
	return out
}

// liveCheckInstances converts GitHub issue #790's roster into what -json
// prints. Presentational only, the same rule liveCheckReport's own doc
// comment states for the rest of this conversion: what is true was decided
// in internal/live/check, this only reshapes it.
func liveCheckInstances(roster []check.Instance) []views.LiveCheckInstance {
	out := make([]views.LiveCheckInstance, 0, len(roster))
	for _, inst := range roster {
		out = append(out, views.LiveCheckInstance{
			Address: inst.Address,
			Type:    inst.Type,
			Rung:    string(inst.Rung),
			Refused: inst.Refused,
			Rule:    inst.Rule,
			Reason:  inst.Reason,
		})
	}
	return out
}

// liveCheckReferences converts #790's cross-estate edges the same way.
func liveCheckReferences(refs []check.Reference) []views.LiveCheckReference {
	out := make([]views.LiveCheckReference, 0, len(refs))
	for _, ref := range refs {
		out = append(out, views.LiveCheckReference{
			From:    ref.From,
			Estate:  ref.Estate,
			Address: ref.Address,
			ReadBy:  ref.ReadBy,
		})
	}
	return out
}

// liveCheckMaxSites is how many positions are printed per refusal before the
// rest are counted. The report is a work list, not a log.
const liveCheckMaxSites = 5

// condense trims a remedy down to what answers "what do I do about this".
// The messages it draws on were written to be read as whole diagnostics and
// several run to a paragraph; the whole text is still what the ordinary
// live-plan run prints.
func condense(s string) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	const limit = 240
	if len(s) <= limit {
		return s
	}
	if idx := strings.LastIndex(s[:limit], ". "); idx > 0 {
		return s[:idx+1]
	}
	return strings.TrimSpace(s[:limit]) + "..."
}

// partialLayerNames renders a partially checked stage as the share it is -
// "projection (2 of 27 refusals; the rest need a cloud)" - rather than as a
// bare name. A reader who sees only the name cannot tell a stage this
// command runs two refusals of from one it runs all but two of, and the
// difference is the whole point of the third list.
func partialLayerNames(layers []check.PartialLayer) []string {
	names := make([]string, 0, len(layers))
	for _, layer := range layers {
		names = append(names, fmt.Sprintf("%s (%d of %d refusals; the rest need a cloud)",
			layer.Layer, len(layer.Refusals), layer.Total))
	}
	return names
}

func layerNames(layers []check.Layer) []string {
	names := make([]string, 0, len(layers))
	for _, layer := range layers {
		names = append(names, string(layer))
	}
	return names
}

func (c *LiveCheckCommand) Help() string {
	helpText := `
Usage: choudoufu [global options] live-check [-json] [DIR]

  Reports whether the configuration in DIR (default ".") can move under live
  resource markers, and what stops it if it cannot.

  Makes no cloud calls, reads no state, and needs no "live" block in the
  configuration under test. It is meant to be run on a repository that has
  never used this fork.

  Three of the six live-path stages are checked: the configuration-subset
  rules, identity resolution, and the data-read phase's eligibility analysis
  (which data-source values a live-plan would read before resolution - the
  analysis only, never the read). Stamping, discovery and projection all need
  a cloud and are not checked, which the report states. A clean result is
  therefore a narrow claim, not a promise that an apply succeeds.

  Running "choudoufu init" in DIR first makes the answer more accurate,
  because provider schemas admit resource types the built-in table does not
  carry. Without them those types read as refused, and the report says so.

  Exits non-zero when anything refuses the configuration, so it can gate CI.

  -json prints GitHub issue #790's declared roster instead of the prose
  above: every instance's address, type and rung, every cross-estate
  reference a data source's marker-tag filters make visible
  (live/OUTPUTS.md), and what was and was not checked - for a reader that
  parses no HCL of its own rather than for a terminal.
`
	return strings.TrimSpace(helpText)
}

func (c *LiveCheckCommand) Synopsis() string {
	return "Check whether a configuration can move under live resource markers"
}
