// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

// GitHub issue #1355's remaining gap, stated on #1466 and again in the
// 2026-09-30 reproduction report: a bulk read that returns a map missing a
// key WITH NO ERROR is invisible to the listing-versus-read cross-check
// (#1429), because [RunCache.List] is answered from that same map. The two
// readings agree because they are one reading.
//
// So the only defence is each [BulkReader]'s own completeness, and this file
// is that defence measured per store, one backend shape at a time. Every
// case runs the same check, [assertNoShortSnapshot]: for every key the
// store's own per-key Get would serve (or would refuse), a [RunCache] over
// that store must not answer "no record" with no error. That is the exact
// failure #1355 printed - an instance missing from prior state under a
// success line - and it is checked through the cache rather than against
// GetAll's map alone, because the cache is what turns a short map into the
// run's settled answer.
//
// Every BulkReader in the tree is here: [LocalStore], [S3Store] and
// [KubernetesStore]. [RunCache] and [CountingStore] forward GetAll to the
// store beneath and add no shape of their own. There is no Parameter Store
// backend any more (#1346).

// assertNoShortSnapshot fails t for every key the store answers for, per key,
// that a fresh RunCache over the store answers "no record" for with no error.
//
// "Answers for" includes an error: a Get that refuses a key and a snapshot
// that silently omits it are not the same answer, and the second is the one
// that drops an instance. A key Get also calls absent is agreement and is
// skipped.
func assertNoShortSnapshot(t *testing.T, store Store, prefix string, keys []string) {
	t.Helper()
	ResetRunCacheForTest(t)
	ctx := context.Background()
	cache := NewRunCache(store, prefix)
	var short []string
	for _, key := range keys {
		_, _, exists, err := store.Get(ctx, key)
		if !exists && err == nil {
			continue
		}
		_, _, cachedExists, cachedErr := cache.Get(ctx, key)
		if !cachedExists && cachedErr == nil {
			why := "serves it"
			if err != nil {
				why = "refuses it: " + err.Error()
			}
			short = append(short, fmt.Sprintf("%s (per-key Get %s)", key, why))
		}
	}
	if len(short) > 0 {
		t.Errorf("a RunCache over this store answered \"no record\" with no error for %d key(s) the store itself answers for, so the bulk read came back short and said nothing; on `apply -destroy` that is one fewer instance destroyed under a success line (GitHub issue #1355):\n  %s",
			len(short), strings.Join(short, "\n  "))
	}
}

