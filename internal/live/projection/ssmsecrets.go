// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"

	"github.com/intentius/choudoufu/pkg/staterecord"
)

// SSMSecretsStore is GitHub issue #1515's write path: a [staterecord.Store]
// over the S3 record store that keeps each record's secret values in SSM
// Parameter Store, as SecureString parameters under the estate's customer
// managed KMS key, and the reference to each in the record. secretsplit.go
// says which values move; this type says where they go and how a reader
// gets them back.
//
// It sits beneath [staterecord.RunCache] and above the trip counter, so
// every reader and writer in this package goes through it without knowing
// it is there: a payload handed down is split, and a payload handed up has
// had every reference resolved and is byte for byte what was handed down.
//
// # Who decides a concurrent write (ruling 1)
//
// The S3 record's If-Match write, and nothing else. Every parameter this
// store creates has a name no other write uses (see [SSMSecretsStore.newName])
// and is created with Overwrite false, so PutParameter decides nothing. The
// order is: create the parameters, then commit the record that references
// them through the inner store's conditional write. A writer that loses
// that write leaves parameters nothing references, which are orphans:
// nothing reads them, and [SSMSecretsStore.SweepOrphans] deletes them once
// they are older than the grace period. A writer that wins deletes the
// parameters its predecessor referenced and it does not.
//
// # Parameter names
//
// <path>/<ns>/<k>-<nonce>, where path is the ssm block's path, ns is a hash
// of the record namespace (so one estate's sweep never reaches another's
// parameters even under a shared path), k is a hash of the record key (the
// instance), and nonce is 128 random bits. #1515 sketches a name derived
// from the content; this deliberately is not. A parameter name is readable
// by anyone with ssm:DescribeParameters and is written into the S3 record,
// and a name that is a hash of a password is an offline guessing oracle for
// every password short enough to guess. What content addressing was for -
// a value that has not changed keeps its parameter, so an unchanged secret
// costs no write - is kept by reuse: a write whose base record already
// references a parameter for the same place with the same value keeps that
// parameter. See [SSMSecretsStore.assignParameters] for why reuse is only
// ever from the base record.
type SSMSecretsStore struct {
	inner staterecord.Store
	cfg   SSMSecretsConfig
	ns    string

	mu sync.Mutex
	// values is every parameter value this run has read or written. A
	// name is never reused for a different value, so an entry stays true
	// for the life of the process.
	values map[string]string
	// base is, per record key, the references and the version of the last
	// raw record this run saw there: what a write expecting that version
	// may reuse and must clean up after.
	base map[string]baseRefs
	// tiers is every parameter this run has seen referenced, by tier, for
	// the capacity check's count of what the estate already holds.
	tiers map[string]string
	// announced is every advanced parameter this run has announced.
	announced     map[string]bool
	announcements []string

	planned      *SecretParameterCount
	capacityDone bool
	capacityErr  error
	contractDone bool
	contractErr  error
}

type baseRefs struct {
	version string
	refs    []secretRef
}

