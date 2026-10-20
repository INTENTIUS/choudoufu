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
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/lang/marks"
)

// The response bodies below are the shapes testdata/README.md documents,
// each with the Google page it was taken from. Keep the two in step.

const bodyPage1 = `{
  "results": [
    {
      "name": "//compute.googleapis.com/projects/p1/global/networks/net-a",
      "assetType": "compute.googleapis.com/Network",
      "project": "projects/111111111111",
      "location": "global",
      "labels": {"tofu-estate": "est", "tofu-address": "google_compute_network.a"},
      "createTime": "2026-10-01T10:00:00Z",
      "updateTime": "2026-10-02T11:00:00Z"
    },
    {
      "name": "//storage.googleapis.com/bucket-b",
      "assetType": "storage.googleapis.com/Bucket",
      "project": "projects/111111111111",
      "location": "us-central1",
      "labels": {"tofu-estate": "est"}
    }
  ],
  "nextPageToken": "page-2"
}`

const bodyPage2 = `{
  "results": [
    {
      "name": "//pubsub.googleapis.com/projects/p1/topics/t",
      "assetType": "pubsub.googleapis.com/Topic",
      "project": "projects/111111111111",
      "location": "global",
      "labels": {"tofu-estate": "est"}
    },
    {
      "name": "//pubsub.googleapis.com/projects/p1/topics/neighbour",
      "assetType": "pubsub.googleapis.com/Topic",
      "project": "projects/111111111111",
      "location": "global",
      "labels": {"tofu-estate": "est-east"}
    }
  ]
}`

const bodyEmpty = `{}`

const bodyServiceDisabled = `{
  "error": {
    "code": 403,
    "message": "Cloud Asset API has not been used in project 111111111111 before or it is disabled. Enable it by visiting https://console.developers.google.com/apis/api/cloudasset.googleapis.com/overview?project=111111111111 then retry. If you enabled this API recently, wait a few minutes for the action to propagate to our systems and retry.",
    "status": "PERMISSION_DENIED",
    "details": [
      {
        "@type": "type.googleapis.com/google.rpc.ErrorInfo",
        "reason": "SERVICE_DISABLED",
        "domain": "googleapis.com",
        "metadata": {
          "service": "cloudasset.googleapis.com",
          "consumer": "projects/111111111111",
          "activationUrl": "https://console.developers.google.com/apis/api/cloudasset.googleapis.com/overview?project=111111111111",
          "serviceTitle": "Cloud Asset API",
          "containerInfo": "111111111111"
        }
      }
    ]
  }
}`

const bodyPermissionDenied = `{
  "error": {
    "code": 403,
    "message": "The caller does not have permission",
    "status": "PERMISSION_DENIED"
  }
}`

const bodyPermissionDeniedNamed = `{
  "error": {
    "code": 403,
    "message": "Permission 'cloudasset.assets.searchAllResources' denied on resource 'projects/p1' (or it may not exist).",
    "status": "PERMISSION_DENIED",
    "details": [
      {
        "@type": "type.googleapis.com/google.rpc.ErrorInfo",
        "reason": "IAM_PERMISSION_DENIED",
        "domain": "cloudasset.googleapis.com",
        "metadata": {
          "permission": "cloudasset.assets.searchAllResources",
          "resource": "projects/p1"
        }
      }
    ]
  }
}`

const bodyInvalidPageToken = `{
  "error": {
    "code": 400,
    "message": "Invalid page token.",
    "status": "INVALID_ARGUMENT"
  }
}`

// fakeAsset is a Cloud Asset API serving searchAllResources, one scripted
// response per call in order, recording every request it is sent.
type fakeAsset struct {
	t         *testing.T
	mu        sync.Mutex
	responses []fakeResponse
	requests  []*http.Request
}

type fakeResponse struct {
	status int
	body   string
}

func (f *fakeAsset) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Clone(context.Background()))
	if !strings.HasSuffix(r.URL.Path, ":searchAllResources") || r.Method != http.MethodGet {
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL)
		http.Error(w, "unexpected", http.StatusNotFound)
		return
	}
	if len(f.responses) == 0 {
		f.t.Errorf("unscripted request %s", r.URL)
		http.Error(w, "unscripted", http.StatusInternalServerError)
		return
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.status)
	_, _ = w.Write([]byte(resp.body))
}

