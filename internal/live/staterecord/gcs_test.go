// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

// fakeGCSObject is one live object: a body, the generation this fake
// assigned it, and the custom metadata the write carried.
type fakeGCSObject struct {
	body     []byte
	gen      int64
	metadata map[string]string
}

// fakeGCSServer is a minimal GCS JSON API: the object media upload, a JSON
// read of the media, a conditional delete, objects.list with pagination, and
// buckets.get. Preconditions are GCS's own: ifGenerationMatch=0 means "no
// live object", any other value must equal the live generation, and a
// mismatch is 412 conditionNotMet. It is not a general emulator, only what
// [GCSStore] and the contract call.
type fakeGCSServer struct {
	mu      sync.Mutex
	bucket  string
	objects map[string]*fakeGCSObject
	gen     int64

	// pageSize, when > 0, caps how many items one objects.list page holds.
	pageSize int

	// missingBucket makes every request answer GCS's 404 for a bucket that
	// does not exist, which is the same status a missing object gets.
	missingBucket bool

	// forbidden makes every request answer 403.
	forbidden bool

	// listForbidden makes objects.list alone answer 403.
	listForbidden bool

	// kmsStatus, when set, makes every object read and write answer this
	// status with a message about the Cloud KMS key, the way GCS reports a
	// key its service agent may not use or a key that is disabled.
	kmsStatus  int
	kmsMessage string

	// vanish makes the next n reads of a name answer 404 although the
	// object is there, which is what a listing followed by a delete looks
	// like from the reader's side.
	vanish map[string]int

	// bucketAttrs is what buckets.get answers, as the JSON API's bucket
	// resource.
	bucketAttrs map[string]any

	// lastUploadQuery is the query of the most recent upload, so a test can
	// see the precondition that went on the wire.
	lastUploadQuery url.Values
}

func newFakeGCSServer(t *testing.T) (*httptest.Server, *fakeGCSServer) {
	t.Helper()
	f := &fakeGCSServer{bucket: "test-bucket", objects: map[string]*fakeGCSObject{}, gen: 1700000000000000, vanish: map[string]int{}}
	server := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(server.Close)
	return server, f
}

func gcsWriteError(w http.ResponseWriter, code int, reason, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
		"code": code, "message": message,
		"errors": []map[string]any{{"reason": reason, "message": message, "domain": "global"}},
	}})
}

func (f *fakeGCSServer) objectResource(name string, o *fakeGCSObject) map[string]any {
	return map[string]any{
		"kind": "storage#object", "bucket": f.bucket, "name": name,
		"generation": strconv.FormatInt(o.gen, 10), "metageneration": "1",
		"size": strconv.Itoa(len(o.body)), "metadata": o.metadata,
		// The client checks an upload's CRC32C against the one the server
		// answers, so the fake answers the real one.
		"crc32c": gcsCRC32C(o.body),
	}
}

