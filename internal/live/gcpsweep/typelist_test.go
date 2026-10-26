// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package gcpsweep

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeList is one family's service serving a list method: one scripted
// body per call in order, recording every request.
type fakeList struct {
	t        *testing.T
	mu       sync.Mutex
	path     string
	bodies   []fakeResponse
	requests []*http.Request
}

func (f *fakeList) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Clone(context.Background()))
	if r.Method != http.MethodGet || r.URL.Path != f.path {
		f.t.Errorf("unexpected request %s %s, want GET %s", r.Method, r.URL.Path, f.path)
		http.Error(w, "unexpected", http.StatusNotFound)
		return
	}
	if len(f.bodies) == 0 {
		f.t.Errorf("unscripted request %s", r.URL)
		http.Error(w, "unscripted", http.StatusInternalServerError)
		return
	}
	resp := f.bodies[0]
	f.bodies = f.bodies[1:]
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.status)
	_, _ = w.Write([]byte(resp.body))
}

// newListFake serves typeName's family at its custom endpoint argument,
// with the base path the way the provider writes it (a version suffix).
func newListFake(t *testing.T, typeName, path string, bodies ...fakeResponse) (*fakeList, *Client) {
	t.Helper()
	clearEnv(t)
	tl, ok := Covered(typeName)
	if !ok {
		t.Fatalf("%s is not covered", typeName)
	}
	f := &fakeList{t: t, path: path, bodies: bodies}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c, err := New(context.Background(), Attrs{AccessToken: "tok", Project: "p1", Region: "us-central1",
		Endpoints: map[string]string{tl.Endpoint.Arg: srv.URL + "/v1/"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return f, c
}

// TestListTypePerFamily is one list per covered family (#2264): the request
// goes to the type's own collection under the product's endpoint, with the
// bearer token and, where the method takes one, the label filter; every
// page is read; and an object whose own label is not exactly the estate
// is dropped even when the server returned it.
func TestListTypePerFamily(t *testing.T) {
	for _, tc := range []struct {
		family, typeName, location, path, field, name string
	}{
		{"Vertex AI", "google_vertex_ai_index_endpoint", "us-central1", "/v1/projects/p1/locations/us-central1/indexEndpoints", "indexEndpoints", "projects/p1/locations/us-central1/indexEndpoints/123"},
		{"Vertex AI", "google_vertex_ai_dataset", "europe-west4", "/v1/projects/p1/locations/europe-west4/datasets", "datasets", "projects/p1/locations/europe-west4/datasets/456"},
		{"Document AI", "google_document_ai_schema", "us", "/v1/projects/p1/locations/us/schemas", "schemas", "projects/p1/locations/us/schemas/789"},
		{"Transcoder", "google_transcoder_job", "us-central1", "/v1/projects/p1/locations/us-central1/jobs", "jobs", "projects/p1/locations/us-central1/jobs/j-1"},
		{"Service Directory", "google_service_directory_namespace", "us-east1", "/v1/projects/p1/locations/us-east1/namespaces", "namespaces", "projects/p1/locations/us-east1/namespaces/ns"},
		{"reCAPTCHA Enterprise", "google_recaptcha_enterprise_key", "", "/v1/projects/p1/keys", "keys", "projects/p1/keys/k-1"},
		{"Managed Microsoft AD", "google_active_directory_domain", "", "/v1/projects/p1/locations/global/domains", "domains", "projects/p1/locations/global/domains/ad.example.com"},
	} {
		t.Run(tc.typeName, func(t *testing.T) {
			page1 := fmt.Sprintf(`{"%s": [
  {"name": %q, "labels": {"tofu-estate": "est", "tofu-address": "a1"}, "createTime": "2026-10-25T10:00:00Z"},
  {"name": "%s-neighbour", "labels": {"tofu-estate": "est-east"}}
], "nextPageToken": "p2"}`, tc.field, tc.name, tc.name)
			page2 := fmt.Sprintf(`{"%s": [{"name": "%s-two", "labels": {"tofu-estate": "est"}}]}`, tc.field, tc.name)
			f, c := newListFake(t, tc.typeName, tc.path, fakeResponse{200, page1}, fakeResponse{200, page2})
			got, err := c.ListType(context.Background(), tc.typeName, ListScope{Project: "p1", Location: tc.location}, "est")
			if err != nil {
				t.Fatalf("%s list: %v", tc.family, err)
			}
			tl, _ := Covered(tc.typeName)
			if len(got) != 2 {
				t.Fatalf("got %d objects, want 2 (the neighbour estate's dropped): %+v", len(got), got)
			}
			if want := "//" + tl.Service + "/" + tc.name; got[0].Name != want || got[0].AssetType != tl.AssetType || got[0].Labels["tofu-address"] != "a1" {
				t.Errorf("first object = %+v, want name %s, asset type %s", got[0], want, tl.AssetType)
			}
			if len(f.requests) != 2 {
				t.Fatalf("%d requests, want 2 pages", len(f.requests))
			}
			for i, r := range f.requests {
				if r.Header.Get("Authorization") != "Bearer tok" {
					t.Errorf("request %d Authorization = %q", i, r.Header.Get("Authorization"))
				}
				if filter := r.URL.Query().Get("filter"); tl.Filter && filter != "labels.tofu-estate=est" || !tl.Filter && filter != "" {
					t.Errorf("request %d filter = %q (type takes a filter: %v)", i, filter, tl.Filter)
				}
			}
			if tok := f.requests[1].URL.Query().Get("pageToken"); tok != "p2" {
				t.Errorf("second request pageToken = %q, want p2", tok)
			}
		})
	}
}

// TestListTypeErrors: a failed list is a typed error naming the type's
// own service and permission, never an empty answer, and a failure after
// the first page discards what was read.
func TestListTypeErrors(t *testing.T) {
	const path = "/v1/projects/p1/locations/us-central1/indexEndpoints"
	t.Run("permission", func(t *testing.T) {
		_, c := newListFake(t, "google_vertex_ai_index_endpoint", path, fakeResponse{403, bodyPermissionDenied})
		_, err := c.ListType(context.Background(), "google_vertex_ai_index_endpoint", ListScope{Project: "p1", Location: "us-central1"}, "est")
		var denied *PermissionDeniedError
		if !errors.As(err, &denied) || denied.Permission != "aiplatform.indexEndpoints.list" {
			t.Errorf("err = %v, want a PermissionDeniedError naming aiplatform.indexEndpoints.list", err)
		}
	})
	t.Run("disabled", func(t *testing.T) {
		body := strings.ReplaceAll(strings.ReplaceAll(bodyServiceDisabled, "cloudasset.googleapis.com", "aiplatform.googleapis.com"), "Cloud Asset API", "Vertex AI API")
		_, c := newListFake(t, "google_vertex_ai_index_endpoint", path, fakeResponse{403, body})
		_, err := c.ListType(context.Background(), "google_vertex_ai_index_endpoint", ListScope{Project: "p1", Location: "us-central1"}, "est")
		var disabled *ServiceDisabledError
		if !errors.As(err, &disabled) || disabled.Service != "aiplatform.googleapis.com" {
			t.Errorf("err = %v, want a ServiceDisabledError naming aiplatform.googleapis.com", err)
		}
	})
	t.Run("partial", func(t *testing.T) {
		_, c := newListFake(t, "google_vertex_ai_index_endpoint", path,
			fakeResponse{200, `{"indexEndpoints": [{"name": "projects/p1/locations/us-central1/indexEndpoints/1", "labels": {"tofu-estate": "est"}}], "nextPageToken": "p2"}`},
			fakeResponse{400, bodyInvalidPageToken})
		got, err := c.ListType(context.Background(), "google_vertex_ai_index_endpoint", ListScope{Project: "p1", Location: "us-central1"}, "est")
		var partial *PartialError
		if !errors.As(err, &partial) || partial.PagesRead != 1 || got != nil {
			t.Errorf("got %v, err %v; want no objects and a PartialError after one page", got, err)
		}
	})
	t.Run("no location", func(t *testing.T) {
		_, c := newListFake(t, "google_transcoder_job", "/v1/unused")
		if _, err := c.ListType(context.Background(), "google_transcoder_job", ListScope{Project: "p1"}, "est"); err == nil {
			t.Error("a located type listed with no location returned no error")
		}
	})
	t.Run("not covered", func(t *testing.T) {
		_, c := newListFake(t, "google_transcoder_job", "/v1/unused")
		if _, err := c.ListType(context.Background(), "google_pubsub_topic", ListScope{Project: "p1"}, "est"); err == nil {
			t.Error("an uncovered type listed with no error")
		}
	})
}

// TestTypeListEndpoints: a product's base path is the block's custom
// endpoint, else its environment variable, and the default otherwise, with
// the location in place where the default carries one.
func TestTypeListEndpoints(t *testing.T) {
	clearEnv(t)
	tl, _ := Covered("google_vertex_ai_index")
	if got := (Attrs{}).endpointFor(tl.Endpoint); got != "https://{{region}}-aiplatform.googleapis.com/v1/" {
		t.Errorf("default = %q", got)
	}
	t.Setenv("GOOGLE_VERTEX_AI_CUSTOM_ENDPOINT", "http://env/v1/")
	if got := (Attrs{}).WithEnv(func(n string) string {
		if n == "GOOGLE_VERTEX_AI_CUSTOM_ENDPOINT" {
			return "http://env/v1/"
		}
		return ""
	}).endpointFor(tl.Endpoint); got != "http://env/v1/" {
		t.Errorf("from the environment = %q", got)
	}
	block := Attrs{Endpoints: map[string]string{"vertex_ai_custom_endpoint": "http://block/v1/"}}
	if got := block.WithEnv(func(string) string { return "http://env/v1/" }).endpointFor(tl.Endpoint); got != "http://block/v1/" {
		t.Errorf("the block's own endpoint lost to the environment: %q", got)
	}
	if _, err := New(context.Background(), Attrs{AccessToken: "t", Marked: []string{"vertex_ai_custom_endpoint"}}); err == nil {
		t.Error("a marked list endpoint did not refuse the client")
	}
}
