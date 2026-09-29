// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// The first slice of the adversarial audit GitHub issue #1442 asks for on
// this store: the cases #1383 found on S3, restated for a namespace. Section
// A of #1448 measured the READ half (kubernetes_absent_live_test.go: a Get,
// a List, a GetAll and an unconditional Delete in a missing namespace each
// refuse by name). These are the two halves it did not:
//
//   - the WRITE half of #1383's missing bucket. There, an update or a delete
//     against a bucket that was not there answered a version CONFLICT
//     ("another writer changed it ... the store now holds (no record)"),
//     which the #1287 unwritten-record ledger skips on purpose. Here the
//     API server answers an update or a conditional delete of a Secret in a
//     missing namespace with `404 secrets "x" not found`, measured on kind
//     v1.36.1, which is the same 404 a missing record gets; only a create
//     names the namespace. So the update and delete paths can tell the two
//     apart only through [KubernetesStore.conflictError]'s re-read and its
//     namespace probe, and this is the test that they do.
//   - claim 31's continue token. A paged LIST whose token has outlived the
//     API server's watch cache answers 410 Gone with reason Expired on the
//     page after, and a store that returned the pages it had would hand a
//     plan a short listing with a nil error. kubernetes_survivors_test.go
//     pins this over a SecretInterface double; this one goes through
//     client-go's real transport and decoding, against a real first page,
//     with the 410 put on the wire by a RoundTripper the way the race test
//     parks requests, because kind cannot be made to expire a token on cue.

// TestKubernetesRefusesAMissingNamespaceOnEveryWrite is the write half of
// #1383's missing bucket. A create, a conditional update and a conditional
// delete into a namespace that does not exist must each answer a
// *NamespaceMissingError naming the namespace, and none of them may answer a
// *VersionConflictError: a conflict says another writer moved the record,
// and nothing moved anything.
//
// The store carries the clientset here, as every store internal/live/projection
// opens does, so the re-read behind a refused update can ask the API server
// about the namespace. Under an identity that may not get namespaces the
// probe answers nothing, and what that costs is measured and stated in
// [TestKubernetesAMissingNamespaceUnderAScopedIdentityIsStillNotAConflict].
func TestKubernetesRefusesAMissingNamespaceOnEveryWrite(t *testing.T) {
	_, clientset, _ := liveKubernetesSecrets(t)
	missing := "tofu-records-never-created-" + randomKeySegment(t)
	store := liveStore(t, clientset.CoreV1().Secrets(missing), clientset, missing, "", "alice")
	ctx := context.Background()
	const key = "tofu-records/alice/aws_thing/one"
	// A version no object holds. A store that read the missing namespace as
	// a missing record would answer "expected 1, the store now holds (no
	// record)", which is #1383's shape exactly.
	const staleVersion = "1"

	for _, tc := range []struct {
		name string
		do   func() (string, error)
	}{
		{"PutIfAbsent", func() (string, error) { return store.PutIfAbsent(ctx, key, []byte("v")) }},
		{"PutIfVersion", func() (string, error) { return store.PutIfVersion(ctx, key, []byte("v"), staleVersion) }},
		{"Delete with a version", func() (string, error) { return "", store.Delete(ctx, key, staleVersion) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			version, err := tc.do()
			var conflict *VersionConflictError
			if errors.As(err, &conflict) {
				t.Fatalf("%s into a missing namespace answered a version conflict, which is #1383's missing bucket on this store: %v", tc.name, err)
			}
			var absent *NamespaceMissingError
			if !errors.As(err, &absent) {
				t.Fatalf("%s into a missing namespace: version=%q err=%v (%T), want *NamespaceMissingError", tc.name, version, err, err)
			}
			if absent.Namespace != missing {
				t.Errorf("the refusal names namespace %q, want %q", absent.Namespace, missing)
			}
			if !strings.Contains(err.Error(), missing) {
				t.Errorf("the error's text does not name the namespace %q: %v", missing, err)
			}
			if version != "" {
				t.Errorf("%s returned version %q beside its error; a refused write has no version", tc.name, version)
			}
		})
	}
}