// SSMSecretsAPI is the part of the SSM client this store calls.
type SSMSecretsAPI interface {
	PutParameter(ctx context.Context, in *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
	GetParameters(ctx context.Context, in *ssm.GetParametersInput, optFns ...func(*ssm.Options)) (*ssm.GetParametersOutput, error)
	DeleteParameters(ctx context.Context, in *ssm.DeleteParametersInput, optFns ...func(*ssm.Options)) (*ssm.DeleteParametersOutput, error)
	DescribeParameters(ctx context.Context, in *ssm.DescribeParametersInput, optFns ...func(*ssm.Options)) (*ssm.DescribeParametersOutput, error)
	// DeleteParameter is the first-contact probe's, through
	// [staterecord.AssertSSMContract].
	DeleteParameter(ctx context.Context, in *ssm.DeleteParameterInput, optFns ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error)
}

// SSMSecretsConfig is what [NewSSMSecretsStore] needs.
type SSMSecretsConfig struct {
	Client SSMSecretsAPI

	// KMSKeyID is the customer managed key every parameter is created
	// under. Required.
	KMSKeyID string

	// KMS is what the first-contact contract describes the key with.
	// Required: a run that writes never skips the contract.
	KMS staterecord.SSMContractKMSAPI

	// AllowInsecure is the ssm block's allow_insecure, the contract
	// assertions this estate proceeds without.
	AllowInsecure []string

	// Path is the parameter hierarchy, from the ssm block or
	// [DefaultSSMSecretsPath]. Required.
	Path string

	// RecordPrefix is the record key namespace this store's records live
	// under. Only keys [RecordAddr] reads under it are split; the sentinel,
	// hints and outputs pass through untouched.
	RecordPrefix string

	// Grace is how old an unreferenced parameter must be before the sweep
	// deletes it, and twice the longest a write may take between its first
	// parameter and its commit. Zero means [DefaultSSMSweepGrace].
	Grace time.Duration

	// Announce receives the advanced-tier announcement (ruling 3). Nil
	// writes it to standard error.
	Announce func(string)

	// The rest are seams for tests.
	Now   func() time.Time
	Sleep func(time.Duration)
	Rand  io.Reader

	// beforeCommit runs after a write's parameters exist and before its
	// record is committed. Tests use it to put another writer in that
	// window deterministically.
	beforeCommit func(key string)
}

const (
	// ssmStandardMaxBytes and ssmAdvancedMaxBytes are SSM's documented
	// value sizes: 4 KB on the standard tier, 8 KB on the advanced.
	ssmStandardMaxBytes = 4096
	ssmAdvancedMaxBytes = 8192

	// SSMStandardParameterCeiling and SSMAdvancedParameterCeiling are the
	// per-account, per-region parameter counts (Service Quotas
	// L-C3B871CB, not adjustable, for the standard tier).
	SSMStandardParameterCeiling = 10000
	SSMAdvancedParameterCeiling = 100000

	// SSMAdvancedMonthlyCostUSD is the advanced tier's price per
	// parameter-month, which ruling 3 says every run states.
	SSMAdvancedMonthlyCostUSD = 0.05

	// ssmGetParametersBatch is GetParameters' limit on names per call, and
	// DeleteParameters'.
	ssmGetParametersBatch = 10

	// ssmMaxAttempts bounds the retry on a throttled SSM call, on top of
	// the SDK's own.
	ssmMaxAttempts = 8

	// DefaultSSMSweepGrace is [SSMSecretsConfig.Grace]'s default. See
	// [SSMSecretsStore.SweepOrphans] for the argument it is part of.
	DefaultSSMSweepGrace = 24 * time.Hour
)

// DefaultSSMSecretsPath is the parameter path an ssm block with no path
// gets: /tofu-secrets/<estate>, beside the bucket's tofu-records/<estate>/.
func DefaultSSMSecretsPath(estate string) string {
	return "/tofu-secrets/" + estate
}

// NewSSMSecretsStore wraps inner.
func NewSSMSecretsStore(inner staterecord.Store, cfg SSMSecretsConfig) (*SSMSecretsStore, error) {
	if inner == nil || cfg.Client == nil {
		return nil, errors.New("secrets in SSM: no record store or no SSM client")
	}
	if cfg.KMSKeyID == "" {
		return nil, errors.New("secrets in SSM: no kms_key_id; a customer managed key is required (GitHub issue #1515)")
	}
	if cfg.KMS == nil {
		return nil, errors.New("secrets in SSM: no KMS client for the first-contact contract")
	}
	path := strings.TrimRight(cfg.Path, "/")
	if !strings.HasPrefix(path, "/") || path == "" {
		return nil, fmt.Errorf("secrets in SSM: the parameter path %q is not an absolute SSM path", cfg.Path)
	}
	// Two levels are added below the path, and SSM allows fifteen.
	if strings.Count(path, "/")+2 > 15 {
		return nil, fmt.Errorf("secrets in SSM: the parameter path %q is %d levels deep, and this store adds two under it to SSM's limit of fifteen", cfg.Path, strings.Count(path, "/"))
	}
	// SSM's 1,011-character limit includes the ARN prefix, about 60
	// characters; the store adds 1+16+1+24+1+32.
	if len(path)+75+60 > 1011 {
		return nil, fmt.Errorf("secrets in SSM: the parameter path %q is too long to leave room for a parameter name under SSM's 1,011-character limit", cfg.Path)
	}
	cfg.Path = path
	if cfg.Grace <= 0 {
		cfg.Grace = DefaultSSMSweepGrace
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Sleep == nil {
		cfg.Sleep = time.Sleep
	}
	if cfg.Rand == nil {
		cfg.Rand = rand.Reader
	}
	if cfg.Announce == nil {
		cfg.Announce = func(msg string) { fmt.Fprintln(os.Stderr, msg) }
	}
	sum := sha256.Sum256([]byte(cfg.RecordPrefix))
	return &SSMSecretsStore{
		inner:     inner,
		cfg:       cfg,
		ns:        path + "/" + hex.EncodeToString(sum[:8]),
		values:    map[string]string{},
		base:      map[string]baseRefs{},
		tiers:     map[string]string{},
		announced: map[string]bool{},
	}, nil
}

// Unwrap returns the wrapped store, for [staterecord.AsContractChecker].
func (s *SSMSecretsStore) Unwrap() staterecord.Store { return s.inner }

// Namespace is the parameter path this store's parameters live under, and
// the only one its sweep lists.
func (s *SSMSecretsStore) Namespace() string { return s.ns }

// Announcements is every advanced-tier announcement this run has made.
func (s *SSMSecretsStore) Announcements() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.announcements...)
}

