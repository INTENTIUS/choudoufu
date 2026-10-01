// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"
	"strings"

	"github.com/intentius/choudoufu/internal/backend"
	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/configs/configload"
	"github.com/intentius/choudoufu/internal/encryption"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

func PlanCommander() Command {
	cmd := Command{
		Name:  "plan",
		Short: "Show changes required by the current configuration",
		Long: `Generates a speculative execution plan, showing what actions OpenTofu would take to apply the current configuration. This command will not actually perform the planned actions.

You can optionally save the plan to a file, which you can then pass to the "apply" command to perform exactly the actions described in the plan.`,

		GroupID: MainCommandGroup.ID,
	}

	args := arguments.BindPlan(&cmd.CommandLine)
	cmd.Run = func(meta Meta) int {
		return PlanCommand{meta}.run(args)
	}

	return cmd
}

// PlanCommand is a Command implementation that compares a OpenTofu
// configuration to an actual infrastructure and shows the differences.
type PlanCommand struct {
	Meta
}

func (c *PlanCommand) Run(rawArgs []string) int {
	return RunCommand(PlanCommander(), c.Meta, rawArgs)
}

// run is this fork's step between parsing and [PlanCommand.Execute]: it
// decides whether the run is GitHub issue #894's alias, and only otherwise
// builds the plan view and executes.
//
// The alias points the opposite way from LivePlanCommand.Execute's: a
// configuration that names its own estate, asked for -json, is asking for
// GitHub issue #788's document, and LivePlanCommand.livePlan is the only
// pipeline in the fork that builds one. Nothing in Execute can -
// statelessBegin and backend_local.go's StatelessRun have no hook that
// renders it, which is what statelessRejections' "Machine-readable output is
// not available under live resource markers yet" has always been saying - so
// the choice is to delegate or to keep refusing, and #894 is the report of a
// consumer who could not get the document for the configuration shape the
// docs recommend.
//
// It runs BEFORE views.NewPlan and it has to, which is why it lives here and
// not in Execute, whose view argument already exists: with ViewType ViewJSON
// that constructor builds a [views.PlanJSON], and building one prints an
// NDJSON "version" message the instant it exists (views.NewJSONView).
// Deciding afterwards would leave that line on stdout ahead of a document
// this command had already decided not to print - the exact stream mixing
// #894's second half is about.
//
// No recursion: LivePlanCommand.Execute delegates back to Execute only when
// -json was NOT requested.
//
// -json-into is excluded rather than delegated. It asks for the general JSON
// UI-message stream written to a second file, which is a different feature
// with no representation on either pipeline, and it keeps its refusal from
// the one shared list in Execute.
//
// statelessSettings resolves the root module call, which is cached and which
// needs the -var values; RunCli has set those on Meta before this runs. Load
// errors are tolerated here, as in LivePlanCommand.Execute's own alias: the
// ordinary path reports them in its own voice.
func (c PlanCommand) run(args *arguments.Plan) int {
	if args.View.ViewType == arguments.ViewJSON && args.View.JSONInto == nil {
		if settings, _ := c.statelessSettings(c.CommandContext(), true); settings != nil {
			return (&LivePlanCommand{Meta: c.Meta}).Execute(&arguments.LivePlan{Plan: args})
		}
	}
	return c.Execute(args, views.NewPlan(args.View, c.View))
}

