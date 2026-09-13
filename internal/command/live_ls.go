// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"
	"log"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/mitchellh/cli"

	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/lint"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// LiveLsCommand answers "what does this account hold under estate X", read
// straight off the live system rather than off anything a configuration
// declares - live-plan's own question, and a different one. It is GitHub
// issue #789.
//
// The prior art is examples/live-mv-workbench/tlmig/govern.py's
// read_inventory, which this command ports rather than reimplements
// against: two passes, because the Resource Groups Tagging API does not
// index IAM on a real account (an aws_iam_role created and tagged natively
// still comes back empty from GetResources - live/floci-capabilities.json
// records the same gap against floci before lex00/floci#229's fix, and the
// fact is real AWS's regardless of what any one emulator pin does about
// it), so an inventory that trusted the tagging index alone would silently
// under-report every estate that owns a role. The second pass -
// iam:ListRoles, then iam:ListRoleTags per role, kept only where the role's
// own tofu-estate tag names this estate - is what closes that gap, at the
// cost this command's own doc comment on liveLsIAMRoles states plainly
// rather than hides.
//
// What this command deliberately does NOT do is reuse
// internal/live/discovery's sweep (Discover, sweepViaTagging): that
// machinery answers "what does this estate's CONFIGURATION not yet know
// about", which needs a loaded configuration, a running provider and a
// resolved identity map before it can list anything at all. This command
// answers a narrower, cheaper question - what carries the estate's tag,
// full stop - that an inheritor or an auditor with nothing but read-only
// IAM can ask with no configuration in hand. The lower-level primitives
// discovery's own tagging sweep is built from
// ([cloudcontrol.Client.GetResources], [markers]'s decode functions) are
// exactly what this command reuses; the configuration-aware parts are not.
//
// The Kubernetes listing (GitHub issue #1081) is the one part that does
// need DIR, because the substrate is learned from the configuration's
// provider blocks and the cluster client is built from one of them, the
// way live-plan's own sweep builds it ([statelessProviders.kubernetesClient]).
// What it lists is what the sweep lists - one cluster-wide, label-selected
// list per kind the cluster serves, controller-made objects excluded
// ([kubesweep.Client]) - and what it calls declared is what the sweep
// calls declared ([discovery.DeclaredKubernetesObjects]), so the inventory
// and the removal plan cannot disagree about an object.
type LiveLsCommand struct {
	Meta
}

func (c *LiveLsCommand) Run(rawArgs []string) int {
	ctx := c.CommandContext()

	common, rawArgs := arguments.ParseView(rawArgs)
	c.View.Configure(common)

	args, closer, diags := arguments.ParseLiveLs(rawArgs)
	defer closer()
	if diags.HasErrors() {
		c.View.Diagnostics(diags)
		return cli.RunResultHelp
	}

	// Nothing here prompts, and nothing reads a variable: this command's
	// whole business is a read-only listing of the live system, by name.
	c.Meta.input = false

	report, lsDiags := c.liveLs(ctx, args)
	diags = diags.Append(lsDiags)
	if report != nil {
		views.NewLiveLs(args.ViewOptions, c.View).Report(*report)
	}
	if args.ViewOptions.ViewType == arguments.ViewJSON {
		// GitHub issue #966. [views.View.Diagnostics] sends WARNINGS to
		// Stdout, and every diagnostic this command raises is a warning by
		// design ("every failure along the way downgrades to a warning" -
		// see liveLsGaps). Under -json that appended prose to the document
		// a caller parses, so the "Declared-instance comparison skipped"
		// warning simultaneously broke stdout and never reached the stderr
		// the issue's reporter was capturing: written, and effectively
		// never printed. Same fix, same reason, as live-mv's #791 and
		// live-plan's #894.
		c.View.DiagnosticsToStderr(diags)
	} else {
		c.View.Diagnostics(diags)
	}
	if diags.HasErrors() {
		return 1
	}
	return 0
}

