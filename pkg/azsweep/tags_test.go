// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package azsweep

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// TestTaggerMergesOntoTheScope is the post-create marker write against a
// fake ARM: one PATCH to the object's tags/default child, at the Tags API
// version, with a bearer token for the Resource Manager audience, and a
// Merge body carrying exactly the markers.
func TestTaggerMergesOntoTheScope(t *testing.T) {
	const id = "/subscriptions/" + sub1 + "/resourceGroups/rg/providers/Microsoft.Web/certificates/c"
	var got struct {
		method, path, query, auth string
		body                      map[string]any
	}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		got.method, got.path, got.query, got.auth = r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization")
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &got.body)
		_, _ = w.Write([]byte(`{"id":"` + id + `/providers/Microsoft.Resources/tags/default","properties":{"tags":{}}}`))
	}))
	defer srv.Close()

	cred := &fakeCred{}
	tg := newTaggerWith(Endpoints{ResourceManager: srv.URL + "/", Audience: "https://management.core.windows.net/"}, cred, srv.Client())
	markers := map[string]string{"tofu-estate": "prod", "tofu-address": "azurerm_app_service_managed_certificate.c"}
	if err := tg.MergeTags(t.Context(), id, markers); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || got.method != http.MethodPatch {
		t.Fatalf("%d calls, method %s; want one PATCH", calls, got.method)
	}
	if want := id + "/providers/Microsoft.Resources/tags/default"; got.path != want {
		t.Errorf("path %s, want %s", got.path, want)
	}
	if got.query != "api-version="+TagsAPIVersion || got.auth != "Bearer fake-token" {
		t.Errorf("query %q, auth %q", got.query, got.auth)
	}
	if !reflect.DeepEqual(cred.scopes, []string{"https://management.core.windows.net/.default"}) {
		t.Errorf("token scopes %v", cred.scopes)
	}
	want := map[string]any{"operation": "Merge", "properties": map[string]any{"tags": map[string]any{
		"tofu-estate": "prod", "tofu-address": "azurerm_app_service_managed_certificate.c",
	}}}
	if !reflect.DeepEqual(got.body, want) {
		t.Errorf("body %v, want %v", got.body, want)
	}
}

// TestTaggerReportsARMErrors: a Policy deny (the boundary template's marker
// guard refusing the write) comes back as an error naming the status, the
// code, ARM's own message and the object, never as success.
func TestTaggerReportsARMErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"RequestDisallowedByPolicy","message":"Resource 'c' was disallowed by policy."}}`))
	}))
	defer srv.Close()
	tg := newTaggerWith(Endpoints{ResourceManager: srv.URL, Audience: "https://management.core.windows.net/"}, &fakeCred{}, srv.Client())
	err := tg.MergeTags(t.Context(), "/subscriptions/"+sub1+"/resourceGroups/rg/providers/Microsoft.Web/certificates/c", map[string]string{"tofu-estate": "prod"})
	if err == nil {
		t.Fatal("a 403 read as success")
	}
	for _, want := range []string{"403", "RequestDisallowedByPolicy", "disallowed by policy", "certificates/c"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
	if err := tg.MergeTags(t.Context(), "https://kv.vault.azure.net/secrets/s", nil); err == nil {
		t.Error("a data-plane URL was accepted as an ARM id")
	}
}

// TestTaggerNeverReplacesTheOperatorsTags: the post-create write leaves
// every tag the object already carries as it is. The fake ARM applies the
// Tags API's three documented operations to a stored tag set (Merge adds and
// replaces the named keys, matching case-insensitively and keeping the
// stored spelling; Replace swaps the whole set; Delete removes named keys)
// and treats a PUT to tags/default, Create Or Update At Scope, as the
// replacement it is. Whatever the Tagger sends, the operator's tags must
// survive it, and a marker the object already spells differently must be
// updated in place rather than added beside it.
func TestTaggerNeverReplacesTheOperatorsTags(t *testing.T) {
	const id = "/subscriptions/" + sub1 + "/resourceGroups/rg/providers/Microsoft.Web/certificates/c"
	stored := map[string]string{"env": "prod", "owner": "team-a", "Tofu-Estate": "prod"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body tagsPatch
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		op := body.Operation
		if r.Method == http.MethodPut {
			op = "Replace"
		}
		switch op {
		case "Merge":
			for k, v := range body.Properties.Tags {
				key := k
				for have := range stored {
					if strings.EqualFold(have, k) {
						key = have
					}
				}
				stored[key] = v
			}
		case "Replace":
			stored = body.Properties.Tags
		case "Delete":
			for k := range body.Properties.Tags {
				delete(stored, k)
			}
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	tg := newTaggerWith(Endpoints{ResourceManager: srv.URL, Audience: "https://management.core.windows.net/"}, &fakeCred{}, srv.Client())
	if err := tg.MergeTags(t.Context(), id, map[string]string{"tofu-estate": "prod", "tofu-address": "azurerm_app_service_managed_certificate.c"}); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"env": "prod", "owner": "team-a", "Tofu-Estate": "prod", "tofu-address": "azurerm_app_service_managed_certificate.c"}
	if !reflect.DeepEqual(stored, want) {
		t.Errorf("the object's tags after the write are %v, want %v", stored, want)
	}
}

// EnvSubscriptionID is WithEnv's subscription for a block that sets none:
// ARM_SUBSCRIPTION_ID for either provider, and nothing else (GitHub issue
// #2133, finding 4).
func TestEnvSubscriptionIDIsWithEnvsAnswer(t *testing.T) {
	env := map[string]string{"ARM_SUBSCRIPTION_ID": "sub-env", "AZURE_SUBSCRIPTION_ID": "not-read"}
	getenv := func(k string) string { return env[k] }
	if got := EnvSubscriptionID(getenv); got != "sub-env" {
		t.Errorf("EnvSubscriptionID = %q, want sub-env", got)
	}
	for _, p := range []string{ProviderAzureRM, ProviderAzAPI} {
		if got := (Attrs{Provider: p}).WithEnv(getenv).SubscriptionID; got != EnvSubscriptionID(getenv) {
			t.Errorf("%s WithEnv = %q, EnvSubscriptionID = %q", p, got, EnvSubscriptionID(getenv))
		}
		if got := (Attrs{Provider: p, SubscriptionID: "sub-block"}).WithEnv(getenv).SubscriptionID; got != "sub-block" {
			t.Errorf("%s: the block's subscription lost to the environment: %q", p, got)
		}
	}
	if got := EnvSubscriptionID(func(string) string { return "" }); got != "" {
		t.Errorf("EnvSubscriptionID with nothing set = %q", got)
	}
}
