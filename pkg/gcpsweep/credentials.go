// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package gcpsweep

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/oauth2"
	iamcredentials "google.golang.org/api/iamcredentials/v1"
	"google.golang.org/api/option"
	htransport "google.golang.org/api/transport/http"
)

// CredentialSource is which of the provider block's credential arguments a
// client authenticates with.
type CredentialSource string

const (
	// SourceAccessToken is the access_token argument, a bearer token used
	// as given.
	SourceAccessToken CredentialSource = "access_token"
	// SourceCredentials is the credentials argument, a credentials file's
	// JSON or its path.
	SourceCredentials CredentialSource = "credentials"
	// SourceADC is Application Default Credentials: the block names no
	// credential, so the environment's own (GOOGLE_APPLICATION_CREDENTIALS,
	// gcloud's application-default login, the metadata server) is used.
	SourceADC CredentialSource = "application default credentials"
	// SourceNone is no credential at all: the block points at local
	// endpoints and names no access_token, so the calls go unauthenticated
	// rather than through a credential that fetches its token from Google
	// ([HTTPClient]).
	SourceNone CredentialSource = "no credential (the provider's custom endpoints are local)"
	// SourceCaller is a client whose credential the caller built and
	// handed over in [Config].
	SourceCaller CredentialSource = "the caller's client"
)

// Credentials is how a client was told to authenticate, so that a failure
// can be reported against what was actually used. It never holds the
// credential itself.
type Credentials struct {
	Source CredentialSource
	// Impersonate is the service account every call is made as, through
	// Source, or "" when the block impersonates no one.
	Impersonate string
	// Delegates is Impersonate's delegation chain.
	Delegates []string
}

// String is the one line a diagnostic names the credential with:
// "access_token", or "credentials impersonating sa@p.iam.gserviceaccount.com".
func (c Credentials) String() string {
	if c.Impersonate == "" {
		return string(c.Source)
	}
	return string(c.Source) + " impersonating " + c.Impersonate
}

// Auth is the precedence hashicorp/google's provider applies
// (transport_tpg's Config.GetCredentials): access_token if set, else
// credentials if set, else Application Default Credentials, and in every
// case through impersonate_service_account when that is set. Call it on
// [Attrs.WithEnv]'s result to see what a client built from the same
// Attrs uses.
func (a Attrs) Auth() Credentials {
	c := Credentials{Source: SourceADC, Impersonate: a.ImpersonateServiceAccount}
	switch {
	case a.AccessToken != "":
		c.Source = SourceAccessToken
	case a.Credentials != "":
		c.Source = SourceCredentials
	}
	if c.Impersonate != "" {
		c.Delegates = append([]string(nil), a.ImpersonateDelegates...)
	}
	return c
}

// defaultScopes are the provider's own default OAuth scopes.
var defaultScopes = []string{
	"https://www.googleapis.com/auth/cloud-platform",
	"https://www.googleapis.com/auth/userinfo.email",
}

func (a Attrs) scopes() []string {
	if len(a.Scopes) > 0 {
		return a.Scopes
	}
	return defaultScopes
}

// refusedMarks are the arguments whose value, left unread because it was
// marked, would change who the sweep calls as or where it calls: dropping
// impersonate_service_account would search as the base credential's own
// principal, and dropping an endpoint override would search the real API
// instead of the one the provider is pointed at. Each refuses the client.
var refusedMarks = map[string]bool{
	"impersonate_service_account":           true,
	"impersonate_service_account_delegates": true,
	"cloud_asset_custom_endpoint":           true,
	"iam_credentials_custom_endpoint":       true,
	"universe_domain":                       true,
}

// RefusedMarks are the arguments a client built from a is refused over:
// set from a marked value, and each one changes who the calls are made as
// or where they go. A caller building its own client checks them as [New]
// does.
func (a Attrs) RefusedMarks() []string { return a.refusedMarks() }

func (a Attrs) refusedMarks() []string {
	listEndpoint := map[string]bool{}
	for _, e := range typeListEndpoints() {
		listEndpoint[e.Arg] = true
	}
	var out []string
	for _, m := range a.Marked {
		if refusedMarks[m] || listEndpoint[m] {
			out = append(out, m)
		}
	}
	return out
}

