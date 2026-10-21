// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"

	"github.com/intentius/choudoufu/internal/backend"
	"github.com/intentius/choudoufu/internal/backend/remote-state/gcs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/encryption"
	"github.com/intentius/choudoufu/internal/live/projection"
)

// stockGCSSchema is stock's gcs backend's argument schema, read from stock's
// own package. This file imports it from the outside, which is the only
// direction the import may go (see gcs_store.go's gcsConnection).
func stockGCSSchema(t *testing.T) *gcs.Backend {
	t.Helper()
	b, ok := gcs.New(encryption.StateEncryptionDisabled()).(*gcs.Backend)
	if !ok {
		t.Fatal("gcs.New no longer returns a *gcs.Backend")
	}
	return b
}

// TestGCSStoreTakesStocksArguments: record_store "gcs" takes every argument
// of stock's gcs backend except "prefix", which every record store spells
// "key_prefix", and nothing stock does not have. An argument stock adds on an
// upstream sync fails this until the record store takes it or says why not.
func TestGCSStoreTakesStocksArguments(t *testing.T) {
	var stock []string
	for name := range stockGCSSchema(t).Schema {
		if name == "bucket" || name == "prefix" {
			continue
		}
		stock = append(stock, name)
	}
	ours := slices.Clone(configs.RecordStoreGCSSettings)
	sort.Strings(stock)
	sort.Strings(ours)
	if !slices.Equal(stock, ours) {
		t.Errorf("stock gcs takes %v besides bucket and prefix; record_store \"gcs\" takes %v", stock, ours)
	}
}

// TestGCSStoreDefaultsFromStocksEnvironment: every argument stock's schema
// defaults from the environment is defaulted from the same variables in the
// same order here. Each variable is set to its own value, then unset one at a
// time from the front, so an order that differs from stock's shows.
func TestGCSStoreDefaultsFromStocksEnvironment(t *testing.T) {
	schema := stockGCSSchema(t).Schema
	for name, s := range schema {
		if s.DefaultFunc == nil {
			continue
		}
		if _, ok := projection.GCSEnvDefaultsForTest[name]; !ok {
			t.Errorf("stock defaults %q from the environment and record_store \"gcs\" does not", name)
		}
	}
	for name, vars := range projection.GCSEnvDefaultsForTest {
		s, ok := schema[name]
		if !ok || s.DefaultFunc == nil {
			t.Errorf("record_store \"gcs\" defaults %q from %v and stock does not default it at all", name, vars)
			continue
		}
		for _, v := range vars {
			t.Setenv(v, "")
		}
		for i := range vars {
			for j, v := range vars {
				if j >= i {
					t.Setenv(v, fmt.Sprintf("%s-%d", v, j))
				}
			}
			want, err := s.DefaultFunc()
			if err != nil {
				t.Fatal(err)
			}
			got := projection.GCSConnectionEnvForTest(&configs.LiveRecordStore{Type: "gcs"})[name]
			if got != want {
				t.Errorf("%s with %v set from index %d: record_store reads %q, stock reads %v", name, vars, i, got, want)
			}
			t.Setenv(vars[i], "")
		}
	}
}

// GitHub issue #2134 item 10: the test above holds the environment defaults
// to stock's. This one holds the choice made once the values are read: with
// an access token, credentials and Application Default Credentials all on
// hand, record_store "gcs" sends the one stock's gcs backend sends, and
// impersonates over the same one.
//
// Stock's choice is in its unexported configure method
// (internal/backend/remote-state/gcs/backend.go, the access_token,
// credentials, GOOGLE_BACKEND_CREDENTIALS, GOOGLE_CREDENTIALS chain and the
// impersonate_service_account block after it), so it is driven through its
// exported surface: Configure, then Workspaces, which lists the bucket. The
// store is driven through newGCSClient and a listing. Both run in this
// process against gcsAuthServer, which every HTTPS connection in the test is
// dialled to whatever host it names, and which reports the identity each
// listing was authorised as. Nothing leaves the machine.

// gcsAuthServer answers the three Google endpoints a listing touches: the
// OAuth token endpoint (a service account's JWT exchanged for "sa:<email>"),
// IAM Credentials' generateAccessToken ("imp:<target>|<caller>"), and the
// Cloud Storage listing, whose caller it records.
type gcsAuthServer struct {
	mu      sync.Mutex
	listers []string
}

