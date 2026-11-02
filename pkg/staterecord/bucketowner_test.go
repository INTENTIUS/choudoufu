// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// GitHub issue #1381. ExpectedBucketOwner is a per-REQUEST check: it protects
// exactly the requests that carry it, and an input this package forgot to set
// it on is a request that reaches a bucket in any account at all. So these
// tests assert the header operation by operation against a fake that records
// what it received, and the operations are named in the failure, rather than
// asserting once that "the store sends it".
//
// expectedBucketOwnerHeader is what the SDK puts ExpectedBucketOwner in.
const expectedBucketOwnerHeader = "x-amz-expected-bucket-owner"

// ownerTestAccount is the account these tests pin.
const ownerTestAccount = "111122223333"

// seenRequest is one request the recording fake received, reduced to what
// these tests assert on.
type seenRequest struct {
	op    string
	owner string
}

// ownerFakeS3 is a fake S3 that records the ExpectedBucketOwner header of
// every request, answers the four object operations well enough for the store
// to work, and answers the three bucket-contract reads.
//
// It is separate from [fakeS3Server] on purpose: that one models conditional
// writes and pagination and knows nothing about bucket-level reads, and these
// tests need the opposite emphasis.
type ownerFakeS3 struct {
	mu      sync.Mutex
	seen    []seenRequest
	objects map[string][]byte
	seq     int

	// refuseWrongOwner, when set, makes every request whose
	// ExpectedBucketOwner header names an account other than realOwner answer
	// 403 AccessDenied, which is what S3 does. A request with no header is
	// served, which is also what S3 does, and is the whole reason the header
	// has to be on every input.
	refuseWrongOwner bool
	realOwner        string
}

func (f *ownerFakeS3) record(op string, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, seenRequest{op: op, owner: r.Header.Get(expectedBucketOwnerHeader)})
}

// opFor names the S3 operation a request is, from the wire alone.
func opFor(r *http.Request) string {
	q := r.URL.Query()
	switch {
	case q.Has("versioning"):
		return "GetBucketVersioning"
	case q.Has("lifecycle"):
		return "GetBucketLifecycleConfiguration"
	case q.Has("publicAccessBlock"):
		return "GetPublicAccessBlock"
	case q.Get("list-type") == "2":
		return "ListObjectsV2"
	}
	switch r.Method {
	case http.MethodPut:
		return "PutObject"
	case http.MethodGet:
		return "GetObject"
	case http.MethodDelete:
		return "DeleteObject"
	}
	return "unknown " + r.Method
}

