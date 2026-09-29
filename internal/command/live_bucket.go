// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// LiveBucketCommand reports whether a record store bucket satisfies its
// contract (GitHub issues #1339 and #1341): versioning, a lifecycle that
// expires noncurrent versions, and public-access block.
//
// # It reports the bucket, not the configuration
//
// A plan or an apply honours allow_insecure (#1340), because whether a RUN
// may proceed is the operator's call. This command answers a different
// question - is this bucket correct - and answering it through the run's
// path would print green for a bucket with versioning off, which is the
// opposite of what someone runs it for. So a waiver never changes a verdict
// or the exit status here. It is named, separately, together with whether it
// is hiding anything.
//
// It writes nothing: no sentinel, no record, no tag.
type LiveBucketCommand struct {
	Meta
}

// liveBucketReport is the -json document, and the source of the table.
type liveBucketReport struct {
	Bucket  string `json:"bucket"`
	Correct bool   `json:"correct"`
	// CheckedAsEstate is the estate whose key namespaces the lifecycle was
	// checked against, or "" when none was given. With none, only a lifecycle
	// rule with no filter is credited: the answer that holds for every estate
	// that might share the bucket, and a stricter one than a run of some
	// estate gives, since a run also credits rules that reach its own three
	// namespaces. The two can disagree, so the report says which question it
	// answered (GitHub issue #1377).
	CheckedAsEstate string                  `json:"checked_as_estate"`
	Settings        []liveBucketSettingLine `json:"settings"`
	// Waived is what the configuration's allow_insecure names, empty with
	// -bucket or with no waiver. It never affects Correct.
	Waived []liveWaiverLine `json:"waived"`
}

type liveBucketSettingLine struct {
	Setting string `json:"setting"`
	// Verdict is "ok", "fail" or "unreadable".
	Verdict string `json:"verdict"`
	Found   string `json:"found"`
}

func (c *LiveBucketCommand) Run(rawArgs []string) int {
	ctx := c.CommandContext()
	common, rawArgs := arguments.ParseView(rawArgs)
	c.View.Configure(common)

	args, diags := arguments.ParseLiveBucket(rawArgs)
	if diags.HasErrors() {
		c.View.Diagnostics(diags)
		return 1
	}
	c.Meta.input = false

	bucket, region, estate := args.Bucket, args.Region, args.Estate
	owner := args.BucketOwner
	var rs *configs.LiveRecordStore
	if bucket == "" {
		live, liveDiags := c.statelessSettings(ctx, false)
		diags = diags.Append(liveDiags)
		switch {
		case liveDiags.HasErrors():
		case live == nil || live.RecordStore == nil || live.RecordStore.Type != "s3":
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "No record store bucket here",
				"This directory's configuration declares no record_store \"s3\" block, so there is no bucket to report on. Name one with -bucket=<name>."))
		default:
			rs = live.RecordStore
			bucket, estate = rs.Bucket, live.Estate
			if region == "" {
				region = rs.Region
			}
			// Same direction as -region: the flag wins, and without it the
			// block's own bucket_owner is used, so running this command in a
			// configuration directory checks the bucket the estate's own runs
			// check (#1381).
			if owner == "" {
				owner = rs.BucketOwner
			}
		}
		if diags.HasErrors() {
			c.View.Diagnostics(diags)
			return 1
		}
	}

	findings, err := projection.VerifyBucket(ctx, bucket, region, owner, estate, rs)
	if err != nil {
		c.View.Diagnostics(diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot read the bucket's settings",
			fmt.Sprintf("Bucket %q could not be checked: %s. This says nothing about whether the bucket is correct.", bucket, err))))
		return 1
	}

	report := buildLiveBucketReport(bucket, estate, findings, rs)

	if args.JSON {
		out, jsonErr := json.MarshalIndent(report, "", "  ")
		if jsonErr != nil {
			c.View.Diagnostics(diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot render the report", jsonErr.Error())))
			return 1
		}
		views.NewLiveBucket(c.View).Output(string(out))
	} else {
		views.NewLiveBucket(c.View).Output(renderLiveBucketReport(report))
	}
	if !report.Correct {
		return 1
	}
	return 0
}

