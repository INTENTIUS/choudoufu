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
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/smithy-go/middleware"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/retry"
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
//
// rt is the live block's retry block, nil when it declares none, and it
// decides how many attempts a record write gets and under which retry mode.
// It is threaded in rather than read from the environment because the record
// store is where the attempt budget actually bites: an estate writes one
// record per resource, so a large one reaches Parameter Store's throughput
// ceiling on its own, and the SDK's default of three attempts is not enough
// to cross it (#1196, #1148).
func NewRecordStore(ctx context.Context, rs *configs.LiveRecordStore, rt *configs.LiveRetry, estate, moduleDir string) (staterecord.Store, error) {
	store, err := newRecordStore(ctx, rs, rt, estate, moduleDir)
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
	createdVersion, err := provisionStoreSentinel(ctx, counted, recordStoreKeyPrefix(rs, estate))
	if err != nil {
		return nil, err
	}
	if createdVersion != "" {
		if err := assertBucketOnFirstContact(ctx, counted, rs, estate, createdVersion); err != nil {
			return nil, err
		}
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
	return keyUnder(prefix, sentinelKeyName)
}

// provisionStoreSentinel is issue #693's handshake: write a sentinel record
// once (PutIfAbsent, so a raced or repeated provision is a no-op), then
// read it back through List - the same code path plans use to enumerate
// records. One handshake proves write, read and List together, and turns
// the failure mode that motivated it inside out: a store whose List
// silently returns nothing used to read as an empty estate and surface as
// a plan proposing to re-create live resources (#688's terralith run);
// now it is a loud, named refusal before any plan is built.
//
// createdVersion is the sentinel's version when THIS call created it and ""
// when it was already there: the one signal this package has that a run is
// an estate's first contact with its store. See [assertBucketOnFirstContact].
func provisionStoreSentinel(ctx context.Context, store staterecord.Store, prefix string) (createdVersion string, err error) {
	key := SentinelKey(prefix)
	createdVersion, err = store.PutIfAbsent(ctx, key, []byte(sentinelPayload))
	if err != nil {
		var conflict *staterecord.VersionConflictError
		if !errors.As(err, &conflict) {
			return "", fmt.Errorf("record_store: provisioning the sentinel at %q: %w", key, err)
		}
		// Already provisioned by an earlier run or a racing one - the
		// conflict is the success case here.
		createdVersion = ""
	}
	listPrefix := staterecord.NamespacePrefix(prefix)
	keys, err := store.List(ctx, listPrefix)
	if err != nil {
		return "", fmt.Errorf("record_store: reading the sentinel back through List: %w", err)
	}
	if !slices.Contains(keys, key) {
		return "", fmt.Errorf("record_store: the store accepted the sentinel write at %q but List(%q) does not return it, so this store's List is broken and every record in it is invisible to a plan; refusing rather than planning against an estate that would read as empty (issue #693)", key, listPrefix)
	}
	return createdVersion, nil
}

// backendKeyPrefix is what the "ssm" and "s3" backends' own KeyPrefix is
// set to: nothing. The namespace a record lives under is carried by the
// KEY - [RecordKey] builds every key from [recordStoreKeyPrefix]'s output,
// [provisionStoreSentinel] puts the sentinel under it, [RecordAddr] reads
// an address back out of it and [staterecord.NewRunCache] bulk-loads it -
// so handing that same string to the backend as its own prefix made every
// backend name carry it twice: measured against real AWS on 2026-09-06,
// a record_store "ssm" with key_prefix = "chdf916probe/e1" wrote the
// parameter "/chdf916probe/e1/chdf916probe/e1/aws_instance/<key>", and the
// s3 backend wrote the object key "chdf916probe/e1/chdf916probe/e1/..." in
// the same run. Nothing inside this package noticed, because both halves
// of every round trip went through the doubled name; what broke was the
// contract with everything OUTSIDE it - an operator's IAM policy, an
// `aws ssm get-parameters-by-path --path /<key_prefix>` listing, and the
// live-cert harness's own teardown - all of which name the prefix the
// configuration set, once. Issue #916.
const backendKeyPrefix = ""

func newRecordStore(ctx context.Context, rs *configs.LiveRecordStore, rt *configs.LiveRetry, estate, moduleDir string) (staterecord.Store, error) {
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
		awsCfg, err := loadAWSConfig(ctx, rs.Region, rt)
		if err != nil {
			return nil, fmt.Errorf("record_store \"ssm\": %w", err)
		}
		store, err := staterecord.NewSSMStore(staterecord.SSMConfig{
			Client: ssm.NewFromConfig(awsCfg),
			// Empty on purpose: see backendKeyPrefix.
			KeyPrefix: backendKeyPrefix,
			// Issue #1146. Empty when the block names no tier, which sends
			// no Tier at all and leaves the account's default-tier
			// configuration in charge - the only default that changes
			// nothing about what a run before this argument existed did.
			Tier: staterecord.SSMTier(rs.Tier),
		})
		if err != nil {
			return nil, fmt.Errorf("record_store \"ssm\": %w", err)
		}
		return store, nil

	case "s3":
		awsCfg, err := loadAWSConfig(ctx, rs.Region, rt)
		if err != nil {
			return nil, fmt.Errorf("record_store \"s3\": %w", err)
		}
		store, err := staterecord.NewS3Store(staterecord.S3Config{
			Client: s3.NewFromConfig(awsCfg),
			Bucket: rs.Bucket,
			// Empty on purpose: see backendKeyPrefix.
			KeyPrefix: backendKeyPrefix,
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

// RecordStoreKeyPrefix is the key namespace every key [NewRecordStore]'s
// store is asked for begins with: rs.KeyPrefix when the block set one, or
// [RecordKeyPrefix](estate) otherwise. Exported so a caller that already
// built the store (or is testing namespace safety) can compute the same
// prefix without re-deriving it.
//
// It is a prefix of the KEY, not of the backend's own namespace - the
// backends are built with no prefix of their own (see backendKeyPrefix),
// so a record's SSM parameter name is "/" + this + "/..." and its S3
// object key is this + "/...", each carrying the configured prefix once.
func RecordStoreKeyPrefix(rs *configs.LiveRecordStore, estate string) string {
	return recordStoreKeyPrefix(rs, estate)
}

func recordStoreKeyPrefix(rs *configs.LiveRecordStore, estate string) string {
	if rs != nil && rs.KeyPrefixSet {
		// An operator's key_prefix gets the same trailing delimiter the
		// default has, so "team/prod" cannot list "team/prod-eu". This is
		// how #1335's hazard is made unreachable for an override, rather
		// than refused in internal/configs: both spellings, with and without
		// the slash, mean the same namespace and neither is a mistake.
		return staterecord.NamespacePrefix(rs.KeyPrefix)
	}
	return RecordKeyPrefix(estate)
}

// loadAWSConfig is the ordinary aws-sdk-go-v2 default-config chain, with an
// explicit region when the record_store block named one and the estate's own
// retry settings applied on top.
//
// The retry options are passed even when they equal the SDK's defaults, so
// the configuration wins over AWS_MAX_ATTEMPTS and AWS_RETRY_MODE in the
// environment. See [retry.Config.Options] for why that direction is the one
// that makes a run's evidence readable.
func loadAWSConfig(ctx context.Context, region string, rt *configs.LiveRetry) (aws.Config, error) {
	opts := retry.Build(rt).Options()
	opts = append(opts, awsconfig.WithAPIOptions([]func(*middleware.Stack) error{recordStoreRequestLog}))
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}
	return awsconfig.LoadDefaultConfig(ctx, opts...)
}

// BucketNamespaces is every key namespace one estate writes under in its
// record store: its records (or the key_prefix override), its hint and its
// root outputs. It is what the bucket contract's lifecycle assertion has to
// cover - see [staterecord.CheckBucketContract].
func BucketNamespaces(rs *configs.LiveRecordStore, estate string) []string {
	return []string{recordStoreKeyPrefix(rs, estate), HintKeyPrefix(estate), RootOutputKeyPrefix(estate)}
}

// BucketContractFindings reads the bucket contract for the store a live
// block's record_store built, for one estate. ok is false when the store is
// not in a bucket and there is nothing to assert.
//
// It reports the bucket and knows nothing about waivers: whether a run may
// proceed past a finding is the caller's question (#1340), and the runnable
// project's verify (#1341) wants the bucket's true state whatever the
// configuration waives.
func BucketContractFindings(ctx context.Context, store staterecord.Store, rs *configs.LiveRecordStore, estate string) (findings []staterecord.BucketFinding, ok bool, err error) {
	checker, ok := staterecord.AsBucketContractChecker(store)
	if !ok {
		return nil, false, nil
	}
	findings, err = checker.CheckBucketContract(ctx, BucketNamespaces(rs, estate))
	return findings, true, err
}

// assertBucketOnFirstContact is one of the two places the bucket contract is
// asserted (GitHub issue #1339); internal/command's BeforeApply is the other,
// and an ordinary plan is deliberately neither.
//
// The ruling on #1339 is that the assertions do not run on every plan: they
// are facts about the bucket, which do not change between two plans, and
// three configuration reads on every plan is a cost and a permission
// requirement paid for nothing. But an estate's FIRST run against a bucket is
// the one moment a wrong bucket costs nothing to walk away from - no record
// has been written yet - so that run asserts, whatever command it is. The
// sentinel this run just created is the evidence that it is the first.
//
// A refusal takes the sentinel back out, so that the next run is a first
// contact again and refuses again until the bucket is fixed. Leaving it
// behind would make the second plan proceed against the bucket the first one
// refused.
func assertBucketOnFirstContact(ctx context.Context, store staterecord.Store, rs *configs.LiveRecordStore, estate, sentinelVersion string) error {
	findings, ok, err := BucketContractFindings(ctx, store, rs, estate)
	if !ok {
		return nil
	}
	var refusal error
	if err != nil {
		refusal = err
	} else {
		// #1340: a waiver reaches only the settings it names. The warning a
		// waived run owes is internal/command's, which sees every run and
		// not just the first.
		refused, _ := staterecord.SplitWaived(findings, rs.AllowInsecure)
		if msg := BucketContractRefusalText(rs.Bucket, refused); msg != "" {
			refusal = errors.New(msg)
		}
	}
	if refusal == nil {
		return nil
	}
	if delErr := store.Delete(ctx, SentinelKey(recordStoreKeyPrefix(rs, estate)), sentinelVersion); delErr != nil {
		return fmt.Errorf("%w\n\n(The store sentinel this run created could not be removed again: %s. The next plan will not repeat this check; the next apply will.)", refusal, delErr)
	}
	return refusal
}

// BucketContractRefusalText renders every failed finding as one message,
// each under its own headline, or "" when all passed.
func BucketContractRefusalText(bucket string, findings []staterecord.BucketFinding) string {
	var b strings.Builder
	for _, f := range findings {
		summary, detail := staterecord.BucketContractRefusal(bucket, f)
		if summary == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(summary)
		b.WriteString(". ")
		b.WriteString(detail)
	}
	return b.String()
}

// VerifyBucket reads the bucket contract for a bucket named directly, with
// no store opened and no sentinel written: it is what `choudoufu live-bucket`
// runs, and what the runnable bucket project's `just verify` calls (GitHub
// issue #1341), so that the project and the tool cannot drift into
// disagreeing about what a correct bucket is. The client is built exactly
// the way [NewRecordStore] builds the store's own.
//
// estate may be "". With one, the lifecycle assertion is checked against
// that estate's three namespaces; without, only a lifecycle rule with no
// prefix filter counts, which is what the project's own bucket carries.
func VerifyBucket(ctx context.Context, bucket, region, estate string, rs *configs.LiveRecordStore) ([]staterecord.BucketFinding, error) {
	awsCfg, err := loadAWSConfig(ctx, region, nil)
	if err != nil {
		return nil, err
	}
	var namespaces []string
	if estate != "" {
		namespaces = BucketNamespaces(rs, estate)
	}
	return staterecord.CheckBucketContract(ctx, s3.NewFromConfig(awsCfg), bucket, namespaces)
}
