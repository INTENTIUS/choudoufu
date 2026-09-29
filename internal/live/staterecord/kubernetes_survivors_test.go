// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

// The mutations that survived. GitHub issue #1448, section F: 28 guards in
// this package were hand-mutated and 8 went unnoticed by every test. This
// file holds the cases for the ones that can be measured without a cluster.
// The pagination case's other half is in kubernetes_survivors_live_test.go,
// because client-go's fake clientset does not paginate (see [pagingSecrets]).

// pagingSecrets is a [corev1client.SecretInterface] that honours Limit and
// Continue, which client-go's fake clientset does not: measured on the fake
// at the version this repository pins, a List carrying Limit 2 over seven
// Secrets returns all seven items and an empty Continue. So a store whose
// page loop stopped after the first page still returned everything against
// the fake, and the mutation that dropped the loop's condition survived every
// test in this package.
//
// It delegates everything else to the fake, so writes, reads and deletes are
// still the fake's. Only the listing is this type's, and only so that there
// is more than one page for the loop to be wrong about.
//
// pageErr fails one page by its zero-based index, which is the other half of
// the same guard: a listing that fails halfway has to fail the whole call.
type pagingSecrets struct {
	corev1client.SecretInterface

	pageErr map[int]error

	// calls counts List calls and limits records the Limit each carried, so
	// a test can say that the store really did ask for pages rather than
	// happening to get them.
	calls  int
	limits []int64
}

func (p *pagingSecrets) List(ctx context.Context, opts metav1.ListOptions) (*corev1.SecretList, error) {
	page := p.calls
	p.calls++
	p.limits = append(p.limits, opts.Limit)
	if err, ok := p.pageErr[page]; ok {
		return nil, err
	}

	all, err := p.SecretInterface.List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	// A real LIST is ordered, and a continue token is only meaningful
	// against a stable order.
	sort.Slice(all.Items, func(i, j int) bool { return all.Items[i].Name < all.Items[j].Name })

	start := 0
	if opts.Continue != "" {
		start, err = strconv.Atoi(opts.Continue)
		if err != nil {
			return nil, fmt.Errorf("pagingSecrets: the store sent a continue token this double did not issue: %q", opts.Continue)
		}
	}
	if start > len(all.Items) {
		start = len(all.Items)
	}
	limit := int(opts.Limit)
	if limit <= 0 {
		limit = len(all.Items)
	}
	end := start + limit

	out := &corev1.SecretList{}
	if end < len(all.Items) {
		out.Continue = strconv.Itoa(end)
	} else {
		end = len(all.Items)
	}
	out.Items = append(out.Items, all.Items[start:end]...)
	return out, nil
}

// newPagingKubernetesStore builds a store over [pagingSecrets] with the given
// page size, and hands back the double so a test can read what it was asked.
func newPagingKubernetesStore(t *testing.T, estate string, pageSize int64) (*KubernetesStore, *pagingSecrets) {
	t.Helper()
	double := &pagingSecrets{SecretInterface: fakeSecrets(t), pageErr: map[int]error{}}
	store, err := NewKubernetesStore(KubernetesConfig{
		Secrets:      double,
		Namespace:    fakeRecordNamespace,
		Estate:       estate,
		ListPageSize: pageSize,
	})
	if err != nil {
		t.Fatalf("NewKubernetesStore: %v", err)
	}
	return store, double
}

