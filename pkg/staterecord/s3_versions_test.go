// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// fakeS3Version is one entry in a versioned bucket's history of one key.
type fakeS3Version struct {
	id           string
	lastModified time.Time
	deleteMarker bool
	size         int
}

// fakeVersionEpoch is the fake's clock: version n was written n seconds after
// it, so every version has its own LastModified and the order is unambiguous.
var fakeVersionEpoch = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

// recordVersion appends the version a put or delete just made. Called with
// f.mu held.
func (f *fakeS3Server) recordVersion(path string, deleteMarker bool) {
	if f.history == nil {
		f.history = map[string][]fakeS3Version{}
	}
	f.vseq++
	size := 0
	if obj, ok := f.objects[path]; ok && !deleteMarker {
		size = len(obj.body)
	}
	f.history[path] = append(f.history[path], fakeS3Version{
		id:           fmt.Sprintf("v%d", f.vseq),
		lastModified: fakeVersionEpoch.Add(time.Duration(f.vseq) * time.Second),
		deleteMarker: deleteMarker,
		size:         size,
	})
}

type listVersionsResult struct {
	XMLName             xml.Name               `xml:"ListVersionsResult"`
	Name                string                 `xml:"Name"`
	Prefix              string                 `xml:"Prefix"`
	MaxKeys             int                    `xml:"MaxKeys"`
	IsTruncated         bool                   `xml:"IsTruncated"`
	NextKeyMarker       string                 `xml:"NextKeyMarker,omitempty"`
	NextVersionIDMarker string                 `xml:"NextVersionIdMarker,omitempty"`
	Versions            []listVersionEntry     `xml:"Version"`
	DeleteMarkers       []listDeleteMarkerItem `xml:"DeleteMarker"`
}

type listVersionEntry struct {
	Key          string `xml:"Key"`
	VersionID    string `xml:"VersionId"`
	IsLatest     bool   `xml:"IsLatest"`
	LastModified string `xml:"LastModified"`
	Size         int    `xml:"Size"`
}

type listDeleteMarkerItem struct {
	Key          string `xml:"Key"`
	VersionID    string `xml:"VersionId"`
	IsLatest     bool   `xml:"IsLatest"`
	LastModified string `xml:"LastModified"`
}

// listObjectVersions answers ListObjectVersions the way S3 does: keys in
// lexical order, each key's versions newest first, the newest one marked
// IsLatest, delete markers in their own elements. pageSize caps the entries
// per page, and the markers name the last entry returned.
func (f *fakeS3Server) listObjectVersions(w http.ResponseWriter, r *http.Request) {
	bucket := strings.Trim(r.URL.Path, "/")
	q := r.URL.Query()
	prefix := q.Get("prefix")
	bucketPrefix := "/" + bucket + "/"

	type entry struct {
		key    string
		v      fakeS3Version
		latest bool
	}
	var keys []string
	for path := range f.history {
		if key := strings.TrimPrefix(path, bucketPrefix); strings.HasPrefix(path, bucketPrefix) && strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var all []entry
	for _, key := range keys {
		h := f.history[bucketPrefix+key]
		for i := len(h) - 1; i >= 0; i-- {
			all = append(all, entry{key: key, v: h[i], latest: i == len(h)-1})
		}
	}

	start := 0
	if km := q.Get("key-marker"); km != "" {
		for i, e := range all {
			if e.key == km && e.v.id == q.Get("version-id-marker") {
				start = i + 1
				break
			}
		}
	}
	end := len(all)
	if f.pageSize > 0 && start+f.pageSize < end {
		end = start + f.pageSize
	}

	result := listVersionsResult{Name: bucket, Prefix: prefix, MaxKeys: 1000, IsTruncated: end < len(all)}
	if result.IsTruncated {
		result.NextKeyMarker = all[end-1].key
		result.NextVersionIDMarker = all[end-1].v.id
	}
	for _, e := range all[start:end] {
		when := e.v.lastModified.Format("2006-01-02T15:04:05.000Z")
		if e.v.deleteMarker {
			result.DeleteMarkers = append(result.DeleteMarkers, listDeleteMarkerItem{Key: e.key, VersionID: e.v.id, IsLatest: e.latest, LastModified: when})
			continue
		}
		result.Versions = append(result.Versions, listVersionEntry{Key: e.key, VersionID: e.v.id, IsLatest: e.latest, LastModified: when, Size: e.v.size})
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(result)
}

func newVersionedTestS3Store(t *testing.T) (*S3Store, *fakeS3Server) {
	t.Helper()
	server, fake := newFakeS3Server(t)
	client := s3.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(server.URL)
		o.UsePathStyle = true
	})
	store, err := NewS3Store(S3Config{Client: client, Bucket: "test-bucket"})
	if err != nil {
		t.Fatal(err)
	}
	return store, fake
}

