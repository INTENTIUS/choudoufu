// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/registry"
)

// TestFlociServesTaggingAPI is the "curl the endpoint shape" probe issue #51
// asks for before trusting the gated e2e test below to say anything about
// floci's real behavior: one real GetResources call against a bare
// emulator, independent of terraform or the AWS provider, so a failure here
// is unambiguously about floci's tagging support and not about anything
// else this fixture does. If this fails, TestTaggingSweepAgainstFloci's
// finding is moot and should be read as "floci does not serve the tagging
// API at all" rather than the narrower "does not yet reflect resources"
// gap that test documents.
//
// Evidence recorded from this probe (floci 1.5.33, ghcr.io/lex00/floci,
// checked 2026-08-12): /_localstack/health lists "tagging": "running", and a
// GetResources call succeeds - but only once the request's Content-Type
// says "application/x-amz-json-1.1", the Resource Groups Tagging API's real
// protocol version (confirmed against botocore's own
// resourcegroupstaggingapi service model: jsonVersion "1.1", vs. Cloud
// Control's "1.0"). The identical call with "1.0" - what this package's
// Client sent before this issue fixed it in cloudcontrol/client.go and
// tagging.go - comes back {"__type":"UnknownOperationException",...} even
// though X-Amz-Target already names GetResources correctly. See
// TestGetResourcesHitsTaggingTarget in cloudcontrol/tagging_test.go for the
// unit-level pin of the fix.
func TestFlociServesTaggingAPI(t *testing.T) {
	flocitest.Gate(t, "discovery/tagging")
	flocitest.RequireBinary(t, "docker")

	flociPort := flocitest.StartFloci(t, "cdf-tagging51-probe")
	endpoint := flocitest.Endpoint(flociPort)

	tagging := cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: endpoint, Region: awsRegion})
	res, err := tagging.GetResources(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("floci does not serve the Resource Groups Tagging API's GetResources: %v", err)
	}
	t.Logf("floci served GetResources: %d resources on a fresh emulator", len(res))
}

