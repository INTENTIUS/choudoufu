// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/pkg/staterecord"
)

// fakeSSM is Parameter Store as this store uses it: create-if-absent puts,
// batched gets, batched deletes, a path listing, and injectable throttling.
type fakeSSM struct {
	mu     sync.Mutex
	params map[string]*fakeParam
	now    func() time.Time
	log    *[]string

	throttleGets int
	getBatches   [][]string
	puts         []*ssm.PutParameterInput
}

type fakeParam struct {
	value    string
	tier     ssmtypes.ParameterTier
	modified time.Time
}

func newFakeSSM(now func() time.Time, log *[]string) *fakeSSM {
	return &fakeSSM{params: map[string]*fakeParam{}, now: now, log: log}
}

func throttle() error {
	return &smithy.GenericAPIError{Code: "ThrottlingException", Message: "Rate exceeded"}
}

func (f *fakeSSM) PutParameter(_ context.Context, in *ssm.PutParameterInput, _ ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts = append(f.puts, in)
	name := aws.ToString(in.Name)
	if aws.ToBool(in.Overwrite) {
		return nil, fmt.Errorf("fake SSM: Overwrite true on %s; this store must only ever create", name)
	}
	if _, ok := f.params[name]; ok {
		return nil, &ssmtypes.ParameterAlreadyExists{Message: aws.String(name)}
	}
	f.params[name] = &fakeParam{value: aws.ToString(in.Value), tier: in.Tier, modified: f.now()}
	if f.log != nil {
		*f.log = append(*f.log, "ssm:put "+name)
	}
	return &ssm.PutParameterOutput{Version: 1}, nil
}

func (f *fakeSSM) GetParameters(_ context.Context, in *ssm.GetParametersInput, _ ...func(*ssm.Options)) (*ssm.GetParametersOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(in.Names) > 10 {
		return nil, fmt.Errorf("fake SSM: %d names in one GetParameters, the API's limit is 10", len(in.Names))
	}
	if !aws.ToBool(in.WithDecryption) {
		return nil, errors.New("fake SSM: GetParameters without decryption")
	}
	if f.throttleGets > 0 {
		f.throttleGets--
		return nil, throttle()
	}
	f.getBatches = append(f.getBatches, append([]string(nil), in.Names...))
	out := &ssm.GetParametersOutput{}
	for _, n := range in.Names {
		if p, ok := f.params[n]; ok {
			out.Parameters = append(out.Parameters, ssmtypes.Parameter{Name: aws.String(n), Value: aws.String(p.value)})
		} else {
			out.InvalidParameters = append(out.InvalidParameters, n)
		}
	}
	return out, nil
}

func (f *fakeSSM) DeleteParameters(_ context.Context, in *ssm.DeleteParametersInput, _ ...func(*ssm.Options)) (*ssm.DeleteParametersOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := &ssm.DeleteParametersOutput{}
	for _, n := range in.Names {
		if _, ok := f.params[n]; ok {
			delete(f.params, n)
			out.DeletedParameters = append(out.DeletedParameters, n)
			if f.log != nil {
				*f.log = append(*f.log, "ssm:delete "+n)
			}
		} else {
			out.InvalidParameters = append(out.InvalidParameters, n)
		}
	}
	return out, nil
}

func (f *fakeSSM) DescribeParameters(_ context.Context, in *ssm.DescribeParametersInput, _ ...func(*ssm.Options)) (*ssm.DescribeParametersOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := in.ParameterFilters[0].Values[0]
	var names []string
	for n := range f.params {
		if strings.HasPrefix(n, path+"/") {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	start := 0
	if in.NextToken != nil {
		fmt.Sscanf(*in.NextToken, "%d", &start)
	}
	end := min(start+int(aws.ToInt32(in.MaxResults)), len(names))
	out := &ssm.DescribeParametersOutput{}
	for _, n := range names[start:end] {
		mod := f.params[n].modified
		out.Parameters = append(out.Parameters, ssmtypes.ParameterMetadata{Name: aws.String(n), LastModifiedDate: &mod, Tier: f.params[n].tier})
	}
	if end < len(names) {
		out.NextToken = aws.String(fmt.Sprint(end))
	}
	return out, nil
}

func (f *fakeSSM) has(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.params[name]
	return ok
}

func (f *fakeSSM) names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for n := range f.params {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (f *fakeSSM) putCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.puts)
}

// loggingStore is the S3 record store for these tests: the local store's
// real compare-and-swap, with every successful write logged and an
// injectable commit failure.
type loggingStore struct {
	staterecord.Store
	log     *[]string
	failPut error
}

func (l *loggingStore) PutIfVersion(ctx context.Context, key string, payload []byte, expected string) (string, error) {
	if l.failPut != nil {
		return "", l.failPut
	}
	v, err := l.Store.PutIfVersion(ctx, key, payload, expected)
	if err == nil && l.log != nil {
		*l.log = append(*l.log, "s3:put "+key)
	}
	return v, err
}

func (l *loggingStore) GetAll(ctx context.Context, prefix string) (map[string]staterecord.Record, error) {
	return l.Store.(staterecord.BulkReader).GetAll(ctx, prefix)
}

// fakeClock is a clock tests move by hand.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

const testKMSKey = "arn:aws:kms:eu-west-1:111122223333:key/1234abcd"

type ssmRig struct {
	t      *testing.T
	clock  *fakeClock
	log    []string
	ssm    *fakeSSM
	s3     *loggingStore
	prefix string
	sleeps []time.Duration
	notes  []string
}

func newSSMRig(t *testing.T) *ssmRig {
	t.Helper()
	r := &ssmRig{t: t, clock: &fakeClock{t: time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)}, prefix: RecordKeyPrefix("prod")}
	r.ssm = newFakeSSM(r.clock.Now, &r.log)
	r.s3 = &loggingStore{Store: newTestLocalStore(t), log: &r.log}
	return r
}

