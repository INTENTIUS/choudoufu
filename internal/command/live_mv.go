// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/mitchellh/cli"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/lint"
	"github.com/intentius/choudoufu/internal/live/mv"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// LiveMvCommand renames a resource in a stateless estate: it rewrites the
// tofu-address ownership marker on the live resource that carries the old
// address.
//
// This is the whole replacement for `moved` blocks and state surgery. There is
// no record to edit, because there is no record - a resource's address lives in
// one tag value on the resource itself, and overwriting that value is the move
// (live/MARKERS.md, "The rename rule"). Nothing about the old address
// survives the write, because a single tag value cannot hold two addresses.
//
// The order of operations is config first, marker second: rename the resource
// block, then run this. That is the ordering that never leaves a marker naming
// an address nothing declares, and it is why a destination address absent from
// configuration is refused rather than written (-allow-missing-config is the
// deliberate opt out, for the operator who wants both halves in one change).
//
// The write is a real, minimal apply through the provider, on one instance:
// the resource is materialized exactly as a projection materializes it, the
// same object is synthesized with one tag changed, and PlanResourceChange and
// ApplyResourceChange run for that instance alone. No cloud CLI, no
// provider-specific code, and no plan over the rest of the configuration.
type LiveMvCommand struct {
	Meta
}

func (c *LiveMvCommand) Run(rawArgs []string) int {
	ctx := c.CommandContext()

	common, rawArgs := arguments.ParseView(rawArgs)
	c.View.Configure(common)

	args, diags := arguments.ParseLiveMv(rawArgs)
	if diags.HasErrors() {
		// Argument errors print the usage text, the way state mv and every
		// other command in this package answers a malformed command line.
		// The diagnostic says what was wrong with it; the help says what the
		// command accepts, and only one copy of that has to be maintained.
		c.View.Diagnostics(diags)
		return cli.RunResultHelp
	}

	oldAddr, oldDiags := addrs.ParseAbsResourceInstanceStr(args.RawOldAddr)
	diags = diags.Append(oldDiags)
	newAddr, newDiags := addrs.ParseAbsResourceInstanceStr(args.RawNewAddr)
	diags = diags.Append(newDiags)
	if diags.HasErrors() {
		c.View.Diagnostics(diags)
		return 1
	}

	var err error
	if c.pluginPath, err = c.loadPluginPath(); err != nil {
		diags = diags.Append(err)
		c.View.Diagnostics(diags)
		return 1
	}
	c.Meta.input = false

	diags = diags.Append(c.providerDevOverrideRuntimeWarnings())

	res, moveDiags := c.liveMv(ctx, liveMvArgs{
		old:          oldAddr,
		new:          newAddr,
		estate:       args.Estate,
		dryRun:       args.DryRun,
		allowMissing: args.AllowMissingConfig,
	})
	diags = diags.Append(moveDiags)

	if !diags.HasErrors() && res != nil {
		views.NewStatelessMv(c.View).Report(liveMvReport(res))
	}
	c.View.Diagnostics(diags)
	if diags.HasErrors() {
		return 1
	}
	return 0
}

type liveMvArgs struct {
	old, new     addrs.AbsResourceInstance
	estate       string
	dryRun       bool
	allowMissing bool
}

