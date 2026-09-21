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
	"github.com/aws/smithy-go/middleware"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/markers"
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
// the "s3" backend defaults to when rs.KeyPrefix is unset; see
// [RecordKeyPrefix].
//
// Building an "s3" store loads the AWS SDK's own default
// credential chain (environment, shared config, IMDS) unless rs.Region asks
// for a specific region; this package has no opinion on credentials beyond
// that, the same position every other AWS client this fork builds takes.
//
// rt is the live block's retry block, nil when it declares none, and it
// decides how many attempts a record write gets and under which retry mode.
// It is threaded in rather than read from the environment because the record
// store is where the attempt budget actually bites: an estate writes one
// record per record-backed resource, and the SDK's default of three attempts
// was measured not to be enough for a large one (#1196, #1148, against the
// Parameter Store backend that #1346 retired; the threading stayed).
func NewRecordStore(ctx context.Context, rs *configs.LiveRecordStore, rt *configs.LiveRetry, estate, moduleDir string, opts ...RecordStoreOption) (staterecord.Store, error) {
	var o recordStoreOptions
	for _, opt := range opts {
		opt(&o)
	}
	store, err := newRecordStore(ctx, rs, rt, estate, moduleDir, o)
	if err != nil || store == nil {
		return store, err
	}
	return openBuiltStore(ctx, store, rs, estate)
}

// StoreRefusal marks a failure to open the record store as a REFUSAL: the
// store was reached and something about it is wrong in a way no retry and no
// other command will get past. A bucket that fails its contract on first
// contact is one, a store whose List does not return what was just written
// is another, and so is a KMS key that refused the run (that one is
// recognised by its own type, [staterecord.KMSDeniedError]).
//
// A store with no sentinel that this run's identity may not provision is a
// refusal too (GitHub issue #1370). The bucket was reached and answered,
// and it will hold no sentinel on the next attempt either; what settles it
// is a person running the estate once under an identity that may write. It
// is kept out of the "reader tolerated" path deliberately - see
// [provisionStoreSentinel] - because the store an unprivileged run cannot
// provision reads exactly like an empty estate, which is #693.
//
// A key that cannot be used at all is a refusal too, by the same rule
// ([staterecord.KMSKeyUnusableError], GitHub issue #1383): the bucket was
// reached and answered, and a key that is disabled, pending deletion or gone
// stays that way for every retry and every other command. Every object in
// the store is unreadable while it lasts, so a plan built without records
// would propose creating an estate that exists.
//
// Everything else - a store that could not be reached, a role IAM would not
// let in - is an outage from where this package stands. The difference is
// for internal/command. `plan` and `apply` fail on both. `live-plan` and
// `live-mv` treat the store as one more source and go on without it after an
// outage, loudly; they must never go on past a refusal, because a refused
// bucket is one the estate should not be planned against at all. GitHub
// issue #1376: before this type existed those two commands logged both kinds
// and carried on.
type StoreRefusal struct{ Err error }

func (e *StoreRefusal) Error() string { return e.Err.Error() }
func (e *StoreRefusal) Unwrap() error { return e.Err }

// IsStoreRefusal reports whether err, from [NewRecordStore], is a refusal
// and not an outage. See [StoreRefusal].
func IsStoreRefusal(err error) bool {
	var refusal *StoreRefusal
	var kms *staterecord.KMSDeniedError
	var unusable *staterecord.KMSKeyUnusableError
	return errors.As(err, &refusal) || errors.As(err, &kms) || errors.As(err, &unusable)
}