func (c PlanCommand) Execute(args *arguments.Plan, view views.Plan) int {
	var diags tfdiags.Diagnostics
	ctx := c.CommandContext()

	// -verbose is this fork's, and per-command rather than a view flag: see
	// views.View's verbose field for why it is set here and not by
	// View.Configure. -show-sensitive needs no such line any more, since
	// v1.13.0 moved it onto arguments.View and RunCli's Configure applies it.
	c.View.SetVerbose(args.Verbose)

	ctx = tfdiags.ContextWithLintFilterHints(ctx, args.View.LintInclude, args.View.LintExclude)
	diags = diags.Append(tfdiags.ExperimentalLintWarn(ctx))

	// Check for user-supplied plugin path
	var err error
	if c.pluginPath, err = c.loadPluginPath(); err != nil {
		diags = diags.Append(err)
		view.Diagnostics(diags)
		return 1
	}

	diags = diags.Append(c.providerDevOverrideRuntimeWarnings())

	// Load the encryption configuration
	enc, encDiags := c.Encryption(ctx)
	diags = diags.Append(encDiags)
	if encDiags.HasErrors() {
		view.Diagnostics(diags)
		return 1
	}

	// Stateless mode is switched on by a "live" block in the
	// configuration, never by a flag, so that a run cannot fall back to
	// writing a state file by forgetting one. Without the block this is nil
	// and nothing below changes.
	statelessCfg, statelessDiags := c.statelessSettings(ctx, false)
	diags = diags.Append(statelessDiags)
	if statelessDiags.HasErrors() {
		view.Diagnostics(diags)
		return 1
	}

	// GitHub issue #587: -adoption-only is a live-markers concept, so a
	// state-backed plan refuses it rather than ignoring it. Checked here
	// because statelessSettings, immediately above, is what says whether
	// this run is a live one, and before the view is wrapped below.
	if moreDiags := planRejectAdoptionOnly(args.AdoptionOnly, statelessCfg != nil); moreDiags.HasErrors() {
		diags = diags.Append(moreDiags)
		view.Diagnostics(diags)
		return 1
	}
	// The resource diff is dropped for the whole run, from here on, by
	// wrapping the view before anything is handed it - the backend, the
	// operation request and the hooks all get the wrapper. See
	// [views.AdoptionOnlyPlan] for what it drops and what it deliberately
	// does not.
	// GitHub issue #1197's -filter, refused where it would do nothing. See
	// planRejectReportFilter.
	if moreDiags := planRejectReportFilter(args.Filter, args.AdoptionOnly, statelessCfg != nil); moreDiags.HasErrors() {
		diags = diags.Append(moreDiags)
		view.Diagnostics(diags)
		return 1
	}
	if args.AdoptionOnly {
		view = views.NewAdoptionOnlyPlan(view, c.View)
	}

	// Prepare the backend with the backend-specific arguments
	be, beDiags := c.PrepareBackend(ctx, args.State, view, enc)
	diags = diags.Append(beDiags)
	if diags.HasErrors() {
		view.Diagnostics(diags)
		return 1
	}

	// Build the operation request
	opReq, opDiags := c.OperationRequest(ctx, be, view, args.View, args.Operation, args.OutPath, args.GenerateConfigPath, enc)
	diags = diags.Append(opDiags)
	if diags.HasErrors() {
		view.Diagnostics(diags)
		return 1
	}

	if statelessCfg != nil {
		moreDiags := statelessBegin(be, opReq, statelessCfg, c.View, args.AdoptionOnly, args.Filter, c.liveEstateOutputs(),
			statelessRejections(surfaceLiveBlock, args.Operation, args.State, args.View, args.OutPath, args.GenerateConfigPath, ""))
		diags = diags.Append(moreDiags)
		if moreDiags.HasErrors() {
			view.Diagnostics(diags)
			return 1
		}
		diags = diags.Append(c.checkAWSProviderVersionSkew())
	} else {
		// GitHub issue #613. A state-backed run is the one that can propose
		// stripping a migrated estate's ownership markers, because it is the
		// one whose prior state has no record of them. See
		// [statefulMarkerGuard].
		opReq.PlanGuard = statefulMarkerGuard()
	}

	// Before we delegate to the backend, we'll print any warning diagnostics
	// we've accumulated here, since the backend will start fresh with its own
	// diagnostics.
	view.Diagnostics(diags)
	diags = nil

	// Perform the operation
	op, diags := c.RunOperation(ctx, be, opReq)
	view.Diagnostics(diags)
	if diags.HasErrors() {
		return 1
	}

	if op.Result != backend.OperationSuccess {
		return op.Result.ExitStatus()
	}
	if args.DetailedExitCode && !op.PlanEmpty {
		return 2
	}

	return op.Result.ExitStatus()
}