// AsSSMSecretsStore finds the [SSMSecretsStore] under s's wrappers.
func AsSSMSecretsStore(s staterecord.Store) (*SSMSecretsStore, bool) {
	for s != nil {
		if v, ok := s.(*SSMSecretsStore); ok {
			return v, true
		}
		u, ok := s.(interface{ Unwrap() staterecord.Store })
		if !ok {
			return nil, false
		}
		s = u.Unwrap()
	}
	return nil, false
}

// isRecordKey reports whether key holds a record envelope this store
// splits.
func (s *SSMSecretsStore) isRecordKey(key string) bool {
	_, ok := RecordAddr(s.cfg.RecordPrefix, key)
	return ok
}

// SecretParameterMissingError is a reference to a parameter SSM does not
// have. It is never read as a null: a record-backed resource whose value
// reads as null is one the plan proposes to recreate.
type SecretParameterMissingError struct {
	Key       string
	Parameter string
}

func (e *SecretParameterMissingError) Error() string {
	where := ""
	if e.Key != "" {
		where = fmt.Sprintf(" by the record at %q", e.Key)
	}
	return fmt.Sprintf("the SSM parameter %s referenced%s does not exist; the secret value it held cannot be read, and this run refuses rather than reading it as null (GitHub issue #1515)", e.Parameter, where)
}

// SecretTooLargeError is a single value over the advanced tier's 8 KB,
// which no parameter can hold.
type SecretTooLargeError struct {
	Key   string
	Place string
	Bytes int
}

func (e *SecretTooLargeError) Error() string {
	return fmt.Sprintf("the record at %q holds a secret value at %s of %d bytes, over SSM's 8 KB advanced-tier limit; nothing was written", e.Key, e.Place, e.Bytes)
}

// SecretWriteTooSlowError is a write that took longer than half the sweep's
// grace period between its first parameter and its commit. It is refused
// rather than committed, because the sweep's safety rests on that bound.
type SecretWriteTooSlowError struct {
	Key     string
	Elapsed time.Duration
	Bound   time.Duration
}

func (e *SecretWriteTooSlowError) Error() string {
	return fmt.Sprintf("the record at %q was not committed: %s passed between creating its secret parameters and committing it, over the %s the orphan sweep's safety allows; its parameters are left for the sweep", e.Key, e.Elapsed, e.Bound)
}

// Get resolves the record's references.
func (s *SSMSecretsStore) Get(ctx context.Context, key string) ([]byte, string, bool, error) {
	payload, version, exists, err := s.inner.Get(ctx, key)
	if err != nil || !exists || !s.isRecordKey(key) {
		return payload, version, exists, err
	}
	out, err := s.resolve(ctx, map[string]staterecord.Record{key: {Payload: payload, Version: version}})
	if err != nil {
		return nil, "", false, err
	}
	return out[key].Payload, version, true, nil
}