// assertCompleteOrFailed is the direct half: GetAll either returns every key
// in want, or an error and no map.
func assertCompleteOrFailed(t *testing.T, got map[string]Record, err error, want []string) {
	t.Helper()
	if err != nil {
		if got != nil {
			t.Errorf("GetAll returned an error AND a map of %d records: %v", len(got), err)
		}
		return
	}
	var missing []string
	for _, key := range want {
		if _, ok := got[key]; !ok {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		t.Errorf("GetAll returned %d records and no error, missing %v", len(got), missing)
	}
}

// ---------------------------------------------------------------- local

const bulkNS = "tofu-records/prod/"

func localStoreForBulk(t *testing.T) (*LocalStore, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewLocalStore(dir)
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	return store, dir
}

// TestLocalBulkReadIsNeverShort runs every shape the local store's walk and
// read can disagree in. The ones that come back as an error were already
// errors; the ones that come back short are keys the store accepted a write
// for and then left out of its own listing.
func TestLocalBulkReadIsNeverShort(t *testing.T) {
	ctx := context.Background()
	ordinary := []string{bulkNS + "terraform_data/a", bulkNS + "terraform_data/b"}

	t.Run("undisturbed", func(t *testing.T) {
		store, _ := localStoreForBulk(t)
		for _, key := range ordinary {
			mustPutLocal(t, store, key)
		}
		got, err := store.GetAll(ctx, bulkNS)
		if err != nil {
			t.Fatalf("an undisturbed bulk read failed: %v", err)
		}
		assertCompleteOrFailed(t, got, err, ordinary)
		assertNoShortSnapshot(t, store, bulkNS, ordinary)
	})

	// The walk lists an entry the read cannot open. Already an error
	// (#1429); pinned here beside the others.
	t.Run("entry vanishes between walk and read", func(t *testing.T) {
		store, dir := localStoreForBulk(t)
		for _, key := range ordinary {
			mustPutLocal(t, store, key)
		}
		path := filepath.Join(dir, filepath.FromSlash(ordinary[1]))
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(dir, "no-such-file"), path); err != nil {
			t.Skipf("cannot stage a dangling symlink here: %v", err)
		}
		got, err := store.GetAll(ctx, bulkNS)
		if err == nil {
			t.Fatalf("GetAll returned %d records and no error over an entry it could not read", len(got))
		}
		assertNoShortSnapshot(t, store, bulkNS, ordinary)
	})

	// A record file the process cannot read. Already an error.
	t.Run("unreadable record file", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("file modes do not deny a read here")
		}
		store, dir := localStoreForBulk(t)
		for _, key := range ordinary {
			mustPutLocal(t, store, key)
		}
		path := filepath.Join(dir, filepath.FromSlash(ordinary[0]))
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
		got, err := store.GetAll(ctx, bulkNS)
		if err == nil {
			t.Fatalf("GetAll returned %d records and no error over a file it could not read", len(got))
		}
		assertNoShortSnapshot(t, store, bulkNS, ordinary)
	})

	// A directory the walk cannot descend. Already an error: WalkDir hands
	// the ReadDir failure to the callback, which returns it.
	t.Run("unreadable directory", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("file modes do not deny a read here")
		}
		store, dir := localStoreForBulk(t)
		hidden := bulkNS + "aws_thing/x"
		for _, key := range append(ordinary, hidden) {
			mustPutLocal(t, store, key)
		}
		sub := filepath.Join(dir, filepath.FromSlash(bulkNS+"aws_thing"))
		if err := os.Chmod(sub, 0o100); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(sub, 0o700) })
		got, err := store.GetAll(ctx, bulkNS)
		if err == nil {
			t.Fatalf("GetAll returned %d records and no error over a directory it could not list", len(got))
		}
		assertNoShortSnapshot(t, store, bulkNS, append(ordinary, hidden))
	})

	// A symlinked directory: WalkDir does not follow it, and a read through
	// it is a read of a directory. Already an error.
	t.Run("symlinked directory", func(t *testing.T) {
		store, dir := localStoreForBulk(t)
		for _, key := range ordinary {
			mustPutLocal(t, store, key)
		}
		elsewhere := t.TempDir()
		if err := os.WriteFile(filepath.Join(elsewhere, "x"), []byte("v"), 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, filepath.FromSlash(bulkNS+"linked"))
		if err := os.Symlink(elsewhere, link); err != nil {
			t.Skipf("cannot make a symlink here: %v", err)
		}
		got, err := store.GetAll(ctx, bulkNS)
		if err == nil {
			assertCompleteOrFailed(t, got, err, append(ordinary, bulkNS+"linked/x"))
		}
		assertNoShortSnapshot(t, store, bulkNS, append(ordinary, bulkNS+"linked/x"))
	})

	// The keys the walk's own exclusions drop. The walk skips a name ending
	// in the lockfile suffix and a name carrying the temp-file infix, which
	// is right for the sidecars the store writes itself and wrong for a
	// record whose key happens to be spelled that way: Get serves it and
	// the listing never names it. And a key that is not a clean path - a
	// doubled slash, a "." segment, a trailing slash - is written at the
	// cleaned path, so the listing names the cleaned key and never the one
	// that was written. Either the write is refused, or the record is in
	// the snapshot.
	for _, key := range []string{
		bulkNS + "terraform_data/x.lock",
		bulkNS + "terraform_data/x.tmp-1",
		bulkNS + "terraform_data//x",
		bulkNS + "terraform_data/./x",
		bulkNS + "terraform_data/x/",
	} {
		t.Run("key "+key, func(t *testing.T) {
			store, _ := localStoreForBulk(t)
			for _, k := range ordinary {
				mustPutLocal(t, store, k)
			}
			if _, err := store.PutIfAbsent(ctx, key, []byte("v")); err != nil {
				// Refused: no record can be under a key the listing
				// cannot name.
				return
			}
			got, err := store.GetAll(ctx, bulkNS)
			assertCompleteOrFailed(t, got, err, append(ordinary, key))
			assertNoShortSnapshot(t, store, bulkNS, append(ordinary, key))
		})
	}
}

func mustPutLocal(t *testing.T, store *LocalStore, key string) {
	t.Helper()
	if _, err := store.PutIfAbsent(context.Background(), key, []byte(`{"key":"`+key+`"}`)); err != nil {
		t.Fatalf("seeding %q: %v", key, err)
	}
}

// ---------------------------------------------------------------- s3