// gcsCaller names who an Authorization header is: a service account's
// self-signed JWT or exchanged token by its email, anything else as sent.
func gcsCaller(header string) string {
	tok := strings.TrimPrefix(header, "Bearer ")
	if iss, ok := gcsJWTIssuer(tok); ok {
		return "sa:" + iss
	}
	return tok
}

func gcsJWTIssuer(jwt string) (string, bool) {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	var claims struct {
		Iss string `json:"iss"`
	}
	if json.Unmarshal(raw, &claims) != nil || claims.Iss == "" {
		return "", false
	}
	return claims.Iss, true
}

func (s *gcsAuthServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/token"):
		_ = r.ParseForm()
		iss, ok := gcsJWTIssuer(r.Form.Get("assertion"))
		if !ok {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "sa:" + iss, "token_type": "Bearer", "expires_in": 3600})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, ":generateAccessToken"):
		target := strings.TrimSuffix(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], ":generateAccessToken")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accessToken": "imp:" + target + "|" + gcsCaller(r.Header.Get("Authorization")),
			"expireTime":  time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/storage/v1/b/"):
		s.mu.Lock()
		s.listers = append(s.listers, gcsCaller(r.Header.Get("Authorization")))
		s.mu.Unlock()
		_, _ = w.Write([]byte(`{"kind":"storage#objects","items":[]}`))
	default:
		http.Error(w, "unexpected "+r.Method+" "+r.Host+r.URL.Path, http.StatusNotFound)
	}
}

// lastLister is who the most recent listing was authorised as.
func (s *gcsAuthServer) lastLister(t *testing.T) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.listers) == 0 {
		t.Fatal("no listing reached the server")
	}
	return s.listers[len(s.listers)-1]
}