// GetAll implements [staterecord.BulkReader] when the inner store does,
// resolving every record's references together, ten names a call.
func (s *SSMSecretsStore) GetAll(ctx context.Context, keyPrefix string) (map[string]staterecord.Record, error) {
	bulk, ok := s.inner.(staterecord.BulkReader)
	if !ok {
		return nil, fmt.Errorf("secrets in SSM: the record store under it has no bulk read")
	}
	recs, err := bulk.GetAll(ctx, keyPrefix)
	if err != nil {
		return nil, err
	}
	split := map[string]staterecord.Record{}
	for k, r := range recs {
		if s.isRecordKey(k) {
			split[k] = r
		}
	}
	resolved, err := s.resolve(ctx, split)
	if err != nil {
		return nil, err
	}
	for k, r := range resolved {
		recs[k] = r
	}
	return recs, nil
}

func (s *SSMSecretsStore) List(ctx context.Context, keyPrefix string) ([]string, error) {
	return s.inner.List(ctx, keyPrefix)
}

// rawRefs reads only the reference list of a stored payload.
func rawRefs(payload []byte) ([]secretRef, error) {
	if !bytes.Contains(payload, []byte(`"secret_refs"`)) {
		return nil, nil
	}
	var head struct {
		SecretRefs []secretRef `json:"secret_refs"`
	}
	if err := json.Unmarshal(payload, &head); err != nil {
		return nil, fmt.Errorf("reading the record's secret references: %w", err)
	}
	return head.SecretRefs, nil
}

// resolve puts every referenced value back into recs, which are raw
// payloads straight from the inner store.
func (s *SSMSecretsStore) resolve(ctx context.Context, recs map[string]staterecord.Record) (map[string]staterecord.Record, error) {
	refsByKey := map[string][]secretRef{}
	var want []string
	seen := map[string]bool{}
	for key, r := range recs {
		refs, err := rawRefs(r.Payload)
		if err != nil {
			return nil, fmt.Errorf("the record at %q: %w", key, err)
		}
		s.noteBase(key, r.Version, refs)
		if len(refs) == 0 {
			continue
		}
		refsByKey[key] = refs
		for _, ref := range refs {
			s.noteTier(ref)
			if !seen[ref.Parameter] {
				seen[ref.Parameter] = true
				want = append(want, ref.Parameter)
			}
		}
	}
	if len(refsByKey) == 0 {
		return recs, nil
	}
	values, err := s.fetch(ctx, want)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(refsByKey))
	for k := range refsByKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]staterecord.Record, len(recs))
	for k, r := range recs {
		out[k] = r
	}
	for _, key := range keys {
		var env recordEnvelope
		if err := json.Unmarshal(recs[key].Payload, &env); err != nil {
			return nil, fmt.Errorf("the record at %q is not valid JSON: %w", key, err)
		}
		if err := restoreSecrets(&env, values); err != nil {
			var missing *SecretParameterMissingError
			if errors.As(err, &missing) {
				missing.Key = key
			}
			return nil, fmt.Errorf("the record at %q: %w", key, err)
		}
		payload, err := json.Marshal(env)
		if err != nil {
			return nil, fmt.Errorf("re-encoding the record at %q: %w", key, err)
		}
		out[key] = staterecord.Record{Payload: payload, Version: recs[key].Version}
		s.announceTiers(key, refsByKey[key])
	}
	return out, nil
}

