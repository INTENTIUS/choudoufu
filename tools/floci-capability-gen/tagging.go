// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
)

// randomSuffix names this run's probe resources so a re-run never collides
// with a previous one's leftovers (cleanup is best-effort, not guaranteed).
func randomSuffix() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is effectively unrecoverable for this process;
		// a fixed fallback still keeps the probe usable rather than crashing
		// on an entropy source outage.
		return "fallback"
	}
	return hex.EncodeToString(b[:])
}

// taggingRecipeResult is one recipe's outcome: the resource it created and
// tagged (identified by the ARN [GetResources] would need to echo back), or
// an error if creation itself failed - a floci-side or aws-CLI-side problem
// that says nothing about the tagging index and is skipped rather than
// turned into a false finding.
type taggingRecipeResult struct {
	tfType   string
	arn      string
	evidence string // how the resource was created and confirmed natively
	cleanup  func()
}

// taggingRecipe creates one minimal, tagged resource of a given type
// through its own ordinary service API (never through Cloud Control, and
// never through terraform - the aws CLI directly, so a finding here is
// unambiguously about the Resource Groups Tagging API and not about
// anything this fork's own client code does), confirms the tags landed
// through that same service's native read call, and returns the ARN
// [probeTagging] then checks for in a GetResources sweep.
//
// Each recipe is hand-written and verified against a live floci instance
// (issue #229's investigation, extended here across seven services) rather
// than derived mechanically - the same "no generic Create recipe is safe to
// invent" constraint probeCloudControl.go's doc comment records for
// mechanism="cloudcontrol-list", except here the curated set itself is what
// the generator drives, so a re-run against a fixed image always replays
// the same recipes rather than needing a fresh hand investigation.
type taggingRecipe struct {
	tfType  string
	service string // for evidence text: which AWS service API created it
	run     func(ctx context.Context, aws awsRunner, suffix string) (taggingRecipeResult, error)
}

// awsRunner runs one aws CLI invocation against the floci endpoint under
// probe and returns its stdout, or an error embedding stderr - the shared
// plumbing every recipe's own service-specific calls sit on top of.
type awsRunner func(ctx context.Context, args ...string) (string, error)

