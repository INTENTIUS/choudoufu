// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package azsweep

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/zclconf/go-cty/cty"

)

const (
	sub1 = "11111111-1111-1111-1111-111111111111"
	sub2 = "22222222-2222-2222-2222-222222222222"
)

// fakeCred is a credential that hands out one fixed token and records the
// scopes it was asked for.
type fakeCred struct {
	mu     sync.Mutex
	scopes []string
}

func (f *fakeCred) GetToken(_ context.Context, o policy.TokenRequestOptions) (azcore.AccessToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scopes = append(f.scopes, o.Scopes...)
	return azcore.AccessToken{Token: "fake-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// sentQuery is the request body a fake server decodes.
type sentQuery struct {
	Subscriptions []string `json:"subscriptions"`
	Query         string   `json:"query"`
	Options       struct {
		Top          int    `json:"$top"`
		SkipToken    string `json:"$skipToken"`
		ResultFormat string `json:"resultFormat"`
	} `json:"options"`
}

// server is a fake Resource Graph: answer is called for each request, in
// order, with the decoded body.
type server struct {
	t      *testing.T
	mu     sync.Mutex
	sent   []sentQuery
	answer func(n int, q sentQuery, w http.ResponseWriter)
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/providers/Microsoft.ResourceGraph/resources" || r.URL.Query().Get("api-version") != APIVersion {
		s.t.Errorf("request %s %s, want POST /providers/Microsoft.ResourceGraph/resources?api-version=%s", r.Method, r.URL, APIVersion)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer fake-token" {
		s.t.Errorf("Authorization = %q, want the fake token", got)
	}
	body, _ := io.ReadAll(r.Body)
	var q sentQuery
	if err := json.Unmarshal(body, &q); err != nil {
		s.t.Errorf("request body %s: %s", body, err)
	}
	s.mu.Lock()
	s.sent = append(s.sent, q)
	n := len(s.sent)
	s.mu.Unlock()
	s.answer(n, q, w)
}

func newTestClient(t *testing.T, answer func(n int, q sentQuery, w http.ResponseWriter)) (*Client, *server, *[]time.Duration) {
	t.Helper()
	s := &server{t: t, answer: answer}
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	c := newWith(Endpoints{ResourceManager: ts.URL + "/", Audience: "https://management.core.windows.net/"}, &fakeCred{}, ts.Client())
	var waits []time.Duration
	c.sleep = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	return c, s, &waits
}

func write(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// The page bodies are QueryResponse as api-version 2022-10-01 documents
// it, resultFormat objectArray.
const page1 = `{
  "totalRecords": 4, "count": 3, "resultTruncated": "false", "$skipToken": "skip-2",
  "data": [
    {"id": "/subscriptions/` + sub1 + `/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet",
     "type": "microsoft.network/virtualnetworks", "name": "vnet", "resourceGroup": "rg",
     "subscriptionId": "` + sub1 + `", "location": "westeurope",
     "tags": {"tofu-estate": "est", "tofu-address": "azurerm_virtual_network.a"}, "managedBy": ""},
    {"id": "/subscriptions/` + sub1 + `/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/other",
     "type": "microsoft.storage/storageaccounts", "name": "other", "resourceGroup": "rg",
     "subscriptionId": "` + sub1 + `", "location": "westeurope",
     "tags": {"tofu-estate": "est-2"}},
    {"id": "/subscriptions/` + sub2 + `/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/elsewhere",
     "type": "microsoft.storage/storageaccounts", "name": "elsewhere", "resourceGroup": "rg",
     "subscriptionId": "` + sub2 + `", "location": "westeurope",
     "tags": {"tofu-estate": "est"}}
  ]
}`

const page2 = `{
  "totalRecords": 4, "count": 2, "resultTruncated": "false",
  "data": [
    {"id": "/subscriptions/` + sub1 + `/resourceGroups/RG-Two",
     "type": "microsoft.resources/subscriptions/resourcegroups", "name": "RG-Two", "resourceGroup": "rg-two",
     "subscriptionId": "` + sub1 + `", "location": "westeurope",
     "tags": {"Tofu-Estate": "est"}, "managedBy": "/subscriptions/` + sub1 + `/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/aks"},
    {"id": "/subscriptions/` + sub1 + `/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/VNET",
     "type": "microsoft.network/virtualnetworks", "name": "VNET", "resourceGroup": "rg",
     "subscriptionId": "` + sub1 + `", "location": "westeurope",
     "tags": {"tofu-estate": "est"}}
  ]
}`

func TestSearchPagesAndMatchesTheKeyCaseInsensitively(t *testing.T) {
	c, s, _ := newTestClient(t, func(n int, q sentQuery, w http.ResponseWriter) {
		switch q.Options.SkipToken {
		case "":
			write(w, 200, page1)
		case "skip-2":
			write(w, 200, page2)
		default:
			t.Errorf("unexpected $skipToken %q", q.Options.SkipToken)
			write(w, 500, `{}`)
		}
	})
	got, err := c.Search(context.Background(), sub1, "est")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range got {
		ids = append(ids, r.Name)
	}
	// RG-Two is tagged Tofu-Estate and must be found; "other" carries a
	// different estate and "elsewhere" is in another subscription, so
	// both are dropped; VNET repeats vnet's id in other casing.
	want := []string{"RG-Two", "vnet"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("resources = %v, want %v", ids, want)
	}
	rg := got[0]
	if rg.Type != "microsoft.resources/subscriptions/resourcegroups" || rg.ResourceGroup != "rg-two" || rg.SubscriptionID != sub1 || rg.Location != "westeurope" ||
		!strings.HasSuffix(rg.ManagedBy, "/managedClusters/aks") || rg.Tags["Tofu-Estate"] != "est" {
		t.Errorf("resource group row = %+v", rg)
	}
	if len(s.sent) != 2 {
		t.Fatalf("sent %d requests, want 2", len(s.sent))
	}
	for i, q := range s.sent {
		if !reflect.DeepEqual(q.Subscriptions, []string{sub1}) || q.Options.Top != PageSize || q.Options.ResultFormat != "objectArray" || q.Query != Query(testKeys, "est") {
			t.Errorf("request %d = %+v", i, q)
		}
	}
	if fc := c.cred.(*fakeCred); len(fc.scopes) == 0 || fc.scopes[0] != "https://management.core.windows.net/.default" {
		t.Errorf("token scopes = %v", fc.scopes)
	}
}

func TestQueryReadsTheKeyCaseInsensitively(t *testing.T) {
	q := Query(testKeys, "o'brien")
	for _, want := range []string{
		"mv-expand markerTag",
		"where markerKey =~ 'tofu-estate'",
		`tostring(markerTag[markerKey]) == 'o\'brien'`,
		"resourcecontainers",
		"project id, type, name, resourceGroup, subscriptionId, location, tags, managedBy",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("query lacks %q:\n%s", want, q)
		}
	}
	// The case-sensitive dynamic access is the form the query exists to
	// avoid.
	if strings.Contains(q, "tags['tofu-estate']") || strings.Contains(q, "tags.tofu-estate") {
		t.Errorf("query reads the tag case-sensitively:\n%s", q)
	}
}

func TestOwnerTag(t *testing.T) {
	for name, tc := range map[string]struct {
		tags map[string]string
		want string
		ok   bool
	}{
		"lowercase":   {map[string]string{"tofu-estate": "a"}, "a", true},
		"title case":  {map[string]string{"Tofu-Estate": "b"}, "b", true},
		"upper case":  {map[string]string{"TOFU-ESTATE": "c"}, "c", true},
		"absent":      {map[string]string{"tofu-address": "x"}, "", false},
		"nil":         {nil, "", false},
		"similar key": {map[string]string{"tofu-estates": "d"}, "", false},
	} {
		got, ok := OwnerTag(testKeys, tc.tags)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: OwnerTag = %q, %v; want %q, %v", name, got, ok, tc.want, tc.ok)
		}
	}
}