// open is one run's store over the rig's shared S3 and SSM.
func (r *ssmRig) open() *SSMSecretsStore {
	r.t.Helper()
	s, err := NewSSMSecretsStore(r.s3, SSMSecretsConfig{
		Client:       r.ssm,
		KMSKeyID:     testKMSKey,
		Path:         "/choudoufu/prod/secrets",
		RecordPrefix: r.prefix,
		Now:          r.clock.Now,
		Sleep:        func(d time.Duration) { r.sleeps = append(r.sleeps, d) },
		Announce:     func(m string) { r.notes = append(r.notes, m) },
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return s
}

// rawRecord is what the bucket holds at addr's key.
func (r *ssmRig) rawRecord(addr string) []byte {
	r.t.Helper()
	p, _, ok, err := r.s3.Get(context.Background(), RecordKey(r.prefix, mustAddr(r.t, addr)))
	if err != nil || !ok {
		r.t.Fatalf("reading the raw record for %s: exists=%v err=%v", addr, ok, err)
	}
	return p
}

// danglingRefs is every reference in the bucket to a parameter SSM does not
// have.
func (r *ssmRig) danglingRefs() []string {
	r.t.Helper()
	ctx := context.Background()
	keys, err := r.s3.List(ctx, r.prefix)
	if err != nil {
		r.t.Fatal(err)
	}
	var out []string
	for _, k := range keys {
		p, _, ok, err := r.s3.Get(ctx, k)
		if err != nil || !ok {
			continue
		}
		refs, err := rawRefs(p)
		if err != nil {
			r.t.Fatal(err)
		}
		for _, ref := range refs {
			if !r.ssm.has(ref.Parameter) {
				out = append(out, k+" -> "+ref.Parameter)
			}
		}
	}
	return out
}

// passwordValue is a random_password-shaped object whose result is marked
// sensitive, the way a decoded state object arrives.
func passwordValue(result string) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":     cty.StringVal("none"),
		"length": cty.NumberIntVal(int64(len(result))),
		"result": cty.StringVal(result).Mark(marks.Sensitive),
	})
}

func seedPassword(t *testing.T, rs *RecordStore, addr, result string, private []byte) {
	t.Helper()
	if _, err := SeedRecordForInstance(context.Background(), rs, mustAddr(t, addr), nullProvider, passwordValue(result), private, states.ObjectReady); err != nil {
		t.Fatalf("seeding %s: %v", addr, err)
	}
}