// liveLs is the whole pipeline: validate the estate name, build whichever
// cloud clients [cloudControlTarget] allows, read the listing (once, or
// repeatedly under -consistent), and - only when a configuration directory
// was given - cross-reference it against what the configuration declares.
func (c *LiveLsCommand) liveLs(ctx context.Context, args *arguments.LiveLs) (*views.LiveLsReport, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	if !markers.ValidEstateName(args.Estate) {
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Invalid estate name",
			fmt.Sprintf("%q does not match the tofu-estate marker grammar in live/MARKERS.md: a lowercase letter followed by lowercase letters, digits or hyphens, at most 128 characters.", args.Estate),
		))
	}

	// DIR's configuration is loaded before anything is listed, because it
	// is what says which substrates the estate lives on (GitHub issue
	// #1081): an aws provider among its managed resources' providers means
	// the AWS listing below, a kubernetes provider the cluster listing
	// liveLsGaps runs, both means both. No DIR, or one that will not load,
	// or one naming neither, is the AWS listing this command has always
	// been - liveLsSubstrates. The load's own diagnostics travel to
	// liveLsGaps, which phrases the skip exactly as it did when it loaded
	// the configuration itself.
	var config *configs.Config
	var cfgDiags tfdiags.Diagnostics
	if args.ConfigDir != "" {
		config, cfgDiags = c.loadConfig(ctx, args.ConfigDir)
	}
	substrates := liveLsSubstrates(config, cfgDiags)

	// The same gate live-plan and live-mv build their own Tagging client
	// behind (cloudControlTarget, live_plan.go): off during this package's
	// own offline test suite (TestMain sets TOFU_LIVE_CLOUDCONTROL=off), on
	// by default everywhere else, real AWS included. This command has no
	// fallback path the way a plan's native sweep does - the tagging index
	// and the IAM pass ARE the mechanism, not an accelerant over one - so
	// "off" means an empty listing, not a degraded one, and the warning
	// below says so rather than letting a silent empty report stand in for
	// "nothing is tagged".
	ep, on := cloudControlTarget()
	var tagging *cloudcontrol.Client
	var iamClient *iam.Client
	if !substrates.aws {
		// A Kubernetes-only configuration: no AWS client at all, and no
		// warning about one, because nothing in DIR could carry an AWS
		// tag for this listing to find.
	} else if on {
		tagging = cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: ep, Region: args.Region})
		// No BaseEndpoint override here: aws-sdk-go-v2's own default config
		// resolution already reads AWS_ENDPOINT_URL / AWS_ENDPOINT_URL_IAM,
		// the same variables cloudControlTarget reads by hand for the
		// client above, which is why floci (and any endpoint override) just
		// works with no extra plumbing - internal/live/projection/store.go's
		// ssm.NewFromConfig/s3.NewFromConfig calls take the same shortcut for
		// the same reason.
		if awsCfg, err := liveLsAWSConfig(ctx, args.Region); err != nil {
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Warning,
				"IAM listing unavailable",
				fmt.Sprintf("The AWS SDK's default credential chain could not be loaded, so the second pass over iam:ListRoles/iam:ListRoleTags this command's own doc comment describes did not run: %s. The Resource Groups Tagging API listing above is unaffected, but it does not index IAM on a real account, so any role this estate owns may be missing from it.", err),
			))
		} else {
			iamClient = iam.NewFromConfig(awsCfg)
		}
	} else {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Warning,
			"Listing disabled",
			"TOFU_LIVE_CLOUDCONTROL is off, which turns off both the Resource Groups Tagging API listing and the IAM native pass this command is built from. The report below is empty rather than a partial answer.",
		))
	}

	read := func(readCtx context.Context) ([]views.LiveLsItem, tfdiags.Diagnostics) {
		return c.liveLsRead(readCtx, args.Estate, tagging, iamClient)
	}

	var items []views.LiveLsItem
	var attempts int
	var stabilized bool
	if args.Consistent {
		var pollDiags tfdiags.Diagnostics
		items, attempts, stabilized, pollDiags = pollConsistent(ctx, read)
		diags = diags.Append(pollDiags)
	} else {
		var readDiags tfdiags.Diagnostics
		items, readDiags = read(ctx)
		diags = diags.Append(readDiags)
		attempts, stabilized = 1, true
	}

	rep := &views.LiveLsReport{
		Estate:     args.Estate,
		Region:     args.Region,
		Consistent: args.Consistent,
		Stabilized: stabilized,
		Attempts:   attempts,
		ConfigDir:  args.ConfigDir,
		Items:      items,
	}

	if args.ConfigDir != "" {
		cmp, gapDiags := c.liveLsGaps(ctx, args.Estate, args.ConfigDir, config, cfgDiags, substrates.kubernetes, items)
		diags = diags.Append(gapDiags)
		rep.Gaps = cmp.Gaps
		rep.GapsSkipped = cmp.Skipped
		rep.Schemas = cmp.Schemas
		if len(cmp.Kubernetes) > 0 {
			rep.Items = append(rep.Items, cmp.Kubernetes...)
			sortLiveLsItems(rep.Items)
		}
		for i := range rep.Items {
			if rep.Items[i].Address != "" && cmp.Declared[rep.Items[i].Address] {
				rep.Items[i].Declared = true
			}
		}
	} else {
		// No directory, so no comparison, so an empty gap list is the
		// absence of an answer rather than one. GitHub issue #966: the
		// document used to omit the key here too, which reads the same as
		// "compared, and found no gaps".
		rep.GapsSkipped = "no configuration directory was given; pass DIR to compare this listing against a configuration's declared instances."
	}

	return rep, diags
}