func TestUnregisteredResourceGraphIsRefusedByName(t *testing.T) {
	c, _, _ := newTestClient(t, func(n int, q sentQuery, w http.ResponseWriter) {
		write(w, 409, `{"error":{"code":"MissingSubscriptionRegistration","message":"The subscription is not registered to use namespace 'Microsoft.ResourceGraph'."}}`)
	})
	_, err := c.Search(context.Background(), sub1, "est")
	var nr *NotRegisteredError
	if !errors.As(err, &nr) {
		t.Fatalf("err = %v (%T), want *NotRegisteredError", err, err)
	}
	if nr.Subscription != sub1 || nr.Code != "MissingSubscriptionRegistration" {
		t.Errorf("err = %+v", nr)
	}
	if got := Summary(err); got != SummaryResourceGraphNotRegistered {
		t.Errorf("Summary = %q, want %q", got, SummaryResourceGraphNotRegistered)
	}
}

func TestUnreadableSubscriptionIsAnIncompleteSweepNotAnEmptyOne(t *testing.T) {
	answers := map[string]func(w http.ResponseWriter){
		sub1: func(w http.ResponseWriter) {
			write(w, 200, `{"totalRecords":0,"count":0,"resultTruncated":"false","data":[]}`)
		},
		sub2: func(w http.ResponseWriter) {
			write(w, 400, `{"error":{"code":"BadRequest","message":"Please provide below info when asking for support","details":[{"code":"NoValidSubscriptionsInQueryRequest","message":"There must be at least one valid subscription in the query request."}]}}`)
		},
	}
	c, _, _ := newTestClient(t, func(n int, q sentQuery, w http.ResponseWriter) {
		answers[q.Subscriptions[0]](w)
	})
	res := Sweep(context.Background(), Coverage{sub1: c, strings.ToUpper(sub2): c}, []string{sub1, sub2, "33333333-3333-3333-3333-333333333333", sub1}, "est")
	if len(res) != 3 {
		t.Fatalf("results = %+v, want one per distinct subscription", res)
	}
	if res[0].Err != nil || res[0].Resources != nil {
		t.Errorf("readable, empty subscription = %+v, want no resources and no error", res[0])
	}
	var ur *UnreadableSubscriptionError
	if !errors.As(res[1].Err, &ur) || ur.Code != "NoValidSubscriptionsInQueryRequest" || ur.Subscription != sub2 {
		t.Errorf("unreadable subscription = %+v, want *UnreadableSubscriptionError", res[1])
	}
	if res[1].Resources != nil || Summary(res[1].Err) != SummarySubscriptionUnreadable {
		t.Errorf("unreadable subscription = %+v, summary %q", res[1], Summary(res[1].Err))
	}
	if !strings.Contains(res[1].Err.Error(), sub2) {
		t.Errorf("warning %q does not name the subscription", res[1].Err)
	}
	var uc *UncoveredSubscriptionError
	if !errors.As(res[2].Err, &uc) || Summary(res[2].Err) != SummarySubscriptionUncovered {
		t.Errorf("uncovered subscription = %+v", res[2])
	}
}