// secretEnvelope is a record with secret material in all three places:
// a sensitive object attribute (and one inside a list), the object's
// private bytes, a sensitive residue value and the residue's provider
// private.
func secretEnvelope(t *testing.T, addr string) []byte {
	t.Helper()
	val := cty.ObjectVal(map[string]cty.Value{
		"id":     cty.StringVal("pw-1"),
		"result": cty.StringVal("hunter2-object").Mark(marks.Sensitive),
		"keys": cty.ListVal([]cty.Value{
			cty.StringVal("public-0"),
			cty.StringVal("key-in-a-list").Mark(marks.Sensitive),
		}),
		"empty": cty.NullVal(cty.String).Mark(marks.Sensitive),
	})
	of, err := encodeObjectFields(val, []byte("object-private-bytes"), states.ObjectReady)
	if err != nil {
		t.Fatal(err)
	}
	rf, err := encodeResidueFields(map[string]cty.Value{
		"master_password": cty.StringVal("hunter2-residue"),
		"filename":        cty.StringVal("lambda.zip"),
	})
	if err != nil {
		t.Fatal(err)
	}
	rf.Sensitive = []string{"master_password"}
	rf.ProviderPrivate = []byte("residue-private-bytes")
	payload, err := marshalEnvelopeForWrite(recordEnvelope{Kind: recordKindObject, Object: of, Residue: rf}, mustAddr(t, addr))
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

var secretsInEnvelope = []string{"hunter2-object", "key-in-a-list", "hunter2-residue", "b2JqZWN0LXByaXZhdGUtYnl0ZXM", "cmVzaWR1ZS1wcml2YXRlLWJ5dGVz"}

// TestSSMSecretsSplitRoundTripsAllThreePlaces: every secret value leaves
// the bucket, everything else stays, and a read gives back the very bytes
// that were written.
func TestSSMSecretsSplitRoundTripsAllThreePlaces(t *testing.T) {
	ctx := context.Background()
	r := newSSMRig(t)
	s := r.open()
	addr := "random_password.db"
	key := RecordKey(r.prefix, mustAddr(t, addr))
	payload := secretEnvelope(t, addr)

	if _, err := s.PutIfVersion(ctx, key, payload, ""); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw := r.rawRecord(addr)
	for _, secret := range secretsInEnvelope {
		if bytes.Contains(raw, []byte(secret)) {
			t.Errorf("the bucket's record holds %q:\n%s", secret, raw)
		}
	}
	for _, kept := range []string{`"id":"pw-1"`, `"public-0"`, `lambda.zip`, `sensitive_attributes`} {
		if !bytes.Contains(raw, []byte(kept)) {
			t.Errorf("the bucket's record lost %s, which is not secret:\n%s", kept, raw)
		}
	}
	refs, err := rawRefs(raw)
	if err != nil {
		t.Fatal(err)
	}
	places := map[string]int{}
	for _, ref := range refs {
		places[ref.In]++
	}
	want := map[string]int{secretInObjectAttrs: 2, secretInObjectPrivate: 1, secretInResidueAttr: 1, secretInResiduePrivate: 1}
	if fmt.Sprint(places) != fmt.Sprint(want) {
		t.Errorf("references by place = %v, want %v (a null sensitive value moves nowhere)", places, want)
	}
	if got := len(r.ssm.names()); got != 5 {
		t.Errorf("SSM holds %d parameters, want 5", got)
	}

	// A fresh run, with nothing remembered, reads the same bytes back.
	got, _, ok, err := r.open().Get(ctx, key)
	if err != nil || !ok {
		t.Fatalf("read: exists=%v err=%v", ok, err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("the read is not the bytes written:\nwrote %s\nread  %s", payload, got)
	}
}

// TestSSMSecretsRoundTripThroughTheRecordStore drives the production
// writer and reader: a record-backed value with its sensitive mark and its
// private bytes comes back whole.
func TestSSMSecretsRoundTripThroughTheRecordStore(t *testing.T) {
	ctx := context.Background()
	r := newSSMRig(t)
	seedPassword(t, NewRecordEnvelopeStore(r.open(), r.prefix), "random_password.db", "s3cr3t-value", []byte("priv"))

	if raw := r.rawRecord("random_password.db"); bytes.Contains(raw, []byte("s3cr3t-value")) {
		t.Fatalf("the bucket's record holds the password:\n%s", raw)
	}
	env, _, ok, err := NewRecordEnvelopeStore(r.open(), r.prefix).getRaw(ctx, mustAddr(t, "random_password.db"))
	if err != nil || !ok {
		t.Fatalf("read: exists=%v err=%v", ok, err)
	}
	val, private, _, err := decodeObjectValue(env.Object)
	if err != nil {
		t.Fatal(err)
	}
	if !val.RawEquals(passwordValue("s3cr3t-value")) {
		t.Errorf("value read back = %#v", val)
	}
	if !val.GetAttr("result").HasMark(marks.Sensitive) {
		t.Error("the result came back without its sensitive mark")
	}
	if string(private) != "priv" {
		t.Errorf("private read back = %q", private)
	}
}

// TestSSMSecretsWriteCreatesBeforeItCommits: every parameter exists before
// the record that references it, each is a create of a SecureString under
// the configured key, and an unchanged secret is not written again.
func TestSSMSecretsWriteCreatesBeforeItCommits(t *testing.T) {
	ctx := context.Background()
	r := newSSMRig(t)
	rs := NewRecordEnvelopeStore(r.open(), r.prefix)
	seedPassword(t, rs, "random_password.db", "first", []byte("p"))

	if len(r.log) != 3 || !strings.HasPrefix(r.log[0], "ssm:put ") || !strings.HasPrefix(r.log[1], "ssm:put ") || !strings.HasPrefix(r.log[2], "s3:put ") {
		t.Fatalf("write order = %q, want two parameter creates and then the record", r.log)
	}
	for _, in := range r.ssm.puts {
		if aws.ToBool(in.Overwrite) || in.Type != ssmtypes.ParameterTypeSecureString || aws.ToString(in.KeyId) != testKMSKey || in.Tier != ssmtypes.ParameterTierStandard {
			t.Errorf("PutParameter(%s): overwrite=%v type=%s key=%s tier=%s", aws.ToString(in.Name), aws.ToBool(in.Overwrite), in.Type, aws.ToString(in.KeyId), in.Tier)
		}
		if !strings.HasPrefix(aws.ToString(in.Name), "/choudoufu/prod/secrets/") {
			t.Errorf("parameter %s is outside the configured path", aws.ToString(in.Name))
		}
	}

	// A change to a non-secret field keeps the parameters.
	addr := mustAddr(t, "random_password.db")
	_, version, _, _ := rs.getRaw(ctx, addr)
	before := r.ssm.names()
	if _, err := rs.mergeEnvelope(ctx, addr, version, func(e *recordEnvelope) { e.Provider = "provider[\"registry.opentofu.org/hashicorp/random\"]" }); err != nil {
		t.Fatal(err)
	}
	if r.ssm.putCount() != 2 || fmt.Sprint(r.ssm.names()) != fmt.Sprint(before) {
		t.Errorf("an unchanged secret was written again: %d creates, parameters %v -> %v", r.ssm.putCount(), before, r.ssm.names())
	}

	// A changed secret gets a new parameter, and the old one goes after
	// the commit.
	r.log = nil
	_, version, _, _ = rs.getRaw(ctx, addr)
	of, err := encodeObjectFields(passwordValue("second"), []byte("p"), states.ObjectReady)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rs.mergeEnvelope(ctx, addr, version, func(e *recordEnvelope) { e.Object = of }); err != nil {
		t.Fatal(err)
	}
	if len(r.log) != 3 || !strings.HasPrefix(r.log[0], "ssm:put ") || !strings.HasPrefix(r.log[1], "s3:put ") || !strings.HasPrefix(r.log[2], "ssm:delete ") {
		t.Errorf("rotation order = %q, want create, commit, delete of the superseded parameter", r.log)
	}
	if d := r.danglingRefs(); len(d) > 0 {
		t.Errorf("dangling references: %v", d)
	}
	if len(r.ssm.names()) != 2 {
		t.Errorf("SSM holds %v after a rotation, want the new value and the unchanged private", r.ssm.names())
	}
}

// TestSSMSecretsFailedCommitOrphansTheParameter: the record write fails
// after the parameter exists. No reference is written, the error is the
// store's, and the parameter is an orphan the sweep collects.
func TestSSMSecretsFailedCommitOrphansTheParameter(t *testing.T) {
	ctx := context.Background()
	r := newSSMRig(t)
	boom := errors.New("s3: connection reset")
	r.s3.failPut = boom
	s := r.open()
	rs := NewRecordEnvelopeStore(s, r.prefix)

	_, err := SeedRecordForInstance(ctx, rs, mustAddr(t, "random_password.db"), nullProvider, passwordValue("lost"), nil, states.ObjectReady)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the store's own failure", err)
	}
	if len(r.ssm.names()) != 1 {
		t.Fatalf("SSM holds %v, want the one parameter created before the commit", r.ssm.names())
	}
	if keys, _ := r.s3.List(ctx, r.prefix); len(keys) != 0 {
		t.Fatalf("the bucket holds %v after a failed commit", keys)
	}

	r.s3.failPut = nil
	if res, err := s.SweepOrphans(ctx); err != nil || len(res.Deleted) != 0 || len(res.Young) != 1 {
		t.Fatalf("sweep inside the grace period: %+v, %v; want the orphan left as young", res, err)
	}
	r.clock.Add(DefaultSSMSweepGrace + time.Minute)
	res, err := s.SweepOrphans(ctx)
	if err != nil || len(res.Deleted) != 1 {
		t.Fatalf("sweep after the grace period: %+v, %v; want the orphan deleted", res, err)
	}
	if len(r.ssm.names()) != 0 {
		t.Errorf("SSM still holds %v", r.ssm.names())
	}
}

// TestSSMSecretsRaceOneWinsNoDanglingReference is two runs writing one
// record from the same version. A has created its parameter and not yet
// committed when B writes the whole record; A's commit then loses. Driven
// by the store's beforeCommit hook, not by timing.
func TestSSMSecretsRaceOneWinsNoDanglingReference(t *testing.T) {
	ctx := context.Background()
	r := newSSMRig(t)
	addr := mustAddr(t, "random_password.db")
	seedPassword(t, NewRecordEnvelopeStore(r.open(), r.prefix), "random_password.db", "v1", nil)
	original := r.ssm.names()

	storeA, storeB := r.open(), r.open()
	runA, runB := NewRecordEnvelopeStore(storeA, r.prefix), NewRecordEnvelopeStore(storeB, r.prefix)
	_, version, _, err := runA.getRaw(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := runB.getRaw(ctx, addr); err != nil {
		t.Fatal(err)
	}
	objA, _ := encodeObjectFields(passwordValue("from-A"), nil, states.ObjectReady)
	objB, _ := encodeObjectFields(passwordValue("from-B"), nil, states.ObjectReady)

	var errB error
	fired := false
	storeA.cfg.beforeCommit = func(string) {
		if fired {
			return
		}
		fired = true
		_, errB = runB.mergeEnvelope(ctx, addr, version, func(e *recordEnvelope) { e.Object = objB })
	}
	_, errA := runA.mergeEnvelope(ctx, addr, version, func(e *recordEnvelope) { e.Object = objA })

	if !fired || errB != nil {
		t.Fatalf("B's write inside A's window: fired=%v err=%v", fired, errB)
	}
	var conflict *staterecord.VersionConflictError
	if !errors.As(errA, &conflict) {
		t.Fatalf("A's error = %v, want the store's named version conflict", errA)
	}
	if d := r.danglingRefs(); len(d) > 0 {
		t.Fatalf("a reference points at a missing parameter: %v", d)
	}
	env, _, _, err := NewRecordEnvelopeStore(r.open(), r.prefix).getRaw(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	if val, _, _, _ := decodeObjectValue(env.Object); !val.RawEquals(passwordValue("from-B")) {
		t.Errorf("the record holds %#v, want B's value", val)
	}
	for _, n := range original {
		if r.ssm.has(n) {
			t.Errorf("B superseded %s and it was not deleted", n)
		}
	}
	// A's parameter is an orphan, young, and the sweep leaves it until the
	// grace period has passed; B's is referenced and is never touched.
	referenced := map[string]bool{}
	refs, _ := rawRefs(r.rawRecord("random_password.db"))
	for _, ref := range refs {
		referenced[ref.Parameter] = true
	}
	res, err := storeA.SweepOrphans(ctx)
	if err != nil || len(res.Deleted) != 0 || len(res.Young) != 1 {
		t.Fatalf("sweep inside A's window: %+v, %v", res, err)
	}
	r.clock.Add(DefaultSSMSweepGrace + time.Minute)
	res, err = storeA.SweepOrphans(ctx)
	if err != nil || len(res.Deleted) != 1 || referenced[res.Deleted[0]] {
		t.Fatalf("sweep after the grace period: %+v, %v; want A's orphan and nothing referenced", res, err)
	}
	if d := r.danglingRefs(); len(d) > 0 {
		t.Fatalf("after the sweep a reference points at a missing parameter: %v", d)
	}
}

// manySecretsEnvelope is an object with n sensitive attributes.
func manySecretsEnvelope(t *testing.T, addr string, n int) []byte {
	t.Helper()
	attrs := map[string]cty.Value{}
	for i := 0; i < n; i++ {
		attrs[fmt.Sprintf("secret_%02d", i)] = cty.StringVal(fmt.Sprintf("value-%02d", i)).Mark(marks.Sensitive)
	}
	of, err := encodeObjectFields(cty.ObjectVal(attrs), nil, states.ObjectReady)
	if err != nil {
		t.Fatal(err)
	}
	p, err := marshalEnvelopeForWrite(recordEnvelope{Kind: recordKindObject, Object: of}, mustAddr(t, addr))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestSSMSecretsReadBatchesByTen: 23 references resolve in three calls of
// at most ten names, through a get and through the bulk read alike.
func TestSSMSecretsReadBatchesByTen(t *testing.T) {
	ctx := context.Background()
	r := newSSMRig(t)
	key := RecordKey(r.prefix, mustAddr(t, "random_password.many"))
	payload := manySecretsEnvelope(t, "random_password.many", 23)
	if _, err := r.open().PutIfVersion(ctx, key, payload, ""); err != nil {
		t.Fatal(err)
	}

	r.ssm.getBatches = nil
	got, _, _, err := r.open().Get(ctx, key)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("get: err=%v, same bytes=%v", err, bytes.Equal(got, payload))
	}
	if sizes := batchSizes(r.ssm.getBatches); fmt.Sprint(sizes) != "[10 10 3]" {
		t.Errorf("GetParameters batches = %v, want [10 10 3]", sizes)
	}

	r.ssm.getBatches = nil
	all, err := r.open().GetAll(ctx, r.prefix)
	if err != nil || !bytes.Equal(all[key].Payload, payload) {
		t.Fatalf("bulk read: err=%v", err)
	}
	if sizes := batchSizes(r.ssm.getBatches); fmt.Sprint(sizes) != "[10 10 3]" {
		t.Errorf("bulk GetParameters batches = %v, want [10 10 3]", sizes)
	}
}

func batchSizes(b [][]string) []int {
	var out []int
	for _, x := range b {
		out = append(out, len(x))
	}
	return out
}

// TestSSMSecretsReadRetriesThrottling: three throttled reads, backed off
// and retried, and then the value.
func TestSSMSecretsReadRetriesThrottling(t *testing.T) {
	ctx := context.Background()
	r := newSSMRig(t)
	key := RecordKey(r.prefix, mustAddr(t, "random_password.db"))
	payload := secretEnvelope(t, "random_password.db")
	if _, err := r.open().PutIfVersion(ctx, key, payload, ""); err != nil {
		t.Fatal(err)
	}
	r.ssm.throttleGets = 3
	got, _, _, err := r.open().Get(ctx, key)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("throttled read: err=%v", err)
	}
	if fmt.Sprint(r.sleeps) != "[100ms 200ms 400ms]" {
		t.Errorf("backoff = %v, want [100ms 200ms 400ms]", r.sleeps)
	}

	r.ssm.throttleGets = ssmMaxAttempts
	if _, _, _, err := r.open().Get(ctx, key); err == nil || !strings.Contains(err.Error(), "throttled") {
		t.Errorf("a read throttled on every attempt: err=%v, want it to give up and say so", err)
	}
}

// TestSSMSecretsMissingParameterIsANamedError: a reference to a parameter
// that is gone fails the read by name, through the record store too.
func TestSSMSecretsMissingParameterIsANamedError(t *testing.T) {
	ctx := context.Background()
	r := newSSMRig(t)
	seedPassword(t, NewRecordEnvelopeStore(r.open(), r.prefix), "random_password.db", "gone-soon", nil)
	refs, _ := rawRefs(r.rawRecord("random_password.db"))
	r.ssm.mu.Lock()
	delete(r.ssm.params, refs[0].Parameter)
	r.ssm.mu.Unlock()

	_, _, _, err := NewRecordEnvelopeStore(r.open(), r.prefix).getRaw(ctx, mustAddr(t, "random_password.db"))
	var missing *SecretParameterMissingError
	if !errors.As(err, &missing) || missing.Parameter != refs[0].Parameter || missing.Key == "" {
		t.Fatalf("err = %v, want a SecretParameterMissingError naming %s and the record", err, refs[0].Parameter)
	}
}

// TestSSMSecretsSweepNeverDeletesAReferencedParameter: an old referenced
// parameter stays, an old orphan goes, a young orphan stays. Red-proven by
// making sweepRefs return no references: the referenced parameter is then
// deleted and this fails.
func TestSSMSecretsSweepNeverDeletesAReferencedParameter(t *testing.T) {
	ctx := context.Background()
	r := newSSMRig(t)
	s := r.open()
	seedPassword(t, NewRecordEnvelopeStore(s, r.prefix), "random_password.db", "keep-me", nil)
	referenced := r.ssm.names()

	ns := s.Namespace()
	r.ssm.params[ns+"/old-orphan"] = &fakeParam{value: "x", modified: r.clock.Now()}
	r.clock.Add(2 * DefaultSSMSweepGrace)
	r.ssm.params[ns+"/young-orphan"] = &fakeParam{value: "y", modified: r.clock.Now()}
	r.ssm.params["/choudoufu/prod/secrets/another-estate/old"] = &fakeParam{value: "z", modified: time.Time{}}

	res, err := s.SweepOrphans(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(res.Deleted) != fmt.Sprint([]string{ns + "/old-orphan"}) {
		t.Errorf("deleted %v, want only the old orphan", res.Deleted)
	}
	for _, n := range referenced {
		if !r.ssm.has(n) {
			t.Errorf("the sweep deleted %s, which a record references", n)
		}
	}
	if !r.ssm.has(ns+"/young-orphan") || !r.ssm.has("/choudoufu/prod/secrets/another-estate/old") {
		t.Errorf("the sweep deleted a young orphan or a parameter outside its namespace: %v", r.ssm.names())
	}
	if res.Referenced != len(referenced) {
		t.Errorf("Referenced = %d, want %d", res.Referenced, len(referenced))
	}
}

// slowClock advances by step on every read, so a sweep that reads the
// clock more than once sees its own run take time.
type slowClock struct {
	t    time.Time
	step time.Duration
}

func (c *slowClock) Now() time.Time { c.t = c.t.Add(c.step); return c.t }

// TestSSMSecretsSweepAbortsWhenItsOwnRunIsSlow is bound 2 of the sweep's
// argument: a sweep slower than half the grace period deletes nothing.
func TestSSMSecretsSweepAbortsWhenItsOwnRunIsSlow(t *testing.T) {
	ctx := context.Background()
	r := newSSMRig(t)
	s := r.open()
	r.ssm.params[s.Namespace()+"/old-orphan"] = &fakeParam{value: "x", modified: time.Time{}}
	clock := &slowClock{t: r.clock.Now(), step: DefaultSSMSweepGrace / 2}
	s.cfg.Now = clock.Now
	_, err := s.SweepOrphans(ctx)
	var aborted *SweepOrphanSecretsAbortedError
	if !errors.As(err, &aborted) || !r.ssm.has(s.Namespace()+"/old-orphan") {
		t.Fatalf("err = %v, orphan still there = %v; want the sweep to stop before deleting", err, r.ssm.has(s.Namespace()+"/old-orphan"))
	}
}

// TestSSMSecretsSlowWriteIsNotCommitted is bound 1: a write slower than
// half the grace period between its first parameter and its commit is
// refused, and leaves the bucket untouched.
func TestSSMSecretsSlowWriteIsNotCommitted(t *testing.T) {
	r := newSSMRig(t)
	s := r.open()
	clock := &slowClock{t: r.clock.Now(), step: DefaultSSMSweepGrace / 2}
	s.cfg.Now = clock.Now
	_, err := SeedRecordForInstance(context.Background(), NewRecordEnvelopeStore(s, r.prefix), mustAddr(t, "random_password.db"), nullProvider, passwordValue("slow"), nil, states.ObjectReady)
	var slow *SecretWriteTooSlowError
	if !errors.As(err, &slow) {
		t.Fatalf("err = %v, want SecretWriteTooSlowError", err)
	}
	if keys, _ := r.s3.List(context.Background(), r.prefix); len(keys) != 0 {
		t.Errorf("the bucket holds %v after a refused slow write", keys)
	}
}

// TestSSMSecretsAdvancedTierIsChosenAndAnnounced is ruling 3: a single
// value over 4 KB is an advanced parameter, announced with its cost by the
// run that writes it and by every run that reads it. Over 8 KB nothing is
// written.
func TestSSMSecretsAdvancedTierIsChosenAndAnnounced(t *testing.T) {
	ctx := context.Background()
	r := newSSMRig(t)
	big := strings.Repeat("k", 5000)
	seedPassword(t, NewRecordEnvelopeStore(r.open(), r.prefix), "tls_private_key.big", big, nil)

	var tiers []ssmtypes.ParameterTier
	for _, in := range r.ssm.puts {
		tiers = append(tiers, in.Tier)
	}
	if fmt.Sprint(tiers) != fmt.Sprint([]ssmtypes.ParameterTier{ssmtypes.ParameterTierAdvanced}) {
		t.Fatalf("tiers = %v, want one advanced parameter", tiers)
	}
	if len(r.notes) != 1 || !strings.Contains(r.notes[0], "$0.05 per parameter-month") || !strings.Contains(r.notes[0], "tls_private_key.big") {
		t.Fatalf("announcements = %q, want one naming the instance and the cost", r.notes)
	}
	r.notes = nil
	reader := r.open()
	if _, _, _, err := NewRecordEnvelopeStore(reader, r.prefix).getRaw(ctx, mustAddr(t, "tls_private_key.big")); err != nil {
		t.Fatal(err)
	}
	if len(r.notes) != 1 || len(reader.Announcements()) != 1 {
		t.Errorf("a run that only reads announced %q, want the same one announcement", r.notes)
	}

	before := r.ssm.putCount()
	_, err := SeedRecordForInstance(ctx, NewRecordEnvelopeStore(r.open(), r.prefix), mustAddr(t, "tls_private_key.huge"), nullProvider, passwordValue(strings.Repeat("k", 9000)), nil, states.ObjectReady)
	var tooBig *SecretTooLargeError
	if !errors.As(err, &tooBig) || r.ssm.putCount() != before {
		t.Errorf("a 9000-byte value: err=%v, creates %d -> %d; want SecretTooLargeError and no write", err, before, r.ssm.putCount())
	}
}

// TestSSMSecretsCapacityRefusedBeforeAnyWrite: a planned count over the
// standard tier's ceiling refuses the run's first write before a parameter
// or a record is written, and every write after it.
func TestSSMSecretsCapacityRefusedBeforeAnyWrite(t *testing.T) {
	ctx := context.Background()
	r := newSSMRig(t)
	s := r.open()
	s.PlanSecretParameters(SecretParameterCount{Standard: SSMStandardParameterCeiling + 1})
	rs := NewRecordEnvelopeStore(s, r.prefix)
	for _, addr := range []string{"random_password.a", "random_password.b"} {
		_, err := SeedRecordForInstance(ctx, rs, mustAddr(t, addr), nullProvider, passwordValue("x"), nil, states.ObjectReady)
		var refused *SecretsCapacityError
		if !errors.As(err, &refused) {
			t.Fatalf("%s: err = %v, want SecretsCapacityError", addr, err)
		}
	}
	if r.ssm.putCount() != 0 || len(r.log) != 0 {
		t.Errorf("writes after a capacity refusal: %d parameter creates, log %q", r.ssm.putCount(), r.log)
	}

	for _, tc := range []struct {
		n    SecretParameterCount
		want string
	}{
		{SecretParameterCount{Standard: 8999}, ""},
		{SecretParameterCount{Standard: 9000}, SummarySecretsCapacityNear},
		{SecretParameterCount{Standard: 10000}, SummarySecretsCapacityNear},
		{SecretParameterCount{Standard: 10001}, SummarySecretsCapacityExceeded},
		{SecretParameterCount{Advanced: 100001}, SummarySecretsCapacityExceeded},
	} {
		diags := CheckSecretsCapacity(tc.n)
		got := ""
		if len(diags) > 0 {
			got = diags[0].Description().Summary
		}
		if got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.n, got, tc.want)
		}
	}

	n, err := CountSecretParameters([][]byte{secretEnvelope(t, "random_password.db"), manySecretsEnvelope(t, "random_password.many", 3)})
	if err != nil || n != (SecretParameterCount{Standard: 8}) {
		t.Errorf("CountSecretParameters = %+v, %v; want 8 standard", n, err)
	}
}

// TestDecodeEnvelopeRefusesUnresolvedSecretRefs: a record written under
// the setting, read by a run opened without it, is refused by name rather
// than read with nulls where its secrets were.
func TestDecodeEnvelopeRefusesUnresolvedSecretRefs(t *testing.T) {
	r := newSSMRig(t)
	seedPassword(t, NewRecordEnvelopeStore(r.open(), r.prefix), "random_password.db", "x", nil)
	_, _, _, err := NewRecordEnvelopeStore(r.s3, r.prefix).getRaw(context.Background(), mustAddr(t, "random_password.db"))
	var unresolved *UnresolvedSecretRefsError
	if !errors.As(err, &unresolved) {
		t.Fatalf("err = %v, want UnresolvedSecretRefsError", err)
	}
}

// TestMarkSensitiveResidueOnlyUnderSSM: the residue's sensitive list is
// written under "ssm" and under nothing else, so the record of an estate
// that never set it is the bytes it always was.
func TestMarkSensitiveResidueOnlyUnderSSM(t *testing.T) {
	block := &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"master_password": {Type: cty.String, Optional: true, Sensitive: true},
			"filename":        {Type: cty.String, Optional: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"auth": {Nesting: configschema.NestingSingle, Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
				"token": {Type: cty.String, Optional: true, Sensitive: true},
				"user":  {Type: cty.String, Optional: true},
			}}},
		},
	}
	tokenKey, err := encodeResiduePathKey(cty.GetAttrPath("auth").GetAttr("token"))
	if err != nil {
		t.Fatal(err)
	}
	userKey, _ := encodeResiduePathKey(cty.GetAttrPath("auth").GetAttr("user"))
	attrs := map[string]cty.Value{"master_password": cty.StringVal("a"), "filename": cty.StringVal("b"), tokenKey: cty.StringVal("c"), userKey: cty.StringVal("d")}

	for _, setting := range []strict.Secrets{strict.Store, strict.Refuse, ""} {
		rf, _ := encodeResidueFields(attrs)
		markSensitiveResidue(rf, block, setting)
		if rf.Sensitive != nil {
			t.Errorf("under %q the residue carries a sensitive list %v", setting, rf.Sensitive)
		}
	}
	rf, _ := encodeResidueFields(attrs)
	markSensitiveResidue(rf, block, strict.SSM)
	want := []string{tokenKey, "master_password"}
	sort.Strings(want)
	if fmt.Sprint(rf.Sensitive) != fmt.Sprint(want) {
		t.Errorf("under ssm, Sensitive = %v, want %v", rf.Sensitive, want)
	}
}