// liveLsAWSConfig is the ordinary aws-sdk-go-v2 default-config chain, with
// an explicit region when one was named - the same shape
// internal/live/projection/store.go's loadAWSConfig takes for the record
// store's own "ssm"/"s3" clients, restated here because that function is
// unexported in a different package.
func liveLsAWSConfig(ctx context.Context, region string) (aws.Config, error) {
	if region != "" {
		return awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	}
	return awsconfig.LoadDefaultConfig(ctx)
}

// liveLsRead is one snapshot of the listing: the tagging pass, then the IAM
// native pass over whatever the tagging pass did not already find. Either
// client may be nil (cloudControlTarget was off, or the IAM client could
// not be built), in which case that pass is skipped entirely rather than
// attempted and refused.
func (c *LiveLsCommand) liveLsRead(ctx context.Context, estate string, tagging *cloudcontrol.Client, iamClient *iam.Client) ([]views.LiveLsItem, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	var items []views.LiveLsItem
	seen := map[string]bool{}

	if tagging != nil {
		tagged, err := tagging.GetResources(ctx, nil, []cloudcontrol.TagFilter{
			{Key: markers.TagEstate, Values: []string{estate}},
		})
		if err != nil {
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Warning,
				"Tagging index unavailable",
				fmt.Sprintf("The Resource Groups Tagging API's GetResources call failed: %s. The listing below, if any, comes from the IAM native pass alone.", err),
			))
		} else {
			for _, tr := range tagged {
				items = append(items, liveLsItemFromTags(tr.ResourceARN, tr.Tags, "tagging"))
				seen[tr.ResourceARN] = true
			}
		}
	}

	if iamClient != nil {
		roleItems, iamDiags := c.liveLsIAMRoles(ctx, estate, iamClient, seen)
		diags = diags.Append(iamDiags)
		items = append(items, roleItems...)
	}

	sortLiveLsItems(items)
	return items, diags
}

// liveLsIAMRoles is the second pass GitHub issue #789 asks for by name: the
// Resource Groups Tagging API does not index IAM roles on a real account
// (this command's own doc comment has the evidence), so the only way to
// find a role this estate owns is to list every role in the account and
// read each one's own tags. That is the honest cost of the gap, not a bug
// in this pass - the same cost examples/live-mv-workbench/tlmig/
// govern.py's read_inventory pays, restricted there to roles matching a
// smoke-fixture name prefix this general-purpose command has no equivalent
// of and so does not apply.
//
// A role whose ARN the tagging pass already returned (seen) is skipped
// before its own ListRoleTags call, both to avoid a duplicate item and to
// avoid paying for a read this run does not need - relevant chiefly against
// floci, whose tagging index unions IAM in today's pin
// (live/floci-capabilities.json) and so already returns most of an
// estate's roles through the tagging pass alone.
func (c *LiveLsCommand) liveLsIAMRoles(ctx context.Context, estate string, client *iam.Client, seen map[string]bool) ([]views.LiveLsItem, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	var items []views.LiveLsItem

	paginator := iam.NewListRolesPaginator(client, &iam.ListRolesInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Warning,
				"IAM role listing unavailable",
				fmt.Sprintf("iam:ListRoles failed: %s. Any role this estate owns that the tagging index above did not already return is missing from this listing.", err),
			))
			return items, diags
		}
		for _, role := range page.Roles {
			arn := aws.ToString(role.Arn)
			if arn == "" || seen[arn] {
				continue
			}
			tagsOut, err := client.ListRoleTags(ctx, &iam.ListRoleTagsInput{RoleName: role.RoleName})
			if err != nil {
				// One role's tags failing to read is not this pass's news:
				// iam:ListRoleTags is a per-role call over what may be a
				// large account, and a permission or throttling hiccup on
				// one role must not blank out every other role's listing.
				log.Printf("[WARN] live-ls: iam:ListRoleTags for %s: %s", arn, err)
				continue
			}
			tags := iamTagMap(tagsOut.Tags)
			if tags[markers.TagEstate] != estate {
				continue
			}
			items = append(items, liveLsItemFromTags(arn, tags, "iam"))
			seen[arn] = true
		}
	}
	return items, diags
}

func iamTagMap(tags []iamtypes.Tag) map[string]string {
	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	return out
}