func TestForbiddenIsUnreadable(t *testing.T) {
	c, _, _ := newTestClient(t, func(n int, q sentQuery, w http.ResponseWriter) {
		write(w, 403, `{"error":{"code":"AuthorizationFailed","message":"The client does not have authorization to perform action 'Microsoft.ResourceGraph/resources/read'."}}`)
	})
	_, err := c.Search(context.Background(), sub1, "est")
	var ur *UnreadableSubscriptionError
	if !errors.As(err, &ur) || ur.Status != 403 {
		t.Fatalf("err = %v, want *UnreadableSubscriptionError", err)
	}
}

func TestThrottlingWaitsAsTheHeadersSay(t *testing.T) {
	c, s, waits := newTestClient(t, func(n int, q sentQuery, w http.ResponseWriter) {
		switch n {
		case 1:
			w.Header().Set("Retry-After", "3")
			write(w, 429, `{"error":{"code":"RateLimiting","message":"Please try again later."}}`)
		case 2:
			w.Header().Set(headerQuotaRemaining, "0")
			w.Header().Set(headerQuotaResets, "00:00:02")
			write(w, 429, `{"error":{"code":"RateLimiting","message":"Please try again later."}}`)
		case 3:
			// Answered, with more to read and the window spent.
			w.Header().Set(headerQuotaRemaining, "0")
			w.Header().Set(headerQuotaResets, "00:00:04")
			write(w, 200, page1)
		default:
			write(w, 200, page2)
		}
	})
	got, err := c.Search(context.Background(), sub1, "est")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || len(s.sent) != 4 {
		t.Fatalf("got %d resources over %d requests, want 2 over 4", len(got), len(s.sent))
	}
	want := []time.Duration{3 * time.Second, 2 * time.Second, 4 * time.Second}
	if !reflect.DeepEqual(*waits, want) {
		t.Errorf("waits = %v, want %v", *waits, want)
	}
}