// TestSecretsInSSMOffLeavesTheStoreAlone: with no secrets = "ssm" the
// opener returns the very store it was given.
func TestSecretsInSSMOffLeavesTheStoreAlone(t *testing.T) {
	inner := newTestLocalStore(t)
	for _, rs := range []*configs.LiveRecordStore{nil, {Type: "s3"}, {Type: "local"}} {
		got, err := wrapSecretsInSSM(context.Background(), inner, rs, nil, "prod")
		if err != nil || got != inner {
			t.Errorf("%+v: got %T, %v; want the store unchanged", rs, got, err)
		}
	}
	for _, rs := range []*configs.LiveRecordStore{
		{Type: "local", SecretsInSSM: true, SecretsSSM: &configs.LiveStrictSSM{KMSKeyID: testKMSKey}},
		{Type: "s3", SecretsInSSM: true},
		{Type: "s3", SecretsInSSM: true, SecretsSSM: &configs.LiveStrictSSM{}},
	} {
		if _, err := wrapSecretsInSSM(context.Background(), inner, rs, nil, "prod"); !IsStoreRefusal(err) {
			t.Errorf("%+v: err = %v, want a store refusal", rs, err)
		}
	}
}

// TestSSMSecretsPassesNonRecordKeysThrough: the sentinel and anything else
// that is not a record envelope is the inner store's business.
func TestSSMSecretsPassesNonRecordKeysThrough(t *testing.T) {
	ctx := context.Background()
	r := newSSMRig(t)
	s := r.open()
	key := SentinelKey(r.prefix)
	if _, err := s.PutIfAbsent(ctx, key, []byte(sentinelPayload)); err != nil {
		t.Fatal(err)
	}
	got, _, _, err := r.s3.Get(ctx, key)
	if err != nil || string(got) != sentinelPayload || r.ssm.putCount() != 0 {
		t.Errorf("sentinel: %q, %v, %d creates", got, err, r.ssm.putCount())
	}
}