// liveMv is the pipeline: load, lint, resolve identities, establish the
// estate, start the provider, and hand the whole thing to the mv package.
//
// It is the same front half live-plan runs, for the same reason: the
// estate name and the identity map are what turn two addresses on a command
// line into one live resource, and a rename that derived them differently
// from a plan would eventually rewrite a marker a plan then disputes.
//
// Lint runs after the provider is launched, schemas in hand, exactly as
// live-plan's does: a type with no admission-table row can still pass when
// the provider's own identity schema describes it completely enough. See
// [lint.CheckWith]. Nothing between the config load and the lint check below
// reads or writes the live system: liveMvEstate only reads tag values back
// out of the configuration, contextOpts only resolves which plugin binaries
// are available, and newStatelessProviders only builds the struct - the
// first live-system call of any kind is resourceSchemas, whose answer lint
// consumes immediately.
func (c *LiveMvCommand) liveMv(ctx context.Context, args liveMvArgs) (result *mv.Result, diags tfdiags.Diagnostics) {
	config, cfgDiags := c.loadConfig(ctx, ".")
	diags = diags.Append(cfgDiags)
	if cfgDiags.HasErrors() {
		return nil, diags
	}

	estate, estateDiags := c.liveMvEstate(ctx, args.estate, config)
	diags = diags.Append(estateDiags)
	if estateDiags.HasErrors() {
		return nil, diags
	}

	coreOpts, err := c.contextOpts(ctx)
	if err != nil {
		diags = diags.Append(err)
		return nil, diags
	}

	provs := newStatelessProviders(config, coreOpts.Plugins)
	// A named return, so that the shutdown warning still reaches the caller.
	defer func() { diags = diags.Append(provs.close(ctx)) }()

	// Read once and handed to both the subset check and resolution below, so
	// that a type the schemas admit reads the same answer at both points. See
	// internal/command/live_plan.go, which does the same.
	resourceSchemas := provs.resourceSchemas(ctx)

	// Subset check first: a configuration outside the stateless subset has to
	// fail with an explanation, and it has to fail before anything else is
	// read from or written to the cloud. See [lint.CheckWith].
	if issues := lint.CheckWith(ctx, config, lint.Context{Schemas: resourceSchemas}); len(issues) > 0 {
		diags = diags.Append(lint.Diagnostics(issues))
		return nil, diags
	}

	// Resolved now that lint has passed and the estate name is settled, so
	// that any verb here is already known valid for its quadrant (see
	// internal/live/lint's checkLivePolicy). Nothing downstream reads this
	// yet - GitHub issue #67's config/lint half only, see [statelessPolicy].
	if config.Module != nil {
		log.Printf("[TRACE] live-mv: ownership policy: %s", statelessPolicy(config.Module.Live, estate))
	}

	// The same resolution a plan runs, with the same inputs, for the reason
	// the comment above gives: a rename that derived the identity map
	// differently from a plan would rewrite a marker a plan then disputes.
	resolutions, idDiags := identity.ResolveWith(ctx, config, identity.Context{
		Schemas: resourceSchemas,
	})
	diags = diags.Append(idDiags)
	if idDiags.HasErrors() {
		// Fatal for the same reason it is fatal in a plan: an identity map
		// with holes in it cannot say which live resource an address means.
		return nil, diags
	}

	// Starting the provider here rather than leaving it to the mv package is
	// what lets the region a list call goes to come from the provider block
	// this run evaluated, exactly as discovery gets it in a plan.
	region := c.liveMvRegion(ctx, config, provs, args.new, args.old)

	res, moveDiags := mv.Move(ctx, mv.Request{
		Estate:             estate,
		Old:                args.old,
		New:                args.new,
		Config:             config,
		Resolutions:        resolutions.All(),
		Providers:          provs,
		Region:             region,
		DryRun:             args.dryRun,
		AllowMissingConfig: args.allowMissing,
	})
	diags = diags.Append(moveDiags)
	return res, diags
}

// liveMvEstate establishes the estate, from -estate, from a live
// block, or from the tofu-estate values the configuration itself stamps.
//
// Unlike a plan, this cannot degrade. A plan with no estate name still plans -
// it just cannot find the resources whose identity only a marker recovers. A
// rename with no estate name has nothing to look for at all, and picking a
// resource without checking which estate owns it is exactly the mistake the
// marker exists to prevent.
//
// The live block is consulted because of where this command is printed
// from. A plan of a live-block configuration prints the exact rename
// command beside a renamed for_each key, and that configuration stamps no
// tofu-estate tag of its own - stamping is what puts the markers on, and it
// happens to the configuration the plan reads rather than to the file on
// disk. Without this, the command an operator is told to run would be a
// command that cannot run.
func (c *LiveMvCommand) liveMvEstate(ctx context.Context, flagValue string, config *configs.Config) (string, tfdiags.Diagnostics) {
	name, found, diags := statelessEstateFor(ctx, flagValue, config)
	if diags.HasErrors() {
		return "", diags
	}
	if name != "" {
		return name, diags
	}

	if config != nil && config.Module != nil && config.Module.Live != nil {
		settings := config.Module.Live
		if estate := settings.Estate; estate != "" {
			if !discovery.ValidEstateName(estate) {
				// The configuration is what is wrong, so the diagnostic points
				// at the argument that named the estate.
				return "", diags.Append(&hcl.Diagnostic{
					Severity: hcl.DiagError,
					Summary:  "Invalid estate name",
					Detail:   fmt.Sprintf("The live block names estate %q, which does not match the tofu-estate marker grammar in live/MARKERS.md: a lowercase letter followed by lowercase letters, digits or hyphens, at most 128 characters.", estate),
					Subject:  settings.EstateRange.Ptr(),
				})
			}
			return estate, diags
		}
	}

	switch len(found) {
	case 0:
		return "", diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No estate name",
			"A rename rewrites the tofu-address marker on the one live resource this estate owns at the old address, so it has to know which estate that is. This configuration has no live block naming one, and nothing in it stamps a tofu-estate tag with a value readable from configuration alone. Pass -estate=<name>.",
		))
	case 1:
		return "", diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Invalid estate name in configuration",
			fmt.Sprintf(
				"The configuration stamps tofu-estate = %q, which does not match the marker grammar in live/MARKERS.md, so it cannot be used to find this estate's resources. Pass -estate=<name>.",
				found[0]),
		))
	default:
		return "", diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Several estates named by the configuration",
			fmt.Sprintf(
				"Resources in this configuration stamp tofu-estate with %s. An estate is the unit of ownership and a rename happens within exactly one, so which of these to search is not something this command will pick. Pass -estate=<name>.",
				strings.Join(quoteAll(found), " and ")),
		))
	}
}