// liveLsItemFromTags builds one report item from a resource's ARN (or
// other stable id) and its raw tags: decode the address per
// live/MARKERS.md when a readable tofu-address marker is present, and fall
// back to an ARN-derived type label when it is not.
func liveLsItemFromTags(id string, tags map[string]string, source string) views.LiveLsItem {
	item := views.LiveLsItem{
		ID:     id,
		Slot:   tags[markers.TagSlot],
		Source: source,
		Tags:   tags,
	}

	if raw, corrupt := markers.GatherAddress(tags); !corrupt && raw != "" {
		// EscapeAddress is idempotent over an already-escaped value (its own
		// doc comment), so this is a normalization pass, not a second
		// escaping - the same defensive call
		// internal/live/discovery/tagging.go's fileTaggingCandidate makes
		// over the same GatherAddress result.
		escaped := markers.EscapeAddress(raw)
		if markers.ValidMarkerAddress(escaped) {
			if addr, ok := markers.UnescapeAddress(escaped); ok {
				item.Address = addr.String()
				item.Type = addr.Resource.Resource.Type
			}
		}
	}
	if item.Type == "" {
		item.Type = arnTypeLabel(id)
	}
	return item
}

// arnTypeLabel is the fallback "type" for an item with no readable
// tofu-address marker to read a real resource type off: the ARN's own
// service and resource-type segments, close enough for a legend - the same
// approximation examples/live-mv-workbench/tlmig/govern.py's _arn_type
// applies to every item unconditionally. This command prefers the marker-
// decoded type whenever one is available (liveLsItemFromTags above), which
// is the ordinary case for anything this estate actually owns; this
// fallback is what a malformed or unreadable marker still gets, rather than
// an empty string.
func arnTypeLabel(id string) string {
	a, ok := cloudcontrol.ParseARN(id)
	if !ok {
		return "unknown"
	}
	if a.ResourceType == "" {
		return a.Service
	}
	return a.Service + ":" + a.ResourceType
}

func sortLiveLsItems(items []views.LiveLsItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Type != items[j].Type {
			return items[i].Type < items[j].Type
		}
		if items[i].Address != items[j].Address {
			return items[i].Address < items[j].Address
		}
		return items[i].ID < items[j].ID
	})
}

// consistentPollInterval and consistentMaxAttempts bound -consistent's
// retry loop. live/MARKERS.md and GitHub issue #789 both describe the
// Resource Groups Tagging API's index as lagging a tag write by "about a
// minute"; polling every 5 seconds for up to 20 attempts covers a full
// minute of lag with margin, and is a small, fixed cost against an account
// that is already consistent (the ordinary case), where it costs exactly
// two reads.
const (
	consistentPollInterval = 5 * time.Second
	consistentMaxAttempts  = 20
)

// pollConsistent re-reads read until two consecutive reads agree, or gives
// up after consistentMaxAttempts. Every consumer of a listing taken right
// after a tag write (a live-mv, an apply, a fresh live-import) would
// otherwise reinvent this exact wait by hand - see LiveLsCommand's own doc
// comment and live/MARKERS.md's index-lag note - so it lives here once,
// behind -consistent, rather than in each of them.
//
// Only the last attempt's diagnostics survive into the return value: an
// attempt that disagreed with its predecessor is not this function's
// failure to report, it is the lag this function exists to wait out, and
// diagnostics from a superseded read would either double-report a
// transient hiccup or, worse, outlive the read that produced them and
// describe a report that is no longer what is being shown.
func pollConsistent(ctx context.Context, read func(ctx context.Context) ([]views.LiveLsItem, tfdiags.Diagnostics)) (items []views.LiveLsItem, attempts int, stabilized bool, diags tfdiags.Diagnostics) {
	return pollConsistentEvery(ctx, read, consistentPollInterval, consistentMaxAttempts)
}

// pollConsistentEvery is [pollConsistent] with the interval and attempt
// bound as parameters, so a test can drive the same retry logic on a clock
// it controls rather than the real one - see live_ls_test.go's
// TestPollConsistent.
func pollConsistentEvery(ctx context.Context, read func(ctx context.Context) ([]views.LiveLsItem, tfdiags.Diagnostics), interval time.Duration, maxAttempts int) (items []views.LiveLsItem, attempts int, stabilized bool, diags tfdiags.Diagnostics) {
	var prev []views.LiveLsItem
	var havePrev bool

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		cur, readDiags := read(ctx)
		diags = readDiags
		attempts = attempt

		if havePrev && reflect.DeepEqual(prev, cur) {
			return cur, attempts, true, diags
		}
		prev, havePrev = cur, true
		items = cur

		if attempt == maxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return items, attempts, false, diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Listing canceled",
				ctx.Err().Error(),
			))
		case <-time.After(interval):
		}
	}
	return items, attempts, false, diags
}

