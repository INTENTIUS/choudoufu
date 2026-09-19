// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// This file is GitHub issue #1370's first half: a run whose identity may
// read the record store and not write to it. Everything here drives
// openBuiltStore, the production sequence, rather than its pieces - see
// that function's own comment for why the tests do not reassemble it.

// s3AccessDenied is the error an S3Store returns when the role has no
// s3:PutObject, wrapped the way s3OpError wraps every failure from the
// wire, so these tests exercise the same unwrapping production does.
func s3AccessDenied(key string) error {
	return fmt.Errorf("staterecord: s3: creating %q: %w", key, &smithy.GenericAPIError{
		Code:    "AccessDenied",
		Message: "User: arn:aws:sts::111122223333:assumed-role/plan/ci is not authorized to perform: s3:PutObject on resource: \"arn:aws:s3:::records/" + key + "\"",
	})
}

// s3Forbidden is the other shape a refusal arrives in: a bare 403 from a
// store that sent no error code this package recognises. The bucket
// contract's reads have always counted it as a denial, and the sentinel
// write counts it as the same thing.
func s3Forbidden(key string) error {
	return fmt.Errorf("staterecord: s3: creating %q: %w", key, &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: http.StatusForbidden}},
		Err:      errors.New("api error: forbidden"),
	})
}

// s3ServerError is a store that FAILED rather than refused: the role may be
// perfectly correct and the next attempt may well succeed.
func s3ServerError(key string) error {
	return fmt.Errorf("staterecord: s3: creating %q: %w", key, &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: http.StatusInternalServerError}},
		Err:      &smithy.GenericAPIError{Code: "InternalError", Message: "We encountered an internal error. Please try again."},
	})
}

// writeRefusingStore is a real local store whose writes are all refused,
// the way a role with s3:GetObject and s3:ListBucket and no s3:PutObject
// has every PutObject refused. Reads and List go to the real store, so what
// the handshake sees when it reads the sentinel back is a genuine store's
// genuine answer.
//
// It answers the bucket contract too, and counts the calls: the point of
// half these tests is that first contact did NOT run, and a store the
// contract could not be read from at all would make that assertion pass for
// the wrong reason.
type writeRefusingStore struct {
	staterecord.Store
	denial func(key string) error
	checks int
	puts   int
}

func (s *writeRefusingStore) PutIfAbsent(_ context.Context, key string, _ []byte) (string, error) {
	s.puts++
	return "", s.denial(key)
}

func (s *writeRefusingStore) CheckBucketContract(_ context.Context, _ []string) ([]staterecord.BucketFinding, error) {
	s.checks++
	return passing(), nil
}

// seedSentinel writes the sentinel the way a run with write access would
// have, straight into the underlying store, so the run under test meets a
// store an earlier run already provisioned.
func seedSentinel(t *testing.T, inner staterecord.Store, rs *configs.LiveRecordStore, estate string) {
	t.Helper()
	if _, err := inner.PutIfAbsent(context.Background(), SentinelKey(RecordStoreKeyPrefix(rs, estate)), []byte(sentinelPayload)); err != nil {
		t.Fatalf("seeding the sentinel: %v", err)
	}
}

// TestAReaderOpensAStoreThatIsAlreadyProvisioned is #1370's whole point. A
// plan under a read-only role sends the sentinel write like every other
// run, is refused, and the store opens anyway because the List that the
// handshake already makes proves the sentinel is there.
//
// The first-contact count is the second half and is not decoration: the run
// did not create that sentinel, and treating another run's sentinel as this
// run's first contact would put three bucket configuration reads - and the
// permissions for them - on every read-only plan, which is what the ruling
// on #1339 decided against.
func TestAReaderOpensAStoreThatIsAlreadyProvisioned(t *testing.T) {
	const estate = "prod"
	rs := &configs.LiveRecordStore{Type: "s3", Bucket: "the-bucket"}

	for _, tc := range []struct {
		name   string
		denial func(string) error
	}{
		{"AccessDenied on PutObject", s3AccessDenied},
		{"a bare 403 with no code", s3Forbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner := newTestLocalStore(t)
			seedSentinel(t, inner, rs, estate)
			store := &writeRefusingStore{Store: inner, denial: tc.denial}

			opened, err := openBuiltStore(context.Background(), store, rs, estate)
			if err != nil {
				t.Fatalf("a role that may read the store and not write it could not open an estate that is already recorded: %v", err)
			}
			if opened == nil {
				t.Fatal("openBuiltStore returned no store and no error")
			}
			if store.puts != 1 {
				t.Errorf("the sentinel write was attempted %d time(s), want 1: the write is how a run with access provisions the store, and skipping it would leave a first run with nothing", store.puts)
			}
			if store.checks != 0 {
				t.Errorf("the bucket contract was read %d time(s); this run created no sentinel, so it is not this estate's first contact with the bucket (#1339)", store.checks)
			}
		})
	}
}