// liveMvRegion configures the provider the renamed resource uses and
// reports the region its list calls should go to. Failures are silent on
// purpose: the mv package asks for the same provider a moment later and
// reports the failure with the context to explain it.
func (c *LiveMvCommand) liveMvRegion(ctx context.Context, config *configs.Config, provs *statelessProviders, candidates ...addrs.AbsResourceInstance) string {
	for _, addr := range candidates {
		rc, ok := config.Module.ManagedResources[addr.Resource.Resource.String()]
		if !ok {
			continue
		}
		providerAddr := providerConfigAddr(rc)
		if _, err := provs.ConfiguredProvider(ctx, providerAddr); err != nil {
			return ""
		}
		return provs.region(providerAddr)
	}
	return ""
}

// liveMvReport turns what the rename did into the shape the view renders. It
// is a fixed set of labelled facts rather than prose: the operator needs to be
// able to see, and to grep, which live resource was written to and what its
// marker says now.
func liveMvReport(res *mv.Result) views.StatelessMvReport {
	return views.StatelessMvReport{
		Estate:      res.Estate,
		TypeName:    res.TypeName,
		LiveID:      res.LiveID,
		DisplayName: res.DisplayName,
		OldAddr:     res.Old.String(),
		NewAddr:     res.New.String(),
		OldMarker:   res.OldMarker,
		NewMarker:   res.NewMarker,
		FoundBy:     liveMvFoundBy(res),
		DryRun:      res.DryRun,
	}
}

func liveMvFoundBy(res *mv.Result) string {
	switch res.Path {
	case mv.PathList:
		return fmt.Sprintf("listing every %s and reading its ownership markers", res.TypeName)
	case mv.PathIdentity:
		return fmt.Sprintf("reading the %s whose identity this configuration names", res.TypeName)
	default:
		return "(unknown)"
	}
}

func (c *LiveMvCommand) Help() string {
	helpText := `
Usage: choudoufu [global options] live-mv [options] <old-address> <new-address>

  EXPERIMENTAL. Renames a resource in a marker-managed estate by rewriting the
  tofu-address ownership marker on the live resource that carries the old
  address. That tag write is the move: there is no state to edit and no
  "moved" block to author, and the old address is gone the instant the new
  one is written, because one tag value cannot hold both.

  Rename the resource block in your configuration first, then run this. A
  destination address that is not in the configuration is refused, since a
  marker naming an address nothing declares is an orphan; pass
  -allow-missing-config if you want to rewrite the marker now and rename the
  block in the same change.

  Both addresses must name the same resource type and the same estate. The
  live resource is found by its marker - by listing the type where the
  provider can list it, and by the identity the configuration names where it
  cannot - and it is an error, never a guess, when nothing carries the old
  address, when two live resources carry it, or when something already
  carries the new one.

  The write itself is a minimal apply of a tags-only change through the
  provider, on that one resource instance. A plan that would replace the
  resource, or that would change anything other than its tags, is refused
  rather than applied.

  This command reads and writes the live system. It never reads or writes a
  state file, and it does not run a plan over the rest of the configuration.

Options:

  -estate=name            The estate that owns the resource, matching the
                          tofu-estate tag grammar in live/MARKERS.md.
                          Defaults to the value the configuration itself
                          stamps in its tofu-estate tags, when every resource
                          that stamps one agrees. Unlike live-plan, this
                          command cannot run without one.

  -dry-run                Find the resource and make every check, then stop
                          before writing. Reports what would be rewritten.

  -allow-missing-config   Permit a destination address that the configuration
                          does not declare, for a rename whose configuration
                          edit has not happened yet.

  -no-color               If specified, output won't contain any color.

  -compact-warnings       Show warnings in a more compact form.
`
	return strings.TrimSpace(helpText)
}

func (c *LiveMvCommand) Synopsis() string {
	return "Rename a resource by rewriting its ownership marker, with no state file (experimental)"
}