// openBuiltStore is everything [NewRecordStore] does to a store once it is
// built: the trip counter, the provisioning handshake, the first-contact
// contract, the run cache. It is its own function so that a test can
// drive exactly the production sequence over a fake store. The first-contact
// tests used to reimplement this glue, and deleting the first-contact call
// from NewRecordStore left them green (#1376).
func openBuiltStore(ctx context.Context, store staterecord.Store, rs *configs.LiveRecordStore, estate string) (staterecord.Store, error) {
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
		if err := assertStoreOnFirstContact(ctx, counted, rs, estate, createdVersion); err != nil {
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
// when it was already there OR when this run may not write it at all: the
// one signal this package has that a run is an estate's first contact with
// its store. See [assertStoreOnFirstContact].
//
// # A run that may read the store and not write it
//
// GitHub issue #1370. A plan changes no resource, and a CI plan job is
// often given an identity that may read the estate's records and nothing
// more - an AWS role with s3:GetObject and s3:ListBucket and no
// s3:PutObject, or a record_store "local" directory mounted read-only. The
// write above is the only thing such a run ever asks the store to do, and
// it used to end the run: a denial is not a version conflict, so the
// handshake reported it and `plan` stopped with "Cannot open the record
// store".
//
// A denial is now carried past the write and answered by the SAME List the
// handshake already makes. If the sentinel is there, an earlier run with
// write access proved this store's write, read and List paths, which is
// everything #693 asks for, and nothing about this run being unable to
// repeat the proof makes the store less sound. The run goes on, read-only
// in effect. createdVersion stays "", so the run is not treated as a first
// contact: it did not create the sentinel, and asserting the bucket
// contract off another run's sentinel would check the bucket on every
// read-only plan, which is exactly what the ruling on #1339 decided against.
//
// If the sentinel is NOT there, the run is refused by name. A store with no
// sentinel and an identity that cannot provision one is indistinguishable
// from an empty one from here, and reading an unprovisioned store as an
// empty estate is #693's failure whole: the plan proposes creating an
// estate that already exists. That refusal is a [StoreRefusal] and not an
// outage, under #1376's rule - no retry gets past it, and it is settled by
// a person running the estate once under an identity that may write.
func provisionStoreSentinel(ctx context.Context, store staterecord.Store, prefix string) (createdVersion string, err error) {
	key := SentinelKey(prefix)
	var writeDenied error
	createdVersion, err = store.PutIfAbsent(ctx, key, []byte(sentinelPayload))
	if err != nil {
		var conflict *staterecord.VersionConflictError
		switch {
		case errors.As(err, &conflict):
			// Already provisioned by an earlier run or a racing one - the
			// conflict is the success case here.
			createdVersion = ""
		case staterecord.IsAccessDenied(err):
			// #1370. Whether this is survivable depends on the List below,
			// so the denial is kept rather than returned: it is the whole
			// of what the refusal has to say if the sentinel turns out to
			// be missing. A KMS refusal is not one of these - see
			// [staterecord.IsAccessDenied] - and still returns here.
			createdVersion, writeDenied = "", err
		default:
			return "", fmt.Errorf("record_store: provisioning the sentinel at %q: %w", key, err)
		}
	}
	listPrefix := staterecord.NamespacePrefix(prefix)
	keys, err := store.List(ctx, listPrefix)
	if err != nil {
		return "", fmt.Errorf("record_store: reading the sentinel back through List: %w", err)
	}
	if !slices.Contains(keys, key) {
		if writeDenied != nil {
			return "", &StoreRefusal{Err: fmt.Errorf("record_store: this store holds no sentinel at %q and this run's identity may not write one, so nothing here can tell an estate that has never been recorded from one whose records this run cannot see; refusing rather than planning against a store that would read as an empty estate and propose creating everything in it again (issue #693). A run whose role has write access to the record store - an ordinary plan or apply under the estate's full role - provisions the sentinel once, and a read-only role can plan against the store from then on (issue #1370). What the store said about the write: %w", key, writeDenied)}
		}
		return "", &StoreRefusal{Err: fmt.Errorf("record_store: the store accepted the sentinel write at %q but List(%q) does not return it, so this store's List is broken and every record in it is invisible to a plan; refusing rather than planning against an estate that would read as empty (issue #693)", key, listPrefix)}
	}
	return createdVersion, nil
}

// backendKeyPrefix is what the "s3" backend's own KeyPrefix is set to:
// nothing. The namespace a record lives under is carried by the
// KEY - [RecordKey] builds every key from [recordStoreKeyPrefix]'s output,
// [provisionStoreSentinel] puts the sentinel under it, [RecordAddr] reads
// an address back out of it and [staterecord.NewRunCache] bulk-loads it -
// so handing that same string to the backend as its own prefix made every
// backend name carry it twice: measured against real AWS on 2026-09-06,
// a record_store "s3" with key_prefix = "chdf916probe/e1" wrote the object
// key "chdf916probe/e1/chdf916probe/e1/...", and the Parameter Store
// backend of the time doubled its parameter names the same way. Nothing inside this package noticed, because both halves
// of every round trip went through the doubled name; what broke was the
// contract with everything OUTSIDE it - an operator's IAM policy, a
// listing of the prefix with the AWS CLI, and the live-cert harness's own
// teardown - all of which name the prefix the
// configuration set, once. Issue #916.
const backendKeyPrefix = ""

// RecordStoreOption adjusts how [NewRecordStore] builds its store. Options
// are for operational levers, which internal/command reads from the
// environment; what a team checks in stays in the record_store block.
type RecordStoreOption func(*recordStoreOptions)

type recordStoreOptions struct {
	bulkReadParallelism int

	// estate is not an option a caller passes: newRecordStore fills it in
	// from its own argument, so the tag can never name a different estate
	// from the one the store was opened for.
	estate string
}

// WithBulkReadParallelism bounds how many reads a backend that fans its bulk
// read out has in flight at once - today the "s3" backend's GetAll (GitHub
// issue #1336). Zero or negative leaves the backend's own default,
// [staterecord.DefaultS3GetAllParallelism]. The local backend reads its
// namespace another way and ignores it.
func WithBulkReadParallelism(n int) RecordStoreOption {
	return func(o *recordStoreOptions) { o.bulkReadParallelism = n }
}

func newRecordStore(ctx context.Context, rs *configs.LiveRecordStore, rt *configs.LiveRetry, estate, moduleDir string, o recordStoreOptions) (staterecord.Store, error) {
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

	case "s3":
		awsCfg, err := loadAWSConfig(ctx, rs.Region, rt)
		if err != nil {
			return nil, fmt.Errorf("record_store \"s3\": %w", err)
		}
		o.estate = estate
		store, err := staterecord.NewS3Store(s3StoreConfig(awsCfg, rs, o))
		if err != nil {
			return nil, fmt.Errorf("record_store \"s3\": %w", err)
		}
		return store, nil

	case "kubernetes":
		// No AWS configuration is loaded on this path and no AWS client is
		// built: a Kubernetes-only estate reaches its records with the
		// cluster credential it already has. GitHub issue #1392.
		return newKubernetesStore(rs, estate)

	default:
		// internal/configs/live.go's decodeRecordStoreBlock already refuses
		// anything but "local"/"s3"/"kubernetes" at config-decode time, so a
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
// so a record's S3 object key is this + "/...", carrying the configured
// prefix once.
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

// s3StoreConfig is the "s3" backend's configuration, apart from
// newRecordStore so that what an option sets can be checked without the
// network the rest of that function needs.
func s3StoreConfig(awsCfg aws.Config, rs *configs.LiveRecordStore, o recordStoreOptions) staterecord.S3Config {
	return staterecord.S3Config{
		Client: s3.NewFromConfig(awsCfg),
		Bucket: rs.Bucket,
		// Empty on purpose: see backendKeyPrefix.
		KeyPrefix: backendKeyPrefix,

		// #1381: a bucket name is global, so the name alone does not say
		// whose bucket this is. Set, every request carries the account.
		ExpectedBucketOwner: rs.BucketOwner,

		GetAllParallelism: o.bulkReadParallelism,

		// #1337: every object in an estate's namespaces is the estate's.
		BaseTags: map[string]string{markers.TagEstate: o.estate},
	}
}

// BucketNamespaces is every key namespace one estate writes under in its
// record store: its records (or the key_prefix override), its hint and its
// root outputs. It is what the bucket contract's lifecycle assertion has to
// cover - see [staterecord.CheckBucketContract].
func BucketNamespaces(rs *configs.LiveRecordStore, estate string) []string {
	return []string{recordStoreKeyPrefix(rs, estate), HintKeyPrefix(estate), RootOutputKeyPrefix(estate)}
}

// ContractFindings reads the contract of whatever store a live block's
// record_store built, for one estate. checker is nil when the store has no
// contract - a local store - and that is not a store that failed.
//
// It reports the store and knows nothing about waivers: whether a run may
// proceed past a finding is the caller's question (#1340), and the runnable
// project's verify (#1341) wants the store's true state whatever the
// configuration waives.
//
// The options carry what every contract might want and each store reads what
// its own needs. RequiredVerbs is left nil on purpose: every caller of this
// function reached the store through an apply or a first contact, which is a
// run that WRITES records, and nil means exactly the verbs such a run asks
// for. `choudoufu live-cluster -plan-identity` is how a read-only identity
// asks the narrower question, through [VerifyCluster].
func ContractFindings(ctx context.Context, store staterecord.Store, rs *configs.LiveRecordStore, estate string) (findings []staterecord.Finding, checker staterecord.ContractChecker, err error) {
	checker, ok := staterecord.AsContractChecker(store)
	if !ok {
		return nil, nil, nil
	}
	findings, err = checker.CheckContract(ctx, staterecord.ContractOptions{Namespaces: BucketNamespaces(rs, estate)})
	return findings, checker, err
}

// assertStoreOnFirstContact runs whichever contract this store has - the
// bucket's (GitHub issue #1339), the cluster's (#1393), or none at all for a
// local store. It is one of the two places a contract is asserted;
// internal/command's BeforeApply is the other, and an ordinary plan is
// deliberately neither.
//
// The ruling on #1339 is that the assertions do not run on every plan: they
// are facts about the store, which do not change between two plans, and
// paying for the reads on every plan is a cost and a permission requirement
// bought for nothing. But an estate's FIRST run against a store is the one
// moment a wrong one costs nothing to walk away from - no record has been
// written yet - so that run asserts, whatever command it is. The sentinel
// this run just created is the evidence that it is the first.
//
// What this gives up, stated rather than hidden: a plan against an estate
// whose store has drifted proceeds without a word, and the operator learns at
// apply time, after approving. `choudoufu live-bucket` and `choudoufu
// live-cluster` are the on-demand answer for anyone who wants it sooner.
//
// # Why a read-only plan never reaches here
//
// This runs only when THIS run created the sentinel, and creating it needs
// write access to the store. A plan under a read-only identity gets
// createdVersion == "" from [provisionStoreSentinel] and never arrives, which
// is #1370's reader tolerance and the reason the contract cannot refuse such
// a run for lacking the verbs it never uses.
//
// A refusal takes the sentinel back out, so that the next run is a first
// contact again and refuses again until the store is fixed. Leaving it behind
// would make the second plan proceed against the store the first one refused.
func assertStoreOnFirstContact(ctx context.Context, store staterecord.Store, rs *configs.LiveRecordStore, estate, sentinelVersion string) error {
	findings, checker, err := ContractFindings(ctx, store, rs, estate)
	if checker == nil {
		return nil
	}
	var refusal error
	if err != nil {
		refusal = err
	} else {
		// #1340: a waiver reaches only the settings it names. The warnings
		// are internal/command's, which sees every run and not just the
		// first; a first contact has no channel for one and must not refuse
		// on it.
		refused, _, _ := staterecord.SplitWaived(findings, rs.AllowInsecure)
		if msg := staterecord.ContractRefusalText(checker, refused); msg != "" {
			refusal = &StoreRefusal{Err: errors.New(msg)}
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
//
// expectedOwner may be "" as well. With one, the three reads carry it as
// ExpectedBucketOwner, so this reports on a bucket in that account or on no
// bucket at all (#1381).
func VerifyBucket(ctx context.Context, bucket, region, expectedOwner, estate string, rs *configs.LiveRecordStore) ([]staterecord.Finding, error) {
	awsCfg, err := loadAWSConfig(ctx, region, nil)
	if err != nil {
		return nil, err
	}
	var namespaces []string
	if estate != "" {
		namespaces = BucketNamespaces(rs, estate)
	}
	return staterecord.CheckBucketContract(ctx, s3.NewFromConfig(awsCfg), bucket, expectedOwner, namespaces)
}
