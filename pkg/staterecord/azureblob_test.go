// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
)

// fakeAzure is an httptest fake of the two Azure surfaces record_store
// "azurerm" speaks: the Blob REST API on one container, with the
// conditional-write answers Azure documents (409 BlobAlreadyExists for
// If-None-Match: * on an existing blob, 412 ConditionNotMet for an If-Match
// mismatch, 404 BlobNotFound for an If-Match on a missing blob), and the four
// ARM reads the opening checks make. It is a fake, so what it proves is the
// store's half of each exchange; the maintainer measures Azure's half on
// floci-az.
type fakeAzure struct {
	mu        sync.Mutex
	container string
	blobs     map[string]fakeBlob
	seq       int
	pageSize  int

	// puts records the x-ms-tags header of every Put Blob, in order.
	puts []string
	// listCalls counts List Blobs requests.
	listCalls int

	// ARM answers, as JSON bodies, or a status to answer instead.
	versioning   string
	policy       string
	account      string
	containerARM string
	armStatus    map[string]int
}

type fakeBlob struct {
	data []byte
	etag string
	tags string
}

func newFakeAzure() *fakeAzure {
	return &fakeAzure{
		container:    "records",
		blobs:        map[string]fakeBlob{},
		pageSize:     5000,
		versioning:   `{"properties":{"isVersioningEnabled":true}}`,
		policy:       `{"properties":{"policy":{"rules":[{"name":"expire-versions","type":"Lifecycle","definition":{"filters":{"blobTypes":["blockBlob"]},"actions":{"version":{"delete":{"daysAfterCreationGreaterThan":30}}}}}]}}}`,
		account:      `{"properties":{"allowBlobPublicAccess":false}}`,
		containerARM: `{"properties":{"publicAccess":"None"}}`,
		armStatus:    map[string]int{},
	}
}

func (f *fakeAzure) fail(w http.ResponseWriter, status int, code string) {
	w.Header().Set("x-ms-error-code", code)
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?><Error><Code>%s</Code><Message>fake</Message></Error>`, code)
}

func (f *fakeAzure) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.HasPrefix(r.URL.Path, "/subscriptions/") {
		f.serveARM(w, r)
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/"+f.container)
	if !ok {
		f.fail(w, http.StatusNotFound, "ContainerNotFound")
		return
	}
	name := strings.TrimPrefix(rest, "/")
	if name == "" && r.URL.Query().Get("comp") == "list" {
		f.list(w, r)
		return
	}
	cur, exists := f.blobs[name]
	ifMatch, ifNoneMatch := r.Header.Get("If-Match"), r.Header.Get("If-None-Match")
	switch r.Method {
	case http.MethodPut:
		f.puts = append(f.puts, r.Header.Get("x-ms-tags"))
		switch {
		case ifNoneMatch == "*" && exists:
			f.fail(w, http.StatusConflict, "BlobAlreadyExists")
			return
		case ifMatch != "" && !exists:
			f.fail(w, http.StatusNotFound, "BlobNotFound")
			return
		case ifMatch != "" && ifMatch != cur.etag:
			f.fail(w, http.StatusPreconditionFailed, "ConditionNotMet")
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.seq++
		b := fakeBlob{data: body, etag: fmt.Sprintf(`"0x%d"`, f.seq), tags: r.Header.Get("x-ms-tags")}
		f.blobs[name] = b
		w.Header().Set("ETag", b.etag)
		w.WriteHeader(http.StatusCreated)
	case http.MethodGet:
		if !exists {
			f.fail(w, http.StatusNotFound, "BlobNotFound")
			return
		}
		w.Header().Set("ETag", cur.etag)
		w.Header().Set("Content-Length", fmt.Sprint(len(cur.data)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(cur.data)
	case http.MethodDelete:
		switch {
		case !exists:
			f.fail(w, http.StatusNotFound, "BlobNotFound")
			return
		case ifMatch != "" && ifMatch != cur.etag:
			f.fail(w, http.StatusPreconditionFailed, "ConditionNotMet")
			return
		}
		delete(f.blobs, name)
		w.WriteHeader(http.StatusAccepted)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *fakeAzure) list(w http.ResponseWriter, r *http.Request) {
	f.listCalls++
	prefix, marker := r.URL.Query().Get("prefix"), r.URL.Query().Get("marker")
	var names []string
	for name := range f.blobs {
		if strings.HasPrefix(name, prefix) && name > marker {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	next := ""
	if len(names) > f.pageSize {
		names = names[:f.pageSize]
		next = names[len(names)-1]
	}
	type blobXML struct {
		Name string `xml:"Name"`
	}
	out := struct {
		XMLName    xml.Name  `xml:"EnumerationResults"`
		Blobs      []blobXML `xml:"Blobs>Blob"`
		NextMarker string    `xml:"NextMarker"`
	}{NextMarker: next}
	for _, n := range names {
		out.Blobs = append(out.Blobs, blobXML{Name: n})
	}
	w.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(w).Encode(out)
}

func (f *fakeAzure) serveARM(w http.ResponseWriter, r *http.Request) {
	var key, body string
	switch p := r.URL.Path; {
	case strings.HasSuffix(p, "/blobServices/default"):
		key, body = "versioning", f.versioning
	case strings.HasSuffix(p, "/managementPolicies/default"):
		key, body = "policy", f.policy
	case strings.Contains(p, "/blobServices/default/containers/"):
		key, body = "container", f.containerARM
	default:
		key, body = "account", f.account
	}
	w.Header().Set("Content-Type", "application/json")
	if status := f.armStatus[key]; status != 0 {
		code := map[int]string{403: "AuthorizationFailed", 404: "ResourceNotFound"}[status]
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"error":{"code":%q,"message":"fake"}}`, code)
		return
	}
	_, _ = io.WriteString(w, body)
}