// liveLsGaps cross-references a configuration directory's declared
// instances against what the listing already found, and reports every
// instance the listing cannot see for a structural reason - the record rung
// or the declaration-carried rung, live/MARKERS.md's tier definitions
// (#417) - rather than leaving a reader to guess whether a missing address
// is a gap or an absence.
//
// Every failure along the way downgrades to a warning and an empty result
// rather than failing the whole command: the cloud listing above is this
// command's primary deliverable and does not need a configuration to exist
// at all, so a configuration that will not load, is outside the stateless
// subset, or cannot be resolved is news worth printing, never a reason to
// withhold the listing that already succeeded.
//
// config and cfgDiags are [Meta.loadConfig]'s answer for dir, loaded by the
// caller because the substrates are read off it before any listing runs
// (GitHub issue #1081); kubernetes says whether that read found a
// kubernetes provider, in which case the cluster listing runs here too,
// after resolution, since what it calls declared is a resolution's kind and
// natural key. Its items come back in the comparison's Kubernetes field
// and are counted found for the gap list below.
func (c *LiveLsCommand) liveLsGaps(ctx context.Context, estate, dir string, config *configs.Config, cfgDiags tfdiags.Diagnostics, kubernetes bool, items []views.LiveLsItem) (liveLsComparison, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	// Whether provider schemas were read, tracked across the skip paths
	// below rather than only on the path that completes: a comparison that
	// stops at the subset check still knows which library answered, and
	// "schemas" is a fact about the run, not about the gap list. GitHub
	// issue #973, whose repro is a skip.
	schemasRead := false
	skip := func(reason string) (liveLsComparison, tfdiags.Diagnostics) {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Warning,
			"Declared-instance comparison skipped",
			fmt.Sprintf("%s The cloud listing above is unaffected.", reason),
		))
		// The reason travels in the report as well as in the warning, so
		// the -json document says why its gap list is empty without a
		// caller parsing prose - GitHub issue #966, which reports both a
		// missing key and a warning that never reached the stream the
		// caller was reading.
		return liveLsComparison{Skipped: reason, Schemas: schemasRead}, diags
	}

	if cfgDiags.HasErrors() {
		return skip(fmt.Sprintf("%s could not be loaded as a configuration: %s.", dir, cfgDiags.Err()))
	}
	if config == nil || config.Module == nil {
		return skip(fmt.Sprintf("%s has no readable module configuration.", dir))
	}

	// Resolved against DIR rather than against the process working
	// directory - GitHub issue #973. An error here is not a skip: it means
	// DIR's lock file names a provider that is not installed beside it,
	// which is an un-initialized directory, and this command's whole
	// contract is that a comparison it cannot make fully still runs as far
	// as it can and says how far that was. The library returned alongside
	// the error still carries every provider that IS installed.
	lib, err := c.pluginsForDir(dir)
	if err != nil {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Warning,
			"Provider plugins are not fully installed",
			fmt.Sprintf("%s The comparison below runs with whatever provider schemas were available; run \"choudoufu init\" in that directory for the complete answer.", err),
		))
	}

	provs := newStatelessProviders(config, lib)
	closeProviders := func() {
		if cd := provs.close(ctx); cd.HasErrors() {
			log.Printf("[WARN] live-ls: closing providers after the declared-instance comparison: %s", cd.Err())
		}
	}

	resourceSchemas := provs.resourceSchemas(ctx)
	schemasRead = len(resourceSchemas) > 0

	// GitHub issue #966: the comparison below runs without schemas, but
	// liveLsRung's markers.Taggable check cannot, so no instance can be
	// classified declaration-carried and every one of them drops silently
	// out of the gap list. That is the issue's own repro - a clean-looking
	// empty answer - so it is said out loud here and carried in the
	// report's Schemas field for a reader that parses no prose.
	if len(resourceSchemas) == 0 {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Warning,
			"Declared-instance comparison ran without provider schemas",
			fmt.Sprintf("No provider schema was available for %s, so no resource type's taggability could be read: an instance whose type carries no tags argument - the declaration-carried rung, which this listing structurally cannot see - is missing from the gap list below rather than reported in it. Run \"choudoufu init\" in that directory for the accurate answer.", dir),
		))
	}

	if issues := lint.CheckWith(ctx, config, lint.Context{Schemas: resourceSchemas}); len(issues) > 0 {
		closeProviders()
		return skip(fmt.Sprintf("%s is outside the stateless subset (%d issue(s)); run \"choudoufu live-check %s\" for the detail.", dir, len(issues), dir))
	}

	dataResults, drDiags := statelessDataReads(ctx, config, provs, resourceSchemas, nil)
	if drDiags.HasErrors() {
		closeProviders()
		return skip(fmt.Sprintf("the data-read phase could not complete: %s.", drDiags.Err()))
	}

	resolutions, idDiags := statelessResolve(ctx, config, provs, resourceSchemas, dataResults, nil)
	if idDiags.HasErrors() {
		closeProviders()
		return skip(fmt.Sprintf("identity resolution could not complete: %s.", idDiags.Err()))
	}

	// The cluster listing runs with the providers still open: the client
	// is built from the provider block's evaluated arguments, which
	// [statelessProviders.ConfiguredProvider] is what evaluates.
	var kube []views.LiveLsItem
	if kubernetes {
		var kubeDiags tfdiags.Diagnostics
		kube, kubeDiags = c.liveLsKubernetes(ctx, estate, config, provs, resolutions.All())
		diags = diags.Append(kubeDiags)
	}
	closeProviders()

	declared := make(map[string]bool, resolutions.Len())
	for _, res := range resolutions.All() {
		declared[res.Addr.String()] = true
	}

	foundInCloud := make(map[string]bool, len(items)+len(kube))
	for _, item := range items {
		if item.Address != "" {
			foundInCloud[item.Address] = true
		}
	}
	for _, item := range kube {
		if item.Address != "" {
			foundInCloud[item.Address] = true
		}
	}

	var gaps []views.LiveLsGap
	for _, res := range resolutions.All() {
		addr := res.Addr.String()
		if foundInCloud[addr] {
			continue
		}
		rung, detail, ok := liveLsRung(res, resourceSchemas)
		if !ok {
			// A marker-carried instance the listing still did not find is a
			// genuine absence - not yet created, or created without its
			// marker - and reporting it here as a "rung" would be exactly
			// the overstatement this function exists to avoid making in the
			// other direction.
			continue
		}
		gaps = append(gaps, views.LiveLsGap{Address: addr, Type: res.Type(), Rung: rung, Detail: detail})
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i].Address < gaps[j].Address })

	return liveLsComparison{Gaps: gaps, Declared: declared, Schemas: schemasRead, Kubernetes: kube}, diags
}