func TestThrottlingGivesUp(t *testing.T) {
	t.Run("retries spent", func(t *testing.T) {
		c, s, waits := newTestClient(t, func(n int, q sentQuery, w http.ResponseWriter) {
			w.Header().Set("Retry-After", "1")
			write(w, 429, `{}`)
		})
		_, err := c.Search(context.Background(), sub1, "est")
		var th *ThrottledError
		if !errors.As(err, &th) || Summary(err) != SummaryResourceGraphThrottled {
			t.Fatalf("err = %v, want *ThrottledError", err)
		}
		if len(*waits) != MaxThrottleRetries || len(s.sent) != MaxThrottleRetries+1 {
			t.Errorf("waited %d times over %d requests, want %d over %d", len(*waits), len(s.sent), MaxThrottleRetries, MaxThrottleRetries+1)
		}
	})
	t.Run("wait too long", func(t *testing.T) {
		c, s, waits := newTestClient(t, func(n int, q sentQuery, w http.ResponseWriter) {
			w.Header().Set(headerQuotaResets, "00:05:00")
			write(w, 429, `{}`)
		})
		_, err := c.Search(context.Background(), sub1, "est")
		var th *ThrottledError
		if !errors.As(err, &th) || th.RetryAfter != 5*time.Minute {
			t.Fatalf("err = %v, want *ThrottledError asking 5m", err)
		}
		if len(*waits) != 0 || len(s.sent) != 1 {
			t.Errorf("waited %v over %d requests, want no wait", *waits, len(s.sent))
		}
	})
}

func TestLaterPageFailureDiscardsTheEarlierPages(t *testing.T) {
	c, _, _ := newTestClient(t, func(n int, q sentQuery, w http.ResponseWriter) {
		if n == 1 {
			write(w, 200, page1)
			return
		}
		write(w, 500, `{"error":{"code":"InternalServerError","message":"boom"}}`)
	})
	got, err := c.Search(context.Background(), sub1, "est")
	var pe *PartialError
	if got != nil || !errors.As(err, &pe) || pe.PagesRead != 1 || pe.RowsRead != 3 {
		t.Fatalf("got %v, %v; want no resources and a PartialError after one page", got, err)
	}
	if Summary(err) != SummarySweepUnavailable {
		t.Errorf("Summary = %q", Summary(err))
	}
}

func TestTruncatedWithoutSkipTokenIsPartial(t *testing.T) {
	c, _, _ := newTestClient(t, func(n int, q sentQuery, w http.ResponseWriter) {
		write(w, 200, `{"totalRecords":5000,"count":1000,"resultTruncated":"true","data":[]}`)
	})
	_, err := c.Search(context.Background(), sub1, "est")
	var pe *PartialError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want *PartialError", err)
	}
}