func (c *PlanCommand) PrepareBackend(ctx context.Context, args *arguments.State, view views.Plan, enc encryption.Encryption) (backend.Enhanced, tfdiags.Diagnostics) {
	backendConfig, diags := c.loadBackendConfig(ctx, ".")
	if diags.HasErrors() {
		return nil, diags
	}

	// Load the backend
	be, beDiags := c.Backend(ctx, &BackendOpts{
		Config: backendConfig,
		View:   view.Backend(),
	}, enc.State())
	diags = diags.Append(beDiags)
	if beDiags.HasErrors() {
		return nil, diags
	}

	return be, diags
}

func (c *PlanCommand) OperationRequest(
	ctx context.Context,
	be backend.Enhanced,
	view views.Plan,
	viewOptions *arguments.View,
	args *arguments.Operation,
	planOutPath string,
	generateConfigOut string,
	enc encryption.Encryption,
) (*backend.Operation, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	// Build the operation
	opReq := c.Operation(ctx, be, view.Backend(), enc)
	opReq.ConfigDir = "."
	opReq.PlanMode = args.PlanMode
	opReq.Hooks = view.Hooks()
	opReq.PlanRefresh = args.Refresh
	opReq.PlanOutPath = planOutPath
	opReq.GenerateConfigOut = generateConfigOut
	opReq.Targets = args.Targets
	opReq.Excludes = args.Excludes
	opReq.ForceReplace = args.ForceReplace
	opReq.Type = backend.OperationTypePlan
	opReq.View = view.Operation()

	var err error
	opReq.ConfigLoader, err = configload.Initialise(c.configLoader())
	if err != nil {
		diags = diags.Append(fmt.Errorf("Failed to initialize config loader: %w", err))
		return nil, diags
	}

	return opReq, diags
}

