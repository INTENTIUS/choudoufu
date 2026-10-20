// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package gcpsweep

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/oauth2"
	iamcredentials "google.golang.org/api/iamcredentials/v1"
	"google.golang.org/api/option"
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

func (a Attrs) refusedMarks() []string {
	var out []string
	for _, m := range a.Marked {
		if refusedMarks[m] {
			out = append(out, m)
		}
	}
	return out
}

// clientOptions builds the Cloud Asset client's options from a: the
// credential [Attrs.Auth] chose, the scopes, the endpoint and the
// universe. ctx bounds only the building; an impersonated token source
// mints its tokens on the context of the call that needs one.
func (a Attrs) clientOptions(ctx context.Context) ([]option.ClientOption, error) {
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
	if a.CloudAssetEndpoint != "" {
		opts = append(opts, option.WithEndpoint(serviceRoot(a.CloudAssetEndpoint)))
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
