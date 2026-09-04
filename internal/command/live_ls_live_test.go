// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// TestLiveLsIAMAgainstFloci is GitHub issue #789's "Done when" clause for
// the IAM path: iam:ListRoles and iam:ListRoleTags, driven by
// LiveLsCommand's own liveLsIAMRoles, against a real floci emulator rather
// than the fake wire-protocol server live_ls_test.go's
// TestLiveLsRead_mergesTaggingAndIAM uses. That test proves this command's
// XML decoding matches the real IAM wire shape byte for byte; this one
// proves the emulator actually answers those two calls the way this
// command assumes - the "real-account fixture or a documented emulator
// gap" the issue's Done-when clause asks for, in the same spirit
// tagging_live_test.go's TestFlociServesTaggingAPI probes the tagging side
// before trusting anything built on top of it.
//
// A role's own tags are read straight back through iam:ListRoleTags first,
// independent of this command, to keep this test's own verdict from
// depending on whether the round trip through liveLsIAMRoles is correct -
// see internal/live/discovery/tagging_live_test.go's TestFlociServesTaggingAPI
// for the same discipline.
//
//	TF_FLOCI_TEST=1 go test ./internal/command/ -run TestLiveLsIAMAgainstFloci -v
func TestLiveLsIAMAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "command/live-ls-iam")
	flocitest.RequireBinary(t, "docker")

	const awsRegion = "us-east-1"

	ctx := context.Background()
	flociPort := flocitest.StartFloci(t, "cdf-livels-iam")
	endpoint := flocitest.Endpoint(flociPort)

	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)

	awsCfg, err := liveLsAWSConfig(ctx, awsRegion)
	if err != nil {
		t.Fatalf("loading the AWS config: %v", err)
	}
	iamClient := iam.NewFromConfig(awsCfg, func(o *iam.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	const estate = "livels-iam-e2e"
	const roleName = "tofu-livels-iam-probe"

	_, err = iamClient.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String(roleName),
		AssumeRolePolicyDocument: aws.String(`{"Version":"2012-10-17","Statement":[]}`),
		Tags: []iamtypes.Tag{
			{Key: aws.String("tofu-estate"), Value: aws.String(estate)},
			{Key: aws.String("tofu-address"), Value: aws.String("aws_iam_role.probe")},
		},
	})
	if err != nil {
		t.Fatalf("floci does not serve iam:CreateRole with inline tags: %v", err)
	}

	// Read the role's tags back directly, with no choudoufu code involved,
	// so a failure below is unambiguously about liveLsIAMRoles and not
	// about whether floci itself carried the tags at all.
	directTags, err := iamClient.ListRoleTags(ctx, &iam.ListRoleTagsInput{RoleName: aws.String(roleName)})
	if err != nil {
		t.Fatalf("floci does not serve iam:ListRoleTags: %v", err)
	}
	if len(directTags.Tags) == 0 {
		t.Fatal("floci accepted CreateRole's inline tags but iam:ListRoleTags reads none back; nothing this command does could recover from that")
	}

	c := &LiveLsCommand{}
	items, diags := c.liveLsIAMRoles(ctx, estate, iamClient, map[string]bool{})
	if diags.HasErrors() {
		t.Fatalf("liveLsIAMRoles: %v", diags.Err())
	}

	var found bool
	for _, item := range items {
		if item.ID == "" {
			continue
		}
		if item.Address == "aws_iam_role.probe" {
			found = true
			if item.Source != "iam" {
				t.Errorf("Source = %q, want iam", item.Source)
			}
		}
	}
	if !found {
		t.Fatalf("the probe role was not found by liveLsIAMRoles against real floci; items:\n%+v", items)
	}
}