// newAWSRunner does not force an --output: each recipe passes its own,
// "text" for a --query'd scalar (an ARN, an ID - text renders it bare, with
// no surrounding quotes) and "json" for a native tag-confirmation call,
// because the CLI's text formatter renders a map-shaped result (Tags as
// {key: value}, e.g. list-queue-tags) as bare values with no key names at
// all - a first version of this file forced --output=text everywhere and
// that formatter quirk made every tag-confirmation check fail to find the
// key "tofu-estate" it was actually looking at, misreporting six working
// services as recipe failures before this was caught by rerunning one by
// hand and diffing json against text output.
func newAWSRunner(endpoint, region string) awsRunner {
	return func(ctx context.Context, args ...string) (string, error) {
		full := append([]string{"--endpoint-url=" + endpoint, "--region=" + region}, args...)
		cmd := exec.CommandContext(ctx, "aws", full...) //nolint:gosec // fixed binary, args are this file's own literals plus a probe-local suffix
		cmd.Env = append(os.Environ(),
			"AWS_ACCESS_KEY_ID=test",
			"AWS_SECRET_ACCESS_KEY=test",
			"AWS_DEFAULT_REGION="+region,
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("aws %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return strings.TrimSpace(string(out)), nil
	}
}

// taggingRecipes is the curated set probeTagging drives. All seven were
// hand-verified against ghcr.io/lex00/floci@sha256:1362e856... (the batch-3
// re-pin this same session's floci-capabilities.json update targets) on
// 2026-08-16: each creates cleanly, each confirms its own tags via a native
// per-service read, and get-resources (filtered and unfiltered) returned an
// empty ResourceTagMappingList for every one of them, extending issue #229's
// single-type finding (aws_ebs_volume) and the pre-existing aws_iam_role row
// across storage, queueing, messaging, database and secrets services -
// evidence the gap is in the tagging index itself, not in any one service's
// emulation.
var taggingRecipes = []taggingRecipe{
	{
		tfType:  "aws_ebs_volume",
		service: "ec2",
		run: func(ctx context.Context, aws awsRunner, suffix string) (taggingRecipeResult, error) {
			volID, err := aws(ctx, "ec2", "create-volume",
				"--availability-zone", "us-east-1a", "--size", "1",
				"--tag-specifications", fmt.Sprintf("ResourceType=volume,Tags=[{Key=tofu-estate,Value=probe-%s},{Key=tofu-address,Value=aws_ebs_volume.probe}]", suffix),
				"--query", "VolumeId", "--output", "text")
			if err != nil {
				return taggingRecipeResult{}, err
			}
			volID = strings.TrimSpace(volID)
			tags, err := aws(ctx, "ec2", "describe-volumes", "--volume-ids", volID, "--output", "json")
			if err != nil {
				return taggingRecipeResult{}, fmt.Errorf("confirming tags natively via describe-volumes: %w", err)
			}
			if !strings.Contains(tags, "tofu-estate") {
				return taggingRecipeResult{}, fmt.Errorf("describe-volumes did not echo the tags this recipe just wrote: %s", tags)
			}
			return taggingRecipeResult{
				tfType:   "aws_ebs_volume",
				arn:      fmt.Sprintf("arn:aws:ec2:us-east-1:000000000000:volume/%s", volID),
				evidence: fmt.Sprintf("ec2 create-volume tagged %s at creation; describe-volumes confirms tofu-estate/tofu-address natively", volID),
				cleanup:  func() { _, _ = aws(context.Background(), "ec2", "delete-volume", "--volume-id", volID) },
			}, nil
		},
	},
	{
		tfType:  "aws_s3_bucket",
		service: "s3",
		run: func(ctx context.Context, aws awsRunner, suffix string) (taggingRecipeResult, error) {
			bucket := "tofu-probe-bucket-" + suffix
			if _, err := aws(ctx, "s3api", "create-bucket", "--bucket", bucket, "--output", "json"); err != nil {
				return taggingRecipeResult{}, err
			}
			if _, err := aws(ctx, "s3api", "put-bucket-tagging", "--bucket", bucket,
				"--tagging", "TagSet=[{Key=tofu-estate,Value=probe-"+suffix+"},{Key=tofu-address,Value=aws_s3_bucket.probe}]", "--output", "json"); err != nil {
				return taggingRecipeResult{}, err
			}
			tags, err := aws(ctx, "s3api", "get-bucket-tagging", "--bucket", bucket, "--output", "json")
			if err != nil {
				return taggingRecipeResult{}, fmt.Errorf("confirming tags natively via get-bucket-tagging: %w", err)
			}
			if !strings.Contains(tags, "tofu-estate") {
				return taggingRecipeResult{}, fmt.Errorf("get-bucket-tagging did not echo the tags this recipe just wrote: %s", tags)
			}
			return taggingRecipeResult{
				tfType:   "aws_s3_bucket",
				arn:      "arn:aws:s3:::" + bucket,
				evidence: fmt.Sprintf("s3api create-bucket + put-bucket-tagging on %s; get-bucket-tagging confirms tofu-estate/tofu-address natively", bucket),
				cleanup:  func() { _, _ = aws(context.Background(), "s3api", "delete-bucket", "--bucket", bucket) },
			}, nil
		},
	},
	{
		tfType:  "aws_sqs_queue",
		service: "sqs",
		run: func(ctx context.Context, aws awsRunner, suffix string) (taggingRecipeResult, error) {
			name := "tofu-probe-queue-" + suffix
			url, err := aws(ctx, "sqs", "create-queue", "--queue-name", name,
				"--tags", "tofu-estate=probe-"+suffix+",tofu-address=aws_sqs_queue.probe", "--query", "QueueUrl", "--output", "text")
			if err != nil {
				return taggingRecipeResult{}, err
			}
			url = strings.TrimSpace(url)
			arn, err := aws(ctx, "sqs", "get-queue-attributes", "--queue-url", url, "--attribute-names", "QueueArn", "--query", "Attributes.QueueArn", "--output", "text")
			if err != nil {
				return taggingRecipeResult{}, fmt.Errorf("resolving the queue ARN: %w", err)
			}
			arn = strings.TrimSpace(arn)
			tags, err := aws(ctx, "sqs", "list-queue-tags", "--queue-url", url, "--output", "json")
			if err != nil {
				return taggingRecipeResult{}, fmt.Errorf("confirming tags natively via list-queue-tags: %w", err)
			}
			if !strings.Contains(tags, "tofu-estate") {
				return taggingRecipeResult{}, fmt.Errorf("list-queue-tags did not echo the tags this recipe just wrote: %s", tags)
			}
			return taggingRecipeResult{
				tfType:   "aws_sqs_queue",
				arn:      arn,
				evidence: fmt.Sprintf("sqs create-queue tagged %s at creation; list-queue-tags confirms tofu-estate/tofu-address natively", name),
				cleanup:  func() { _, _ = aws(context.Background(), "sqs", "delete-queue", "--queue-url", url) },
			}, nil
		},
	},
	{
		tfType:  "aws_sns_topic",
		service: "sns",
		run: func(ctx context.Context, aws awsRunner, suffix string) (taggingRecipeResult, error) {
			name := "tofu-probe-topic-" + suffix
			arn, err := aws(ctx, "sns", "create-topic", "--name", name,
				"--tags", "Key=tofu-estate,Value=probe-"+suffix, "Key=tofu-address,Value=aws_sns_topic.probe", "--query", "TopicArn", "--output", "text")
			if err != nil {
				return taggingRecipeResult{}, err
			}
			arn = strings.TrimSpace(arn)
			tags, err := aws(ctx, "sns", "list-tags-for-resource", "--resource-arn", arn, "--output", "json")
			if err != nil {
				return taggingRecipeResult{}, fmt.Errorf("confirming tags natively via list-tags-for-resource: %w", err)
			}
			if !strings.Contains(tags, "tofu-estate") {
				return taggingRecipeResult{}, fmt.Errorf("list-tags-for-resource did not echo the tags this recipe just wrote: %s", tags)
			}
			return taggingRecipeResult{
				tfType:   "aws_sns_topic",
				arn:      arn,
				evidence: fmt.Sprintf("sns create-topic tagged %s at creation; list-tags-for-resource confirms tofu-estate/tofu-address natively", name),
				cleanup:  func() { _, _ = aws(context.Background(), "sns", "delete-topic", "--topic-arn", arn) },
			}, nil
		},
	},
	{
		tfType:  "aws_dynamodb_table",
		service: "dynamodb",
		run: func(ctx context.Context, aws awsRunner, suffix string) (taggingRecipeResult, error) {
			name := "tofu-probe-table-" + suffix
			if _, err := aws(ctx, "dynamodb", "create-table", "--table-name", name,
				"--attribute-definitions", "AttributeName=id,AttributeType=S",
				"--key-schema", "AttributeName=id,KeyType=HASH",
				"--billing-mode", "PAY_PER_REQUEST",
				"--tags", "Key=tofu-estate,Value=probe-"+suffix, "Key=tofu-address,Value=aws_dynamodb_table.probe", "--output", "json"); err != nil {
				return taggingRecipeResult{}, err
			}
			arn, err := aws(ctx, "dynamodb", "describe-table", "--table-name", name, "--query", "Table.TableArn", "--output", "text")
			if err != nil {
				return taggingRecipeResult{}, fmt.Errorf("resolving the table ARN: %w", err)
			}
			arn = strings.TrimSpace(arn)
			tags, err := aws(ctx, "dynamodb", "list-tags-of-resource", "--resource-arn", arn, "--output", "json")
			if err != nil {
				return taggingRecipeResult{}, fmt.Errorf("confirming tags natively via list-tags-of-resource: %w", err)
			}
			if !strings.Contains(tags, "tofu-estate") {
				return taggingRecipeResult{}, fmt.Errorf("list-tags-of-resource did not echo the tags this recipe just wrote: %s", tags)
			}
			return taggingRecipeResult{
				tfType:   "aws_dynamodb_table",
				arn:      arn,
				evidence: fmt.Sprintf("dynamodb create-table tagged %s at creation; list-tags-of-resource confirms tofu-estate/tofu-address natively", name),
				cleanup:  func() { _, _ = aws(context.Background(), "dynamodb", "delete-table", "--table-name", name) },
			}, nil
		},
	},
	{
		tfType:  "aws_iam_role",
		service: "iam",
		run: func(ctx context.Context, aws awsRunner, suffix string) (taggingRecipeResult, error) {
			name := "tofu-probe-role-" + suffix
			policy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}`
			arn, err := aws(ctx, "iam", "create-role", "--role-name", name,
				"--assume-role-policy-document", policy,
				"--tags", "Key=tofu-estate,Value=probe-"+suffix, "Key=tofu-address,Value=aws_iam_role.probe",
				"--query", "Role.Arn", "--output", "text")
			if err != nil {
				return taggingRecipeResult{}, err
			}
			arn = strings.TrimSpace(arn)
			tags, err := aws(ctx, "iam", "list-role-tags", "--role-name", name, "--output", "json")
			if err != nil {
				return taggingRecipeResult{}, fmt.Errorf("confirming tags natively via list-role-tags: %w", err)
			}
			if !strings.Contains(tags, "tofu-estate") {
				return taggingRecipeResult{}, fmt.Errorf("list-role-tags did not echo the tags this recipe just wrote: %s", tags)
			}
			return taggingRecipeResult{
				tfType:   "aws_iam_role",
				arn:      arn,
				evidence: fmt.Sprintf("iam create-role tagged %s at creation; list-role-tags confirms tofu-estate/tofu-address natively", name),
				cleanup:  func() { _, _ = aws(context.Background(), "iam", "delete-role", "--role-name", name) },
			}, nil
		},
	},
	{
		tfType:  "aws_secretsmanager_secret",
		service: "secretsmanager",
		run: func(ctx context.Context, aws awsRunner, suffix string) (taggingRecipeResult, error) {
			name := "tofu-probe-secret-" + suffix
			arn, err := aws(ctx, "secretsmanager", "create-secret", "--name", name,
				"--secret-string", "probe",
				"--tags", "Key=tofu-estate,Value=probe-"+suffix, "Key=tofu-address,Value=aws_secretsmanager_secret.probe",
				"--query", "ARN", "--output", "text")
			if err != nil {
				return taggingRecipeResult{}, err
			}
			arn = strings.TrimSpace(arn)
			tags, err := aws(ctx, "secretsmanager", "describe-secret", "--secret-id", name, "--output", "json")
			if err != nil {
				return taggingRecipeResult{}, fmt.Errorf("confirming tags natively via describe-secret: %w", err)
			}
			if !strings.Contains(tags, "tofu-estate") {
				return taggingRecipeResult{}, fmt.Errorf("describe-secret did not echo the tags this recipe just wrote: %s", tags)
			}
			return taggingRecipeResult{
				tfType:   "aws_secretsmanager_secret",
				arn:      arn,
				evidence: fmt.Sprintf("secretsmanager create-secret tagged %s at creation; describe-secret confirms tofu-estate/tofu-address natively", name),
				cleanup: func() {
					_, _ = aws(context.Background(), "secretsmanager", "delete-secret", "--secret-id", name, "--force-delete-without-recovery")
				},
			}, nil
		},
	},
}

// probeTagging runs every taggingRecipes entry against endpoint (creating
// one small real resource per type, natively confirming its own tags, and
// cleaning up afterward on a best-effort basis), then makes exactly one
// GetResources sweep - unfiltered, the estate-wide shape
// internal/live/discovery's TaggingSweep path itself uses - and classifies
// each recipe's type by whether its ARN appears in that sweep's result. A
// recipe whose own creation fails (a transport problem, or floci not
// emulating that service's ordinary API at all) is skipped rather than
// misreported: a service outage there says nothing about the tagging
// index, the thing this mode exists to measure.
func probeTagging(ctx context.Context, endpoint, region string) (rows []typeRow, checked int, err error) {
	const source = "live probe (tools/floci-capability-gen -mode=tagging)"

	aws := newAWSRunner(endpoint, region)
	suffix := randomSuffix()

	var created []taggingRecipeResult
	defer func() {
		for _, r := range created {
			if r.cleanup != nil {
				r.cleanup()
			}
		}
	}()

	for _, recipe := range taggingRecipes {
		res, rerr := recipe.run(ctx, aws, suffix)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "floci-capability-gen: tagging: skipping %s, creating it failed: %v\n", recipe.tfType, rerr)
			continue
		}
		checked++
		created = append(created, res)
	}

	tagging := cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: endpoint, Region: region, RoundTripper: http.DefaultTransport})
	seen, gerr := tagging.GetResources(ctx, nil, nil)
	if gerr != nil {
		return nil, checked, fmt.Errorf("the shared GetResources sweep failed, so no recipe's outcome can be classified: %w", gerr)
	}
	seenARNs := map[string]bool{}
	for _, tr := range seen {
		seenARNs[tr.ResourceARN] = true
	}

	for _, res := range created {
		if seenARNs[res.arn] {
			rows = append(rows, typeRow{
				Type:      res.tfType,
				Mechanism: "tagging-sweep",
				Status:    "implemented",
				Evidence:  fmt.Sprintf("%s, and an unfiltered resourcegroupstaggingapi GetResources sweep includes %s", res.evidence, res.arn),
				Source:    source,
			})
			continue
		}
		rows = append(rows, typeRow{
			Type:      res.tfType,
			Mechanism: "tagging-sweep",
			Status:    "unimplemented",
			Evidence:  fmt.Sprintf("%s, but an unfiltered resourcegroupstaggingapi GetResources sweep (%d resources total) does not include %s", res.evidence, len(seen), res.arn),
			Source:    source,
		})
	}

	return rows, checked, nil
}