func (c *PlanCommand) Help() string {
	helpText := `
Usage: choudoufu [global options] plan [options]

  Generates a speculative execution plan, showing what actions OpenTofu would
  take to apply the current configuration. This command will not actually
  perform the planned actions.

  You can optionally save the plan to a file, which you can then pass to the
  "apply" command to perform exactly the actions described in the plan.

Plan Customization Options:

  The following options customize how OpenTofu will produce its plan. You can
  also use these options when you run "choudoufu apply" without passing it a saved
  plan, in order to plan and apply in a single command.

  -destroy                Select the "destroy" planning mode, which creates a
                          plan to destroy all objects currently managed by this
                          OpenTofu configuration instead of the usual behavior.

  -refresh-only           Select the "refresh only" planning mode, which checks
                          whether remote objects still match the outcome of the
                          most recent OpenTofu apply but does not propose any
                          actions to undo any changes made outside of OpenTofu.

  -refresh=false          Skip checking for external changes to remote objects
                          while creating the plan. This can potentially make
                          planning faster, but at the expense of possibly
                          planning against a stale record of the remote system
                          state.

  -replace=resource       Force replacement of a particular resource instance
                          using its resource address. If the plan would've
                          otherwise produced an update or no-op action for this
                          instance, OpenTofu will plan to replace it instead.
                          You can use this option multiple times to replace
                          more than one object.

  -target=resource        Limit the planning operation to only the given
                          module, resource, or resource instance and all of its
                          dependencies. You can use this option multiple times
                          to include more than one object. This is for
                          exceptional use only. Cannot be used alongside the
                          -exclude option.

  -target-file=filename   Similar to -target, but specifies zero or more
                          resource addresses from a file.

  -exclude=resource       Limit the planning operation to not operate on the
                          given module, resource, or resource instance and all
                          of the resources and modules that depend on it. You
                          can use this option multiple times to exclude more
                          than one object. This is for exceptional use only.
                          Cannot be used together with the -target option.

  -exclude-file=filename  Similar to -exclude, but specifies zero or more
                          resource addresses from a file.

  -var 'foo=bar'          Set a value for one of the input variables in the
                          root module of the configuration. Use this option
                          more than once to set more than one variable.

  -var-file=filename      Load variable values from the given file, in addition
                          to the default files terraform.tfvars and
                          *.auto.tfvars. Use this option more than once to
                          include more than one variables file.

Other Options:

  -compact-warnings            If OpenTofu produces any warnings that are not
                               accompanied by errors, shows them in a more
                               compact form that includes only the summary
                               messages.

  -consolidate-warnings=false  If OpenTofu produces any warnings, do not
                               attempt to consolidate similar messages. All
                               locations for all warnings will be listed.

  -consolidate-errors          If OpenTofu produces any errors, attempt to
                               consolidate similar messages into a single item.

  -detailed-exitcode           Return detailed exit codes when the command
                               exits. The detailed exit codes are:
                                 0 - Succeeded but no changes proposed
                                 1 - Planning failed with an error
                                 2 - Succeeded and changes are proposed

  -generate-config-out=path    (Experimental) If import blocks are present in
                               configuration, instructs OpenTofu to generate
                               HCL for any imported resources not already
                               present. The configuration is written to a new
                               file at PATH, which must not already exist.
                               OpenTofu may still attempt to write
                               configuration if planning fails with an error.

  -input=false                 Disable prompting for required input variables
                               that are not set some other way.

  -lock=false                  Don't hold a state lock during the operation.
                               This is dangerous if others might concurrently
                               run commands against the same workspace.

  -lock-timeout=duration       Duration to retry a state lock, such as "5s"
                               to represent five seconds.

  -no-color                    Disable virtual terminal escape sequences.

  -concise                     Disable progress-related messages.

  -verbose                     Print detail some commands summarize by
                               default. Under live resource markers, prints
                               every type the removal sweep could not cover
                               by name instead of a one-line count.

  -adoption-only               Under live resource markers only, print the
                               adoption ledger and nothing else: what this
                               estate can adopt, what it cannot, and why. The
                               resource diff and the other live-markers
                               sections are suppressed, and each warning is
                               compacted to one line naming it - errors are
                               never touched. It also asks the estate-wide
                               sweep which live resources carry no ownership
                               marker at all, which an ordinary plan does not:
                               that question is bounded by the account rather
                               than by the estate, and it is what this ledger
                               is for. Set TOFU_LIVE_COLLECT_UNCLAIMED=1 to
                               ask it on an ordinary plan, or 0 to skip it
                               here. Refused on a state-backed plan, which has
                               no adoption question.

  -filter=category             Under live resource markers only, print only
                               the named report sections: unowned, adoptable
                               or foreign. Repeat the flag to show several;
                               the categories union. Read-only: the plan, the
                               resource diff and the exit code are the same
                               with or without it, and every other section
                               still prints. A category that matches nothing
                               says so rather than printing nothing. Any other
                               word is a usage error. Refused on a state-backed
                               plan and with -adoption-only.

  -out=path                    Write a plan file to the given path. This can be
                               used as input to the "apply" command.

  -parallelism=n               Limit the number of concurrent operations.
                               Defaults to 10.

  -state=statefile             A legacy option used for the local backend only.
                               Refer to the local backend's documentation for
                               more information.

  -show-sensitive              If specified, sensitive values will not be
                               redacted in te UI output.

  -json                        Produce output in a machine-readable JSON
                               format, suitable for use in text editor
                               integrations and other automated systems.

  -json-into=out.json          Produce the same output as -json, but sent directly
                               to the given file. This allows automation to preserve
                               the original human-readable output streams, while
                               capturing more detailed logs for machine analysis.

  -deprecation=module:m        Specify what type of warnings are shown.
                               Accepted values for "m": all, local, none. 
                               Default: all. When "all" is selected, OpenTofu
                               will show the deprecation warnings for all
                               modules. When "local" is selected, the warns
                               will be shown only for the modules that are
                               imported with a relative path. When "none" is
                               selected, all the deprecation warnings will be
                               dropped.

  -lint=all                    Configures the linting rules to be executed during
                               this command. By specifying this flag, the built-in
                               linting will be enabled, which will start issuing
                               warning diagnostics if any included rule will be
                               violated. For more details on the format and
                               available linting rules, refer to the official
                               documentation.
`
	return strings.TrimSpace(helpText)
}

func (c *PlanCommand) Synopsis() string {
	return "Show changes required by the current configuration"
}