// TestKubernetesAMissingNamespaceUnderAScopedIdentityIsStillNotAConflict is
// the same three writes from a store that CANNOT ask about the namespace,
// which is what the Role the docs recommend gives a CI job: Secrets in one
// namespace and no cluster-scoped get on namespaces. It is built the way
// [KubernetesStore.namespaceFault] behaves after one Forbidden: with no
// clientset at all, so the probe is never made.
//
// The create still refuses by name, because the API server's own answer to a
// create names the namespace whatever the identity. The update and the
// delete cannot: their 404 names the Secret, and the re-read that would tell
// the two apart may not ask. What this pins is that the answer is a version
// conflict against NO record - the conflict's ActualVersion is "" - which a
// caller can read as "the record this version belonged to is gone", and not
// a conflict against some version that never existed. The remedy for the
// window this leaves (a namespace deleted mid-run, under a scoped identity)
// is one layer up: the sentinel handshake on open, whose create names the
// namespace for any identity, and the namespace's Terminating phase, which
// refuses every create for as long as the delete takes.
func TestKubernetesAMissingNamespaceUnderAScopedIdentityIsStillNotAConflict(t *testing.T) {
	_, clientset, _ := liveKubernetesSecrets(t)
	missing := "tofu-records-never-created-" + randomKeySegment(t)
	store, err := NewKubernetesStore(KubernetesConfig{
		Secrets:   clientset.CoreV1().Secrets(missing),
		Namespace: missing,
		Estate:    "alice",
	})
	if err != nil {
		t.Fatalf("NewKubernetesStore: %v", err)
	}
	ctx := context.Background()
	const key = "tofu-records/alice/aws_thing/one"
	const staleVersion = "1"

	_, err = store.PutIfAbsent(ctx, key, []byte("v"))
	var absent *NamespaceMissingError
	if !errors.As(err, &absent) {
		t.Errorf("PutIfAbsent into a missing namespace with no way to ask about it: %v (%T), want *NamespaceMissingError, because the API server's answer to a create names the namespace for any identity", err, err)
	}

	for _, tc := range []struct {
		name string
		do   func() error
	}{
		{"PutIfVersion", func() error { _, err := store.PutIfVersion(ctx, key, []byte("v"), staleVersion); return err }},
		{"Delete with a version", func() error { return store.Delete(ctx, key, staleVersion) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.do()
			if err == nil {
				t.Fatalf("%s into a missing namespace succeeded", tc.name)
			}
			var conflict *VersionConflictError
			if !errors.As(err, &conflict) {
				// A refusal naming the namespace would be better still, and
				// is not something this identity can be given; if the store
				// found a way, this case should be folded into the one
				// above rather than left asserting the weaker answer.
				t.Fatalf("%s into a missing namespace from a store that cannot ask about it: %v (%T); the measured answer is a *VersionConflictError against no record, and this is something else", tc.name, err, err)
			}
			if conflict.ActualVersion != "" {
				t.Errorf("the conflict says the store holds version %q; nothing is there, and a version that never existed would send the caller re-reading a record that cannot be read", conflict.ActualVersion)
			}
			if conflict.ExpectedVersion != staleVersion {
				t.Errorf("the conflict names expected version %q, want %q", conflict.ExpectedVersion, staleVersion)
			}
		})
	}
}

// expiredPageTransport puts the API server's own 410 on the wire for every
// LIST of Secrets in one namespace that carries a continue token, and passes
// everything else through untouched. The first page is the real API server's
// and really carries a token; only the page after is answered here, with the
// exact Status a kube-apiserver sends when a token has outlived its watch
// cache (reason Expired, code 410).
type expiredPageTransport struct {
	next      http.RoundTripper
	namespace string
	pages     atomic.Int32
	expired   atomic.Int32
}

func (t *expiredPageTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodGet && req.URL.Path == "/api/v1/namespaces/"+t.namespace+"/secrets" {
		t.pages.Add(1)
		if req.URL.Query().Get("continue") != "" {
			t.expired.Add(1)
			status := metav1.Status{
				TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
				Status:   metav1.StatusFailure,
				Message:  "The provided continue parameter is too old to display a consistent list result. If the list result is not expected to change within the watch cache, use RV=0 or set resourceVersionMatch to Exact. Otherwise, either re-list from the start, or use a newer continue parameter.",
				Reason:   metav1.StatusReasonExpired,
				Code:     http.StatusGone,
			}
			body, err := json.Marshal(status)
			if err != nil {
				return nil, err
			}
			return &http.Response{
				Status:        "410 Gone",
				StatusCode:    http.StatusGone,
				Header:        http.Header{"Content-Type": []string{"application/json"}},
				Body:          io.NopCloser(bytes.NewReader(body)),
				ContentLength: int64(len(body)),
				Request:       req,
			}, nil
		}
	}
	return t.next.RoundTrip(req)
}