// fetch returns the value of every name it can find, from this run's own
// memory first and then from GetParameters, ten names a call. A name SSM
// does not have is simply absent from the result.
func (s *SSMSecretsStore) fetch(ctx context.Context, names []string) (map[string]string, error) {
	out := map[string]string{}
	var todo []string
	s.mu.Lock()
	for _, n := range names {
		if v, ok := s.values[n]; ok {
			out[n] = v
		} else {
			todo = append(todo, n)
		}
	}
	s.mu.Unlock()
	sort.Strings(todo)
	for i := 0; i < len(todo); i += ssmGetParametersBatch {
		batch := todo[i:min(i+ssmGetParametersBatch, len(todo))]
		var resp *ssm.GetParametersOutput
		err := s.retry(ctx, "GetParameters", func() error {
			var err error
			resp, err = s.cfg.Client.GetParameters(ctx, &ssm.GetParametersInput{Names: batch, WithDecryption: aws.Bool(true)})
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("reading secret values from SSM: %w", err)
		}
		s.mu.Lock()
		for _, p := range resp.Parameters {
			name, value := aws.ToString(p.Name), aws.ToString(p.Value)
			out[name] = value
			s.values[name] = value
		}
		s.mu.Unlock()
	}
	return out, nil
}

// retry runs call until it succeeds, fails with something other than
// throttling, or has been tried [ssmMaxAttempts] times, backing off from
// 100ms and doubling to a cap of 5s.
func (s *SSMSecretsStore) retry(ctx context.Context, op string, call func() error) error {
	delay := 100 * time.Millisecond
	var err error
	for attempt := 1; attempt <= ssmMaxAttempts; attempt++ {
		err = call()
		if err == nil || !isSSMThrottle(err) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Printf("[DEBUG] live/secrets: %s throttled (attempt %d of %d); retrying in %s", op, attempt, ssmMaxAttempts, delay)
		s.cfg.Sleep(delay)
		delay = min(delay*2, 5*time.Second)
	}
	return fmt.Errorf("%s was throttled %d times in a row: %w", op, ssmMaxAttempts, err)
}

func isSSMThrottle(err error) bool {
	var api smithy.APIError
	if !errors.As(err, &api) {
		return false
	}
	switch api.ErrorCode() {
	case "ThrottlingException", "TooManyUpdates", "Throttling", "RequestLimitExceeded", "TooManyRequestsException":
		return true
	}
	return false
}

func (s *SSMSecretsStore) noteBase(key, version string, refs []secretRef) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.base[key] = baseRefs{version: version, refs: refs}
}

func (s *SSMSecretsStore) noteTier(ref secretRef) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tiers[ref.Parameter] = ref.Tier
}

// announceTiers is ruling 3: every advanced parameter, once per run, with
// what it costs.
func (s *SSMSecretsStore) announceTiers(key string, refs []secretRef) {
	for _, ref := range refs {
		if ref.Tier != secretTierAdvancedLabel {
			continue
		}
		s.mu.Lock()
		if s.announced[ref.Parameter] {
			s.mu.Unlock()
			continue
		}
		s.announced[ref.Parameter] = true
		addr := key
		if a, ok := RecordAddr(s.cfg.RecordPrefix, key); ok {
			addr = a.String()
		}
		place := ref.In
		if ref.Attribute != "" {
			place += " " + ref.Attribute
		} else if len(ref.Path) > 0 {
			place += " " + string(ref.Path)
		}
		msg := fmt.Sprintf("Warning: secrets in SSM: %s keeps a secret value over 4 KB (%s) in the advanced tier, parameter %s, at $%.2f per parameter-month (GitHub issue #1515, ruling 3).", addr, place, ref.Parameter, SSMAdvancedMonthlyCostUSD)
		s.announcements = append(s.announcements, msg)
		s.mu.Unlock()
		s.cfg.Announce(msg)
	}
}

// baseFor returns the references of the record at key as of version: from
// this run's memory when it last saw that version, and otherwise from the
// store. ok is false when the store holds a different version, which the
// conditional write is about to refuse anyway.
func (s *SSMSecretsStore) baseFor(ctx context.Context, key, version string) (refs []secretRef, actual string, ok bool, err error) {
	s.mu.Lock()
	b, seen := s.base[key]
	s.mu.Unlock()
	if seen && b.version == version {
		return b.refs, version, true, nil
	}
	payload, current, exists, err := s.inner.Get(ctx, key)
	if err != nil {
		return nil, "", false, err
	}
	if !exists {
		return nil, "", version == "", nil
	}
	refs, err = rawRefs(payload)
	if err != nil {
		return nil, "", false, err
	}
	s.noteBase(key, current, refs)
	if current != version {
		return nil, current, false, nil
	}
	return refs, current, true, nil
}

