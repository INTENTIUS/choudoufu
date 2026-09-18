// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/intentius/choudoufu/internal/configs"
)

// TestBulkReadParallelismReachesTheS3Store: an option that is parsed,
// accepted and then dropped on the way to the store would leave
// TOFU_LIVE_RECORD_READ_PARALLELISM a variable that does nothing, and nothing
// else here would notice. GitHub issue #1336.
func TestBulkReadParallelismReachesTheS3Store(t *testing.T) {
	rs := &configs.LiveRecordStore{Type: "s3", Bucket: "the-bucket"}
	for _, tc := range []struct {
		name string
		opts []RecordStoreOption
		want int
	}{
		{name: "no option leaves the store's default", want: 0},
		{name: "an option is passed through", opts: []RecordStoreOption{WithBulkReadParallelism(3)}, want: 3},
		{name: "the last option wins", opts: []RecordStoreOption{WithBulkReadParallelism(3), WithBulkReadParallelism(1)}, want: 1},
	} {
		var o recordStoreOptions
		for _, opt := range tc.opts {
			opt(&o)
		}
		cfg := s3StoreConfig(aws.Config{Region: "us-east-1"}, rs, o)
		if cfg.GetAllParallelism != tc.want {
			t.Errorf("%s: GetAllParallelism = %d, want %d", tc.name, cfg.GetAllParallelism, tc.want)
		}
		if cfg.Bucket != "the-bucket" || cfg.KeyPrefix != backendKeyPrefix {
			t.Errorf("%s: bucket %q, key prefix %q", tc.name, cfg.Bucket, cfg.KeyPrefix)
		}
	}
}