// HTTPClient builds the HTTP client a [Config] carries, authenticated the
// way hashicorp/google's provider would be from a, with the environment
// fallbacks applied ([Attrs.WithEnv]): [Attrs.Auth]'s precedence, the
// scopes, the universe and impersonation through IAM Credentials. It is a
// helper for a caller that wants the provider's own rule; [New] never
// calls it.
//
// It never reaches Google's public auth endpoints for a block pointed at
// local endpoints ([Attrs.Local]): a credentials file mints its tokens at
// the token_uri it names, and Application Default Credentials look in
// GOOGLE_APPLICATION_CREDENTIALS, gcloud's login and the metadata server,
// none of which may be reached for an emulator run. For such a block an
// access_token is sent as given, and with none the calls carry no
// credential at all (an emulator takes an anonymous call); impersonation
// mints only through a local iam_credentials_custom_endpoint. Anything else
// is refused rather than fallen back from.
func HTTPClient(ctx context.Context, a Attrs) (*http.Client, Credentials, error) {
	a = a.WithEnv(os.Getenv)
	if refused := a.refusedMarks(); len(refused) > 0 {
		return nil, Credentials{}, fmt.Errorf("the google provider block sets %s from a sensitive value, which the estate sweep does not read; set it from a non-sensitive value so the sweep calls as and where the provider does", strings.Join(refused, " and "))
	}
	if a.ExternalCredentials {
		return nil, Credentials{}, fmt.Errorf("the google provider block authenticates through an external_credentials block, which the estate sweep cannot use; configure credentials, access_token or Application Default Credentials for it")
	}
	if a.Local() {
		return a.localHTTPClient(ctx)
	}
	return a.providerHTTPClient(ctx)
}

// providerHTTPClient is [HTTPClient] for a block whose endpoints are not
// local: the provider's own credential chain, unchanged.
func (a Attrs) providerHTTPClient(ctx context.Context) (*http.Client, Credentials, error) {
	opts, err := a.authOptions(ctx)
	if err != nil {
		return nil, Credentials{}, err
	}
	hc, _, err := htransport.NewClient(ctx, opts...)
	if err != nil {
		return nil, Credentials{}, fmt.Errorf("building the GCP HTTP client (%s): %w", a.Auth(), err)
	}
	return hc, a.Auth(), nil
}

// customEndpoints is every custom endpoint a names, by argument name.
func (a Attrs) customEndpoints() map[string]string {
	out := map[string]string{}
	for k, v := range a.Endpoints {
		if v != "" {
			out[k] = v
		}
	}
	if a.CloudAssetEndpoint != "" {
		out["cloud_asset_custom_endpoint"] = a.CloudAssetEndpoint
	}
	if a.IAMCredentialsEndpoint != "" {
		out["iam_credentials_custom_endpoint"] = a.IAMCredentialsEndpoint
	}
	return out
}

// Local reports whether a points any custom endpoint at a loopback host:
// an emulator run. Every call a client built from a makes then has to stay
// on local endpoints, its credential's token fetches included.
func (a Attrs) Local() bool {
	for _, v := range a.customEndpoints() {
		if IsLoopback(v) {
			return true
		}
	}
	return false
}

// IsLoopback reports whether raw, a URL or a bare host[:port], names
// localhost or a loopback address.
func IsLoopback(raw string) bool {
	s := strings.TrimSpace(raw)
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	host := s
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		host = u.Host
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.Trim(host, "[]"), ".")
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// publicCall refuses a call to target, service's URL, from a client whose
// block points at local endpoints when target is not local itself: the
// block named an emulator for some products and left this one at Google's
// default, and the call would leave the machine. "" is the service's
// default endpoint.
func (a Attrs) publicCall(service, target string) error {
	if !a.Local() || (target != "" && IsLoopback(target)) {
		return nil
	}
	var local []string
	for k, v := range a.customEndpoints() {
		if IsLoopback(v) {
			local = append(local, k)
		}
	}
	sort.Strings(local)
	where := target
	if where == "" {
		where = "its default endpoint"
	}
	return fmt.Errorf("not calling %s at %s: the google provider block points %s at a local endpoint, so this run is an emulator run and no call may reach a public endpoint; set the custom endpoint for %s too", service, where, strings.Join(local, ", "), service)
}

// localHTTPClient is [HTTPClient] for a block pointed at local endpoints:
// a static access_token, else no credential, and impersonation only
// through a local IAM Credentials endpoint. No credentials file is read
// and Application Default Credentials are never looked up.
func (a Attrs) localHTTPClient(ctx context.Context) (*http.Client, Credentials, error) {
	creds := Credentials{Source: SourceNone, Impersonate: a.ImpersonateServiceAccount}
	base := &http.Client{}
	if a.AccessToken != "" {
		token, err := readPathOrContents(a.AccessToken)
		if err != nil {
			return nil, Credentials{}, fmt.Errorf("reading access_token: %w", err)
		}
		creds.Source = SourceAccessToken
		base = oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: strings.TrimSpace(token)}))
	}
	if creds.Impersonate == "" {
		return base, creds, nil
	}
	creds.Delegates = append([]string(nil), a.ImpersonateDelegates...)
	if a.IAMCredentialsEndpoint == "" || !IsLoopback(a.IAMCredentialsEndpoint) {
		return nil, Credentials{}, fmt.Errorf("the google provider block impersonates %s and points its other endpoints at local hosts, but iam_credentials_custom_endpoint is not local, so the impersonated token would be minted by Google; point it at the emulator too", creds.Impersonate)
	}
	iam, err := iamcredentials.NewService(ctx, option.WithHTTPClient(base), option.WithEndpoint(serviceRoot(a.IAMCredentialsEndpoint)))
	if err != nil {
		return nil, Credentials{}, fmt.Errorf("building the IAM Credentials client to impersonate %s: %w", creds.Impersonate, err)
	}
	ts := oauth2.ReuseTokenSource(nil, impersonated{iam: iam, target: creds.Impersonate, delegates: creds.Delegates, scopes: a.scopes()})
	return &http.Client{Transport: &oauth2.Transport{Source: ts, Base: http.DefaultTransport}}, creds, nil
}