// writeRecords writes n records under one prefix and returns their keys.
func writeRecords(t *testing.T, store *KubernetesStore, prefix string, n int) []string {
	t.Helper()
	ctx := context.Background()
	keys := make([]string, 0, n)
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("%s/rec-%02d", prefix, i)
		if _, err := store.PutIfAbsent(ctx, key, []byte(fmt.Sprintf("payload-%02d", i))); err != nil {
			t.Fatalf("PutIfAbsent(%q): %v", key, err)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// TestKubernetesListReadsEveryPage is M13r. A LIST is paginated, and nothing
// in this package's suite wrote more than [DefaultKubernetesListPageSize]
// records or set a page size, so a store that stopped after the first page
// returned a short listing with a nil error and every test still passed. That
// is the same failure the whole of #1448's section A is about, reached
// through the page loop instead of through a label: an estate that reads as
// having fewer records than it has is planned against as if the missing ones
// were never created.
//
// Seven records over a page size of two, so there are four pages and the
// first one holds well under half of them.
func TestKubernetesListReadsEveryPage(t *testing.T) {
	store, double := newPagingKubernetesStore(t, "prod", 2)
	keys := writeRecords(t, store, "tofu-records/prod/aws_thing", 7)
	ctx := context.Background()

	got, err := store.List(ctx, "tofu-records/prod/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !equalStrings(got, keys) {
		t.Errorf("List returned %d keys, want all %d: %v", len(got), len(keys), got)
	}
	if double.calls < 2 {
		t.Fatalf("the store made %d List call(s) for seven records at a page size of two, so this case never had a second page to lose", double.calls)
	}
	for i, limit := range double.limits {
		if limit != 2 {
			t.Errorf("List call %d carried Limit %d, want the configured page size 2", i, limit)
		}
	}

	all, err := store.GetAll(ctx, "tofu-records/prod/")
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(all) != len(keys) {
		t.Fatalf("GetAll returned %d records, want all %d", len(all), len(keys))
	}
	for i, key := range keys {
		rec, ok := all[key]
		if !ok {
			t.Errorf("GetAll is missing %q", key)
			continue
		}
		// The keys are written in order, so key i holds payload i.
		want := fmt.Sprintf("payload-%02d", i)
		if string(rec.Payload) != want {
			t.Errorf("GetAll[%q].Payload = %q, want %q", key, rec.Payload, want)
		}
	}
}

// TestKubernetesDefaultPageSizeIsTheOneOnTheStore pins that a store built
// without a page size still bounds its pages, so the loop above is the loop
// production runs and not one a test configured into being.
func TestKubernetesDefaultPageSizeIsTheOneOnTheStore(t *testing.T) {
	store, double := newPagingKubernetesStore(t, "prod", 0)
	writeRecords(t, store, "tofu-records/prod/aws_thing", 3)
	if _, err := store.List(context.Background(), "tofu-records/prod/"); err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(double.limits) == 0 {
		t.Fatal("List made no List call")
	}
	if double.limits[0] != DefaultKubernetesListPageSize {
		t.Errorf("the first List carried Limit %d, want DefaultKubernetesListPageSize (%d)", double.limits[0], DefaultKubernetesListPageSize)
	}
}

// TestKubernetesAFailedSecondPageFailsTheWholeCall is M13r's adjacent half,
// which the audit found holding and which is kept here so it stays that way.
// A listing that read three pages and was refused the fourth knows nothing
// about the records on the fourth, and answering with the three it has is a
// short listing with a nil error - the failure this file exists for. So the
// call fails, and it returns no partial result a caller could use by mistake.
//
// The three answers are the ones a real API server gives: 410 Gone with
// reason Expired, which is a continue token that outlived its watch window
// and is the one a long listing actually meets; a 500; and a 403 for an
// identity whose read was revoked mid-listing.
func TestKubernetesAFailedSecondPageFailsTheWholeCall(t *testing.T) {
	secretsGR := schema.GroupResource{Group: "", Resource: "secrets"}
	for _, tc := range []struct {
		name string
		err  error
	}{
		{
			"410 the continue token expired",
			k8serrors.NewResourceExpired("The provided continue parameter is too old to display a consistent list result"),
		},
		{
			"500 from the API server",
			k8serrors.NewInternalError(fmt.Errorf("the server had an unexpected error")),
		},
		{
			"403 the identity lost its read mid-listing",
			k8serrors.NewForbidden(secretsGR, "", fmt.Errorf("secrets is forbidden")),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, double := newPagingKubernetesStore(t, "prod", 2)
			keys := writeRecords(t, store, "tofu-records/prod/aws_thing", 7)
			// Written first, failed second: the records above are already in
			// the fake before the second page is made to fail.
			double.pageErr[1] = tc.err

			gotKeys, err := store.List(context.Background(), "tofu-records/prod/")
			if err == nil {
				t.Fatalf("List returned %d of %d keys and a nil error after the second page failed; a short listing that reads as an empty-ish estate is what a caller cannot tell from the truth", len(gotKeys), len(keys))
			}
			if gotKeys != nil {
				t.Errorf("List returned %v alongside its error; nothing may be returned beside a refusal", gotKeys)
			}

			store2, double2 := newPagingKubernetesStore(t, "prod", 2)
			writeRecords(t, store2, "tofu-records/prod/aws_thing", 7)
			double2.pageErr[1] = tc.err
			all, err := store2.GetAll(context.Background(), "tofu-records/prod/")
			if err == nil {
				t.Fatalf("GetAll returned %d of %d records and a nil error after the second page failed", len(all), len(keys))
			}
			if all != nil {
				t.Errorf("GetAll returned %d records alongside its error; a partial snapshot is not a snapshot", len(all))
			}
		})
	}

	// The 403 leg must not be swallowed by #1370's reader tolerance either:
	// a listing refused halfway is an incomplete listing whoever refused it.
	store, double := newPagingKubernetesStore(t, "prod", 2)
	writeRecords(t, store, "tofu-records/prod/aws_thing", 7)
	double.pageErr[1] = k8serrors.NewForbidden(secretsGR, "", fmt.Errorf("secrets is forbidden"))
	if _, err := store.List(context.Background(), "tofu-records/prod/"); err == nil {
		t.Fatal("a 403 on the second page did not fail the listing")
	} else if !strings.Contains(err.Error(), "listing") {
		t.Errorf("the refusal does not say what failed: %v", err)
	}
}

// TestKubernetesGetAllRefusesAnUndecompressablePayloadByName is M14. A
// payload that will not decompress used to be skipped, and a key skipped out
// of a bulk read is a key the caller reads as holding NO record - which is
// how a corrupted record Secret became a resource the next plan proposes
// creating again. Complete or fail is the contract [BulkReader] states, and a
// record that cannot be read is the "or fail" half.
//
// The refusal has to name the Secret, because the key alone does not say
// which object to look at: the name is a hash.
func TestKubernetesGetAllRefusesAnUndecompressablePayloadByName(t *testing.T) {
	secrets := fakeSecrets(t)
	store, err := NewKubernetesStore(KubernetesConfig{Secrets: secrets, Namespace: fakeRecordNamespace, Estate: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const broken = "tofu-records/prod/aws_thing/broken"
	for _, key := range []string{"tofu-records/prod/aws_thing/fine", broken} {
		if _, err := store.PutIfAbsent(ctx, key, []byte("v")); err != nil {
			t.Fatalf("PutIfAbsent(%q): %v", key, err)
		}
	}

	// What a truncated write, a restore of half an object or an operator
	// editing the data by hand leaves behind: the object is still a record
	// by every other signal, and the bytes are not gzip.
	name := store.SecretName(broken)
	secret, err := secrets.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the Secret back: %v", err)
	}
	secret.Data[kubernetesPayloadKey] = []byte("this is not gzip")
	if _, err := secrets.Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("corrupting the payload: %v", err)
	}

	all, err := store.GetAll(ctx, "tofu-records/prod/")
	if err == nil {
		t.Fatalf("GetAll returned %d records and a nil error over an undecompressable payload; the broken record is then a key holding no record, which is a resource the next plan proposes creating again: %v", len(all), all)
	}
	if all != nil {
		t.Errorf("GetAll returned %d records alongside its error", len(all))
	}
	msg := err.Error()
	if !strings.Contains(msg, name) {
		t.Errorf("the refusal does not name the Secret %q, and the name is a hash so nothing else says which object to look at: %v", name, err)
	}
	if !strings.Contains(msg, broken) {
		t.Errorf("the refusal does not name the record key %q: %v", broken, err)
	}
	if !strings.Contains(msg, "not gzip") {
		t.Errorf("the refusal does not say what was wrong with the payload: %v", err)
	}
}

// TestKubernetesRecordSecretNeverAnnotatesAnotherEstate is M11.
//
// buildSecret skips the tofu-estate key when it copies the context's object
// tags into the ANNOTATIONS, and deleting that skip was not noticed by any
// test. The reason it was not is worth writing down: the test that looks like
// it covers this checks the tofu-estate LABEL, and the label is set
// separately, from the store's own estate, after the loop - so it reads
// "prod" whether or not the guard is there. What the guard protects is the
// annotation map, and that is what this pins.
//
// It matters because of what the object says about itself. #1016's ruling is
// that the Kubernetes marker is the estate LABEL, and
// live/kubernetes/estate-boundary.yaml reads that label and nothing else. An
// annotation spelled tofu-estate is a second spelling of the marker on the
// same object, fenced by nothing, and with the guard gone it carries whatever
// the caller put in the context - a record Secret whose labels say it is
// prod's and whose annotations say it is some-other-estate's. The store's
// contract, which [S3Store] keeps by encoding its base tags last, is that an
// object this store writes can never name an estate other than the one the
// store was opened for. This is that contract on this backend.
func TestKubernetesRecordSecretNeverAnnotatesAnotherEstate(t *testing.T) {
	secrets := fakeSecrets(t)
	store, err := NewKubernetesStore(KubernetesConfig{Secrets: secrets, Namespace: fakeRecordNamespace, Estate: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithObjectTags(context.Background(), map[string]string{
		KubernetesEstateLabel: "some-other-estate",
		"tofu-address-0":      "aws_instance.this",
	})
	const key = "tofu-records/prod/aws_instance/one"
	if _, err := store.PutIfAbsent(ctx, key, []byte("v")); err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	secret, err := secrets.Get(context.Background(), store.SecretName(key), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the Secret back: %v", err)
	}

	if got, ok := secret.Annotations[KubernetesEstateLabel]; ok {
		t.Errorf("the record Secret carries a %s ANNOTATION of %q. The estate marker is the label (#1016) and the boundary policy reads only the label, so this is an unfenced second spelling of the marker naming an estate the store was not opened for", KubernetesEstateLabel, got)
	}
	// The other context tags still have to arrive: the guard is one key, not
	// a reason to drop the caller's tags.
	if got := secret.Annotations["tofu-address-0"]; got != "aws_instance.this" {
		t.Errorf("tofu-address-0 annotation = %q, want %q; the guard skips one key and keeps the rest", got, "aws_instance.this")
	}
	if got := secret.Labels[KubernetesEstateLabel]; got != "prod" {
		t.Errorf("%s label = %q, want %q", KubernetesEstateLabel, got, "prod")
	}
	// And the annotation is not simply absent because no annotation of that
	// shape is ever written: the address one above is there, from the same
	// map, through the same loop.
	if len(secret.Annotations) < 3 {
		t.Errorf("the Secret carries %d annotations, so this case may be passing because the tag loop ran at all: %v", len(secret.Annotations), secret.Annotations)
	}
}