func TestProviderAttrs(t *testing.T) {
	t.Run("azurerm", func(t *testing.T) {
		val := cty.ObjectVal(map[string]cty.Value{
			"subscription_id": cty.StringVal(sub1),
			"tenant_id":       cty.StringVal("tenant"),
			"client_id":       cty.StringVal("app"),
			"client_secret":   cty.StringVal("s3cret").Mark(testSensitive),
			"use_cli":         cty.False,
			"use_msi":         cty.NullVal(cty.Bool),
			"environment":     cty.StringVal("china"),
			"metadata_host":   cty.NullVal(cty.String),
		})
		a := ProviderAttrs(ProviderAzureRM, val, true)
		if a.SubscriptionID != sub1 || a.TenantID != "tenant" || a.ClientID != "app" || a.ClientSecret != "s3cret" || a.Environment != "china" {
			t.Errorf("attrs = %+v", a)
		}
		if a.UseCLI == nil || *a.UseCLI || a.UseMSI != nil || len(a.Marked) != 0 {
			t.Errorf("switches = %v %v, marked %v", a.UseCLI, a.UseMSI, a.Marked)
		}
	})
	t.Run("marked identity is refused", func(t *testing.T) {
		val := cty.ObjectVal(map[string]cty.Value{
			"subscription_id": cty.StringVal(sub1).Mark(testSensitive),
		})
		a := ProviderAttrs(ProviderAzureRM, val, true)
		if a.SubscriptionID != "" || !reflect.DeepEqual(a.Marked, []string{"subscription_id"}) {
			t.Fatalf("attrs = %+v", a)
		}
		if got := a.RefusedMarks(); !reflect.DeepEqual(got, []string{"subscription_id"}) {
			t.Errorf("RefusedMarks = %v, want subscription_id; a credential built from these arguments is refused by name (internal/live/sweepconfig)", got)
		}
	})
	t.Run("azapi endpoint block", func(t *testing.T) {
		val := cty.ObjectVal(map[string]cty.Value{
			"subscription_id": cty.StringVal(sub1),
			"endpoint": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
				"resource_manager_endpoint":       cty.StringVal("https://arm.example/"),
				"resource_manager_audience":       cty.StringVal("https://arm.example/"),
				"active_directory_authority_host": cty.NullVal(cty.String),
			})}),
			"oidc_azure_service_connection_id": cty.StringVal("conn"),
		})
		a := ProviderAttrs(ProviderAzAPI, val, true)
		if a.ResourceManagerEndpoint != "https://arm.example/" || a.ResourceManagerAudience != "https://arm.example/" || a.ADOServiceConnectionID != "conn" {
			t.Errorf("attrs = %+v", a)
		}
		marked := cty.ObjectVal(map[string]cty.Value{"endpoint": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"resource_manager_endpoint": cty.StringVal("x"), "resource_manager_audience": cty.NullVal(cty.String), "active_directory_authority_host": cty.NullVal(cty.String),
		}).Mark(testSensitive)})})
		if a := ProviderAttrs(ProviderAzAPI, marked, true); !reflect.DeepEqual(a.Marked, []string{"endpoint"}) {
			t.Errorf("marked endpoint attrs = %+v", a)
		}
	})
	t.Run("null block", func(t *testing.T) {
		if a := ProviderAttrs(ProviderAzureRM, cty.NullVal(cty.EmptyObject), true); a.SubscriptionID != "" || a.Provider != ProviderAzureRM {
			t.Errorf("attrs = %+v", a)
		}
	})
}

func TestWithEnv(t *testing.T) {
	env := map[string]string{
		"ARM_SUBSCRIPTION_ID":           sub2,
		"ARM_TENANT_ID":                 "env-tenant",
		"AZURE_CLIENT_ID":               "azure-client",
		"ARM_USE_MSI":                   "true",
		"ARM_USE_CLI":                   "false",
		"ARM_METADATA_HOSTNAME":         "meta.example",
		"ARM_RESOURCE_MANAGER_ENDPOINT": "https://arm.example/",
	}
	getenv := func(k string) string { return env[k] }

	rm := Attrs{Provider: ProviderAzureRM, SubscriptionID: sub1}.WithEnv(getenv)
	if rm.SubscriptionID != sub1 || rm.TenantID != "env-tenant" || rm.ClientID != "" || rm.MetadataHost != "meta.example" || rm.ResourceManagerEndpoint != "" {
		t.Errorf("azurerm = %+v", rm)
	}
	if !on(rm.UseMSI, false) || on(rm.UseCLI, true) {
		t.Errorf("azurerm switches: msi %v cli %v", rm.UseMSI, rm.UseCLI)
	}
	api := Attrs{Provider: ProviderAzAPI}.WithEnv(getenv)
	if api.SubscriptionID != sub2 || api.ClientID != "azure-client" || api.MetadataHost != "" || api.ResourceManagerEndpoint != "https://arm.example/" {
		t.Errorf("azapi = %+v", api)
	}
	marked := Attrs{Provider: ProviderAzureRM, Marked: []string{"subscription_id"}}.WithEnv(getenv)
	if marked.SubscriptionID != "" {
		t.Errorf("a marked argument was filled from the environment: %+v", marked)
	}
	if def := (Attrs{}).WithEnv(func(string) string { return "" }); !on(def.UseCLI, true) || on(def.UseMSI, false) {
		t.Errorf("defaults: cli %v msi %v", def.UseCLI, def.UseMSI)
	}
}