// PutIfVersion splits payload, creates its parameters, and commits the
// record with its references through the inner store's conditional write.
func (s *SSMSecretsStore) PutIfVersion(ctx context.Context, key string, payload []byte, expectedVersion string) (string, error) {
	return s.put(ctx, key, payload, expectedVersion, false)
}

// PutIfAbsent is [SSMSecretsStore.PutIfVersion] against no record.
func (s *SSMSecretsStore) PutIfAbsent(ctx context.Context, key string, payload []byte) (string, error) {
	return s.put(ctx, key, payload, "", true)
}

func (s *SSMSecretsStore) put(ctx context.Context, key string, payload []byte, expectedVersion string, absent bool) (string, error) {
	commit := func(p []byte) (string, error) {
		if absent {
			return s.inner.PutIfAbsent(ctx, key, p)
		}
		return s.inner.PutIfVersion(ctx, key, p, expectedVersion)
	}
	if !s.isRecordKey(key) {
		return commit(payload)
	}
	base, actual, current, err := s.baseFor(ctx, key, expectedVersion)
	if err != nil {
		return "", err
	}
	if !current {
		// The conditional write would refuse, so this refuses first and
		// creates nothing for the sweep. It never hands the inner store
		// the payload as it came: that holds every secret in clear, and a
		// record deleted in between would let it land.
		return "", &staterecord.VersionConflictError{Key: key, ExpectedVersion: expectedVersion, ActualVersion: actual}
	}

	var env recordEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return "", fmt.Errorf("secrets in SSM: the record for %q is not valid JSON: %w", key, err)
	}
	pending, err := splitSecrets(&env)
	if err != nil {
		return "", fmt.Errorf("secrets in SSM: the record at %q: %w", key, err)
	}
	stored := payload
	var refs []secretRef
	var created time.Time
	if len(pending) > 0 {
		var create []pendingSecret
		refs, create, err = s.assignParameters(ctx, key, base, pending)
		if err != nil {
			return "", err
		}
		env.SecretRefs = refs
		stored, err = json.Marshal(env)
		if err != nil {
			return "", fmt.Errorf("secrets in SSM: encoding the record at %q: %w", key, err)
		}
		values := make(map[string]string, len(refs))
		for i, r := range refs {
			values[r.Parameter] = pending[i].value
		}
		// Before anything is created: a split that would not read back as
		// the same bytes is refused with nothing left behind.
		if err := checkIdentity(stored, payload, values); err != nil {
			return "", fmt.Errorf("secrets in SSM: the record at %q: %w", key, err)
		}
		if created, err = s.createParameters(ctx, key, create); err != nil {
			return "", err
		}
		s.announceTiers(key, refs)
	}
	if s.cfg.beforeCommit != nil {
		s.cfg.beforeCommit(key)
	}
	if !created.IsZero() {
		if elapsed, bound := s.cfg.Now().Sub(created), s.cfg.Grace/2; elapsed >= bound {
			return "", &SecretWriteTooSlowError{Key: key, Elapsed: elapsed, Bound: bound}
		}
	}
	newVersion, err := commit(stored)
	if err != nil {
		// The parameters this call created are orphans: nothing
		// references them, and the sweep deletes them after the grace
		// period. Deleting them here would be tidier and is not safe
		// against a commit that landed and reported an error on the way
		// back.
		return "", err
	}
	s.noteBase(key, newVersion, refs)
	s.deleteSuperseded(ctx, key, base, refs)
	return newVersion, nil
}

// checkIdentity is secretsplit.go's "Byte identity" check: stored,
// resolved with values, must be the payload it came from.
func checkIdentity(stored, payload []byte, values map[string]string) error {
	var env recordEnvelope
	if err := json.Unmarshal(stored, &env); err != nil {
		return errSecretSplitNotIdentity
	}
	if err := restoreSecrets(&env, values); err != nil {
		return errSecretSplitNotIdentity
	}
	back, err := json.Marshal(env)
	if err != nil || !bytes.Equal(back, payload) {
		return errSecretSplitNotIdentity
	}
	return nil
}