func gcsCRC32C(b []byte) string {
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], crc32.Checksum(b, crc32.MakeTable(crc32.Castagnoli)))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func (f *fakeGCSServer) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	path := r.URL.EscapedPath()
	i := strings.Index(path, "/b/")
	if i < 0 {
		gcsWriteError(w, http.StatusBadRequest, "invalid", "unrecognised path "+path)
		return
	}
	rest := strings.Split(path[i+len("/b/"):], "/")
	bucket, _ := url.PathUnescape(rest[0])

	if f.forbidden {
		gcsWriteError(w, http.StatusForbidden, "forbidden", "caller does not have storage permission")
		return
	}
	if f.missingBucket || bucket != f.bucket {
		gcsWriteError(w, http.StatusNotFound, "notFound", "The specified bucket does not exist.")
		return
	}

	q := r.URL.Query()
	switch {
	case len(rest) == 1 && r.Method == http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		attrs := map[string]any{"kind": "storage#bucket", "name": f.bucket}
		for k, v := range f.bucketAttrs {
			attrs[k] = v
		}
		_ = json.NewEncoder(w).Encode(attrs)

	case len(rest) == 2 && rest[1] == "o" && r.Method == http.MethodPost:
		if f.kmsStatus != 0 {
			gcsWriteError(w, f.kmsStatus, "forbidden", f.kmsMessage)
			return
		}
		f.upload(w, r, q)

	case len(rest) == 2 && rest[1] == "o" && r.Method == http.MethodGet:
		if f.listForbidden {
			gcsWriteError(w, http.StatusForbidden, "forbidden", "caller does not have storage.objects.list access")
			return
		}
		f.list(w, q)

	case len(rest) >= 3 && rest[1] == "o":
		name, _ := url.PathUnescape(strings.Join(rest[2:], "/"))
		o, ok := f.objects[name]
		switch r.Method {
		case http.MethodGet:
			if f.kmsStatus != 0 && ok {
				gcsWriteError(w, f.kmsStatus, "forbidden", f.kmsMessage)
				return
			}
			if n := f.vanish[name]; n > 0 {
				f.vanish[name] = n - 1
				ok = false
			}
			if !ok {
				gcsWriteError(w, http.StatusNotFound, "notFound", "No such object: "+f.bucket+"/"+name)
				return
			}
			if q.Get("alt") != "media" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(f.objectResource(name, o))
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("X-Goog-Generation", strconv.FormatInt(o.gen, 10))
			w.Header().Set("X-Goog-Metageneration", "1")
			w.Header().Set("Content-Length", strconv.Itoa(len(o.body)))
			_, _ = w.Write(o.body)
		case http.MethodDelete:
			if !f.preconditionHolds(q, o, ok) {
				gcsWriteError(w, http.StatusPreconditionFailed, "conditionNotMet", "At least one of the pre-conditions you specified did not hold.")
				return
			}
			if !ok {
				gcsWriteError(w, http.StatusNotFound, "notFound", "No such object: "+f.bucket+"/"+name)
				return
			}
			delete(f.objects, name)
			w.WriteHeader(http.StatusNoContent)
		default:
			gcsWriteError(w, http.StatusMethodNotAllowed, "invalid", r.Method)
		}

	default:
		gcsWriteError(w, http.StatusBadRequest, "invalid", "unrecognised request "+r.Method+" "+path)
	}
}

// preconditionHolds is GCS's ifGenerationMatch: absent means no condition,
// 0 means no live object, anything else the live generation.
func (f *fakeGCSServer) preconditionHolds(q url.Values, o *fakeGCSObject, exists bool) bool {
	raw := q.Get("ifGenerationMatch")
	if raw == "" {
		return true
	}
	want, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return false
	}
	if want == 0 {
		return !exists
	}
	return exists && o.gen == want
}

func (f *fakeGCSServer) upload(w http.ResponseWriter, r *http.Request, q url.Values) {
	f.lastUploadQuery = q
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		gcsWriteError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("want a multipart upload, got %q", r.Header.Get("Content-Type")))
		return
	}
	mr := multipart.NewReader(r.Body, params["boundary"])
	metaPart, err := mr.NextPart()
	if err != nil {
		gcsWriteError(w, http.StatusBadRequest, "invalid", "no metadata part")
		return
	}
	var meta struct {
		Name     string            `json:"name"`
		Metadata map[string]string `json:"metadata"`
	}
	if err := json.NewDecoder(metaPart).Decode(&meta); err != nil {
		gcsWriteError(w, http.StatusBadRequest, "invalid", "metadata part: "+err.Error())
		return
	}
	mediaPart, err := mr.NextPart()
	if err != nil {
		gcsWriteError(w, http.StatusBadRequest, "invalid", "no media part")
		return
	}
	body, _ := io.ReadAll(mediaPart)
	if meta.Name == "" {
		meta.Name = q.Get("name")
	}
	o, exists := f.objects[meta.Name]
	if !f.preconditionHolds(q, o, exists) {
		gcsWriteError(w, http.StatusPreconditionFailed, "conditionNotMet", "At least one of the pre-conditions you specified did not hold.")
		return
	}
	f.gen++
	nobj := &fakeGCSObject{body: body, gen: f.gen, metadata: meta.Metadata}
	f.objects[meta.Name] = nobj
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(f.objectResource(meta.Name, nobj))
}

