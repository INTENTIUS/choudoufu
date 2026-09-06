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
	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/identity"
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
	c.View.Diagnostics(diags)
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
	if on {
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
		gaps, declared, gapDiags := c.liveLsGaps(ctx, args.ConfigDir, items)
		diags = diags.Append(gapDiags)
		rep.Gaps = gaps
		for i := range rep.Items {
			if rep.Items[i].Address != "" && declared[rep.Items[i].Address] {
				rep.Items[i].Declared = true
			}
		}
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
func (c *LiveLsCommand) liveLsGaps(ctx context.Context, dir string, items []views.LiveLsItem) ([]views.LiveLsGap, map[string]bool, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	skip := func(reason string) ([]views.LiveLsGap, map[string]bool, tfdiags.Diagnostics) {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Warning,
			"Declared-instance comparison skipped",
			fmt.Sprintf("%s The cloud listing above is unaffected.", reason),
		))
		return nil, nil, diags
	}

	config, cfgDiags := c.loadConfig(ctx, dir)
	if cfgDiags.HasErrors() {
		return skip(fmt.Sprintf("%s could not be loaded as a configuration: %s.", dir, cfgDiags.Err()))
	}
	if config == nil || config.Module == nil {
		return skip(fmt.Sprintf("%s has no readable module configuration.", dir))
	}

	coreOpts, err := c.contextOpts(ctx)
	if err != nil {
		return skip(fmt.Sprintf("providers could not be launched to read schemas: %s.", err))
	}

	provs := newStatelessProviders(config, coreOpts.Plugins)
	closeProviders := func() {
		if cd := provs.close(ctx); cd.HasErrors() {
			log.Printf("[WARN] live-ls: closing providers after the declared-instance comparison: %s", cd.Err())
		}
	}

	resourceSchemas := provs.resourceSchemas(ctx)

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
	closeProviders()
	if idDiags.HasErrors() {
		return skip(fmt.Sprintf("identity resolution could not complete: %s.", idDiags.Err()))
	}

	declared := make(map[string]bool, resolutions.Len())
	for _, res := range resolutions.All() {
		declared[res.Addr.String()] = true
	}

	foundInCloud := make(map[string]bool, len(items))
	for _, item := range items {
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

	return gaps, declared, diags
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

  With DIR given, the listing is cross-referenced against that directory's
  declared instances: one this listing cannot find is reported as a gap, named
  by which rung explains the absence - "record" for an instance whose identity
  lives in the estate's own record store rather than on a tagged cloud object,
  "declaration-carried" for one whose type carries no tags argument at all, so
  no marker was ever written for it - rather than left silently missing, which
  reads as an absence when it is really a rung this listing's own mechanism
  cannot reach. DIR is never required: the listing above needs no
  configuration to be complete on its own terms.

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