// TestKubernetesAnExpiredContinueTokenFailsTheListingOnARealAPIServer is
// claim 31 on this store, over the wire. Seven records and a page size of
// two, so the first page is real and short by five; the second is answered
// 410 Expired. List and GetAll must each fail and return nothing, and the
// failure must be an ordinary error that says what was being done, not a
// namespace refusal (the namespace is there) and not a short result.
func TestKubernetesAnExpiredContinueTokenFailsTheListingOnARealAPIServer(t *testing.T) {
	plainSecrets, _, ns := liveKubernetesSecrets(t)
	prefix := "expired-" + randomKeySegment(t)
	t.Cleanup(func() { deleteEveryRecordUnder(t, plainSecrets, prefix) })

	// The records are written through an unwrapped client, so nothing about
	// the setup goes through the transport under test.
	seed, err := NewKubernetesStore(KubernetesConfig{Secrets: plainSecrets, Namespace: ns, KeyPrefix: prefix, Estate: "conformance"})
	if err != nil {
		t.Fatalf("NewKubernetesStore: %v", err)
	}
	ctx := context.Background()
	const listPrefix = "tofu-records/conformance/"
	const records = 7
	for i := 0; i < records; i++ {
		key := fmt.Sprintf("%saws_thing/rec-%02d", listPrefix, i)
		if _, err := seed.PutIfAbsent(ctx, key, []byte(fmt.Sprintf("payload-%02d", i))); err != nil {
			t.Fatalf("PutIfAbsent(%q): %v", key, err)
		}
	}

	cfg, err := clientcmd.BuildConfigFromFlags("", strings.TrimSpace(os.Getenv(kubeconfigEnvVar)))
	if err != nil {
		t.Fatalf("reading the kubeconfig: %v", err)
	}
	wire := &expiredPageTransport{namespace: ns}
	cfg.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		wire.next = rt
		return wire
	})
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("building the wrapped client: %v", err)
	}
	store, err := NewKubernetesStore(KubernetesConfig{
		Secrets:      client.CoreV1().Secrets(ns),
		Clientset:    client,
		Namespace:    ns,
		KeyPrefix:    prefix,
		Estate:       "conformance",
		ListPageSize: 2,
	})
	if err != nil {
		t.Fatalf("NewKubernetesStore over the wrapped client: %v", err)
	}

	// The control first, so a failure below is the 410's doing and not the
	// wrapped client's: with no token expired yet, the same store reads
	// every record. The pages counter proves the client really paged.
	if got, err := seed.List(ctx, listPrefix); err != nil || len(got) != records {
		t.Fatalf("the unwrapped store lists %d records (err=%v), want %d; the case below would measure nothing", len(got), err, records)
	}

	keys, err := store.List(ctx, listPrefix)
	if err == nil {
		t.Fatalf("List over a listing whose second page answered 410 Expired returned %d of %d keys and a nil error; a short listing a plan cannot tell from the truth is what this store must never return", len(keys), records)
	}
	if keys != nil {
		t.Errorf("List returned %v beside its error; nothing may be returned beside a refusal", keys)
	}
	assertOrdinaryListingError(t, "List", err, ns)

	all, err := store.GetAll(ctx, listPrefix)
	if err == nil {
		t.Fatalf("GetAll over a listing whose second page answered 410 Expired returned %d of %d records and a nil error", len(all), records)
	}
	if all != nil {
		t.Errorf("GetAll returned %d records beside its error; a partial snapshot is not a snapshot", len(all))
	}
	assertOrdinaryListingError(t, "GetAll", err, ns)

	// The measurement is only worth something if the first page was the
	// API server's and the second was the 410: at least two LIST requests
	// per call, exactly one of which carried a token.
	if pages, expired := wire.pages.Load(), wire.expired.Load(); pages < 4 || expired != 2 {
		t.Errorf("the wire saw %d LIST request(s) of which %d carried a continue token, want at least 4 and exactly 2 (one real first page and one expired second page per call); the store did not page the way this test assumes", pages, expired)
	}
}

// assertOrdinaryListingError holds a 410 to being reported as what it is: a
// listing that failed, in this namespace, with the API server's reason
// carried. Not a namespace refusal - the namespace is there and the
// refusal would send an operator creating it - and not a #1370 denial.
func assertOrdinaryListingError(t *testing.T, call string, err error, ns string) {
	t.Helper()
	var absent *NamespaceMissingError
	var terminating *NamespaceTerminatingError
	switch {
	case errors.As(err, &absent):
		t.Errorf("%s reported the namespace missing (%v); it is there, and the operator would be sent to create it", call, err)
	case errors.As(err, &terminating):
		t.Errorf("%s reported the namespace terminating (%v); it is not", call, err)
	case IsAccessDenied(err):
		t.Errorf("%s reported an access denial (%v); #1370's reader tolerance would carry a run past this", call, err)
	}
	// The API server's own sentence rides along, so the operator reads why
	// the listing stopped rather than only that it did.
	text := err.Error()
	for _, want := range []string{"listing", ns, "continue parameter is too old"} {
		if !strings.Contains(text, want) {
			t.Errorf("%s's error does not carry %q, so it does not say what failed or why: %s", call, want, text)
		}
	}
}