// assignParameters names a parameter for each pending value and creates
// the ones that are new.
//
// A value is given the parameter its base record already references for
// the same place when that parameter holds the same value, and a new one
// otherwise. Reuse is from the base record and from nowhere else, and
// that is what keeps every reference pointing at a parameter that exists:
// a parameter is deleted only by a write that commits a successor of a
// record referencing it without referencing it (after which a write
// expecting the old version loses its conditional write), or by the sweep,
// which deletes only what no record references. A parameter found by any
// other route - one with the same content under another key, an orphan of
// a lost race - may be deleted between being found and being committed,
// so it is never used.
func (s *SSMSecretsStore) assignParameters(ctx context.Context, key string, base []secretRef, pending []pendingSecret) (refs []secretRef, create []pendingSecret, err error) {
	for _, p := range pending {
		if len(p.value) > ssmAdvancedMaxBytes {
			return nil, nil, &SecretTooLargeError{Key: key, Place: strings.ReplaceAll(p.ref.slot(), "\x00", " "), Bytes: len(p.value)}
		}
	}
	reusable := map[string]secretRef{}
	if len(base) > 0 {
		var names []string
		for _, r := range base {
			names = append(names, r.Parameter)
		}
		values, err := s.fetch(ctx, names)
		if err != nil {
			return nil, nil, err
		}
		for _, r := range base {
			if _, ok := values[r.Parameter]; ok {
				reusable[r.slot()] = r
			}
		}
	}
	refs = make([]secretRef, 0, len(pending))
	for _, p := range pending {
		ref := p.ref
		// The tier is the value's, never inherited from the base.
		if len(p.value) > ssmStandardMaxBytes {
			ref.Tier = secretTierAdvancedLabel
		}
		if prev, ok := reusable[ref.slot()]; ok && prev.Tier == ref.Tier {
			s.mu.Lock()
			same := s.values[prev.Parameter] == p.value
			s.mu.Unlock()
			if same {
				refs = append(refs, prev)
				continue
			}
		}
		name, err := s.newName(key)
		if err != nil {
			return nil, nil, err
		}
		ref.Parameter = name
		refs = append(refs, ref)
		create = append(create, pendingSecret{ref: ref, value: p.value})
	}
	return refs, create, nil
}

