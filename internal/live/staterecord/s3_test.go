// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// fakeS3Object is one stored object: a body and the ETag this fake server
// assigns it, sequentially, so a test can assert on the exact value a
// conditional write must have carried.
type fakeS3Object struct {
	body []byte
	etag string
}

// fakeS3Server is a minimal S3 speaking the real wire shapes this
// package's client actually depends on: conditional PutObject
// (If-Match/If-None-Match), GetObject, conditional DeleteObject
// (If-Match), and ListObjectsV2 with prefix + continuation-token
// pagination. It is not a general S3 emulator — only what [S3Store]
// calls.
type fakeS3Server struct {
	mu      sync.Mutex
	objects map[string]*fakeS3Object // key: "/bucket/key"
	seq     int

	// pageSize, when > 0, caps how many keys ListObjectsV2 returns per
	// page, so pagination can be exercised deterministically without a
	// thousand-object fixture.
	pageSize int
}

func newFakeS3Server(t *testing.T) (*httptest.Server, *fakeS3Server) {
	t.Helper()
	f := &fakeS3Server{objects: map[string]*fakeS3Object{}}
	server := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(server.Close)
	return server, f
}

func (f *fakeS3Server) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if r.URL.Query().Get("list-type") == "2" {
		f.listObjectsV2(w, r)
		return
	}

	switch r.Method {
	case http.MethodPut:
		f.putObject(w, r)
	case http.MethodGet:
		f.getObject(w, r)
	case http.MethodDelete:
		f.deleteObject(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *fakeS3Server) putObject(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	cur, exists := f.objects[r.URL.Path]

	ifNoneMatch := r.Header.Get("If-None-Match")
	ifMatch := r.Header.Get("If-Match")
	if ifNoneMatch == "*" && exists {
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	if ifMatch != "" && (!exists || ifMatch != cur.etag) {
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}

	f.seq++
	etag := fmt.Sprintf(`"etag-%d"`, f.seq)
	f.objects[r.URL.Path] = &fakeS3Object{body: body, etag: etag}
	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusOK)
}

func (f *fakeS3Server) getObject(w http.ResponseWriter, r *http.Request) {
	obj, exists := f.objects[r.URL.Path]
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchKey</Code><Message>not found</Message></Error>`))
		return
	}
	w.Header().Set("ETag", obj.etag)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(obj.body)
}

func (f *fakeS3Server) deleteObject(w http.ResponseWriter, r *http.Request) {
	obj, exists := f.objects[r.URL.Path]
	ifMatch := r.Header.Get("If-Match")
	if ifMatch != "" {
		if !exists || ifMatch != obj.etag {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
	}
	delete(f.objects, r.URL.Path)
	w.WriteHeader(http.StatusNoContent)
}

// listBucketResult mirrors S3's ListObjectsV2 XML response shape closely
// enough for aws-sdk-go-v2's own deserializer to parse it.
type listBucketResult struct {
	XMLName               xml.Name      `xml:"ListBucketResult"`
	Name                  string        `xml:"Name"`
	Prefix                string        `xml:"Prefix"`
	KeyCount              int           `xml:"KeyCount"`
	MaxKeys               int           `xml:"MaxKeys"`
	IsTruncated           bool          `xml:"IsTruncated"`
	NextContinuationToken string        `xml:"NextContinuationToken,omitempty"`
	Contents              []listContent `xml:"Contents"`
}

type listContent struct {
	Key  string `xml:"Key"`
	ETag string `xml:"ETag"`
	Size int    `xml:"Size"`
}

func (f *fakeS3Server) listObjectsV2(w http.ResponseWriter, r *http.Request) {
	// Path-style addressing: "/bucket" or "/bucket/".
	bucket := strings.Trim(r.URL.Path, "/")
	prefix := r.URL.Query().Get("prefix")
	token := r.URL.Query().Get("continuation-token")

	bucketPrefix := "/" + bucket + "/"
	var keys []string
	for path := range f.objects {
		if !strings.HasPrefix(path, bucketPrefix) {
			continue
		}
		key := strings.TrimPrefix(path, bucketPrefix)
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	start := 0
	if token != "" {
		idx, err := strconv.Atoi(token)
		if err == nil {
			start = idx
		}
	}

	pageSize := f.pageSize
	if pageSize <= 0 {
		pageSize = len(keys)
	}
	end := start + pageSize
	if end > len(keys) {
		end = len(keys)
	}
	if start > len(keys) {
		start = len(keys)
	}
	page := keys[start:end]

	result := listBucketResult{
		Name:        bucket,
		Prefix:      prefix,
		KeyCount:    len(page),
		MaxKeys:     1000,
		IsTruncated: end < len(keys),
	}
	if result.IsTruncated {
		result.NextContinuationToken = strconv.Itoa(end)
	}
	for _, key := range page {
		obj := f.objects[bucketPrefix+key]
		result.Contents = append(result.Contents, listContent{Key: key, ETag: obj.etag, Size: len(obj.body)})
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(result)
}

// newTestS3Store builds an [S3Store] against a fresh [fakeS3Server].
func newTestS3Store(t *testing.T, keyPrefix string) Store {
	t.Helper()
	server, _ := newFakeS3Server(t)
	client := s3.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(server.URL)
		o.UsePathStyle = true
	})
	store, err := NewS3Store(S3Config{Client: client, Bucket: "test-bucket", KeyPrefix: keyPrefix})
	if err != nil {
		t.Fatalf("NewS3Store: %v", err)
	}
	return store
}

func TestS3StoreConformance(t *testing.T) {
	runConformance(t, func(t *testing.T) Store {
		t.Helper()
		return newTestS3Store(t, "")
	})
}

func TestS3StoreConformanceWithKeyPrefix(t *testing.T) {
	runConformance(t, func(t *testing.T) Store {
		t.Helper()
		return newTestS3Store(t, "prefix/for/one/caller")
	})
}

func TestS3StoreListPaginates(t *testing.T) {
	server, fake := newFakeS3Server(t)
	fake.pageSize = 2
	client := s3.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(server.URL)
		o.UsePathStyle = true
	})
	store, err := NewS3Store(S3Config{Client: client, Bucket: "test-bucket"})
	if err != nil {
		t.Fatalf("NewS3Store: %v", err)
	}

	ctx := context.Background()
	want := []string{"a", "b", "c", "d", "e"}
	for _, key := range want {
		if _, err := store.PutIfAbsent(ctx, key, []byte(key)); err != nil {
			t.Fatalf("PutIfAbsent(%q): %v", key, err)
		}
	}

	got, err := store.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !equalStrings(got, want) {
		t.Errorf("List = %v, want %v", got, want)
	}
}

// TestS3StoreConflictNamesTheActualVersion proves ActualVersion in the
// error is the record's true current version, not an echo of what the
// caller sent.
func TestS3StoreConflictNamesTheActualVersion(t *testing.T) {
	store := newTestS3Store(t, "")
	ctx := context.Background()

	v1, err := store.PutIfAbsent(ctx, "k1", []byte("v1"))
	if err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	v2, err := store.PutIfVersion(ctx, "k1", []byte("v2"), v1)
	if err != nil {
		t.Fatalf("PutIfVersion: %v", err)
	}

	_, err = store.PutIfVersion(ctx, "k1", []byte("v3"), v1)
	var conflict *VersionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("got %v (%T), want *VersionConflictError", err, err)
	}
	if conflict.ActualVersion != v2 {
		t.Errorf("ActualVersion = %q, want %q", conflict.ActualVersion, v2)
	}
}

// TestS3StoreSequencedCASMismatch is the fake-server race test the task
// asked for: a PutIfVersion call whose condition is checked, deterministically,
// against a value a second "writer" changed between this test's own steps —
// no real goroutines are needed to prove the conflict surfaces, since
// S3Store's conditional write is a single atomic request rather than a
// read-compare-write with a window to race inside.
func TestS3StoreSequencedCASMismatch(t *testing.T) {
	store := newTestS3Store(t, "")
	ctx := context.Background()

	v1, err := store.PutIfAbsent(ctx, "k1", []byte("v1"))
	if err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}

	// Writer A reads v1 and prepares to write, but writer B lands first.
	if _, err := store.PutIfVersion(ctx, "k1", []byte("from-b"), v1); err != nil {
		t.Fatalf("writer B's PutIfVersion: %v", err)
	}

	// Writer A's write, still carrying the now-stale v1, must be rejected.
	_, err = store.PutIfVersion(ctx, "k1", []byte("from-a"), v1)
	if !isVersionConflict(err) {
		t.Fatalf("writer A's stale PutIfVersion: got %v (%T), want *VersionConflictError", err, err)
	}

	payload, _, _, err := store.Get(ctx, "k1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(payload) != "from-b" {
		t.Errorf("payload = %q, want %q (writer A must not have landed)", payload, "from-b")
	}
}
