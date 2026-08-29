// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Command ratification-queue-gen writes live/ratification-queue.json, issue
// #426's ordered worklist: every type live/readiness.json marks
// pending-ratification, batched by service family in the priority order
// live/COVERAGE.md's "usage-weighted summary" paragraph states (EC2/VPC, S3,
// IAM, Lambda, RDS, DynamoDB, SQS/SNS, EKS/ECS, ELB, Route53, KMS,
// CloudWatch first), with the evidence pointer for each type - PROPOSE's own
// pastable block where it covers the type, live/readiness.json's own facts
// otherwise.
//
//	go run ./tools/ratification-queue-gen
//
// This tool ratifies nothing: it does not touch
// internal/live/identity/table_generated.go, tools/row-gen/ratified.json or
// live/readiness.json itself. It reads live/readiness.json and
// live/mapping.json (both committed artifacts) and runs
// `go run ./tools/row-gen -propose` as a read-only subprocess the same way
// tools/admission-pipeline does (propose.go's own doc comment: row-gen is
// its own package main, so every caller shells out rather than importing).
// See build.go's package doc comment for the family-assignment rule and what
// it approximates.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ratification-queue-gen: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	artifact, err := Build(root)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", OutputJSONRel, err)
	}
	data = append(data, '\n')
	path := filepath.Join(root, filepath.FromSlash(OutputJSONRel))
	if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // a committed artifact, not a secret
		return fmt.Errorf("writing %s: %w", OutputJSONRel, err)
	}
	fmt.Printf("%s: %d pending-ratification types in %d batches (target %d/batch)\n",
		OutputJSONRel, artifact.Counts.PendingRatification, artifact.Counts.Batches, artifact.BatchSizeTarget)
	return nil
}