// TestS3StoreListVersions is GitHub issue #1954's done-when at the store: a
// record written by three applies lists three versions, newest first, and
// nothing of the record's contents. Its BREAK is reading only the current
// version, which lists one.
func TestS3StoreListVersions(t *testing.T) {
	ctx := context.Background()

	t.Run("three applies, three versions, newest first", func(t *testing.T) {
		store, _ := newVersionedTestS3Store(t)
		const key = "tofu-records/prod/terraform_data/abc"
		// A neighbour whose key extends this one's, as RecordKey's chunking
		// can produce, and a sibling: neither is this record's history.
		if _, err := store.PutIfAbsent(ctx, key+"/more", []byte("x")); err != nil {
			t.Fatal(err)
		}
		if _, err := store.PutIfAbsent(ctx, key+"x", []byte("x")); err != nil {
			t.Fatal(err)
		}
		v, err := store.PutIfAbsent(ctx, key, []byte(`{"apply":1}`))
		if err != nil {
			t.Fatal(err)
		}
		if v, err = store.PutIfVersion(ctx, key, []byte(`{"apply":2}`), v); err != nil {
			t.Fatal(err)
		}
		if _, err = store.PutIfVersion(ctx, key, []byte(`{"apply":3}`), v); err != nil {
			t.Fatal(err)
		}

		got, err := store.ListVersions(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 3 {
			t.Fatalf("a record written by three applies lists %d versions, want 3: %+v", len(got), got)
		}
		for i := 1; i < len(got); i++ {
			if !got[i-1].LastModified.After(got[i].LastModified) {
				t.Errorf("versions are not newest first: %+v", got)
			}
		}
		if !got[0].Current || got[1].Current || got[2].Current {
			t.Errorf("only the newest version is current: %+v", got)
		}
		for _, rv := range got {
			if rv.VersionID == "" || rv.Deleted {
				t.Errorf("version %+v: want an id and no delete marker", rv)
			}
		}
	})

	t.Run("a deleted record lists its delete marker first and no current version", func(t *testing.T) {
		store, _ := newVersionedTestS3Store(t)
		const key = "tofu-records/prod/terraform_data/gone"
		v, err := store.PutIfAbsent(ctx, key, []byte("1"))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Delete(ctx, key, v); err != nil {
			t.Fatal(err)
		}
		got, err := store.ListVersions(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || !got[0].Deleted || got[0].Current || got[1].Deleted || got[1].Current {
			t.Fatalf("want [delete marker, the noncurrent write], got %+v", got)
		}
	})

	t.Run("a listing that pages is read to the end", func(t *testing.T) {
		store, fake := newVersionedTestS3Store(t)
		fake.pageSize = 2
		const key = "tofu-records/prod/terraform_data/paged"
		v, err := store.PutIfAbsent(ctx, key, []byte("0"))
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i < 5; i++ {
			if v, err = store.PutIfVersion(ctx, key, []byte(fmt.Sprint(i)), v); err != nil {
				t.Fatal(err)
			}
		}
		got, err := store.ListVersions(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 5 {
			t.Fatalf("five writes over pages of two list %d versions, want 5", len(got))
		}
	})

	t.Run("a key never written has no history", func(t *testing.T) {
		store, _ := newVersionedTestS3Store(t)
		got, err := store.ListVersions(ctx, "tofu-records/prod/terraform_data/never")
		if err != nil || len(got) != 0 {
			t.Fatalf("got %+v, %v; want nothing and no error", got, err)
		}
	})

	t.Run("found under the run cache and the trip counter", func(t *testing.T) {
		store, _ := newVersionedTestS3Store(t)
		wrapped := NewRunCache(NewCountingStore(store, nil), "tofu-records/prod/")
		if _, ok := AsVersionLister(wrapped); !ok {
			t.Fatal("the S3 store under its wrappers is not found as a VersionLister")
		}
		local, err := NewLocalStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := AsVersionLister(NewRunCache(local, "tofu-records/prod/")); ok {
			t.Fatal("the local store claims to keep past versions")
		}
	})
}