// liveLsSubstrateSet is which substrates a listing covers, read off DIR's
// configuration by [liveLsSubstrates].
type liveLsSubstrateSet struct {
	aws        bool
	kubernetes bool
}

// liveLsSubstrates reads the substrates off a configuration the way the
// estate-wide sweep picks its provider passes: every distinct provider
// configuration among the managed resources
// ([statelessManagedResourceProviders], which falls back to the root's
// declared provider blocks when nothing is declared). An aws provider
// among them is the AWS listing, a kubernetes provider the cluster
// listing. No configuration at all (no DIR, or one whose load failed -
// cfgDiags carries the error the comparison will report) or one naming
// neither provider is the AWS listing alone, which is what this command
// was before GitHub issue #1081 and stays for every caller that passes no
// DIR.
func liveLsSubstrates(config *configs.Config, cfgDiags tfdiags.Diagnostics) liveLsSubstrateSet {
	if config == nil || config.Module == nil || cfgDiags.HasErrors() {
		return liveLsSubstrateSet{aws: true}
	}
	var s liveLsSubstrateSet
	for _, addr := range statelessManagedResourceProviders(config) {
		switch addr.Provider.Type {
		case "aws":
			s.aws = true
		case "kubernetes":
			s.kubernetes = true
		}
	}
	if !s.aws && !s.kubernetes {
		s.aws = true
	}
	return s
}

// liveLsKubernetes lists the estate's objects through every kubernetes
// provider configuration DIR's managed resources use, one cluster each:
// the client from the block's own connection arguments, exactly as
// live-plan's sweep builds it ([statelessProviders.kubernetesClient]), so
// the inventory reads the cluster the plan would. A block this run cannot
// configure or connect with is one warning - the sweep's own summary,
// [discovery.SummaryKubernetesSweepUnavailable] - and the listing goes on
// without it, the same way an unreachable tagging index leaves the AWS
// listing a warning rather than a failure.
func (c *LiveLsCommand) liveLsKubernetes(ctx context.Context, estate string, config *configs.Config, provs *statelessProviders, resolutions []identity.Resolution) ([]views.LiveLsItem, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	var items []views.LiveLsItem
	for _, addr := range statelessManagedResourceProviders(config) {
		if addr.Provider.Type != "kubernetes" {
			continue
		}
		if _, err := provs.ConfiguredProvider(ctx, addr); err != nil {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, discovery.SummaryKubernetesSweepUnavailable,
				fmt.Sprintf("Provider configuration %s could not be configured, so no Kubernetes object owned by estate %q is listed through it: %s.", addr, estate, err)))
			continue
		}
		client, types, manifestType, schemaDiags, err := provs.kubernetesClient(ctx, addr)
		if schemaDiags.HasErrors() {
			diags = diags.Append(schemaDiags)
			continue
		}
		if err != nil {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, discovery.SummaryKubernetesSweepUnavailable,
				fmt.Sprintf("No cluster client could be built from provider configuration %s, so no Kubernetes object owned by estate %q is listed through it: %s.", addr, estate, err)))
			continue
		}
		found, listDiags := liveLsKubernetesList(ctx, estate, client, types, manifestType, resolutions)
		diags = diags.Append(listDiags)
		items = append(items, found...)
	}
	return items, diags
}