// TestAReaderIsRefusedByNameWhenTheStoreHasNoSentinel is the arm that must
// not be lost. A store with no sentinel and an identity that cannot write
// one reads exactly like an empty estate, and planning against it proposes
// creating every resource the estate already has - issue #693, which is the
// whole reason the sentinel exists.
//
// It is a REFUSAL and not an outage (#1376), because `live-plan` and
// `live-mv` go on without the store after an outage: going on here is the
// failure itself.
func TestAReaderIsRefusedByNameWhenTheStoreHasNoSentinel(t *testing.T) {
	const estate = "prod"
	rs := &configs.LiveRecordStore{Type: "s3", Bucket: "the-bucket"}
	store := &writeRefusingStore{Store: newTestLocalStore(t), denial: s3AccessDenied}

	_, err := openBuiltStore(context.Background(), store, rs, estate)
	if err == nil {
		t.Fatal("a store with no sentinel opened for a role that cannot provision one; every record-backed resource in the estate would read as absent and the plan would propose creating it again (issue #693)")
	}
	if !IsStoreRefusal(err) {
		t.Errorf("the refusal is reported as an outage, so live-plan would warn and carry on against a store it must not be planned against (#1376): %v", err)
	}
	for _, want := range []string{
		SentinelKey(RecordStoreKeyPrefix(rs, estate)), // the sentinel, by name
		"may not write", // what this role cannot do
		"write access",  // what has to happen once
		"#693",
		"#1370",
		"s3:PutObject", // what the store itself said
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%s", want, err)
		}
	}
	if store.checks != 0 {
		t.Errorf("the bucket contract was read %d time(s) on a run that wrote no sentinel", store.checks)
	}
}

// TestAKMSRefusalOnTheSentinelWriteIsNotAReader: S3 relays a KMS denial as
// AccessDenied on a 403, so the reader tolerance above is one careless
// errors.As away from swallowing it. A KMS refusal is the bucket refusing
// this run outright - every object in the store is unreadable while it
// lasts - so a run that carried on "read-only" would carry on with a store
// it cannot read at all. #1345, #1376, #1383.
func TestAKMSRefusalOnTheSentinelWriteIsNotAReader(t *testing.T) {
	const estate = "prod"
	rs := &configs.LiveRecordStore{Type: "s3", Bucket: "the-bucket"}

	for _, tc := range []struct {
		name   string
		denial func(string) error
		// want is a phrase only that KMS type's own rendering produces.
		want string
	}{
		{
			// The wire error underneath is the real thing, a 403 whose
			// code is AccessDenied, and that is the point: with a
			// hand-made cause this case would pass however the denial
			// classification reads, because there would be no denial in it
			// to recognise. It is only because this one IS an AccessDenied
			// that dropping the KMS guard turns this test red.
			name: "the key policy refused the run",
			denial: func(key string) error {
				return fmt.Errorf("staterecord: s3: creating %q: %w", key, &staterecord.KMSDeniedError{
					Action:    "kms:GenerateDataKey",
					KeyARN:    "arn:aws:kms:us-east-2:111122223333:key/abcd",
					Principal: "arn:aws:sts::111122223333:assumed-role/plan/ci",
					Where:     staterecord.KMSDeniedByKeyPolicy,
					Err: &smithyhttp.ResponseError{
						Response: &smithyhttp.Response{Response: &http.Response{StatusCode: http.StatusForbidden}},
						Err: &smithy.GenericAPIError{
							Code:    "AccessDenied",
							Message: "User: arn:aws:sts::111122223333:assumed-role/plan/ci is not authorized to perform: kms:GenerateDataKey on resource: arn:aws:kms:us-east-2:111122223333:key/abcd because no resource-based policy allows the kms:GenerateDataKey action",
						},
					},
				})
			},
			want: "kms:GenerateDataKey",
		},
		{
			// A key-state failure arrives on a 400 with its own code, so
			// the denial classification would not claim it even without
			// the guard; what this pins is that it still reaches the
			// operator as the key's own failure and is not lost on the way
			// out of the handshake (#1383).
			name: "the key is disabled",
			denial: func(key string) error {
				return fmt.Errorf("staterecord: s3: creating %q: %w", key, &staterecord.KMSKeyUnusableError{
					Code: "KMS.DisabledException",
					Err: &smithyhttp.ResponseError{
						Response: &smithyhttp.Response{Response: &http.Response{StatusCode: http.StatusBadRequest}},
						Err:      &smithy.GenericAPIError{Code: "KMS.DisabledException", Message: "arn:aws:kms:us-east-2:111122223333:key/abcd is disabled."},
					},
				})
			},
			want: "KMS.DisabledException",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The sentinel IS there, which is the dangerous shape: the
			// reader path would find it and open the store.
			inner := newTestLocalStore(t)
			seedSentinel(t, inner, rs, estate)
			store := &writeRefusingStore{Store: inner, denial: tc.denial}

			_, err := openBuiltStore(context.Background(), store, rs, estate)
			if err == nil {
				t.Fatal("a KMS refusal was read as a role that may read but not write, and the store opened; every record in it is unreadable")
			}
			if !IsStoreRefusal(err) {
				t.Errorf("a KMS refusal lost its refusal type on the way out: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the error no longer says %q, so the operator is sent to the S3 policy of a bucket whose S3 policy is fine:\n%s", tc.want, err)
			}
		})
	}
}