// TestTaggingSweepAgainstFloci is issue #51's e2e case, mirroring
// cloudcontrol_live_test.go's TestDiscoverCloudControlFallbackAgainstFloci:
// real terraform, real AWS provider, real floci - but through
// Request.TaggingSweep and cloudcontrol.Client.GetResources rather than
// Cloud Control's ListResources.
//
// It asserts the bind. From the pin's move to
// ghcr.io/lex00/floci@sha256:a1c729f4... this test discovers
// aws_iam_role.demo through the estate-wide sweep and checks its ImportID,
// rather than recording a gap and skipping.
//
// That was not always true, and the history is worth keeping because the
// skip branch below is what a regression would land back on. Through
// sha256:1362e856... floci served GetResources correctly on the wire - once
// the Content-Type fix TestFlociServesTaggingAPI documents was in place -
// but the index it answered from was fed by only 2 of its 64 services, so
// `aws iam create-role ... --tags tofu-estate=...` followed immediately by
// `aws resourcegroupstaggingapi get-resources` returned an empty
// ResourceTagMappingList even though `iam list-role-tags` on the same role
// returned the tags that were written. Issue #229 (2026-08-16) established
// the gap was estate-wide rather than aws_iam_role- or S3-specific:
// tools/floci-capability-gen -mode=tagging drives seven curated recipes
// across seven distinct services (EC2, S3, SQS, SNS, DynamoDB, IAM, Secrets
// Manager), each confirming its own tags natively first, and all seven came
// back empty.
//
// floci's fix (lex00/floci#229) unions that private map with a live read of
// every service's stores through StorageFactory, recognising tags and ARNs
// structurally rather than per service. Re-probed against
// sha256:a1c729f445a96fce8858ac45318d5188b5c2afc76a06e819f234326d52e6bd5f
// on 2026-08-16: the same seven recipes are 7/7 implemented - see
// live/floci-capabilities.json's "tagging-sweep" rows under that digest for
// each one's ARN and native tag confirmation, and
// tools/floci-capability-gen/tagging.go's package-level doc comment for the
// probe's own mechanics.
//
// A separate direct probe on the same digest answered the one thing the
// oracle above cannot, because it sweeps unfiltered while
// sweepViaTagging sends TagFilter{Key: "tofu-estate", Values: [estate]}:
// the union index honours TagFilters. Two SQS queues and one SNS topic
// tagged across two estates plus one untagged topic gave 3 hits unfiltered,
// 2 for tofu-estate=alpha, 1 for tofu-estate=beta, 0 for an absent value, 3
// for a key-only filter and 0 for an absent key, with ResourceTypeFilters
// narrowing correctly too. So the loop at tagging.go's sweepViaTagging does
// not see foreign-estate ARNs and cannot raise spurious
// ProblemUnsweepableOwnedType/ProblemUnresolvedTaggedARN warnings.
//
// The skip branch below is deliberately self-retiring:
// flocitest.TaggingSweepCapabilityGate skips only on unimplemented/broken,
// so an implemented row makes it a no-op and the t.Fatal after it fires.
// Nothing needs editing here if floci regresses or if a future pin loses the
// union index - the manifest row is what decides. See
// testdata/tagging-e2e/main.tf for the type choice (aws_iam_role) and
// TestFlociServesTaggingAPI for the Content-Type finding that makes this
// test reach floci's tagging service at all.
//
// Amendment, issue #1050: aws_iam_role's own scan below no longer takes the
// tagging leg this test was written to exercise. partitionSweepTypes routes
// it through the native per-type leg unconditionally now, regardless of
// Request.TaggingSweep - see the scan.Source assertion below (which used to
// read SourceTagging and now reads SourceProvider) for the two independent
// gates that do it. That routing assertion does NOT depend on floci serving
// a tagging sweep for IAM, and it runs and passes at the current pin.
//
// What still does, and is gated separately below in its own subtest: this
// pin cannot recover aws_iam_role.demo as a removal candidate either, but
// not because enumeration needs the tagging leg (it does not any more) - the
// native leg's marker read for an object iam:ListRoles returns with no tags
// falls back to issue #266's estate tag-index join, fed by the exact same
// GetResources call the tagging leg needed. That join still fails at this
// pin, so the role's marker reads as unset and an ordinary sweep drops it
// with no Orphan, no Unclaimed entry and no Problem - see the subtest's own
// comment for the exact code path. TestFlociServesTaggingAPI above is what
// still exercises GetResources directly.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/discovery/ -run TestTaggingSweepAgainstFloci -v
func TestTaggingSweepAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "discovery/tagging")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, terraformBin)

	ctx := context.Background()
	flociPort := flocitest.StartFloci(t, "cdf-tagging51")
	endpoint := flocitest.Endpoint(flociPort)

	t.Setenv("AWS_ENDPOINT_URL", endpoint)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)

	dir := copyFixture(t, filepath.Join(flocitest.RepoRoot(t), "internal", "live", "discovery", "testdata", "tagging-e2e"))
	flocitest.PluginCacheDir(t)
	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")
	flocitest.Run(t, dir, terraformBin, "apply", "-auto-approve", "-input=false", "-no-color")

	stateFile := filepath.Join(dir, "terraform.tfstate")
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("stock apply left no state file: %v", err)
	}
	if err := os.Remove(stateFile); err != nil {
		t.Fatalf("removing the state file: %v", err)
	}
	_ = os.Remove(stateFile + ".backup")

	provider := launchAWSProvider(t, dir)
	cfg := loadConfig(t, dir)

	roster, err := registry.Load(
		filepath.Join(flocitest.RepoRoot(t), "live", "mapping.json"),
		filepath.Join(flocitest.RepoRoot(t), "live", "registry.json"),
	)
	if err != nil {
		t.Fatalf("loading the real live/mapping.json and live/registry.json: %v", err)
	}

	tagging := cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: endpoint, Region: awsRegion})

	// The role is not in this pass's declared config at all - the whole
	// point is that the estate-wide sweep finds it with nothing waiting on
	// discovery, the same shape TestSweepFindsDeletedBlock exercises against
	// a fake cloud. Resolutions is empty, and Sweep is what does the work.
	res, diags := Discover(ctx, Request{
		Estate:       "tagging-e2e",
		Config:       cfg,
		Resolutions:  nil,
		Provider:     provider,
		Region:       awsRegion,
		Sweep:        true,
		SweepTypes:   []string{"aws_iam_role"},
		Tagging:      tagging,
		TaggingSweep: true,
		Roster:       roster,
	})
	t.Logf("discovery result:\n%s", res)
	assertNoErrors(t, diags)

	// aws_iam_role cannot land here as SourceTagging any more (issue #1050).
	// partitionSweepTypes puts a type in the native universe when EITHER
	// typeNeedsResourceObjectToRecompose(t) is true OR arnJoinReaches(...)
	// is false, and this repository's own worked example proved BOTH arms
	// independently true for aws_iam_role at the current pin - disabling
	// only typeNeedsResourceObjectToRecompose's IAM branch did not flip
	// scan.Source; disabling taggingAPIUnservedServices's "aws_iam_" entry
	// as well did:
	//  - typeNeedsResourceObjectToRecompose (issue #394) answers true for
	//    aws_iam_role unconditionally - it is the aws_iam_service_linked_role
	//    sibling pair's companion, and the tag sweep's ARN-joined candidate
	//    never carries enough to bind that pair safely.
	//  - arnJoinReaches (issue #692) answers false for it too:
	//    taggingAPIUnservedType says IAM is a service GetResources never
	//    indexes at all (probed against real AWS and floci alike), and the
	//    AWS provider's own schema DOES support listing aws_iam_role
	//    natively, so arnJoinReaches's own "prefer a leg that can enumerate
	//    the type" rule (issue #881) picks native over a tagging leg that
	//    would find nothing.
	// Either alone is sufficient to keep sweepViaTagging from ever seeing
	// this type; both are true today. internal/command/tagging_sweep_premise_test.go's
	// alwaysNativeSweepTypes records the same fact by name (citing #394),
	// for the package that cannot import discovery's unexported
	// typeNeedsResourceObjectToRecompose to check it directly - that
	// citation names one of the two gates, not the only one. This
	// assertion predates aws_iam_role's routing having ever been
	// unconditional here - see #394, #692 and #1045 for how the type went
	// from tagging-served to native-only as the emulator's own IAM
	// coverage changed - and this is the CURRENT routing, not the
	// original one.
	scan, ok := res.ScanFor("aws_iam_role")
	if !ok {
		t.Fatal("no scan was recorded for aws_iam_role at all")
	}
	if scan.Source != SourceProvider {
		t.Fatalf("aws_iam_role scan source = %q, want %q (the native per-type leg - partitionSweepTypes keeps it out of the tagging leg unconditionally, per both typeNeedsResourceObjectToRecompose and arnJoinReaches)", scan.Source, SourceProvider)
	}

	// Everything above this line does NOT depend on floci serving a tagging
	// sweep for IAM, and now runs and passes at the current pin (issue
	// #1050's whole point - the routing assertion above regressed to
	// asserting the OLD, now-impossible shape and this run proves the fix
	// without ever reaching the gate below).
	//
	// Recovering aws_iam_role.demo as a REMOVAL candidate is a separate
	// question this pin still cannot answer, for a reason worth being
	// precise about because it is not the one this test used to assume.
	// It is not that enumeration needs the tagging leg - scan.Source above
	// already proves enumeration went through the native leg instead. It is
	// that the native leg's own MARKER read for this object does:
	// iam:ListRoles never returns tags at all (discovery.go's own comment,
	// issue #266), so scanType falls back to joining the object's identifier
	// against the estate's tag index (req.markers.join) - the SAME
	// GetResources call the tagging leg needs and live/floci-capabilities.json
	// records as unimplemented for aws_iam_role at this pin. The join fails,
	// the role's marker reads as unset, and an ordinary sweep
	// (CollectUnclaimed unset) treats an unset marker as nothing worth
	// reporting (discovery.go: `case estate == "": if sweep &&
	// !collectUnclaimed { continue }`) - no Orphan, no Unclaimed entry, no
	// Problem, confirmed by inspecting Result.Orphans/Unclaimed/Problems
	// directly against a live run before writing this comment. So this
	// fragment - and only this fragment - stays gated, in its own subtest so
	// a skip here cannot take the routing assertion above down with it.
	//
	// Issue #1136 splits the ungated half back out of it. Whether this pin
	// can RECOVER the role is a capability question and stays gated below;
	// whether the run SAYS it could not look is not, and never was - that
	// is this fork's own behaviour, true at every pin, and the defect #1136
	// fixed was precisely that it said nothing. So it is asserted here with
	// no gate in front of it, and the assertion is self-retiring in the
	// same way the gate is: a future pin that serves IAM through
	// GetResources recovers the removal and files no gap, and this subtest
	// follows it without editing.
	t.Run("removal-or-gap-never-silence", func(t *testing.T) {
		_, recovered := removalsByAddr(res)[`aws_iam_role.demo`]

		var gap SweepGap
		for _, g := range res.SweepGaps {
			if g.TypeName == "aws_iam_role" {
				gap = g
			}
		}
		var covered bool
		for _, c := range res.SweepCovered {
			if c == "aws_iam_role" {
				covered = true
			}
		}

		if recovered {
			// The pin serves the join. Then the sweep really did search,
			// and claiming a gap would be the opposite error.
			if gap.Reason != "" {
				t.Errorf("aws_iam_role.demo was recovered as a removal candidate AND a sweep gap was filed for the type (%s) - the gap says the search established nothing, on the run that found the object", gap)
			}
			if !covered {
				t.Errorf("aws_iam_role.demo was recovered but the type is missing from Result.SweepCovered, so a correct search reads as an unsearched one")
			}
			return
		}

		// The pin cannot serve the join, which is the state #1045 pinned
		// and #1134 re-measured against real AWS (RGTA returns 0 for
		// iam:role in every region while iam:ListRoleTags shows the tags on
		// the object). The run must say so.
		if gap.Reason != SweepGapMarkerUnreadable {
			t.Fatalf("aws_iam_role.demo was not recovered and the sweep filed %q for the type, want %q. The provider listed the role (scan.Source=PROVIDER, asserted above), its listing carries no tags, and GetResources does not index IAM - so nothing in this run established whether the estate owns a role it no longer declares, and an operator reading an empty plan cannot tell that from \"it owns none\". This is issue #1136's silent drop.\nSweepGaps: %v\nSweepCovered: %v", gap.Reason, SweepGapMarkerUnreadable, res.SweepGaps, res.SweepCovered)
		}
		if covered {
			t.Errorf("aws_iam_role is in Result.SweepCovered - \"searched for resources this estate owns but no longer declares\" - on the same run that files %s. A coverage claim over a search that established nothing is a false claim.", gap.Reason)
		}
		var spoken bool
		for _, d := range diags {
			if d.Description().Summary == SummaryIncompleteSweep {
				spoken = true
			}
		}
		if !spoken {
			t.Errorf("the gap was recorded on the Result but no diagnostic carries it, so nothing reaches the operator:\n%s", renderDiags(diags))
		}
		t.Logf("gap reported, quoted verbatim: %s", gap.Detail)
	})

	t.Run("removal-detected-via-tag-index-join", func(t *testing.T) {
		rm := removalsByAddr(res)
		o, ok := rm[`aws_iam_role.demo`]
		if !ok {
			// flocitest.TaggingSweepCapabilityGate skips with a loud,
			// digest-cited reason when live/floci-capabilities.json already
			// explains this exact gap. If the manifest has no matching
			// entry, this falls through to a real failure instead of a
			// silent skip: an unexplained miss here means either floci's
			// tagging-sweep coverage regressed, or a new gap needs
			// investigating and recording, not waving through by hand again.
			flocitest.TaggingSweepCapabilityGate(t, "aws_iam_role")
			t.Fatal("aws_iam_role.demo was not recovered as a removal candidate: its own native listing " +
				"(source=PROVIDER, confirmed by the parent test) carries no tags, so recovering its marker " +
				"depends on issue #266's estate tag-index join, fed by the same GetResources call this pin " +
				"cannot answer for IAM - and live/floci-capabilities.json has no entry explaining that gap for " +
				"this floci image - investigate and record the finding there (tools/floci-capability-gen's " +
				"doc comment) rather than skip unexplained")
		}
		if !strings.Contains(o.ImportID, "tagging-e2e-demo") {
			t.Errorf("ImportID = %q, want it to name the role tagging-e2e-demo", o.ImportID)
		}
		if strings.Contains(o.ImportID, "arn:") {
			t.Errorf("ImportID = %q carries a raw ARN; aws_iam_role's identity is its name, not the ARN itself", o.ImportID)
		}
		if !o.Swept {
			t.Error("the removal is not marked as found by the sweep")
		}
	})

	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Errorf("a state file exists after discovery (err = %v)", err)
	}
}