// TestSSMSecretsSplitRefusesWhatItCannotRestore is the byte-identity guard:
// a payload whose plain decode and encode are not the identity is refused,
// never stored in a shape that would read back different.
func TestSSMSecretsSplitRefusesWhatItCannotRestore(t *testing.T) {
	ctx := context.Background()
	r := newSSMRig(t)
	payload := secretEnvelope(t, "random_password.db")
	// Indented JSON decodes to the same envelope and does not re-encode to
	// the same bytes.
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, payload, "", "  "); err != nil {
		t.Fatal(err)
	}
	_, err := r.open().PutIfVersion(ctx, RecordKey(r.prefix, mustAddr(t, "random_password.db")), pretty.Bytes(), "")
	if !errors.Is(err, errSecretSplitNotIdentity) {
		t.Fatalf("err = %v, want the byte-identity refusal", err)
	}
	if keys, _ := r.s3.List(ctx, r.prefix); len(keys) != 0 {
		t.Errorf("the bucket holds %v after a refused split", keys)
	}
}

// TestSSMSecretsStaleWriteCreatesNothing: a write expecting a version the
// bucket no longer holds is refused before a parameter is created, and the
// payload it was handed - every secret in clear - never reaches the bucket.
func TestSSMSecretsStaleWriteCreatesNothing(t *testing.T) {
	ctx := context.Background()
	r := newSSMRig(t)
	seedPassword(t, NewRecordEnvelopeStore(r.open(), r.prefix), "random_password.db", "first", nil)
	before := r.ssm.putCount()
	key := RecordKey(r.prefix, mustAddr(t, "random_password.db"))
	_, err := r.open().PutIfVersion(ctx, key, secretEnvelope(t, "random_password.db"), "")
	var conflict *staterecord.VersionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v, want a version conflict", err)
	}
	if r.ssm.putCount() != before {
		t.Errorf("a stale write created %d parameters", r.ssm.putCount()-before)
	}
	if raw := r.rawRecord("random_password.db"); bytes.Contains(raw, []byte("hunter2")) {
		t.Errorf("the stale write's clear payload reached the bucket:\n%s", raw)
	}
}
