// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// defaultRecordDirName is the local backend's default directory when a
// record_store "local" block sets no path, mirroring plain local state's
// own "just a filename beside the module, no configuration required" shape
// (issue #73's "it should feel just like OpenTofu" principle).
const defaultRecordDirName = ".tofu-records"

// NewRecordStore builds the [staterecord.Store] a live block's record_store
// block names, or nil when rs is nil - the "no store configured" case every
// caller in this package already treats as "RECORD_ADMITTED types stay
// refused" (internal/live/lint) or "nothing to hydrate or write back"
// (build.go, writeback.go).
//
// moduleDir is the directory the "local" backend's relative path (rs.Path,
// or the default) is resolved against - ordinarily the module directory a
// stateless run's live block was read from. estate names the key namespace
// the "ssm" and "s3" backends default to when rs.KeyPrefix is unset; see
// [RecordKeyPrefix].
//
// Building an "ssm" or "s3" store loads the AWS SDK's own default
// credential chain (environment, shared config, IMDS) unless rs.Region asks
// for a specific region; this package has no opinion on credentials beyond
// that, the same position every other AWS client this fork builds takes.
func NewRecordStore(ctx context.Context, rs *configs.LiveRecordStore, estate, moduleDir string) (staterecord.Store, error) {
	if rs == nil {
		return nil, nil
	}

	switch rs.Type {
	case "local":
		dir := rs.Path
		if dir == "" {
			dir = defaultRecordDirName
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(moduleDir, dir)
		}
		store, err := staterecord.NewLocalStore(dir)
		if err != nil {
			return nil, fmt.Errorf("record_store \"local\": %w", err)
		}
		return store, nil

	case "ssm":
		awsCfg, err := loadAWSConfig(ctx, rs.Region)
		if err != nil {
			return nil, fmt.Errorf("record_store \"ssm\": %w", err)
		}
		store, err := staterecord.NewSSMStore(staterecord.SSMConfig{
			Client:    ssm.NewFromConfig(awsCfg),
			KeyPrefix: recordStoreKeyPrefix(rs, estate),
		})
		if err != nil {
			return nil, fmt.Errorf("record_store \"ssm\": %w", err)
		}
		return store, nil

	case "s3":
		awsCfg, err := loadAWSConfig(ctx, rs.Region)
		if err != nil {
			return nil, fmt.Errorf("record_store \"s3\": %w", err)
		}
		store, err := staterecord.NewS3Store(staterecord.S3Config{
			Client:    s3.NewFromConfig(awsCfg),
			Bucket:    rs.Bucket,
			KeyPrefix: recordStoreKeyPrefix(rs, estate),
		})
		if err != nil {
			return nil, fmt.Errorf("record_store \"s3\": %w", err)
		}
		return store, nil

	default:
		// internal/configs/live.go's decodeRecordStoreBlock already refuses
		// anything but "local"/"ssm"/"s3" at config-decode time, so a
		// caller reaching here has a *configs.LiveRecordStore that bypassed
		// that decoder - an internal inconsistency, not a configuration
		// mistake an operator could have made.
		return nil, fmt.Errorf("record_store: unknown backend %q", rs.Type)
	}
}

// RecordStoreKeyPrefix is the key namespace [NewRecordStore] builds an
// "ssm" or "s3" store with: rs.KeyPrefix when the block set one, or
// [RecordKeyPrefix](estate) otherwise. Exported so a caller that already
// built the store (or is testing namespace safety) can compute the same
// prefix without re-deriving it.
func RecordStoreKeyPrefix(rs *configs.LiveRecordStore, estate string) string {
	return recordStoreKeyPrefix(rs, estate)
}

func recordStoreKeyPrefix(rs *configs.LiveRecordStore, estate string) string {
	if rs != nil && rs.KeyPrefixSet {
		return rs.KeyPrefix
	}
	return RecordKeyPrefix(estate)
}

// loadAWSConfig is the ordinary aws-sdk-go-v2 default-config chain, with an
// explicit region when the record_store block named one.
func loadAWSConfig(ctx context.Context, region string) (aws.Config, error) {
	if region != "" {
		return awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	}
	return awsconfig.LoadDefaultConfig(ctx)
}