// buildLiveBucketReport is the whole of this command's judgement, kept apart
// from the AWS call so it can be held to the one rule that matters: Correct
// comes from the findings alone. rs is nil with -bucket. estate is what the
// findings were checked as and is only reported, never judged.
func buildLiveBucketReport(bucket, estate string, findings []staterecord.Finding, rs *configs.LiveRecordStore) liveBucketReport {
	report := liveBucketReport{Bucket: bucket, Correct: true, CheckedAsEstate: estate, Settings: []liveBucketSettingLine{}}
	failing := map[staterecord.Setting]bool{}
	for _, f := range findings {
		verdict := "ok"
		switch {
		case f.Outcome == staterecord.Unreadable:
			verdict = "unreadable"
		case !f.OK():
			verdict = "fail"
		}
		if !f.OK() {
			report.Correct = false
			failing[f.Setting] = true
		}
		report.Settings = append(report.Settings, liveBucketSettingLine{Setting: string(f.Setting), Verdict: verdict, Found: f.Found})
	}
	report.Waived = liveWaiverLines(rs, failing)
	return report
}

func renderLiveBucketReport(r liveBucketReport) string {
	var b strings.Builder
	for _, s := range r.Settings {
		fmt.Fprintf(&b, "  %-22s %-11s %s\n", s.Setting, strings.ToUpper(s.Verdict), s.Found)
	}
	renderLiveWaiverLines(&b, r.Waived, "bucket")
	if r.CheckedAsEstate == "" {
		b.WriteString("  checked with no estate: only a lifecycle rule with no filter is credited, which is the answer that holds for every estate sharing this bucket. A run also credits rules that reach its own estate's namespaces. -estate=<name> checks as that estate's runs would.\n")
	} else {
		fmt.Fprintf(&b, "  checked as estate %q: a lifecycle rule is credited when it reaches that estate's key namespaces.\n", r.CheckedAsEstate)
	}
	b.WriteString("\n")
	if r.Correct {
		fmt.Fprintf(&b, "bucket %s: correct", r.Bucket)
	} else {
		fmt.Fprintf(&b, "bucket %s: NOT correct", r.Bucket)
	}
	return b.String()
}

func (c *LiveBucketCommand) Help() string {
	return strings.TrimSpace(`
Usage: choudoufu [global options] live-bucket [options]

  Reports whether a record store bucket satisfies the three settings its
  records depend on: versioning, a lifecycle rule that expires noncurrent
  versions, and public-access block. Exits non-zero unless all three hold.

  Run with no options in a configuration directory to check the bucket its
  live block declares, or name any bucket with -bucket.

  This reports the BUCKET, not the configuration. An allow_insecure waiver
  lets a plan or an apply proceed; it never changes a verdict here. A
  configured waiver is named separately, with whether it is hiding a
  failure. Nothing is written.

  Needs s3:GetBucketVersioning, s3:GetLifecycleConfiguration and
  s3:GetBucketPublicAccessBlock. A setting the caller may not read is
  reported UNREADABLE, which is not a pass.

Options:

  -bucket=name   The bucket to check, instead of the configuration's.
  -estate=name   With -bucket: check the lifecycle against this estate's
                 key namespaces. Without it only a lifecycle rule with no
                 prefix filter counts. The report says which of the two
                 it did.
  -region=name   The bucket's region.
  -bucket-owner=id
                 The twelve-digit AWS account that must own the bucket.
                 Every read carries it, so a bucket of this name in
                 another account is refused rather than reported on.
                 Without it, the configuration's bucket_owner is used.
  -json          One JSON document on stdout.
`)
}

func (c *LiveBucketCommand) Synopsis() string {
	return "Report whether a record store bucket keeps its contract"
}