func (f *ownerFakeS3) handle(w http.ResponseWriter, r *http.Request) {
	op := opFor(r)
	f.record(op, r)

	if got := r.Header.Get(expectedBucketOwnerHeader); f.refuseWrongOwner && got != "" && got != f.realOwner {
		writeS3Error(w, http.StatusForbidden, "AccessDenied", "Access Denied")
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	switch op {
	case "GetBucketVersioning":
		writeXML(w, `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`)
	case "GetBucketLifecycleConfiguration":
		writeXML(w, `<LifecycleConfiguration><Rule><ID>expire-noncurrent</ID><Status>Enabled</Status><Filter></Filter><NoncurrentVersionExpiration><NoncurrentDays>30</NoncurrentDays></NoncurrentVersionExpiration></Rule></LifecycleConfiguration>`)
	case "GetPublicAccessBlock":
		writeXML(w, `<PublicAccessBlockConfiguration><BlockPublicAcls>true</BlockPublicAcls><IgnorePublicAcls>true</IgnorePublicAcls><BlockPublicPolicy>true</BlockPublicPolicy><RestrictPublicBuckets>true</RestrictPublicBuckets></PublicAccessBlockConfiguration>`)
	case "ListObjectsV2":
		var keys []string
		for k := range f.objects {
			keys = append(keys, strings.TrimPrefix(k, "/test-bucket/"))
		}
		sort.Strings(keys)
		var b strings.Builder
		fmt.Fprintf(&b, `<ListBucketResult><Name>test-bucket</Name><KeyCount>%d</KeyCount><MaxKeys>1000</MaxKeys><IsTruncated>false</IsTruncated>`, len(keys))
		for _, k := range keys {
			fmt.Fprintf(&b, `<Contents><Key>%s</Key><ETag>"e"</ETag><Size>1</Size></Contents>`, k)
		}
		b.WriteString(`</ListBucketResult>`)
		writeXML(w, b.String())
	case "PutObject":
		f.seq++
		etag := fmt.Sprintf(`"etag-%d"`, f.seq)
		f.objects[r.URL.Path] = []byte(etag)
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusOK)
	case "GetObject":
		body, ok := f.objects[r.URL.Path]
		if !ok {
			writeS3Error(w, http.StatusNotFound, "NoSuchKey", "not found")
			return
		}
		w.Header().Set("ETag", string(body))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	case "DeleteObject":
		delete(f.objects, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func writeXML(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>` + body))
}

// newOwnerFakeStore builds an [S3Store] against a recording fake. owner is
// what the configuration pins, "" for a store that pins nothing.
func newOwnerFakeStore(t *testing.T, owner string) (*S3Store, *ownerFakeS3) {
	t.Helper()
	fake := &ownerFakeS3{objects: map[string][]byte{}, realOwner: ownerTestAccount}
	server := httptest.NewServer(http.HandlerFunc(fake.handle))
	t.Cleanup(server.Close)
	client := s3.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(server.URL)
		o.UsePathStyle = true
	})
	store, err := NewS3Store(S3Config{Client: client, Bucket: "test-bucket", ExpectedBucketOwner: owner})
	if err != nil {
		t.Fatalf("NewS3Store: %v", err)
	}
	return store, fake
}

// everyOperation drives one of each request this store can make, so that a
// field left off any single input shows up. The operations it covers are the
// ones asserted in wantOwnerOperations below; adding a call site to the store
// without adding it here leaves that call site unchecked.
func everyOperation(t *testing.T, store *S3Store) {
	t.Helper()
	ctx := context.Background()

	if _, _, _, err := store.Get(ctx, "k1"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	v, err := store.PutIfAbsent(ctx, "k1", []byte("v1"))
	if err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	v2, err := store.PutIfVersion(ctx, "k1", []byte("v2"), v)
	if err != nil {
		t.Fatalf("PutIfVersion: %v", err)
	}
	if _, err := store.List(ctx, ""); err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, err := store.GetAll(ctx, ""); err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if _, err := store.CheckContract(ctx, ContractOptions{Namespaces: []string{"tofu-records/prod/"}}); err != nil {
		t.Fatalf("CheckContract: %v", err)
	}
	if err := store.Delete(ctx, "k1", v2); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

// wantOwnerOperations is every S3 operation the store issues. A request this
// package sends that is not in this list is a request no test has ever looked
// at the header of.
var wantOwnerOperations = []string{
	"GetObject",
	"PutObject",
	"DeleteObject",
	"ListObjectsV2",
	"GetBucketVersioning",
	"GetBucketLifecycleConfiguration",
	"GetPublicAccessBlock",
}

// seenOps is the set of operations the fake received.
func (f *ownerFakeS3) seenOps() map[string]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]bool{}
	for _, s := range f.seen {
		out[s.op] = true
	}
	return out
}

// TestS3StoreSendsExpectedBucketOwnerOnEveryRequest is the red-provable half
// of #1381's binary layer. Dropping ExpectedBucketOwner from any one input
// leaves that operation's requests without the header, and this names it.
func TestS3StoreSendsExpectedBucketOwnerOnEveryRequest(t *testing.T) {
	store, fake := newOwnerFakeStore(t, ownerTestAccount)
	everyOperation(t, store)

	fake.mu.Lock()
	seen := append([]seenRequest(nil), fake.seen...)
	fake.mu.Unlock()

	if len(seen) == 0 {
		t.Fatal("the fake received no requests at all, so this test proves nothing")
	}
	// Per operation, and with the offending value, because the two GetObject
	// call sites (Get and the bulk read) are the same operation on the wire
	// and only the count tells them apart.
	bad := map[string][]string{}
	for _, s := range seen {
		if s.owner != ownerTestAccount {
			bad[s.op] = append(bad[s.op], s.owner)
		}
	}
	for op, owners := range bad {
		t.Errorf("%s sent %d request(s) carrying %s = %q, want %q on every one: a request without it reaches a bucket of this name in any account",
			op, len(owners), expectedBucketOwnerHeader, owners, ownerTestAccount)
	}

	got := fake.seenOps()
	for _, op := range wantOwnerOperations {
		if !got[op] {
			t.Errorf("no %s request was made, so this test says nothing about that call site", op)
		}
	}
	for op := range got {
		if !containsString(wantOwnerOperations, op) {
			t.Errorf("the store issued %s, which wantOwnerOperations does not list; add it there so the header is asserted for it", op)
		}
	}
}

// TestS3StoreSendsNoOwnerHeaderWhenNoneIsConfigured is the control. Without
// it a store that hard-coded an account would pass the test above, and every
// build before #1381 (and every configuration that names no bucket_owner)
// must keep working against a bucket in any account, including one shared on
// purpose.
func TestS3StoreSendsNoOwnerHeaderWhenNoneIsConfigured(t *testing.T) {
	store, fake := newOwnerFakeStore(t, "")
	everyOperation(t, store)

	fake.mu.Lock()
	seen := append([]seenRequest(nil), fake.seen...)
	fake.mu.Unlock()

	for _, s := range seen {
		if s.owner != "" {
			t.Errorf("%s sent %s = %q with no bucket_owner configured", s.op, expectedBucketOwnerHeader, s.owner)
		}
	}
	got := fake.seenOps()
	for _, op := range wantOwnerOperations {
		if !got[op] {
			t.Errorf("no %s request was made, so this control says nothing about that call site", op)
		}
	}
}

func containsString(list []string, want string) bool {
	for _, e := range list {
		if e == want {
			return true
		}
	}
	return false
}

// TestS3StoreNamesAForeignOwnerOnDenial: S3 answers a bucket owned by another
// account with the same 403 AccessDenied an IAM denial gets, so the store has
// to name the possibility from what it knows rather than from the response.
func TestS3StoreNamesAForeignOwnerOnDenial(t *testing.T) {
	const wrongAccount = "444455556666"
	store, fake := newOwnerFakeStore(t, wrongAccount)
	fake.refuseWrongOwner = true
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"Get", func() error { _, _, _, err := store.Get(ctx, "k1"); return err }},
		{"PutIfAbsent", func() error { _, err := store.PutIfAbsent(ctx, "k1", []byte("v")); return err }},
		{"PutIfVersion", func() error { _, err := store.PutIfVersion(ctx, "k1", []byte("v"), `"etag-1"`); return err }},
		{"Delete", func() error { return store.Delete(ctx, "k1", `"etag-1"`) }},
		{"List", func() error { _, err := store.List(ctx, ""); return err }},
		{"GetAll", func() error { _, err := store.GetAll(ctx, ""); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatal("the request was refused by the fake and the store reported success")
			}
			var foreign *BucketOwnerMismatchError
			if !errors.As(err, &foreign) {
				t.Fatalf("got %v (%T), want a *BucketOwnerMismatchError: a bare AccessDenied sends an operator to the role's S3 statements, which may be correct", err, err)
			}
			if foreign.Bucket != "test-bucket" || foreign.ExpectedOwner != wrongAccount {
				t.Errorf("the error names bucket %q and owner %q", foreign.Bucket, foreign.ExpectedOwner)
			}
			msg := err.Error()
			for _, want := range []string{"test-bucket", wrongAccount, "may be owned by an account other than"} {
				if !strings.Contains(msg, want) {
					t.Errorf("the message does not say %q:\n%s", want, msg)
				}
			}
			// It must not claim what it cannot know: proving the bucket is
			// owned by someone else takes a call this run was just refused.
			for _, forbidden := range []string{"is owned by", "is not owned by"} {
				if strings.Contains(msg, forbidden) {
					t.Errorf("the message says %q, which is a certainty this error does not have:\n%s", forbidden, msg)
				}
			}
		})
	}
}

// TestBucketContractSaysTheOwnerMayBeWrong: the three contract reads report a
// denial as a finding rather than an error, so the same ambiguity has to be
// said there too. Without it the report says a permission was denied and an
// operator grants it, twice, before looking at who owns the bucket.
func TestBucketContractSaysTheOwnerMayBeWrong(t *testing.T) {
	const wrongAccount = "444455556666"
	store, fake := newOwnerFakeStore(t, wrongAccount)
	fake.refuseWrongOwner = true

	findings, err := store.CheckContract(context.Background(), ContractOptions{Namespaces: []string{"tofu-records/prod/"}})
	if err != nil {
		t.Fatalf("CheckContract: %v", err)
	}
	if len(findings) != len(BucketSettings) {
		t.Fatalf("got %d findings, want one per setting", len(findings))
	}
	for _, f := range findings {
		if f.Outcome != Unreadable {
			t.Errorf("%s: a 403 is an unreadable setting, got %+v", f.Setting, f)
		}
		if !strings.Contains(f.Found, wrongAccount) {
			t.Errorf("%s: the finding does not name the expected account: %q", f.Setting, f.Found)
		}
	}
}

// TestBucketContractSaysNothingAboutTheOwnerWhenNoneIsPinned is that one's
// control: the sentence appears only because an owner was pinned.
func TestBucketContractSaysNothingAboutTheOwnerWhenNoneIsPinned(t *testing.T) {
	store, fake := newOwnerFakeStore(t, "")
	fake.refuseWrongOwner = true
	fake.realOwner = "never matches, and no header is sent anyway"

	findings, err := store.CheckContract(context.Background(), ContractOptions{Namespaces: []string{"tofu-records/prod/"}})
	if err != nil {
		t.Fatalf("CheckContract: %v", err)
	}
	for _, f := range findings {
		if strings.Contains(f.Found, "owned by an account other than") {
			t.Errorf("%s: the finding talks about the bucket's owner with no bucket_owner configured: %q", f.Setting, f.Found)
		}
	}
}

// TestBucketOwnerMismatchDoesNotDisplaceAKMSDenial: a KMS refusal arrives as
// 403 AccessDenied too, and its remedy is a key policy, which naming the
// bucket's owner would send an operator away from. GitHub issue #1345's
// classification stays first.
func TestBucketOwnerMismatchDoesNotDisplaceAKMSDenial(t *testing.T) {
	store := deniedS3Store(t, fmt.Sprintf(realKMSDenial, "kms:GenerateDataKey", "resource-based", "kms:GenerateDataKey"))
	store.expectedBucketOwner = ownerTestAccount

	_, err := store.PutIfAbsent(context.Background(), "a/b", []byte("x"))
	var denied *KMSDeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("got %v (%T), want a *KMSDeniedError even with an owner pinned", err, err)
	}
	var foreign *BucketOwnerMismatchError
	if errors.As(err, &foreign) {
		t.Error("a KMS denial was also reported as a possible owner mismatch; the key policy is the remedy and this sends the reader elsewhere")
	}
}