// TestANonDenialOnTheSentinelWriteIsStillAnError: a store that failed is
// not a store that refused. A 500 or a dial failure may well succeed on the
// next attempt, and reading either as "this identity may only read" would
// turn a transient backend failure into a plan built from no records at
// all, silently, on a role that could have written perfectly well.
func TestANonDenialOnTheSentinelWriteIsStillAnError(t *testing.T) {
	const estate = "prod"
	rs := &configs.LiveRecordStore{Type: "s3", Bucket: "the-bucket"}

	for _, tc := range []struct {
		name   string
		denial func(string) error
	}{
		{"a 500 from the store", s3ServerError},
		{"the store could not be dialled", func(string) error {
			return fmt.Errorf("staterecord: s3: creating %q: %w", "k", errUnreachable)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Again with the sentinel present, so the only thing standing
			// between this error and a silently opened store is the
			// classification of the write's failure.
			inner := newTestLocalStore(t)
			seedSentinel(t, inner, rs, estate)
			store := &writeRefusingStore{Store: inner, denial: tc.denial}

			_, err := openBuiltStore(context.Background(), store, rs, estate)
			if err == nil {
				t.Fatal("a store that failed the sentinel write was read as a role that may only read, and the store opened")
			}
			// An outage, not a refusal: the next attempt may work, which is
			// the distinction #1376 turns on.
			if IsStoreRefusal(err) {
				t.Errorf("a backend failure is reported as a refusal, so live-plan stops where it should warn and go on: %v", err)
			}
			if !strings.Contains(err.Error(), "provisioning the sentinel") {
				t.Errorf("the error does not say which call failed:\n%s", err)
			}
		})
	}
}

// TestTheVersionConflictPathIsUnchanged: the ordinary second run. Every run
// after an estate's first sends the same write and is answered with a
// conflict, which has always been the success case, and the reader branch
// must not have moved it. The count is what pins it: a conflict that
// started reporting a created version would make every run assert the
// bucket contract again.
func TestTheVersionConflictPathIsUnchanged(t *testing.T) {
	ctx := context.Background()
	const estate = "prod"
	rs := &configs.LiveRecordStore{Type: "s3", Bucket: "the-bucket"}
	store := &bucketBackedStore{Store: newTestLocalStore(t), findings: passing()}

	createdVersion, err := provisionStoreSentinel(ctx, store, RecordStoreKeyPrefix(rs, estate))
	if err != nil {
		t.Fatalf("first provision: %v", err)
	}
	if createdVersion == "" {
		t.Fatal("the run that created the sentinel reported no created version, so it would not be treated as the estate's first contact with its bucket (#1339)")
	}

	second, err := provisionStoreSentinel(ctx, store, RecordStoreKeyPrefix(rs, estate))
	if err != nil {
		t.Fatalf("a second run met the existing sentinel and did not treat the version conflict as the success case: %v", err)
	}
	if second != "" {
		t.Errorf("the second run reported created version %q; a conflict means an earlier run created it, and reporting one here asserts the bucket contract on every run", second)
	}
}