// authOptions is the credential, scopes and universe [HTTPClient] builds
// its client from for a block whose endpoints are not local.
func (a Attrs) authOptions(ctx context.Context) ([]option.ClientOption, error) {
	creds := a.Auth()
	var base []option.ClientOption
	switch creds.Source {
	case SourceAccessToken:
		token, err := readPathOrContents(a.AccessToken)
		if err != nil {
			return nil, fmt.Errorf("reading access_token: %w", err)
		}
		base = append(base, option.WithTokenSource(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: strings.TrimSpace(token)})))
	case SourceCredentials:
		contents, err := readPathOrContents(a.Credentials)
		if err != nil {
			return nil, fmt.Errorf("reading credentials: %w", err)
		}
		if !json.Valid([]byte(contents)) {
			return nil, fmt.Errorf("the string provided in credentials is neither valid json nor a valid file path")
		}
		//nolint:staticcheck // [option.WithCredentialsJSON] is deprecated
		// upstream; internal/backend/remote-state/gcs uses it for the same
		// reason, to accept every credentials file the provider accepts.
		base = append(base, option.WithCredentialsJSON([]byte(contents)), option.WithScopes(a.scopes()...))
	case SourceADC:
		base = append(base, option.WithScopes(a.scopes()...))
	}
	if a.UniverseDomain != "" {
		base = append(base, option.WithUniverseDomain(a.UniverseDomain))
	}

	opts := base
	if creds.Impersonate != "" {
		iamOpts := append([]option.ClientOption(nil), base...)
		if a.IAMCredentialsEndpoint != "" {
			iamOpts = append(iamOpts, option.WithEndpoint(serviceRoot(a.IAMCredentialsEndpoint)))
		}
		iam, err := iamcredentials.NewService(ctx, iamOpts...)
		if err != nil {
			return nil, fmt.Errorf("building the IAM Credentials client to impersonate %s: %w", creds.Impersonate, err)
		}
		ts := oauth2.ReuseTokenSource(nil, impersonated{iam: iam, target: creds.Impersonate, delegates: creds.Delegates, scopes: a.scopes()})
		opts = []option.ClientOption{option.WithTokenSource(ts)}
		if a.UniverseDomain != "" {
			opts = append(opts, option.WithUniverseDomain(a.UniverseDomain))
		}
	}
	return opts, nil
}

// impersonated mints an access token for target through IAM Credentials'
// generateAccessToken, called with the base credential. It is what
// google.golang.org/api/impersonate does, written out because that package
// always calls iamcredentials.googleapis.com and the provider's
// iam_credentials_custom_endpoint has to be honoured.
type impersonated struct {
	iam       *iamcredentials.Service
	target    string
	delegates []string
	scopes    []string
}

func (i impersonated) Token() (*oauth2.Token, error) {
	req := &iamcredentials.GenerateAccessTokenRequest{Scope: i.scopes, Lifetime: "3600s"}
	for _, d := range i.delegates {
		req.Delegates = append(req.Delegates, "projects/-/serviceAccounts/"+d)
	}
	resp, err := i.iam.Projects.ServiceAccounts.GenerateAccessToken("projects/-/serviceAccounts/"+i.target, req).Do()
	if err != nil {
		return nil, fmt.Errorf("impersonating %s: %w", i.target, err)
	}
	expiry, err := time.Parse(time.RFC3339, resp.ExpireTime)
	if err != nil {
		return nil, fmt.Errorf("impersonating %s: unreadable expireTime %q: %w", i.target, resp.ExpireTime, err)
	}
	return &oauth2.Token{AccessToken: resp.AccessToken, Expiry: expiry}, nil
}

// serviceRoot turns a provider custom endpoint into the root the generated
// clients join their own "v1/..." path to. The provider writes its
// endpoints with the version on ("https://cloudasset.googleapis.com/v1/"),
// so a trailing "v1" segment is dropped; a path prefix before it (an
// emulator mounting the service under /cloudasset) is kept.
func serviceRoot(endpoint string) string {
	e := strings.TrimRight(endpoint, "/")
	e = strings.TrimSuffix(e, "/v1")
	return e + "/"
}

// readPathOrContents is the provider's pathOrContents: a value naming a
// file that exists is that file's contents, "~" expanded; anything else is
// the value itself.
func readPathOrContents(poc string) (string, error) {
	if poc == "" {
		return poc, nil
	}
	path := poc
	if path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return path, err
		}
		path = filepath.Join(home, path[1:])
	}
	if _, err := os.Stat(path); err == nil {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	return poc, nil
}
