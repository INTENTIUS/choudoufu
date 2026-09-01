// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"

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
	store, err := newRecordStore(ctx, rs, estate, moduleDir)
	if err != nil || store == nil {
		return store, err
	}
	// Order is load-bearing. The counter goes UNDER the cache, so what it
	// counts is trips that actually reached the backend; over the cache it
	// would count the calls the cache absorbs and report no change from
	// having one, which is the measurement reading its own reflection.
	counted, err := wrapForTripLog(store)
	if err != nil {
		return nil, err
	}
	// The provisioning handshake, under the trip counter so its two
	// backend calls are counted honestly and above the run cache so the
	// List it verifies is the backend's own, never a snapshot's.
	if err := provisionStoreSentinel(ctx, counted, recordStoreKeyPrefix(rs, estate)); err != nil {
		return nil, err
	}
	// The estate's records, loaded once and in bulk the way stock loads its
	// state file - see [staterecord.RunCache] for what makes it safe. The
	// prefix is the same namespace [RecordStoreKeyPrefix] gives every caller
	// that builds keys against this store, so a bulk load covers exactly the
	// keys the run will ask for.
	return staterecord.NewRunCache(counted, recordStoreKeyPrefix(rs, estate)), nil
}

// sentinelKeyName is the last segment of every store's sentinel key. It is
// not a resource record: [RecordAddr] returns false for it (the segment is
// not valid unpadded base64 of an address), which is the documented
// contract for keys this package did not write, so every List consumer
// skips it the way it skips any foreign key.
const sentinelKeyName = ".store-sentinel"

// sentinelPayload deliberately says what the record is for, so an operator
// reading the raw store sees an explanation rather than an opaque blob.
const sentinelPayload = "choudoufu record-store sentinel: proves this store's write and List paths work; see INTENTIUS/choudoufu#693"

// SentinelKey is the store's provisioning sentinel under prefix, exported
// so tests and tooling can name it without re-deriving the shape.
func SentinelKey(prefix string) string {
	if prefix == "" {
		return sentinelKeyName
	}
	return prefix + "/" + sentinelKeyName
}

// provisionStoreSentinel is issue #693's handshake: write a sentinel record
// once (PutIfAbsent, so a raced or repeated provision is a no-op), then
// read it back through List - the same code path plans use to enumerate
// records. One handshake proves write, read and List together, and turns
// the failure mode that motivated it inside out: a store whose List
// silently returns nothing used to read as an empty estate and surface as
// a plan proposing to re-create live resources (#688's terralith run);
// now it is a loud, named refusal before any plan is built.
func provisionStoreSentinel(ctx context.Context, store staterecord.Store, prefix string) error {
	key := SentinelKey(prefix)
	if _, err := store.PutIfAbsent(ctx, key, []byte(sentinelPayload)); err != nil {
		var conflict *staterecord.VersionConflictError
		if !errors.As(err, &conflict) {
			return fmt.Errorf("record_store: provisioning the sentinel at %q: %w", key, err)
		}
		// Already provisioned by an earlier run or a racing one - the
		// conflict is the success case here.
	}
	listPrefix := ""
	if prefix != "" {
		listPrefix = prefix + "/"
	}
	keys, err := store.List(ctx, listPrefix)
	if err != nil {
		return fmt.Errorf("record_store: reading the sentinel back through List: %w", err)
	}
	if !slices.Contains(keys, key) {
		return fmt.Errorf("record_store: the store accepted the sentinel write at %q but List(%q) does not return it, so this store's List is broken and every record in it is invisible to a plan; refusing rather than planning against an estate that would read as empty (issue #693)", key, listPrefix)
	}
	return nil
}

func newRecordStore(ctx context.Context, rs *configs.LiveRecordStore, estate, moduleDir string) (staterecord.Store, error) {
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
