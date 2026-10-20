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
	tg := NewTaggerWith(Endpoints{ResourceManager: srv.URL + "/", Audience: "https://management.core.windows.net/"}, cred, srv.Client())
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
	tg := NewTaggerWith(Endpoints{ResourceManager: srv.URL, Audience: "https://management.core.windows.net/"}, &fakeCred{}, srv.Client())
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