// liveLsKubernetesList is one cluster's listing: the kinds the cluster
// serves among the provider's type universe ([kubesweep.Sweeper.Kinds],
// every CRD included under the manifest type), then one label-selected
// list per kind. Each object is an item keyed by its natural key, filed
// under the block that declares it when one does - the join is the kind
// and the natural key, [discovery.DeclaredKubernetesObjects], the sweep's
// own - else under the type the sweep would plan its removal at.
//
// API discovery failing is the whole cluster unlisted, and says so under
// the sweep's summary; one kind's list failing is that kind missing, and
// says so under its own, so a reader can tell "no cluster" from "no
// permission on one kind". Neither is an error: the listing is what
// could be read, and the warning is what could not.
func liveLsKubernetesList(ctx context.Context, estate string, sweeper kubesweep.Sweeper, types []string, manifestType string, resolutions []identity.Resolution) ([]views.LiveLsItem, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	var items []views.LiveLsItem

	declared := discovery.DeclaredKubernetesObjects(resolutions, types, manifestType)
	kinds, _, err := sweeper.Kinds(ctx, types, manifestType)
	if err != nil {
		return nil, diags.Append(tfdiags.Sourceless(tfdiags.Warning, discovery.SummaryKubernetesSweepUnavailable,
			fmt.Sprintf("The cluster's API discovery failed, so no Kubernetes object owned by estate %q could be listed: %s.", estate, err)))
	}
	kindTypes := kubesweep.KindTypes(types)
	for _, k := range kinds {
		objects, _, err := sweeper.List(ctx, k, markers.TagEstate, estate)
		if err != nil {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, "Kubernetes listing incomplete",
				fmt.Sprintf("Listing %s across all namespaces failed: %s. Any %s this estate owns is missing from the listing.", k.GVR.String(), err, k.Kind)))
			continue
		}
		typeName := manifestType
		if !k.Manifest {
			typeName, _ = kubesweep.TypeFor(kindTypes, declared.Types, k.Kind)
		}
		for _, o := range objects {
			key := kubesweep.NaturalKey(o.Namespace, o.Name)
			item := views.LiveLsItem{
				ID:         key,
				Type:       typeName,
				Kind:       k.Kind,
				APIVersion: k.APIVersion,
				Source:     "kubernetes",
				Tags:       o.Labels,
			}
			if addr, ok := declared.Declares(k.Kind, key); ok {
				item.Address = addr.String()
				item.Declared = true
				item.Type = addr.Resource.Resource.Type
			}
			items = append(items, item)
		}
	}
	return items, diags
}

// liveLsComparison is what [LiveLsCommand.liveLsGaps] found: the gaps
// themselves, the declared-address set the item listing is marked against,
// whether provider schemas backed any of it, and - when it did not run at
// all - why.
//
// It is a struct rather than four return values because GitHub issue #966
// added the last two, and the two it added are the ones a caller is most
// likely to forget: a zero liveLsComparison is the honest answer for a
// comparison that produced nothing, and Skipped is what tells a reader
// whether the empty Gaps above it means "none" or "not asked".
type liveLsComparison struct {
	Gaps     []views.LiveLsGap
	Declared map[string]bool
	Schemas  bool
	Skipped  string

	// Kubernetes is the cluster listing (GitHub issue #1081), made here
	// rather than beside the AWS passes because what it calls declared
	// is a resolution's kind and natural key, which only exist once DIR
	// has been resolved. Nil when DIR names no kubernetes provider, or
	// when the comparison skipped before resolution.
	Kubernetes []views.LiveLsItem
}