type fakeTokenCredential struct{}

func (fakeTokenCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// newFakeAzureStore stands a fake up and opens a store on it the way
// internal/live/projection does, with the real container client and the real
// ARM contract reader pointed at the fake.
func newFakeAzureStore(t *testing.T, f *fakeAzure) *AzureBlobStore {
	t.Helper()
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)
	opts := policy.ClientOptions{
		Transport: srv.Client(),
		Retry:     policy.RetryOptions{MaxRetries: -1},
		Telemetry: policy.TelemetryOptions{Disabled: true},
	}
	client, err := container.NewClientWithNoCredential(srv.URL+"/"+f.container, &container.ClientOptions{ClientOptions: opts})
	if err != nil {
		t.Fatal(err)
	}
	armOpts := opts
	armOpts.Cloud = cloud.Configuration{Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
		cloud.ResourceManager: {Endpoint: srv.URL, Audience: "https://management.example"},
	}}
	factory, err := armstorage.NewClientFactory("sub", fakeTokenCredential{}, &arm.ClientOptions{ClientOptions: armOpts, DisableRPRegistration: true})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewAzureBlobStore(AzureBlobConfig{
		Container:     client,
		Account:       "acmerecords",
		ContainerName: f.container,
		ResourceGroup: "records-rg",
		BaseTags:      map[string]string{AzureEstateTag: "prod"},
		Contract:      NewAzureContractAPI(factory, "records-rg", "acmerecords", f.container),
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestAzureBlobStoreConformance(t *testing.T) {
	runConformance(t, func(t *testing.T) Store {
		return newFakeAzureStore(t, newFakeAzure())
	})
}

// TestAzureBlobConditionalWritesConflictByName: each of Azure's three
// refusals of a condition reaches the caller as a *VersionConflictError
// naming the version the store holds, never as a bare error to parse.
func TestAzureBlobConditionalWritesConflictByName(t *testing.T) {
	ctx := context.Background()
	f := newFakeAzure()
	s := newFakeAzureStore(t, f)
	v1, err := s.PutIfAbsent(ctx, "tofu-records/prod/a", []byte("one"))
	if err != nil {
		t.Fatal(err)
	}

	var conflict *VersionConflictError
	// 409 BlobAlreadyExists, from If-None-Match: *.
	if _, err := s.PutIfAbsent(ctx, "tofu-records/prod/a", []byte("two")); !errors.As(err, &conflict) || conflict.ActualVersion != v1 {
		t.Errorf("create over an existing blob: err = %v, want a conflict naming %s", err, v1)
	}
	// 412 ConditionNotMet, from If-Match with a stale ETag.
	if _, err := s.PutIfVersion(ctx, "tofu-records/prod/a", []byte("two"), `"0xstale"`); !errors.As(err, &conflict) || conflict.ActualVersion != v1 {
		t.Errorf("update with a stale version: err = %v, want a conflict naming %s", err, v1)
	}
	// 404 BlobNotFound, from If-Match on a blob that is gone.
	if _, err := s.PutIfVersion(ctx, "tofu-records/prod/gone", []byte("x"), v1); !errors.As(err, &conflict) || conflict.ActualVersion != "" {
		t.Errorf("update of a missing blob: err = %v, want a conflict naming no record", err)
	}
	// Delete with a stale ETag.
	if err := s.Delete(ctx, "tofu-records/prod/a", `"0xstale"`); !errors.As(err, &conflict) || conflict.ActualVersion != v1 {
		t.Errorf("delete with a stale version: err = %v, want a conflict naming %s", err, v1)
	}
	// And the right version goes through, each step.
	v2, err := s.PutIfVersion(ctx, "tofu-records/prod/a", []byte("two"), v1)
	if err != nil || v2 == v1 {
		t.Fatalf("update with the current version: %q, %v", v2, err)
	}
	if err := s.Delete(ctx, "tofu-records/prod/a", v2); err != nil {
		t.Errorf("delete with the current version: %v", err)
	}
}

// TestAzureBlobListPagesThroughEveryMarker: a listing is followed through
// every NextMarker, so an estate bigger than one page is not read as a
// smaller one.
func TestAzureBlobListPagesThroughEveryMarker(t *testing.T) {
	ctx := context.Background()
	f := newFakeAzure()
	f.pageSize = 2
	s := newFakeAzureStore(t, f)
	var want []string
	for i := range 7 {
		k := fmt.Sprintf("tofu-records/prod/k%d", i)
		want = append(want, k)
		if _, err := s.PutIfAbsent(ctx, k, []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.PutIfAbsent(ctx, "tofu-records/prod-eu/k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	f.listCalls = 0
	got, err := s.List(ctx, "tofu-records/prod/")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("List = %v, want %v", got, want)
	}
	if f.listCalls != 4 {
		t.Errorf("List made %d requests for 7 blobs at 2 a page, want 4", f.listCalls)
	}
	all, err := s.GetAll(ctx, "tofu-records/prod/")
	if err != nil || len(all) != 7 {
		t.Errorf("GetAll = %d records, %v; want 7", len(all), err)
	}
}

// TestAzureBlobEveryWriteCarriesTheEstateTag: x-ms-tags on every Put Blob,
// with tofu-estate always and the store's own estate winning over a
// per-write tag of the same key. An address tag outside the blob index tag
// charset leaves the address tags off that blob, never the estate.
func TestAzureBlobEveryWriteCarriesTheEstateTag(t *testing.T) {
	ctx := context.Background()
	f := newFakeAzure()
	s := newFakeAzureStore(t, f)

	v, err := s.PutIfAbsent(WithObjectTags(ctx, map[string]string{"tofu-address": "aws_vpc.main", AzureEstateTag: "other"}), "tofu-records/prod/a", []byte("1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutIfVersion(ctx, "tofu-records/prod/a", []byte("2"), v); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutIfAbsent(WithObjectTags(ctx, map[string]string{"tofu-address": "aws_vpc.main:a@@b"}), "tofu-records/prod/b", []byte("1")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutIfAbsent(ctx, "tofu-records/prod/a", []byte("3")); err == nil {
		t.Fatal("a create over an existing blob succeeded")
	}

	if len(f.puts) != 4 {
		t.Fatalf("saw %d Put Blob requests, want 4", len(f.puts))
	}
	for i, raw := range f.puts {
		tags, err := url.ParseQuery(raw)
		if err != nil {
			t.Fatalf("put %d: x-ms-tags %q does not parse: %v", i, raw, err)
		}
		if got := tags.Get(AzureEstateTag); got != "prod" {
			t.Errorf("put %d: x-ms-tags %q carries tofu-estate %q, want %q", i, raw, got, "prod")
		}
	}
	if tags, _ := url.ParseQuery(f.puts[0]); tags.Get("tofu-address") != "aws_vpc.main" {
		t.Errorf("put 0 lost its address tag: %q", f.puts[0])
	}
	if tags, _ := url.ParseQuery(f.puts[2]); tags.Has("tofu-address") {
		t.Errorf("put 2 sent an address tag Azure refuses (it holds '@'): %q", f.puts[2])
	}
}

// TestAzureBlobForbiddenIsAccessDenied: a 403 from either Azure surface is
// this identity being refused, which #1370's reader tolerance reads.
func TestAzureBlobForbiddenIsAccessDenied(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &azcore.ResponseError{StatusCode: http.StatusForbidden, ErrorCode: "AuthorizationPermissionMismatch"})
	if !IsAccessDenied(err) {
		t.Error("a 403 from Azure is not read as access denied")
	}
	if IsAccessDenied(&azcore.ResponseError{StatusCode: http.StatusNotFound, ErrorCode: "BlobNotFound"}) {
		t.Error("a 404 from Azure is read as access denied")
	}
}

// TestAzureBlobMissingContainerIsNotAnEmptyEstate: a 404 that names the
// container is an error on every read, for #1383's reason.
func TestAzureBlobMissingContainerIsNotAnEmptyEstate(t *testing.T) {
	f := newFakeAzure()
	s := newFakeAzureStore(t, f)
	f.container = "elsewhere"
	if _, _, _, err := s.Get(context.Background(), "tofu-records/prod/a"); err == nil || !strings.Contains(err.Error(), "ContainerNotFound") {
		t.Errorf("Get against a missing container: err = %v, want the container named", err)
	}
	if _, err := s.List(context.Background(), "tofu-records/prod/"); err == nil {
		t.Error("List against a missing container returned no error")
	}
}

var azureNamespaces = []string{"tofu-records/prod/", "tofu-hints/prod/", "tofu-outputs/prod/"}

func azureFinding(t *testing.T, findings []Finding, s Setting) Finding {
	t.Helper()
	for _, f := range findings {
		if f.Setting == s {
			return f
		}
	}
	t.Fatalf("no finding for %s in %+v", s, findings)
	return Finding{}
}

// TestAzureContractPassesACorrectAccount is the control the refusals below
// are measured against: the fake's defaults are a correct account.
func TestAzureContractPassesACorrectAccount(t *testing.T) {
	s := newFakeAzureStore(t, newFakeAzure())
	findings, err := s.CheckContract(context.Background(), ContractOptions{Namespaces: azureNamespaces})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != len(AzureSettings) {
		t.Fatalf("%d findings, want %d", len(findings), len(AzureSettings))
	}
	for _, f := range findings {
		if !f.OK() {
			t.Errorf("%s: %+v, want a pass", f.Setting, f)
		}
	}
}

// TestAzureContractRefusesEachSetting: each opening check refuses by name.
func TestAzureContractRefusesEachSetting(t *testing.T) {
	cases := []struct {
		name       string
		edit       func(f *fakeAzure)
		setting    Setting
		outcome    Outcome
		unwaivable bool
		found      string
	}{
		{"versioning off", func(f *fakeAzure) { f.versioning = `{"properties":{"isVersioningEnabled":false}}` },
			AzureBlobVersioning, Failed, false, "not enabled"},
		{"versioning unreadable", func(f *fakeAzure) { f.armStatus["versioning"] = 403 },
			AzureBlobVersioning, Unreadable, false, "blobServices/read was denied"},
		{"no lifecycle policy", func(f *fakeAzure) { f.armStatus["policy"] = 404 },
			AzureLifecycle, Failed, false, "no lifecycle management policy"},
		{"lifecycle deletes current blobs", func(f *fakeAzure) {
			f.policy = `{"properties":{"policy":{"rules":[{"name":"tidy","type":"Lifecycle","definition":{"filters":{"blobTypes":["blockBlob"]},"actions":{"baseBlob":{"delete":{"daysAfterModificationGreaterThan":90}},"version":{"delete":{"daysAfterCreationGreaterThan":30}}}}}]}}}`
		}, AzureLifecycle, Failed, true, "deletes current blobs"},
		{"lifecycle archives current blobs", func(f *fakeAzure) {
			f.policy = `{"properties":{"policy":{"rules":[{"name":"cold","type":"Lifecycle","definition":{"filters":{"blobTypes":["blockBlob"],"prefixMatch":["records/tofu-records/"]},"actions":{"baseBlob":{"tierToArchive":{"daysAfterModificationGreaterThan":30}}}}}]}}}`
		}, AzureLifecycle, Failed, true, "archive tier"},
		{"lifecycle misses a namespace", func(f *fakeAzure) {
			f.policy = `{"properties":{"policy":{"rules":[{"name":"records-only","type":"Lifecycle","definition":{"filters":{"blobTypes":["blockBlob"],"prefixMatch":["records/tofu-records/"]},"actions":{"version":{"delete":{"daysAfterCreationGreaterThan":30}}}}}]}}}`
		}, AzureLifecycle, Failed, false, `does not contain "records/tofu-hints/prod/"`},
		{"lifecycle prefix without the container", func(f *fakeAzure) {
			f.policy = `{"properties":{"policy":{"rules":[{"name":"no-container","type":"Lifecycle","definition":{"filters":{"blobTypes":["blockBlob"],"prefixMatch":["tofu-records/"]},"actions":{"version":{"delete":{"daysAfterCreationGreaterThan":30}}}}}]}}}`
		}, AzureLifecycle, Failed, false, "does not contain"},
		{"lifecycle rule disabled", func(f *fakeAzure) {
			f.policy = `{"properties":{"policy":{"rules":[{"name":"off","enabled":false,"type":"Lifecycle","definition":{"filters":{"blobTypes":["blockBlob"]},"actions":{"version":{"delete":{"daysAfterCreationGreaterThan":30}}}}}]}}}`
		}, AzureLifecycle, Failed, false, "is disabled"},
		{"public access allowed", func(f *fakeAzure) {
			f.account = `{"properties":{"allowBlobPublicAccess":true}}`
			f.containerARM = `{"properties":{"publicAccess":"Blob"}}`
		}, AzurePublicAccess, Failed, false, `container "records" is already readable anonymously`},
		{"public access unset", func(f *fakeAzure) { f.account = `{"properties":{}}` },
			AzurePublicAccess, Failed, false, "does not set allowBlobPublicAccess"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeAzure()
			tc.edit(fake)
			s := newFakeAzureStore(t, fake)
			findings, err := s.CheckContract(context.Background(), ContractOptions{Namespaces: azureNamespaces})
			if err != nil {
				t.Fatal(err)
			}
			f := azureFinding(t, findings, tc.setting)
			if f.Outcome != tc.outcome || f.Unwaivable != tc.unwaivable || !strings.Contains(f.Found, tc.found) {
				t.Errorf("%s: %+v, want outcome %d unwaivable %v found containing %q", tc.setting, f, tc.outcome, tc.unwaivable, tc.found)
			}
			summary, detail := s.ContractRefusal(f)
			if summary == "" || !strings.Contains(summary, "storage account") {
				t.Errorf("refusal headline %q does not name the storage account", summary)
			}
			if !strings.Contains(summary, string(tc.setting)) && !tc.unwaivable {
				t.Errorf("refusal headline %q does not name %s", summary, tc.setting)
			}
			if !strings.Contains(detail, "acmerecords") {
				t.Errorf("refusal detail does not name the account: %s", detail)
			}
			for _, other := range findings {
				if other.Setting != tc.setting && !other.OK() {
					t.Errorf("%s failed too (%+v); each case breaks one setting", other.Setting, other)
				}
			}
		})
	}
}

// TestAzureContractWithoutAnARMCredentialIsUnreadable: a store opened with an
// access key and no Azure AD login cannot read the account's settings, and
// says so on each rather than passing any.
func TestAzureContractWithoutAnARMCredentialIsUnreadable(t *testing.T) {
	s, err := NewAzureBlobStore(AzureBlobConfig{
		Container:           &container.Client{},
		Account:             "acmerecords",
		ContainerName:       "records",
		ContractUnavailable: "no Azure AD credential could be built",
	})
	if err != nil {
		t.Fatal(err)
	}
	findings, err := s.CheckContract(context.Background(), ContractOptions{Namespaces: azureNamespaces})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Outcome != Unreadable || !strings.Contains(f.Found, "no Azure AD credential") {
			t.Errorf("%s: %+v, want unreadable with the reason", f.Setting, f)
		}
	}
	refused, _, _ := SplitWaived(findings, nil)
	if len(refused) != len(AzureSettings) {
		t.Errorf("%d refused, want all %d", len(refused), len(AzureSettings))
	}
}

// keep encoding/json imported for the fake's bodies being valid JSON: a
// malformed default would read as an error, not a finding, and every case
// above would pass for the wrong reason.
func TestFakeAzureDefaultsAreJSON(t *testing.T) {
	f := newFakeAzure()
	for _, body := range []string{f.versioning, f.policy, f.account, f.containerARM} {
		if !json.Valid([]byte(body)) {
			t.Errorf("fake default is not JSON: %s", body)
		}
	}
}