// createParameters creates each new parameter, after the capacity check,
// and returns when it started: the write's clock for
// [SecretWriteTooSlowError]. Zero when it created nothing.
func (s *SSMSecretsStore) createParameters(ctx context.Context, key string, create []pendingSecret) (time.Time, error) {
	if len(create) == 0 {
		return time.Time{}, nil
	}
	if err := s.admitCapacity(create); err != nil {
		return time.Time{}, err
	}
	if err := s.admitContract(ctx); err != nil {
		return time.Time{}, err
	}
	started := s.cfg.Now()
	for _, c := range create {
		in := &ssm.PutParameterInput{
			Name:        aws.String(c.ref.Parameter),
			Value:       aws.String(c.value),
			Type:        ssmtypes.ParameterTypeSecureString,
			KeyId:       aws.String(s.cfg.KMSKeyID),
			Overwrite:   aws.Bool(false),
			Tier:        ssmtypes.ParameterTierStandard,
			Description: aws.String(truncate("choudoufu: a secret value referenced by the record at "+key, 1024)),
		}
		if c.ref.Tier == secretTierAdvancedLabel {
			in.Tier = ssmtypes.ParameterTierAdvanced
		}
		err := s.retry(ctx, "PutParameter", func() error {
			_, err := s.cfg.Client.PutParameter(ctx, in)
			return err
		})
		if err != nil {
			return time.Time{}, fmt.Errorf("secrets in SSM: creating parameter %s for the record at %q: %w", c.ref.Parameter, key, err)
		}
		s.mu.Lock()
		s.values[c.ref.Parameter] = c.value
		s.tiers[c.ref.Parameter] = c.ref.Tier
		s.mu.Unlock()
	}
	return started, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// newName is <ns>/<k>-<nonce>: see [SSMSecretsStore]'s "Parameter names".
func (s *SSMSecretsStore) newName(key string) (string, error) {
	var nonce [16]byte
	if _, err := io.ReadFull(s.cfg.Rand, nonce[:]); err != nil {
		return "", fmt.Errorf("secrets in SSM: drawing a parameter name: %w", err)
	}
	k := sha256.Sum256([]byte(key))
	return s.ns + "/" + hex.EncodeToString(k[:12]) + "-" + hex.EncodeToString(nonce[:]), nil
}

// deleteSuperseded deletes what the base record referenced and the
// committed one does not. A failure is logged and left to the sweep: the
// record is committed, and an unreferenced parameter is only an orphan.
func (s *SSMSecretsStore) deleteSuperseded(ctx context.Context, key string, base, now []secretRef) {
	keep := map[string]bool{}
	for _, r := range now {
		keep[r.Parameter] = true
	}
	var gone []string
	for _, r := range base {
		if !keep[r.Parameter] {
			gone = append(gone, r.Parameter)
		}
	}
	if len(gone) == 0 {
		return
	}
	if _, err := s.deleteParameters(ctx, gone); err != nil {
		log.Printf("[WARN] live/secrets: the record at %q no longer references %s and deleting them failed (%s); the orphan sweep will", key, strings.Join(gone, ", "), err)
	}
}

func (s *SSMSecretsStore) deleteParameters(ctx context.Context, names []string) ([]string, error) {
	sort.Strings(names)
	var deleted []string
	for i := 0; i < len(names); i += ssmGetParametersBatch {
		batch := names[i:min(i+ssmGetParametersBatch, len(names))]
		var resp *ssm.DeleteParametersOutput
		err := s.retry(ctx, "DeleteParameters", func() error {
			var err error
			resp, err = s.cfg.Client.DeleteParameters(ctx, &ssm.DeleteParametersInput{Names: batch})
			return err
		})
		if err != nil {
			return deleted, err
		}
		deleted = append(deleted, resp.DeletedParameters...)
	}
	return deleted, nil
}

// Delete deletes the record and then the parameters it referenced.
func (s *SSMSecretsStore) Delete(ctx context.Context, key string, expectedVersion string) error {
	if !s.isRecordKey(key) {
		return s.inner.Delete(ctx, key, expectedVersion)
	}
	base, _, current, err := s.baseFor(ctx, key, expectedVersion)
	if err != nil {
		return err
	}
	if err := s.inner.Delete(ctx, key, expectedVersion); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.base, key)
	s.mu.Unlock()
	if current {
		s.deleteSuperseded(ctx, key, base, nil)
	}
	return nil
}

// admitContract is the first-contact contract (#2393 unit S2's
// [staterecord.AssertSSMContract]): once per run, before the run's first
// parameter, and only in a run that creates one, since a plan role holds
// no kms:DescribeKey. A refusal is sticky, like the capacity refusal: it
// holds for every write of the run. The probe is made under this store's
// namespace, so a probe its delete left behind is an unreferenced
// parameter the orphan sweep collects.
func (s *SSMSecretsStore) admitContract(ctx context.Context) error {
	s.mu.Lock()
	if s.contractDone {
		err := s.contractErr
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	waived, err := staterecord.AssertSSMContract(ctx, s.cfg.Client, s.cfg.KMS, s.cfg.KMSKeyID, s.ns, s.cfg.AllowInsecure)
	if err != nil {
		var refusal *staterecord.SSMContractRefusalError
		if !errors.As(err, &refusal) {
			// The check could not be made; a later write may try again.
			return fmt.Errorf("secrets in SSM: the first-contact contract could not be checked: %w", err)
		}
	}
	s.mu.Lock()
	s.contractDone = true
	s.contractErr = err
	s.mu.Unlock()
	if err != nil {
		return err
	}
	for _, f := range waived {
		s.cfg.Announce(fmt.Sprintf("Warning: secrets in SSM: the ssm block's allow_insecure waives %s, so %s.", f.Setting, staterecord.SSMWaiverCost(f.Setting)))
	}
	return nil
}
