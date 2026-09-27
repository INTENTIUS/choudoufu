// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"strings"
	"testing"
)

// TestExamplesStateTheirSubstrate is GitHub issue #1603: every example under
// examples/ hard-codes `provider "aws"` somewhere or names AWS in its
// prose, and four of them (pipeline-governance, cross-estate-dependency,
// ci-pipelines, converge-operator) said so nowhere - a reader had no way to
// tell "AWS-only, deliberately" from "AWS-only, nobody got to the rest yet".
//
// The maintainer's 2026-09-26 ruling: each README states which substrates it
// covers and why, and a Kubernetes variant only where it is small. None of
// these four gets one here: each demonstrates something specific to AWS
// (the Resource Groups Tagging API's IAM exemption, a tag-filtered data
// source, drift introduced from the AWS CLI, or governance over another
// AWS-only example's credentials), and each carries a forge-pipeline or
// chant-Op scaffold that a second substrate would have to duplicate and test
// on its own, which is the opposite of small. record-store-bucket and
// record-store-cluster already say what they cover by name and cross-refer
// to each other, and live-mv-workbench already says "on a real AWS account"
// in its second sentence, so none of those three needed a change here.
//
// This does not assert the reasoning is correct, only that each README says
// it out loud in a place a reader will find before writing a Kubernetes
// estate.chdf.hcl for one of these and hitting a hard-coded `provider "aws"`.
func TestExamplesStateTheirSubstrate(t *testing.T) {
	for _, tc := range []struct {
		readme string
		says   []string
	}{
		{
			"../examples/pipeline-governance/README.md",
			[]string{"## Substrate", "AWS-only", "ci-pipelines"},
		},
		{
			"../examples/cross-estate-dependency/README.md",
			[]string{"## Substrate", "AWS-only", "tag:tofu-estate"},
		},
		{
			"../examples/ci-pipelines/README.md",
			[]string{"## Substrate", "AWS-only", "Resource Groups Tagging API"},
		},
		{
			"../examples/converge-operator/README.md",
			[]string{"## Substrate", "AWS-only", "AWS CLI"},
		},
	} {
		t.Run(tc.readme, func(t *testing.T) {
			raw, err := os.ReadFile(tc.readme)
			if err != nil {
				t.Fatal(err)
			}
			page := string(raw)
			for _, want := range tc.says {
				if !strings.Contains(page, want) {
					t.Errorf("%s does not contain %q; it must say which substrates it covers and why", tc.readme, want)
				}
			}
		})
	}
}