// gcsServiceAccountJSON is a service account key file for email, signed by
// key, whose token endpoint is Google's own (gcsAuthServer answers it).
func gcsServiceAccountJSON(t *testing.T, key *rsa.PrivateKey, email string) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]string{
		"type":           "service_account",
		"project_id":     "p",
		"private_key_id": "k-" + email,
		"private_key":    string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email":   email,
		"client_id":      "1",
		"token_uri":      "https://oauth2.googleapis.com/token",
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// gcsInterceptHTTPS points every connection this process opens at srv until
// the test ends: http.DefaultTransport dials srv for any address and trusts
// its certificate. The Google client libraries clone that transport, dialler
// and TLS settings included, for the clients they build.
func gcsInterceptHTTPS(t *testing.T, srv *httptest.Server) {
	t.Helper()
	addr := srv.Listener.Addr().String()
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	prev := http.DefaultTransport
	http.DefaultTransport = &http.Transport{
		DialContext:     dial,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // a local test server standing in for Google
	}
	t.Cleanup(func() { http.DefaultTransport = prev })
}

func TestGCSStoreChoosesStocksCredential(t *testing.T) {
	srv := httptest.NewTLSServer(&gcsAuthServer{})
	t.Cleanup(srv.Close)
	auth := srv.Config.Handler.(*gcsAuthServer)
	gcsInterceptHTTPS(t, srv)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	sa := func(email string) string { return gcsServiceAccountJSON(t, key, email) }

	// Application Default Credentials are on hand in every case.
	adc := filepath.Join(t.TempDir(), "adc.json")
	if err := os.WriteFile(adc, []byte(sa("adc@p.iam.gserviceaccount.com")), 0o600); err != nil {
		t.Fatal(err)
	}

	const target = "deployer@p.iam.gserviceaccount.com"
	cases := []struct {
		name                     string
		accessToken, credentials string
		impersonate              string
		env                      map[string]string
		want                     string
	}{
		{name: "access token over credentials and ADC", accessToken: "static-token", credentials: sa("arg@p.iam.gserviceaccount.com"),
			env:  map[string]string{"GOOGLE_BACKEND_CREDENTIALS": sa("backend-env@p.iam.gserviceaccount.com"), "GOOGLE_CREDENTIALS": sa("env@p.iam.gserviceaccount.com")},
			want: "static-token"},
		{name: "access token from the environment over credentials", credentials: sa("arg@p.iam.gserviceaccount.com"),
			env:  map[string]string{"GOOGLE_OAUTH_ACCESS_TOKEN": "env-token"},
			want: "env-token"},
		{name: "credentials over the environment's and ADC", credentials: sa("arg@p.iam.gserviceaccount.com"),
			env:  map[string]string{"GOOGLE_BACKEND_CREDENTIALS": sa("backend-env@p.iam.gserviceaccount.com"), "GOOGLE_CREDENTIALS": sa("env@p.iam.gserviceaccount.com")},
			want: "sa:arg@p.iam.gserviceaccount.com"},
		{name: "GOOGLE_BACKEND_CREDENTIALS over GOOGLE_CREDENTIALS",
			env:  map[string]string{"GOOGLE_BACKEND_CREDENTIALS": sa("backend-env@p.iam.gserviceaccount.com"), "GOOGLE_CREDENTIALS": sa("env@p.iam.gserviceaccount.com")},
			want: "sa:backend-env@p.iam.gserviceaccount.com"},
		{name: "GOOGLE_CREDENTIALS over ADC",
			env:  map[string]string{"GOOGLE_CREDENTIALS": sa("env@p.iam.gserviceaccount.com")},
			want: "sa:env@p.iam.gserviceaccount.com"},
		{name: "ADC when nothing else is set",
			want: "sa:adc@p.iam.gserviceaccount.com"},

		{name: "impersonation over the access token", accessToken: "static-token", credentials: sa("arg@p.iam.gserviceaccount.com"), impersonate: target,
			want: "imp:" + target + "|static-token"},
		{name: "impersonation over credentials", credentials: sa("arg@p.iam.gserviceaccount.com"), impersonate: target,
			env:  map[string]string{"GOOGLE_CREDENTIALS": sa("env@p.iam.gserviceaccount.com")},
			want: "imp:" + target + "|sa:arg@p.iam.gserviceaccount.com"},
		{name: "impersonation from the environment over ADC",
			env:  map[string]string{"GOOGLE_BACKEND_IMPERSONATE_SERVICE_ACCOUNT": target, "GOOGLE_IMPERSONATE_SERVICE_ACCOUNT": "other@p.iam.gserviceaccount.com"},
			want: "imp:" + target + "|sa:adc@p.iam.gserviceaccount.com"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, v := range []string{
				"GOOGLE_OAUTH_ACCESS_TOKEN", "GOOGLE_BACKEND_CREDENTIALS", "GOOGLE_CREDENTIALS",
				"GOOGLE_BACKEND_IMPERSONATE_SERVICE_ACCOUNT", "GOOGLE_IMPERSONATE_SERVICE_ACCOUNT",
				"GOOGLE_BACKEND_STORAGE_CUSTOM_ENDPOINT", "GOOGLE_STORAGE_CUSTOM_ENDPOINT", "GOOGLE_BACKEND_UNIVERSE_DOMAIN",
				"GOOGLE_CLOUD_UNIVERSE_DOMAIN", "STORAGE_EMULATOR_HOST", "GOOGLE_ENCRYPTION_KEY", "GOOGLE_KMS_ENCRYPTION_KEY",
			} {
				t.Setenv(v, "")
			}
			t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", adc)
			t.Setenv("GOOGLE_API_USE_CLIENT_CERTIFICATE", "false")
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()

			// Stock, through its exported surface.
			raw := map[string]interface{}{"bucket": "records"}
			if tc.accessToken != "" {
				raw["access_token"] = tc.accessToken
			}
			if tc.credentials != "" {
				raw["credentials"] = tc.credentials
			}
			if tc.impersonate != "" {
				raw["impersonate_service_account"] = tc.impersonate
			}
			b := backend.TestBackendConfig(t, gcs.New(encryption.StateEncryptionDisabled()), backend.TestWrapConfig(raw))
			if _, err := b.Workspaces(ctx); err != nil {
				t.Fatalf("stock's listing: %v", err)
			}
			stock := auth.lastLister(t)

			// The record store, through its own client.
			rs := &configs.LiveRecordStore{Type: "gcs", GCS: configs.LiveRecordStoreGCS{
				AccessToken: tc.accessToken, Credentials: tc.credentials, ImpersonateServiceAccount: tc.impersonate,
			}}
			client, err := projection.GCSClientForTest(ctx, rs, nil)
			if err != nil {
				t.Fatalf("the store's client: %v", err)
			}
			defer client.Close()
			if _, err := client.Bucket("records").Objects(ctx, &storage.Query{}).Next(); err != iterator.Done {
				t.Fatalf("the store's listing: %v", err)
			}
			ours := auth.lastLister(t)

			if stock != tc.want {
				t.Errorf("stock listed as %q, want %q: the case no longer describes stock", stock, tc.want)
			}
			if ours != stock {
				t.Errorf("record_store \"gcs\" listed as %q; stock's gcs backend listed as %q", ours, stock)
			}
		})
	}
}