func (f *fakeGCSServer) list(w http.ResponseWriter, q url.Values) {
	prefix := q.Get("prefix")
	// Only the one glob shape the contract sends: "**/<name>".
	globTail, hasGlob := strings.CutPrefix(q.Get("matchGlob"), "**/")
	var names []string
	for name := range f.objects {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if hasGlob && !strings.HasSuffix(name, "/"+globTail) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	start := 0
	if tok := q.Get("pageToken"); tok != "" {
		start, _ = strconv.Atoi(tok)
	}
	end := len(names)
	next := ""
	if f.pageSize > 0 && start+f.pageSize < end {
		end = start + f.pageSize
		next = strconv.Itoa(end)
	}
	items := []map[string]any{}
	for _, name := range names[start:end] {
		items = append(items, f.objectResource(name, f.objects[name]))
	}
	resp := map[string]any{"kind": "storage#objects", "items": items}
	if next != "" {
		resp["nextPageToken"] = next
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// newFakeGCSClient is a storage client that talks to server, reading through
// the JSON API the way the record store's own client does.
func newFakeGCSClient(t *testing.T, server *httptest.Server) *storage.Client {
	t.Helper()
	client, err := storage.NewClient(context.Background(),
		option.WithEndpoint(server.URL+"/storage/v1/"),
		option.WithoutAuthentication(),
		storage.WithJSONReads(),
	)
	if err != nil {
		t.Fatalf("storage.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func newTestGCSStore(t *testing.T, keyPrefix string) (*GCSStore, *fakeGCSServer) {
	t.Helper()
	server, fake := newFakeGCSServer(t)
	store, err := NewGCSStore(GCSConfig{Client: newFakeGCSClient(t, server), Bucket: "test-bucket", KeyPrefix: keyPrefix})
	if err != nil {
		t.Fatalf("NewGCSStore: %v", err)
	}
	return store, fake
}

func TestGCSStoreConformance(t *testing.T) {
	runConformance(t, func(t *testing.T) Store {
		t.Helper()
		store, _ := newTestGCSStore(t, "")
		return store
	})
}

func TestGCSStoreConformanceWithKeyPrefix(t *testing.T) {
	runConformance(t, func(t *testing.T) Store {
		t.Helper()
		store, _ := newTestGCSStore(t, "prefix/for/one/caller")
		return store
	})
}

// TestGCSCreateSendsGenerationZero pins the wire half of create-if-absent:
// the condition is GCS's, ifGenerationMatch=0, not a read first.
func TestGCSCreateSendsGenerationZero(t *testing.T) {
	store, fake := newTestGCSStore(t, "")
	ctx := context.Background()
	if _, err := store.PutIfAbsent(ctx, "a/b", []byte("v1")); err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	if got := fake.lastUploadQuery.Get("ifGenerationMatch"); got != "0" {
		t.Errorf("create sent ifGenerationMatch=%q, want \"0\"", got)
	}

	// A second create meets a live object: 412, and a conflict naming it.
	_, err := store.PutIfAbsent(ctx, "a/b", []byte("v2"))
	var conflict *VersionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("second PutIfAbsent err = %v, want a *VersionConflictError", err)
	}
	if conflict.ExpectedVersion != "" || conflict.ActualVersion == "" {
		t.Errorf("conflict = %+v, want expected \"\" and the live generation", conflict)
	}
}

// TestGCSUpdateWithStaleGenerationConflicts: an update carries the
// generation read, and a writer that read an older one is refused by GCS,
// not overwritten.
func TestGCSUpdateWithStaleGenerationConflicts(t *testing.T) {
	store, fake := newTestGCSStore(t, "")
	ctx := context.Background()
	v1, err := store.PutIfAbsent(ctx, "k", []byte("one"))
	if err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	v2, err := store.PutIfVersion(ctx, "k", []byte("two"), v1)
	if err != nil {
		t.Fatalf("PutIfVersion(v1): %v", err)
	}
	if got := fake.lastUploadQuery.Get("ifGenerationMatch"); got != v1 {
		t.Errorf("update sent ifGenerationMatch=%q, want the generation read, %q", got, v1)
	}
	_, err = store.PutIfVersion(ctx, "k", []byte("stale"), v1)
	var conflict *VersionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("stale PutIfVersion err = %v, want a *VersionConflictError", err)
	}
	if conflict.ExpectedVersion != v1 || conflict.ActualVersion != v2 {
		t.Errorf("conflict = %+v, want expected %q and actual %q", conflict, v1, v2)
	}
	payload, _, _, _ := store.Get(ctx, "k")
	if string(payload) != "two" {
		t.Errorf("after the refused write the record reads %q, want \"two\"", payload)
	}
}

// TestGCSDeleteCarriesItsPrecondition: a delete with a stale generation
// leaves the record where it was.
func TestGCSDeleteCarriesItsPrecondition(t *testing.T) {
	store, _ := newTestGCSStore(t, "")
	ctx := context.Background()
	v1, _ := store.PutIfAbsent(ctx, "k", []byte("one"))
	v2, _ := store.PutIfVersion(ctx, "k", []byte("two"), v1)

	err := store.Delete(ctx, "k", v1)
	var conflict *VersionConflictError
	if !errors.As(err, &conflict) || conflict.ActualVersion != v2 {
		t.Fatalf("Delete(stale) err = %v, want a conflict naming %q", err, v2)
	}
	if _, _, exists, _ := store.Get(ctx, "k"); !exists {
		t.Fatal("a delete refused by its precondition removed the record")
	}
	if err := store.Delete(ctx, "k", v2); err != nil {
		t.Fatalf("Delete(current): %v", err)
	}
	if _, _, exists, _ := store.Get(ctx, "k"); exists {
		t.Fatal("the record is still there after a delete with the current generation")
	}
}

// TestGCSWritesCarryTheEstateAsMetadata: every object is written with the
// estate's tag and the caller's per-write tags, and BaseTags wins on a
// shared key.
func TestGCSWritesCarryTheEstateAsMetadata(t *testing.T) {
	server, fake := newFakeGCSServer(t)
	store, err := NewGCSStore(GCSConfig{Client: newFakeGCSClient(t, server), Bucket: "test-bucket", BaseTags: map[string]string{"tofu-estate": "prod"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithObjectTags(context.Background(), map[string]string{"tofu-estate": "other", "tofu-address": "aws_s3_bucket.a"})
	if _, err := store.PutIfAbsent(ctx, "k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	md := fake.objects["k"].metadata
	if md["tofu-estate"] != "prod" || md["tofu-address"] != "aws_s3_bucket.a" {
		t.Errorf("object metadata = %v, want tofu-estate=prod and the address", md)
	}
}

// TestGCSMissingBucketIsNotAnEmptyEstate: the client reports both a missing
// object and a missing bucket as a 404, and only the second is an error.
func TestGCSMissingBucketIsNotAnEmptyEstate(t *testing.T) {
	store, fake := newTestGCSStore(t, "")
	fake.missingBucket = true
	ctx := context.Background()
	if _, _, exists, err := store.Get(ctx, "k"); err == nil || exists {
		t.Errorf("Get on a missing bucket = exists %v, err %v; want an error, never an absent record", exists, err)
	}
	if _, err := store.List(ctx, "tofu-records/prod/"); err == nil {
		t.Error("List on a missing bucket returned no error, which reads as an empty estate")
	}
}

// TestGCSSentinelIsProvedThroughTheListing: the sentinel a store writes on
// opening is read back through the same listing a plan uses.
func TestGCSSentinelIsProvedThroughTheListing(t *testing.T) {
	store, _ := newTestGCSStore(t, "")
	ctx := context.Background()
	const sentinel = "tofu-records/prod/.store-sentinel"
	if _, err := store.PutIfAbsent(ctx, sentinel, []byte("sentinel")); err != nil {
		t.Fatal(err)
	}
	keys, err := store.List(ctx, NamespacePrefix("tofu-records/prod"))
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != sentinel {
		t.Errorf("List = %v, want just the sentinel", keys)
	}
	// A neighbour whose name only starts the same way is not listed.
	if _, err := store.PutIfAbsent(ctx, "tofu-records/prod-eu/.store-sentinel", []byte("s")); err != nil {
		t.Fatal(err)
	}
	keys, _ = store.List(ctx, NamespacePrefix("tofu-records/prod"))
	if len(keys) != 1 {
		t.Errorf("List(tofu-records/prod/) = %v, want prod-eu's sentinel left out", keys)
	}
}

// TestGCSGetAllIsCompleteOrFails covers [GCSStore.GetAll]: it pages through
// the listing, believes a 404 only when it repeats, and fails rather than
// returning a map short of a key the listing named.
func TestGCSGetAllIsCompleteOrFails(t *testing.T) {
	store, fake := newTestGCSStore(t, "")
	fake.pageSize = 2
	ctx := context.Background()
	for _, k := range []string{"ns/a", "ns/b", "ns/c", "ns/d", "ns/e"} {
		if _, err := store.PutIfAbsent(ctx, k, []byte("v-"+k)); err != nil {
			t.Fatal(err)
		}
	}

	all, err := store.GetAll(ctx, "ns/")
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(all) != 5 || string(all["ns/e"].Payload) != "v-ns/e" || all["ns/e"].Version == "" {
		t.Errorf("GetAll = %v, want all five with payloads and versions", all)
	}

	// One spurious 404: the second read finds it, and the map is whole.
	fake.vanish["ns/c"] = 1
	all, err = store.GetAll(ctx, "ns/")
	if err != nil || len(all) != 5 {
		t.Errorf("GetAll after one spurious 404 = %d records, err %v; want all five", len(all), err)
	}

	// Two: the listing and the reads disagree, and the read fails.
	fake.vanish["ns/c"] = 2
	all, err = store.GetAll(ctx, "ns/")
	if err == nil {
		t.Fatalf("GetAll returned %d records and no error for a key the listing named and two reads could not find", len(all))
	}
	if !strings.Contains(err.Error(), "ns/c") {
		t.Errorf("GetAll err = %v, want it to name ns/c", err)
	}

	// Any other failure fails the whole read.
	fake.forbidden = true
	if _, err := store.GetAll(ctx, "ns/"); err == nil {
		t.Error("GetAll returned no error with every request forbidden")
	}
}

// TestGCSForbiddenIsAccessDenied: #1370's reader tolerance reaches this
// store, because a 403 from the JSON API is what IsAccessDenied recognises.
func TestGCSForbiddenIsAccessDenied(t *testing.T) {
	store, fake := newTestGCSStore(t, "")
	fake.forbidden = true
	_, err := store.PutIfAbsent(context.Background(), "k", []byte("v"))
	if !IsAccessDenied(err) {
		t.Errorf("IsAccessDenied(%v) = false, want true for a 403", err)
	}
}

// TestGCSKMSRefusalIsNamedAndNeverReadOnly: a Cloud KMS refusal on a write or
// a read is a *GCSKMSError naming the key, and IsAccessDenied is false for
// it, so #1370's reader tolerance never treats a store nobody can decrypt as
// a store this identity may only read.
func TestGCSKMSRefusalIsNamedAndNeverReadOnly(t *testing.T) {
	const key = "projects/acme/locations/us/keyRings/records/cryptoKeys/prod"
	for _, tc := range []struct {
		name     string
		status   int
		message  string
		unusable bool
	}{
		{"service agent denied", http.StatusForbidden, "Permission 'cloudkms.cryptoKeyVersions.useToEncrypt' denied on resource '" + key + "' (or it may not exist).", false},
		{"key disabled", http.StatusBadRequest, "The Cloud KMS key " + key + " is disabled.", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, fake := newTestGCSStore(t, "")
			ctx := context.Background()
			if _, err := store.PutIfAbsent(ctx, "existing", []byte("v")); err != nil {
				t.Fatal(err)
			}
			fake.kmsStatus, fake.kmsMessage = tc.status, tc.message

			_, werr := store.PutIfAbsent(ctx, "k", []byte("v"))
			_, _, _, rerr := store.Get(ctx, "existing")
			for op, err := range map[string]error{"write": werr, "read": rerr} {
				var kms *GCSKMSError
				if !errors.As(err, &kms) {
					t.Fatalf("%s err = %v, want a *GCSKMSError", op, err)
				}
				if kms.Key != key || kms.Unusable != tc.unusable {
					t.Errorf("%s: key %q unusable %v; want %q and %v", op, kms.Key, kms.Unusable, key, tc.unusable)
				}
				if IsAccessDenied(err) {
					t.Errorf("%s: IsAccessDenied is true for a KMS refusal", op)
				}
			}
		})
	}
}
