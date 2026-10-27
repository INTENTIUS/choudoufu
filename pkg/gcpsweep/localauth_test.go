// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package gcpsweep

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLocalEndpointsNeverReadACredential: a block pointed at a loopback
// endpoint (an emulator run) never authenticates through a credentials
// file or Application Default Credentials, either of which would fetch its
// token from Google. The fake's own /token is never asked, the search
// carries no Authorization, and an access_token is still sent as given.
func TestLocalEndpointsNeverReadACredential(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	keyFile := func(tokenURI string) string {
		b, _ := json.Marshal(map[string]string{
			"type": "service_account", "project_id": "p1", "private_key_id": "k1", "private_key": pemKey,
			"client_email": "sa@p1.iam.gserviceaccount.com", "client_id": "1", "token_uri": tokenURI,
		})
		return string(b)
	}
	for _, tc := range []struct {
		name       string
		attrs      func(srv string) Attrs
		adc        bool
		wantBearer string
		wantSource CredentialSource
	}{
		{"credentials JSON is not read", func(srv string) Attrs { return Attrs{Credentials: keyFile(srv + "/token")} }, false, "", SourceNone},
		{"ADC is not looked up", func(string) Attrs { return Attrs{} }, true, "", SourceNone},
		{"access_token is sent as given", func(string) Attrs { return Attrs{AccessToken: "given"} }, true, "Bearer given", SourceAccessToken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			s := &authServer{t: t}
			srv := httptest.NewServer(s)
			defer srv.Close()
			if tc.adc {
				p := filepath.Join(t.TempDir(), "adc.json")
				if err := os.WriteFile(p, []byte(keyFile(srv.URL+"/token")), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", p)
			}
			a := tc.attrs(srv.URL)
			a.CloudAssetEndpoint = srv.URL + "/v1/"
			c, err := newProviderClient(context.Background(), a)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if got := c.Credentials().Source; got != tc.wantSource {
				t.Errorf("credential source %q, want %q", got, tc.wantSource)
			}
			if _, err := c.Search(context.Background(), "p1", "est"); err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(s.bearer) != 1 || s.bearer[0] != tc.wantBearer {
				t.Errorf("search Authorization %q, want %q", s.bearer, tc.wantBearer)
			}
		})
	}

	t.Run("impersonation through a public IAM endpoint is refused", func(t *testing.T) {
		clearEnv(t)
		_, _, err := HTTPClient(context.Background(), Attrs{AccessToken: "t", ImpersonateServiceAccount: "sa@p.iam.gserviceaccount.com", CloudAssetEndpoint: "http://127.0.0.1:1/v1/"})
		if err == nil || !strings.Contains(err.Error(), "iam_credentials_custom_endpoint") {
			t.Errorf("err = %v, want a refusal naming iam_credentials_custom_endpoint", err)
		}
	})

	t.Run("a product left at its public default is not called", func(t *testing.T) {
		clearEnv(t)
		tl, _ := Covered("google_vertex_ai_index_endpoint")
		c, err := newProviderClient(context.Background(), Attrs{AccessToken: "t", Project: "p1", Region: "us-central1",
			Endpoints: map[string]string{tl.Endpoint.Arg: "http://127.0.0.1:1/v1/"}})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, err := c.Search(context.Background(), "p1", "est"); err == nil || !strings.Contains(err.Error(), "public endpoint") {
			t.Errorf("Search err = %v, want a refusal to call Cloud Asset's public endpoint", err)
		}
	})
}

func TestIsLoopback(t *testing.T) {
	for in, want := range map[string]bool{
		"http://localhost:4588/v1/":           true,
		"http://127.0.0.1:4588":               true,
		"https://[::1]:443/":                  true,
		"localhost:4588":                      true,
		"http://floci.localhost:1/":           true,
		"https://cloudasset.googleapis.com/":  false,
		"https://10.0.0.1/":                   false,
		"http://host.docker.internal:4588/v1": false,
	} {
		if got := IsLoopback(in); got != want {
			t.Errorf("IsLoopback(%q) = %v, want %v", in, got, want)
		}
	}
}
