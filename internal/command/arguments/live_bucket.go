// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"fmt"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// LiveBucket is the parsed command line of "choudoufu live-bucket".
type LiveBucket struct {
	// Bucket names the bucket to report on. Empty means "the one this
	// directory's live block declares", which is how an operator checks the
	// bucket an estate actually uses; set, no configuration is read at all,
	// which is how the runnable bucket project checks the bucket it just
	// made before any estate exists.
	Bucket string

	// Region is where the bucket is. Empty defers to the record_store
	// block's own region, then to the AWS SDK's default resolution.
	Region string

	// Estate scopes the lifecycle assertion to one estate's namespaces. Only
	// meaningful with Bucket; with a configuration the live block's own
	// estate is used.
	Estate string

	// JSON asks for one JSON document on stdout instead of the table.
	JSON bool
}

func ParseLiveBucket(args []string) (*LiveBucket, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	lb := &LiveBucket{}

	cmdFlags := defaultFlagSet("live-bucket")
	cmdFlags.StringVar(&lb.Bucket, "bucket", "", "bucket")
	cmdFlags.StringVar(&lb.Region, "region", "", "region")
	cmdFlags.StringVar(&lb.Estate, "estate", "", "estate")
	cmdFlags.BoolVar(&lb.JSON, "json", false, "json")

	if err := cmdFlags.Parse(args); err != nil {
		return lb, diags.Append(tfdiags.Sourceless(tfdiags.Error, "Invalid option", fmt.Sprintf("%s.", err)))
	}
	if len(cmdFlags.Args()) != 0 {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Too many arguments",
			"live-bucket takes no positional arguments. Run it in a configuration directory, or name a bucket with -bucket=<name>."))
	}
	if lb.Estate != "" && lb.Bucket == "" {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "-estate needs -bucket",
			"Without -bucket the estate comes from this directory's live block, and -estate would contradict it. Pass both, or neither."))
	}
	return lb, diags
}
