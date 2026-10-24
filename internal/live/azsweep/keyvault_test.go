// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package azsweep

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestKeyVaultScope(t *testing.T) {
	for host, want := range map[string]string{
		"v1.vault.azure.net":         "https://vault.azure.net/.default",
		"v1.vault.usgovcloudapi.net": "https://vault.usgovcloudapi.net/.default",
		"v1.vault.azure.cn":          "https://vault.azure.cn/.default",
	} {
		u, _ := url.Parse("https://" + host)
		if got := keyVaultScope(u); got != want {
			t.Errorf("%s: scope %q, want %q", host, got, want)
		}
	}
}

func TestKeyVaultListPagesAndBuildsRequests(t *testing.T) {
	var mu sync.Mutex
	var got []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.URL.RequestURI()+" auth="+r.Header.Get("Authorization"))
		mu.Unlock()
		if r.URL.Path != "/secrets" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `{"value":[{"id":"`+srv.URL+`/secrets/c","tags":{"tofu-estate":"e"}}]}`)
			return
		}
		fmt.Fprintf(w, `{"value":[
			{"id":"%[1]s/secrets/a","tags":{"tofu-estate":"e"}},
			{"id":"%[1]s/secrets/cert-backed","managed":true,"tags":{"tofu-estate":"e"}},
			{"id":"%[1]s/secrets/b"}],
			"nextLink":"%[1]s/secrets?api-version=7.4&maxresults=25&page=2"}`, srv.URL)
	}))
	defer srv.Close()

	cred := &fakeCred{}
	items, err := NewKeyVaultWith(cred, srv.Client()).List(context.Background(), srv.URL+"/", "secrets")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, it := range items {
		names = append(names, it.Name)
	}
	if strings.Join(names, ",") != "a,b,c" {
		t.Fatalf("names %v, want a,b,c (the managed secret left out, both pages read)", names)
	}
	if items[0].Tags["tofu-estate"] != "e" || items[0].ID != srv.URL+"/secrets/a" {
		t.Errorf("item 0 = %+v", items[0])
	}
	if len(got) != 2 || got[0] != "/secrets?api-version=7.4&maxresults=25 auth=Bearer fake-token" {
		t.Errorf("requests = %q", got)
	}
	if len(cred.scopes) == 0 || !strings.HasSuffix(cred.scopes[0], "/.default") {
		t.Errorf("scopes = %v", cred.scopes)
	}
}

func TestKeyVaultListKeysUseKID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"value":[{"kid":"https://v.vault.azure.net/keys/k1","tags":{"x":"y"}}]}`)
	}))
	defer srv.Close()
	items, err := NewKeyVaultWith(&fakeCred{}, srv.Client()).List(context.Background(), srv.URL, "keys")
	if err != nil || len(items) != 1 || items[0].Name != "k1" || items[0].ID != "https://v.vault.azure.net/keys/k1" {
		t.Fatalf("items %+v, err %v", items, err)
	}
}

func TestKeyVaultListRefusals(t *testing.T) {
	ctx := context.Background()
	if _, err := NewKeyVaultWith(&fakeCred{}, nil).List(ctx, "https://v.vault.azure.net", "hsm"); err == nil {
		t.Error("an unknown collection was listed")
	}
	if _, err := NewKeyVaultWith(&fakeCred{}, nil).List(ctx, "not a url", "keys"); err == nil {
		t.Error("a bad vault URL was listed")
	}

	t.Run("nextLink on another host is not followed", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"value":[],"nextLink":"https://evil.example/keys?x=1"}`)
		}))
		defer srv.Close()
		if _, err := NewKeyVaultWith(&fakeCred{}, srv.Client()).List(ctx, srv.URL, "keys"); err == nil || !strings.Contains(err.Error(), "not on") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a repeated nextLink ends paging with an error", func(t *testing.T) {
		var srv *httptest.Server
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"value":[],"nextLink":"%s/keys?loop=1"}`, srv.URL)
		}))
		defer srv.Close()
		if _, err := NewKeyVaultWith(&fakeCred{}, srv.Client()).List(ctx, srv.URL, "keys"); err == nil || !strings.Contains(err.Error(), "again") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a refusal on a later page returns no items", func(t *testing.T) {
		var srv *httptest.Server
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("p") == "2" {
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, `{"error":{"code":"Forbidden","message":"no list permission"}}`)
				return
			}
			fmt.Fprintf(w, `{"value":[{"kid":"%s/keys/a"}],"nextLink":"%s/keys?p=2"}`, srv.URL, srv.URL)
		}))
		defer srv.Close()
		items, err := NewKeyVaultWith(&fakeCred{}, srv.Client()).List(ctx, srv.URL, "keys")
		if err == nil || items != nil || !strings.Contains(err.Error(), "403") && !strings.Contains(err.Error(), "no list permission") {
			t.Errorf("items %v, err %v", items, err)
		}
	})
	t.Run("a 429 is waited out and retried", func(t *testing.T) {
		n := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n++
			if n == 1 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			fmt.Fprint(w, `{"value":[{"kid":"https://v/keys/a"}]}`)
		}))
		defer srv.Close()
		items, err := NewKeyVaultWith(&fakeCred{}, srv.Client()).List(ctx, srv.URL, "keys")
		if err != nil || len(items) != 1 || n != 2 {
			t.Errorf("items %v, err %v, calls %d", items, err, n)
		}
	})
}