func TestThrottleWait(t *testing.T) {
	for name, tc := range map[string]struct {
		h    http.Header
		want time.Duration
	}{
		"retry-after seconds": {http.Header{"Retry-After": {"7"}}, 7 * time.Second},
		"retry-after wins":    {http.Header{"Retry-After": {"7"}, "X-Ms-User-Quota-Resets-After": {"00:00:02"}}, 7 * time.Second},
		"quota reset":         {http.Header{"X-Ms-User-Quota-Resets-After": {"00:01:02"}}, 62 * time.Second},
		"fractional seconds":  {http.Header{"X-Ms-User-Quota-Resets-After": {"00:00:01.5"}}, 1500 * time.Millisecond},
		"none":                {http.Header{}, defaultThrottleWait},
		"garbage":             {http.Header{"Retry-After": {"soon"}, "X-Ms-User-Quota-Resets-After": {"later"}}, defaultThrottleWait},
	} {
		if got := throttleWait(tc.h); got != tc.want {
			t.Errorf("%s: throttleWait = %v, want %v", name, got, tc.want)
		}
	}
}

func TestClassify(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"unregistered, detail":  {409, `{"error":{"code":"Conflict","details":[{"code":"SubscriptionNotRegistered"}]}}`, SummaryResourceGraphNotRegistered},
		"unregistered, casing":  {409, `{"error":{"code":"missingsubscriptionregistration"}}`, SummaryResourceGraphNotRegistered},
		"subscription missing":  {404, `{"error":{"code":"SubscriptionNotFound","message":"The subscription could not be found."}}`, SummarySubscriptionUnreadable},
		"wrong tenant":          {401, `{"error":{"code":"InvalidAuthenticationTokenTenant"}}`, SummarySubscriptionUnreadable},
		"bad token":             {401, `{"error":{"code":"InvalidAuthenticationToken"}}`, SummarySweepUnavailable},
		"bad query":             {400, `{"error":{"code":"BadRequest","details":[{"code":"InvalidQuery"}]}}`, SummarySweepUnavailable},
		"server error, no body": {502, ``, SummarySweepUnavailable},
	} {
		if got := Summary(classify(sub1, Credentials{}, tc.status, []byte(tc.body))); got != tc.want {
			t.Errorf("%s: Summary = %q, want %q", name, got, tc.want)
		}
	}
}

// GitHub issue #2206: EnvEnvironment is the cloud a silent provider block
// gets, "public" by default, and nothing when a metadata host decides it.
func TestEnvEnvironment(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for name, c := range map[string]struct {
		env  map[string]string
		want string
	}{
		"unset is public":       {nil, "public"},
		"ARM_ENVIRONMENT wins":  {map[string]string{"ARM_ENVIRONMENT": "china"}, "china"},
		"metadata host decides": {map[string]string{"ARM_ENVIRONMENT": "china", "ARM_METADATA_HOSTNAME": "h.example.test"}, ""},
	} {
		if got := EnvEnvironment(env(c.env)); got != c.want {
			t.Errorf("%s: EnvEnvironment = %q, want %q", name, got, c.want)
		}
	}
}