// clearEnv blanks every variable the provider's environment fallbacks and
// Application Default Credentials read, so a developer's own shell cannot
// decide a test.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, n := range EnvNames() {
		t.Setenv(n, "")
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLOUDSDK_CONFIG", t.TempDir())
	// No metadata server: ADC must not wander off to 169.254.169.254.
	t.Setenv("GCE_METADATA_HOST", "127.0.0.1:1")
	t.Setenv("NO_GCE_CHECK", "True")
}

func newFake(t *testing.T, responses ...fakeResponse) (*fakeAsset, *Client) {
	t.Helper()
	clearEnv(t)
	f := &fakeAsset{t: t, responses: responses}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c, err := New(context.Background(), Attrs{AccessToken: "tok", Project: "p1", CloudAssetEndpoint: srv.URL + "/v1/"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return f, c
}

func TestSearchPagesUntilNoToken(t *testing.T) {
	f, c := newFake(t, fakeResponse{200, bodyPage1}, fakeResponse{200, bodyPage2})
	assets, err := c.Search(context.Background(), "p1", "est")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	var names []string
	for _, a := range assets {
		names = append(names, a.Name)
	}
	want := []string{
		"//compute.googleapis.com/projects/p1/global/networks/net-a",
		"//storage.googleapis.com/bucket-b",
		"//pubsub.googleapis.com/projects/p1/topics/t",
	}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("names\ngot:  %q\nwant: %q (the est-east neighbour dropped)", names, want)
	}
	first := assets[0]
	if first.AssetType != "compute.googleapis.com/Network" || first.Project != "projects/111111111111" ||
		first.Location != "global" || first.CreateTime != "2026-10-01T10:00:00Z" || first.UpdateTime != "2026-10-02T11:00:00Z" ||
		first.Labels["tofu-address"] != "google_compute_network.a" {
		t.Errorf("first asset read wrongly: %+v", first)
	}
	if assets[1].CreateTime != "" {
		t.Errorf("an absent createTime should read empty, got %q", assets[1].CreateTime)
	}

	if len(f.requests) != 2 {
		t.Fatalf("got %d requests, want 2", len(f.requests))
	}
	for i, r := range f.requests {
		if r.URL.Path != "/v1/projects/p1:searchAllResources" {
			t.Errorf("request %d path %q", i, r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("query") != "labels.tofu-estate=est" || q.Get("pageSize") != "500" || q.Get("readMask") != ReadMask {
			t.Errorf("request %d query %v", i, q)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("request %d Authorization %q", i, got)
		}
	}
	if f.requests[0].URL.Query().Has("pageToken") {
		t.Errorf("first request carries a pageToken: %v", f.requests[0].URL.Query())
	}
	if got := f.requests[1].URL.Query().Get("pageToken"); got != "page-2" {
		t.Errorf("second request pageToken %q, want page-2", got)
	}
}

func TestSearchEmptyIsEmpty(t *testing.T) {
	_, c := newFake(t, fakeResponse{200, bodyEmpty})
	assets, err := c.Search(context.Background(), "p1", "est")
	if err != nil || len(assets) != 0 {
		t.Fatalf("got %v, %v; want no assets and no error", assets, err)
	}
}

func TestSearchServiceDisabled(t *testing.T) {
	_, c := newFake(t, fakeResponse{403, bodyServiceDisabled})
	assets, err := c.Search(context.Background(), "p1", "est")
	if assets != nil {
		t.Errorf("assets %v alongside an error", assets)
	}
	var sd *ServiceDisabledError
	if !errors.As(err, &sd) {
		t.Fatalf("got %T %v, want *ServiceDisabledError", err, err)
	}
	if sd.Project != "p1" || sd.Service != Service || !strings.Contains(sd.ActivationURL, "cloudasset.googleapis.com/overview") {
		t.Errorf("read wrongly: %+v", sd)
	}
	var pd *PermissionDeniedError
	if errors.As(err, &pd) {
		t.Errorf("a disabled API also classed as permission denied")
	}
}

func TestSearchPermissionDenied(t *testing.T) {
	for name, body := range map[string]string{"bare": bodyPermissionDenied, "named": bodyPermissionDeniedNamed} {
		t.Run(name, func(t *testing.T) {
			_, c := newFake(t, fakeResponse{403, body})
			_, err := c.Search(context.Background(), "p1", "est")
			var pd *PermissionDeniedError
			if !errors.As(err, &pd) {
				t.Fatalf("got %T %v, want *PermissionDeniedError", err, err)
			}
			if pd.Permission != Permission || pd.Credentials.Source != SourceAccessToken {
				t.Errorf("read wrongly: %+v", pd)
			}
			if !strings.Contains(err.Error(), Permission) {
				t.Errorf("message does not name the permission: %v", err)
			}
		})
	}
}

func TestSearchFailureOnPageTwoIsPartial(t *testing.T) {
	for name, second := range map[string]fakeResponse{
		"invalid token": {400, bodyInvalidPageToken},
		"denied":        {403, bodyPermissionDenied},
	} {
		t.Run(name, func(t *testing.T) {
			_, c := newFake(t, fakeResponse{200, bodyPage1}, second)
			assets, err := c.Search(context.Background(), "p1", "est")
			if assets != nil {
				t.Errorf("page one's %d assets returned alongside the failure", len(assets))
			}
			var pe *PartialError
			if !errors.As(err, &pe) {
				t.Fatalf("got %T %v, want *PartialError", err, err)
			}
			if pe.PagesRead != 1 || pe.ResultsRead != 2 || pe.Project != "p1" {
				t.Errorf("read wrongly: %+v", pe)
			}
			var pd *PermissionDeniedError
			if errors.As(err, &pd) != (second.status == 403) {
				t.Errorf("errors.As through the partial: got %v for status %d", errors.As(err, &pd), second.status)
			}
		})
	}
}

func TestSearchRepeatedTokenIsPartial(t *testing.T) {
	_, c := newFake(t, fakeResponse{200, bodyPage1}, fakeResponse{200, bodyPage1})
	_, err := c.Search(context.Background(), "p1", "est")
	var pe *PartialError
	if !errors.As(err, &pe) || pe.PagesRead != 2 {
		t.Fatalf("got %v, want a partial after two pages", err)
	}
}

func TestSearchQuotaProjectHeader(t *testing.T) {
	clearEnv(t)
	f := &fakeAsset{t: t, responses: []fakeResponse{{200, bodyEmpty}, {200, bodyEmpty}}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	for i, a := range []Attrs{
		{AccessToken: "tok", UserProjectOverride: true, CloudAssetEndpoint: srv.URL},
		{AccessToken: "tok", UserProjectOverride: true, BillingProject: "billing", CloudAssetEndpoint: srv.URL},
	} {
		c, err := New(context.Background(), a)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Search(context.Background(), "p9", "est"); err != nil {
			t.Fatal(err)
		}
		want := []string{"p9", "billing"}[i]
		if got := f.requests[i].Header.Get("X-Goog-User-Project"); got != want {
			t.Errorf("case %d X-Goog-User-Project %q, want %q", i, got, want)
		}
	}
}

// stubSearcher answers Search from a map, recording the projects asked.
type stubSearcher struct {
	answers map[string][]Asset
	errs    map[string]error
	asked   []string
}

func (s *stubSearcher) Search(_ context.Context, project, _ string) ([]Asset, error) {
	s.asked = append(s.asked, project)
	if err := s.errs[project]; err != nil {
		return nil, err
	}
	return s.answers[project], nil
}

func TestSweepReportsUncoveredProjects(t *testing.T) {
	s := &stubSearcher{
		answers: map[string][]Asset{"a": {{Name: "//x/a"}}},
		errs:    map[string]error{"b": &ServiceDisabledError{Project: "b", Service: Service, Err: errors.New("off")}},
	}
	got := Sweep(context.Background(), Coverage{"a": s, "b": s}, []string{"a", "b", "c", "a"}, "est")
	if len(got) != 3 {
		t.Fatalf("got %d results, want 3 (one per distinct project): %+v", len(got), got)
	}
	if got[0].Project != "a" || got[0].Err != nil || len(got[0].Assets) != 1 {
		t.Errorf("a: %+v", got[0])
	}
	var sd *ServiceDisabledError
	if got[1].Project != "b" || !errors.As(got[1].Err, &sd) || got[1].Assets != nil {
		t.Errorf("b: %+v", got[1])
	}
	var un *UncoveredProjectError
	if got[2].Project != "c" || !errors.As(got[2].Err, &un) || un.Project != "c" {
		t.Errorf("c: %+v", got[2])
	}
	if !reflect.DeepEqual(s.asked, []string{"a", "b"}) {
		t.Errorf("asked %v, want [a b]", s.asked)
	}
}

func TestAuthPrecedence(t *testing.T) {
	cases := []struct {
		name string
		a    Attrs
		env  map[string]string
		want Credentials
	}{
		{"nothing is ADC", Attrs{}, nil, Credentials{Source: SourceADC}},
		{"credentials", Attrs{Credentials: "{}"}, nil, Credentials{Source: SourceCredentials}},
		{"access_token beats credentials", Attrs{Credentials: "{}", AccessToken: "t"}, nil, Credentials{Source: SourceAccessToken}},
		{"env token beats block credentials, as in the provider", Attrs{Credentials: "{}"}, map[string]string{"GOOGLE_OAUTH_ACCESS_TOKEN": "t"}, Credentials{Source: SourceAccessToken}},
		{"env credentials", Attrs{}, map[string]string{"GCLOUD_KEYFILE_JSON": "{}"}, Credentials{Source: SourceCredentials}},
		{"impersonation over ADC", Attrs{ImpersonateServiceAccount: "sa@p.iam.gserviceaccount.com", ImpersonateDelegates: []string{"d@p.iam.gserviceaccount.com"}}, nil,
			Credentials{Source: SourceADC, Impersonate: "sa@p.iam.gserviceaccount.com", Delegates: []string{"d@p.iam.gserviceaccount.com"}}},
		{"impersonation over a token", Attrs{AccessToken: "t"}, map[string]string{"GOOGLE_IMPERSONATE_SERVICE_ACCOUNT": "sa@p.iam.gserviceaccount.com"},
			Credentials{Source: SourceAccessToken, Impersonate: "sa@p.iam.gserviceaccount.com"}},
		{"a marked impersonation is not replaced from the env", Attrs{Marked: []string{"impersonate_service_account"}}, map[string]string{"GOOGLE_IMPERSONATE_SERVICE_ACCOUNT": "sa@p.iam.gserviceaccount.com"},
			Credentials{Source: SourceADC}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.a.WithEnv(func(n string) string { return tc.env[n] }).Auth()
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// authServer is a fake OAuth token endpoint, IAM Credentials API and
// Cloud Asset API in one, so that each credential source can be followed
// from the provider block to the Authorization header the search carries.
type authServer struct {
	t      *testing.T
	mu     sync.Mutex
	bearer []string // the Authorization of every search
	minted []string // the Authorization of every generateAccessToken
}

func (s *authServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/token":
		// A JWT-bearer grant: the assertion's payload names the key.
		_ = r.ParseForm()
		who := "unreadable"
		if parts := strings.Split(r.PostForm.Get("assertion"), "."); len(parts) == 3 {
			payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
			var claims struct {
				Iss string `json:"iss"`
			}
			if json.Unmarshal(payload, &claims) == nil {
				who, _, _ = strings.Cut(claims.Iss, "@")
			}
		}
		fmt.Fprintf(w, `{"access_token":"from-%s","token_type":"Bearer","expires_in":3600}`, who)
	case strings.HasSuffix(r.URL.Path, ":generateAccessToken"):
		s.minted = append(s.minted, r.Header.Get("Authorization"))
		var req struct {
			Scope     []string `json:"scope"`
			Delegates []string `json:"delegates"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if r.URL.Path != "/v1/projects/-/serviceAccounts/sa@p.iam.gserviceaccount.com:generateAccessToken" || len(req.Scope) == 0 {
			s.t.Errorf("generateAccessToken %s %+v", r.URL.Path, req)
		}
		fmt.Fprint(w, `{"accessToken":"impersonated","expireTime":"2099-01-01T00:00:00Z"}`)
	case strings.HasSuffix(r.URL.Path, ":searchAllResources"):
		s.bearer = append(s.bearer, r.Header.Get("Authorization"))
		fmt.Fprint(w, bodyEmpty)
	default:
		s.t.Errorf("unexpected %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestCredentialsReachTheRequest(t *testing.T) {
	// A service account key whose token_uri is the fake: the library
	// signs a JWT assertion with the key and exchanges it there, and the
	// fake mints a token named after the key's client_email.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	userCreds := func(srv, who string) string {
		b, _ := json.Marshal(map[string]string{
			"type": "service_account", "project_id": "p1", "private_key_id": "k1", "private_key": pemKey,
			"client_email": who + "@p1.iam.gserviceaccount.com", "client_id": "1", "token_uri": srv + "/token",
		})
		return string(b)
	}
	cases := []struct {
		name       string
		attrs      func(srv string, dir string) Attrs
		adc        func(srv string) string // GOOGLE_APPLICATION_CREDENTIALS contents, "" for none
		wantBearer string
		wantMinted string
	}{
		{
			name:       "access_token as given",
			attrs:      func(srv, _ string) Attrs { return Attrs{AccessToken: "given"} },
			wantBearer: "Bearer given",
		},
		{
			name: "access_token from a file",
			attrs: func(srv, dir string) Attrs {
				p := filepath.Join(dir, "token")
				_ = os.WriteFile(p, []byte("from-file\n"), 0o600)
				return Attrs{AccessToken: p}
			},
			wantBearer: "Bearer from-file",
		},
		{
			name:       "credentials JSON beats ADC",
			attrs:      func(srv, _ string) Attrs { return Attrs{Credentials: userCreds(srv, "block")} },
			adc:        func(srv string) string { return userCreds(srv, "adc") },
			wantBearer: "Bearer from-block",
		},
		{
			name: "credentials from a file",
			attrs: func(srv, dir string) Attrs {
				p := filepath.Join(dir, "key.json")
				_ = os.WriteFile(p, []byte(userCreds(srv, "file")), 0o600)
				return Attrs{Credentials: p}
			},
			wantBearer: "Bearer from-file",
		},
		{
			name:       "ADC when the block names nothing",
			attrs:      func(srv, _ string) Attrs { return Attrs{} },
			adc:        func(srv string) string { return userCreds(srv, "adc") },
			wantBearer: "Bearer from-adc",
		},
		{
			name: "impersonation mints through the base credential",
			attrs: func(srv, _ string) Attrs {
				return Attrs{AccessToken: "base", ImpersonateServiceAccount: "sa@p.iam.gserviceaccount.com", IAMCredentialsEndpoint: srv + "/v1/"}
			},
			wantBearer: "Bearer impersonated",
			wantMinted: "Bearer base",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			s := &authServer{t: t}
			srv := httptest.NewServer(s)
			defer srv.Close()
			dir := t.TempDir()
			if tc.adc != nil {
				p := filepath.Join(dir, "adc.json")
				if err := os.WriteFile(p, []byte(tc.adc(srv.URL)), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", p)
			}
			a := tc.attrs(srv.URL, dir)
			a.CloudAssetEndpoint = srv.URL + "/v1/"
			c, err := New(context.Background(), a)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := c.Search(context.Background(), "p1", "est"); err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(s.bearer) != 1 || s.bearer[0] != tc.wantBearer {
				t.Errorf("search Authorization %q, want %q", s.bearer, tc.wantBearer)
			}
			if tc.wantMinted != "" && (len(s.minted) != 1 || s.minted[0] != tc.wantMinted) {
				t.Errorf("generateAccessToken Authorization %q, want %q", s.minted, tc.wantMinted)
			}
		})
	}
}

func TestNewRefuses(t *testing.T) {
	clearEnv(t)
	for name, a := range map[string]Attrs{
		"marked impersonation":                {AccessToken: "t", Marked: []string{"impersonate_service_account"}},
		"marked endpoint":                     {AccessToken: "t", Marked: []string{"cloud_asset_custom_endpoint"}},
		"external_credentials":                {ExternalCredentials: true},
		"credentials not JSON":                {Credentials: "not json and not a file"},
		"external_credentials beside a token": {AccessToken: "t", ImpersonateServiceAccount: "sa@p.iam.gserviceaccount.com", ExternalCredentials: true},
	} {
		t.Run(name, func(t *testing.T) {
			if c, err := New(context.Background(), a); err == nil {
				t.Errorf("New built %+v, want a refusal", c)
			}
		})
	}
}

func TestProviderAttrs(t *testing.T) {
	val := cty.ObjectVal(map[string]cty.Value{
		"credentials":                           cty.StringVal(`{"type":"authorized_user"}`).Mark(marks.Sensitive),
		"access_token":                          cty.StringVal("tok").Mark(marks.Sensitive),
		"impersonate_service_account":           cty.StringVal("sa@p.iam.gserviceaccount.com").Mark(marks.Sensitive),
		"impersonate_service_account_delegates": cty.ListVal([]cty.Value{cty.StringVal("d@p.iam.gserviceaccount.com")}),
		"scopes":                                cty.NullVal(cty.List(cty.String)),
		"project":                               cty.StringVal("p1"),
		"region":                                cty.StringVal("us-central1").Mark(marks.Sensitive),
		"zone":                                  cty.UnknownVal(cty.String),
		"billing_project":                       cty.StringVal("bill"),
		"user_project_override":                 cty.True,
		"universe_domain":                       cty.NullVal(cty.String),
		"cloud_asset_custom_endpoint":           cty.StringVal("http://localhost:1/v1/"),
		"iam_credentials_custom_endpoint":       cty.NullVal(cty.String),
		"external_credentials":                  cty.ListValEmpty(cty.EmptyObject),
	})
	got := ProviderAttrs(val, true)
	want := Attrs{
		Credentials:          `{"type":"authorized_user"}`,
		AccessToken:          "tok",
		ImpersonateDelegates: []string{"d@p.iam.gserviceaccount.com"},
		Project:              "p1",
		BillingProject:       "bill",
		UserProjectOverride:  true,
		CloudAssetEndpoint:   "http://localhost:1/v1/",
		Marked:               []string{"impersonate_service_account", "region"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if _, err := New(context.Background(), got); err == nil || !strings.Contains(err.Error(), "impersonate_service_account") {
		t.Errorf("a marked impersonate_service_account should refuse the client by name, got %v", err)
	}

	withExt := ProviderAttrs(cty.ObjectVal(map[string]cty.Value{
		"external_credentials": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{"audience": cty.StringVal("x")})}),
	}), true)
	if !withExt.ExternalCredentials {
		t.Errorf("an external_credentials block was not seen")
	}
	if a := ProviderAttrs(cty.NullVal(cty.EmptyObject), true); !reflect.DeepEqual(a, Attrs{}) {
		t.Errorf("a null block read as %+v", a)
	}
	if a := ProviderAttrs(val, false); !reflect.DeepEqual(a, Attrs{}) {
		t.Errorf("ok false read as %+v", a)
	}
}

func TestServiceRoot(t *testing.T) {
	for in, want := range map[string]string{
		"https://cloudasset.googleapis.com/v1/":   "https://cloudasset.googleapis.com/",
		"https://cloudasset.googleapis.com/v1":    "https://cloudasset.googleapis.com/",
		"https://cloudasset.googleapis.com/":      "https://cloudasset.googleapis.com/",
		"http://localhost:4566/cloudasset/v1/":    "http://localhost:4566/cloudasset/",
		"http://localhost:4566/cloudasset":        "http://localhost:4566/cloudasset/",
		"http://localhost:4566/v1beta1/somewhere": "http://localhost:4566/v1beta1/somewhere/",
	} {
		if got := serviceRoot(in); got != want {
			t.Errorf("serviceRoot(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestQuery(t *testing.T) {
	if got := Query("est"); got != "labels.tofu-estate=est" {
		t.Errorf("Query = %q", got)
	}
}