// TestALocalStoreInAReadOnlyDirectoryIsClassifiedTheSameWay is issue
// #1370's local half. The two backends are told apart by nothing a person
// running a plan would recognise - a directory mounted or chmodded
// read-only is the same situation as a role with no s3:PutObject - so the
// local store's EACCES is classified exactly like S3's AccessDenied.
//
// What this measures is the refusal arm, which is the one the local backend
// can actually reach: with the sentinel already present, the local store's
// O_CREATE|O_EXCL answers EEXIST whatever the directory's mode, so that run
// has always succeeded through the version-conflict path and needs nothing
// from #1370. An unprovisioned store under a read-only directory is the
// case that used to stop with a bare "permission denied" and now refuses by
// name.
func TestALocalStoreInAReadOnlyDirectoryIsClassifiedTheSameWay(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory mode does not deny creation on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory denies nothing")
	}
	const estate = "prod"
	dir := filepath.Join(t.TempDir(), "records")
	store, err := staterecord.NewLocalStore(dir)
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	// The guard on the guard: if this machine lets the write through, every
	// assertion below would pass for the wrong reason.
	if _, err := store.PutIfAbsent(context.Background(), "probe", []byte("x")); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("a write into a 0500 directory returned %v, so this test cannot deny anything", err)
	}

	rs := &configs.LiveRecordStore{Type: "local"}
	_, err = openBuiltStore(context.Background(), store, rs, estate)
	if err == nil {
		t.Fatal("a read-only directory with no sentinel opened as though it were an estate with no records (issue #693)")
	}
	if !IsStoreRefusal(err) {
		t.Errorf("the refusal is reported as an outage; no retry gets past a directory this run may not write: %v", err)
	}
	for _, want := range []string{SentinelKey(RecordStoreKeyPrefix(rs, estate)), "may not write", "write access"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%s", want, err)
		}
	}
}

// TestADeniedHintWriteSaysWhoWasDenied. Now that a reader's run opens the
// store, the writes further along the run meet the same denial, and the
// only one a plan can reach is guided discovery's hint: the manager writes
// it from PersistState, which a plan reaches only when the operator
// interrupts one (internal/backend/local/backend.go's opWait, reached from
// backend_plan.go). It has always been a warning and stays one. What
// changes is that "AccessDenied" on its own now has an ordinary
// explanation, and a reader should not go looking at the bucket for it.
func TestADeniedHintWriteSaysWhoWasDenied(t *testing.T) {
	const estate = "my-estate"
	const cause = "may read the record store and not write to it"

	denied := NewManager()
	denied.EnableHint(failingHintStore{putErr: s3AccessDenied(HintKey(estate))}, estate, nil)
	if err := denied.WriteState(testHintState()); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	if err := denied.PersistState(context.Background(), nil); err != nil {
		t.Fatalf("PersistState returned an error for a failed hint write: %v", err)
	}
	warning := denied.HintWarning()
	if len(warning) == 0 {
		t.Fatal("a denied hint write raised no warning at all")
	}
	detail := warning[0].Description().Detail
	if !strings.Contains(detail, cause) {
		t.Errorf("the warning leaves a bare AccessDenied for the reader to place:\n%s", detail)
	}
	if !strings.Contains(detail, HintKey(estate)) {
		t.Errorf("the warning does not name the key that was refused:\n%s", detail)
	}

	// And the sentence is the denial's, not every failure's: a backend that
	// is on fire is not a role that may only read, and telling an operator
	// it is would send them to IAM over an outage.
	broken := NewManager()
	broken.EnableHint(failingHintStore{putErr: errors.New("the backend is on fire")}, estate, nil)
	if err := broken.WriteState(testHintState()); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	if err := broken.PersistState(context.Background(), nil); err != nil {
		t.Fatalf("PersistState: %v", err)
	}
	brokenWarning := broken.HintWarning()
	if len(brokenWarning) == 0 {
		t.Fatal("a failed hint write raised no warning at all")
	}
	if got := brokenWarning[0].Description().Detail; strings.Contains(got, cause) {
		t.Errorf("a backend failure is reported as a role that may only read:\n%s", got)
	}
}

// TestNoRecordStoreBackendIsKubernetes is the maintainer's scope note on
// #1370, stated where it can be checked. A Kubernetes estate's plan role is
// two credentials, not one: the kubernetes provider's (a kubeconfig, or an
// in-cluster ServiceAccount token) reaches the cluster, and the record
// store's - the process's AWS credentials for the "s3" backend, or the
// process's own filesystem user for "local" - reaches the store. Nothing
// routes the first to the second.
//
// So the read-only-plan question for a Kubernetes estate is the AWS or
// local question above, unchanged: a ServiceAccount bound to get/list/watch
// says nothing about whether this run may write the sentinel, and binding
// it more widely would not help.
func TestNoRecordStoreBackendIsKubernetes(t *testing.T) {
	for _, typeName := range []string{"kubernetes", "k8s", "configmap", "secret"} {
		_, err := newRecordStore(context.Background(), &configs.LiveRecordStore{Type: typeName}, nil, "prod", t.TempDir(), recordStoreOptions{})
		if err == nil {
			t.Errorf("record_store %q built a store; if a Kubernetes-backed record store is ever added, every statement about which identity opens an estate's store has to be re-derived", typeName)
			continue
		}
		if !strings.Contains(err.Error(), "unknown backend") {
			t.Errorf("record_store %q failed with %v, want the unknown-backend refusal", typeName, err)
		}
	}
}