// s3StoreBehind is a store over the package's fake S3 with wrap in front of
// it, so a case can answer a request the fake would answer otherwise.
// Retries are off so an injected failure reaches the store.
func s3StoreBehind(t *testing.T, wrap func(fake *fakeS3Server, next http.Handler) http.Handler) (*S3Store, *fakeS3Server) {
	t.Helper()
	fake := &fakeS3Server{objects: map[string]*fakeS3Object{}}
	var next http.Handler = http.HandlerFunc(fake.handle)
	if wrap != nil {
		next = wrap(fake, next)
	}
	server := httptest.NewServer(next)
	t.Cleanup(server.Close)
	client := s3.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
		Retryer:     func() aws.Retryer { return retry.AddWithMaxAttempts(retry.NewStandard(), 1) },
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(server.URL)
		o.UsePathStyle = true
	})
	store, err := NewS3Store(S3Config{Client: client, Bucket: "test-bucket"})
	if err != nil {
		t.Fatalf("NewS3Store: %v", err)
	}
	return store, fake
}

func isObjectGet(r *http.Request, key string) bool {
	return r.Method == http.MethodGet && r.URL.Query().Get("list-type") != "2" && strings.HasSuffix(r.URL.Path, "/"+key)
}

// TestS3BulkReadIsNeverShort: every shape found complete. The listing
// paginates on a token and refuses a truncated page without one; every GET
// that does not find a listed key is asked again and then fails the read by
// name; every other failure fails it.
func TestS3BulkReadIsNeverShort(t *testing.T) {
	ctx := context.Background()
	keys := []string{bulkNS + "terraform_data/a", bulkNS + "terraform_data/b", bulkNS + "terraform_data/c"}
	victim := keys[1]
	seed := func(t *testing.T, store *S3Store) {
		t.Helper()
		for _, key := range keys {
			if _, err := store.PutIfAbsent(ctx, key, []byte(key)); err != nil {
				t.Fatalf("seeding %q: %v", key, err)
			}
		}
	}
	// getOnly answers a GET of the victim with fn and passes everything
	// else through.
	getOnly := func(fn func(w http.ResponseWriter)) func(*fakeS3Server, http.Handler) http.Handler {
		return func(_ *fakeS3Server, next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if isObjectGet(r, victim) {
					fn(w)
					return
				}
				next.ServeHTTP(w, r)
			})
		}
	}

	cases := []struct {
		name     string
		wrap     func(*fakeS3Server, http.Handler) http.Handler
		pageSize int
		truncate bool
		wantErr  bool
	}{
		{name: "one key per page", pageSize: 1},
		{name: "truncated page with no token", pageSize: 1, truncate: true, wantErr: true},
		{name: "404 NoSuchKey for a listed key, twice", wrap: getOnly(func(w http.ResponseWriter) {
			writeS3Error(w, http.StatusNotFound, "NoSuchKey", "injected")
		}), wantErr: true},
		{name: "404 with no code for a listed key, twice", wrap: getOnly(func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusNotFound)
		}), wantErr: true},
		{name: "404 NoSuchBucket on a GET only", wrap: getOnly(func(w http.ResponseWriter) {
			writeS3Error(w, http.StatusNotFound, "NoSuchBucket", "injected")
		}), wantErr: true},
		{name: "500 on a GET", wrap: getOnly(func(w http.ResponseWriter) {
			writeS3Error(w, http.StatusInternalServerError, "InternalError", "injected")
		}), wantErr: true},
		{name: "body cut short", wrap: getOnly(func(w http.ResponseWriter) {
			w.Header().Set("Content-Length", "100")
			w.Header().Set("ETag", `"x"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("short"))
		}), wantErr: true},
		{name: "deleted between LIST and GET", wrap: func(fake *fakeS3Server, next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if isObjectGet(r, victim) {
					fake.mu.Lock()
					delete(fake.objects, r.URL.Path)
					fake.mu.Unlock()
				}
				next.ServeHTTP(w, r)
			})
		}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, fake := s3StoreBehind(t, tc.wrap)
			seed(t, store)
			fake.pageSize = tc.pageSize
			fake.truncateWithoutToken = tc.truncate
			got, err := store.GetAll(ctx, bulkNS)
			if tc.wantErr && err == nil {
				t.Fatalf("GetAll returned %d records and no error", len(got))
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("GetAll failed: %v", err)
			}
			assertCompleteOrFailed(t, got, err, keys)
			assertNoShortSnapshot(t, store, bulkNS, keys)
		})
	}
}

// ---------------------------------------------------------------- kubernetes

// lastPageSecrets answers a LIST with its first item and NO continue token,
// while saying how many items remain. Kubernetes leaves remainingItemCount
// unset on the last page of a list; a page that names items still to come
// and hands back nothing to ask for them with is a short listing, the
// Kubernetes counterpart of S3's truncated page with no token.
type lastPageSecrets struct {
	corev1client.SecretInterface
}

func (p *lastPageSecrets) List(ctx context.Context, opts metav1.ListOptions) (*corev1.SecretList, error) {
	all, err := p.SecretInterface.List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	sort.Slice(all.Items, func(i, j int) bool { return all.Items[i].Name < all.Items[j].Name })
	out := &corev1.SecretList{}
	if len(all.Items) > 0 {
		out.Items = all.Items[:1]
		remaining := int64(len(all.Items) - 1)
		out.RemainingItemCount = &remaining
	}
	return out, nil
}

// TestKubernetesBulkReadIsNeverShort is the store whose LIST carries every
// payload, so there is no per-key GET to come back short. What can come back
// short is the attribution: a Secret the listing does not count as one of
// this store's records while Get, which finds a record by its NAME, still
// reaches it.
func TestKubernetesBulkReadIsNeverShort(t *testing.T) {
	ctx := context.Background()
	keys := []string{bulkNS + "terraform_data/a", bulkNS + "terraform_data/b", bulkNS + "terraform_data/c"}
	victim := keys[1]

	build := func(t *testing.T, wrap func(corev1client.SecretInterface) corev1client.SecretInterface, pageSize int64) (*KubernetesStore, corev1client.SecretInterface) {
		t.Helper()
		secrets := fakeSecrets(t)
		seeder, err := NewKubernetesStore(KubernetesConfig{Secrets: secrets, Namespace: fakeRecordNamespace, Estate: "prod"})
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range keys {
			if _, err := seeder.PutIfAbsent(ctx, key, []byte(key)); err != nil {
				t.Fatalf("seeding %q: %v", key, err)
			}
		}
		read := secrets
		if wrap != nil {
			read = wrap(secrets)
		}
		store, err := NewKubernetesStore(KubernetesConfig{Secrets: read, Namespace: fakeRecordNamespace, Estate: "prod", ListPageSize: pageSize})
		if err != nil {
			t.Fatal(err)
		}
		return store, secrets
	}
	edit := func(t *testing.T, store *KubernetesStore, secrets corev1client.SecretInterface, fn func(*corev1.Secret)) {
		t.Helper()
		secret, err := secrets.Get(ctx, store.SecretName(victim), metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		fn(secret)
		if _, err := secrets.Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name     string
		wrap     func(corev1client.SecretInterface) corev1client.SecretInterface
		pageSize int64
		mutate   func(*corev1.Secret)
		wantErr  bool
	}{
		{name: "one Secret per page", pageSize: 1, wrap: func(s corev1client.SecretInterface) corev1client.SecretInterface {
			return &pagingSecrets{SecretInterface: s, pageErr: map[int]error{}}
		}},
		{name: "a page that names items to come and no continue token", wantErr: true, wrap: func(s corev1client.SecretInterface) corev1client.SecretInterface {
			return &lastPageSecrets{SecretInterface: s}
		}},
		{name: "payload that will not decompress", wantErr: true, mutate: func(s *corev1.Secret) {
			s.Data[kubernetesPayloadKey] = []byte("not gzip")
		}},
		{name: "payload data key removed", wantErr: true, mutate: func(s *corev1.Secret) {
			delete(s.Data, kubernetesPayloadKey)
		}},
		{name: "labels stripped", wantErr: true, mutate: func(s *corev1.Secret) {
			delete(s.Labels, KubernetesManagedByLabel)
			delete(s.Labels, KubernetesEstateLabel)
		}},
		{name: "key annotation stripped", wantErr: true, mutate: func(s *corev1.Secret) {
			delete(s.Annotations, KubernetesRecordKeyAnnotation)
		}},
		{name: "key annotation stripped along with the labels", wantErr: true, mutate: func(s *corev1.Secret) {
			delete(s.Annotations, KubernetesRecordKeyAnnotation)
			s.Labels = nil
		}},
		{name: "key annotation rewritten to a key outside the listed prefix", wantErr: true, mutate: func(s *corev1.Secret) {
			s.Annotations[KubernetesRecordKeyAnnotation] = "tofu-outputs/prod/elsewhere"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, secrets := build(t, tc.wrap, tc.pageSize)
			if tc.mutate != nil {
				edit(t, store, secrets, tc.mutate)
			}
			got, err := store.GetAll(ctx, bulkNS)
			if tc.wantErr && err == nil {
				t.Errorf("GetAll returned %d records and no error", len(got))
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("GetAll failed: %v", err)
			}
			assertCompleteOrFailed(t, got, err, keys)
			assertNoShortSnapshot(t, store, bulkNS, keys)
		})
	}
}