// liveLsRung classifies why a declared instance cannot be found by this
// listing's tag-reading mechanism, reusing the identity package's own
// resolution classes rather than inventing a second taxonomy - the tier
// definitions (#417) fix the same two names this function returns
// ("record-carried" and "declaration-carried"), and [identity.Class] is
// their runtime-computed twin: [identity.ClassRecordBacked] and
// [identity.ClassRecordLocated] are exactly the tier's record-carried
// population (no cloud object at all, or one with nowhere to carry a tag),
// and a taggable check against the instance's own provider schema
// ([markers.Taggable], the same predicate live/survey-full.json's taggable
// signal and internal/live/lint both read) settles declaration-carried the
// same way tools/readiness-gen/build.go's classify does for the static,
// per-type case - computed here from live schemas instead of from a
// committed survey artifact, which is more precise for a live command with
// a provider already in hand.
//
// ok is false for a marker-carried instance (an ordinary taggable type):
// such an instance not being found is not a rung, it is a real gap, and
// this function reports nothing rather than mislabeling it.
func liveLsRung(res identity.Resolution, schemas map[string]providers.Schema) (rung, detail string, ok bool) {
	if h := liveClassTable[res.Class]; h.lsRung != "" {
		return h.lsRung, h.lsDetail, true
	}

	schema, haveSchema := schemas[res.Type()]
	if haveSchema && !markers.Taggable(schema.Block) {
		if _, labels := markers.LabelSurface(schema.Block); labels || markers.ManifestSurface(schema.Block) {
			// Marker-carried on Kubernetes: the marker is a label, not a
			// tag (live/MARKERS.md, "Kubernetes: one label"), and the
			// cluster listing reads exactly that label. Such an instance
			// not being found is the same genuine absence a taggable AWS
			// instance's is - not a rung.
			return "", "", false
		}
		return "declaration-carried",
			fmt.Sprintf("%s has no settable tags argument, so no ownership marker was ever written for it - live/MARKERS.md's tier definitions (#417) name this the declaration-carried tier. Its identity comes entirely from configuration.", res.Type()),
			true
	}
	return "", "", false
}

func (c *LiveLsCommand) Help() string {
	helpText := `
Usage: choudoufu [global options] live-ls -estate=NAME [options] [DIR]

  Lists every resource the account holds under estate NAME, read straight off
  the live system: the Resource Groups Tagging API's estate-wide index, plus a
  second pass over iam:ListRoles and iam:ListRoleTags for the IAM roles that
  index does not serve on a real account. No configuration, state or record
  store is read.

  Per resource: its ARN (or other stable identity), its type, the
  configuration address decoded from its tofu-address marker and any
  continuation tags (live/MARKERS.md), its tofu-slot when present, and every
  marker tag it carries.

  A Kubernetes estate is listed through DIR: the substrate is read off the
  configuration's provider blocks, and a kubernetes provider among them gets
  the cluster listing - the client built from that block's own connection
  arguments, then one cluster-wide, label-selected list per kind the cluster
  serves, custom resources included, with controller-made objects excluded,
  exactly the sweep a plan runs. Per object: the provider type it is filed
  under, its natural key (NAMESPACE/NAME, or NAME for a cluster-scoped kind),
  its kind and API version, every label it carries, and the block in DIR
  that declares it, joined on the kind and the natural key because the
  Kubernetes marker is the estate label alone. A configuration with both an
  aws and a kubernetes provider lists both substrates; one with a kubernetes
  provider and no aws provider lists the cluster alone. A cluster this run
  cannot reach is a warning, "Kubernetes sweep unavailable", and the rest of
  the listing stands. -consistent polls the AWS listing only.

  With DIR given, the listing is cross-referenced against that directory's
  declared instances: one this listing cannot find is reported as a gap, named
  by which rung explains the absence - "record" for an instance whose identity
  lives in the estate's own record store rather than on a tagged cloud object,
  "declaration-carried" for one whose type carries no tags argument at all, so
  no marker was ever written for it - rather than left silently missing, which
  reads as an absence when it is really a rung this listing's own mechanism
  cannot reach. DIR is never required: the listing above needs no
  configuration to be complete on its own terms.

  That comparison needs DIR's provider schemas to tell a declaration-carried
  instance from a real absence, so run "choudoufu init" in DIR first. Without
  them the comparison still runs and still reports the record rung, but no
  declaration-carried instance is classified at all. -json says which it got
  in a top-level "schemas" field, "provider" or "builtin"; its "gaps" key is
  always present, with "gaps_skipped" naming the reason when the comparison
  did not run rather than leaving an empty list to read as "no gaps".

Options:

  -estate=name            The estate to list. Required.

  -region=name            The AWS region to list in. Defaults to the AWS
                          SDK's own region resolution (AWS_REGION, the shared
                          config file, or an endpoint override's own region).

  -consistent             Re-read the listing until two consecutive reads
                          agree, rather than returning the first read as-is.
                          The Resource Groups Tagging API's index lags a tag
                          write by about a minute, so a listing taken right
                          after a live-mv or an apply can show a resource
                          under both its old and new estate, or under
                          neither; this polls past that window (up to 100
                          seconds) instead of leaving every caller to
                          reinvent the same wait.

  -json                   Print the listing as one JSON object instead of
                          text.

  -no-color               If specified, output won't contain any color.

  -compact-warnings       Show warnings in a more compact form.
`
	return strings.TrimSpace(helpText)
}

func (c *LiveLsCommand) Synopsis() string {
	return "List what the account holds under an estate, read from the live system"
}
