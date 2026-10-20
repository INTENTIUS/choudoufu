// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package gcpsweep

import (
	"github.com/zclconf/go-cty/cty"
)

// Attrs are the hashicorp/google provider block arguments the sweep
// understands, read off its evaluated configuration by [ProviderAttrs]. The
// names are the provider's own (8.6.0 schema); an empty field is an
// argument the block leaves unset.
type Attrs struct {
	// Credentials is the provider's credentials argument: a path to a
	// service account key or other credentials file, or its JSON contents.
	Credentials string
	// AccessToken is the provider's access_token argument: an OAuth 2.0
	// access token, or a path to a file holding one.
	AccessToken string
	// ImpersonateServiceAccount is the service account email every call is
	// made as, through the base credential, and ImpersonateDelegates its
	// delegation chain.
	ImpersonateServiceAccount string
	ImpersonateDelegates      []string
	// Scopes overrides the provider's default OAuth scopes.
	Scopes []string

	// Project, Region and Zone are the block's defaults. Only Project
	// bears on a sweep: it is the project a client searches when its
	// caller names none ([Client.Project]).
	Project string
	Region  string
	Zone    string

	// BillingProject and UserProjectOverride are how the provider sends a
	// quota project: with the override on, every call carries
	// X-Goog-User-Project, BillingProject's value when set, else the
	// project the call is about.
	BillingProject      string
	UserProjectOverride bool
	// UniverseDomain is the provider's universe_domain, for a sovereign
	// cloud; empty is googleapis.com.
	UniverseDomain string

	// CloudAssetEndpoint is the provider's cloud_asset_custom_endpoint, a
	// base path ending in the API version as the provider writes it
	// ("https://cloudasset.googleapis.com/v1/"); an emulator or a test
	// sets it. IAMCredentialsEndpoint is iam_credentials_custom_endpoint,
	// where an impersonated token is minted.
	CloudAssetEndpoint     string
	IAMCredentialsEndpoint string

	// ExternalCredentials is whether the block declares an
	// external_credentials block (workload identity federation through
	// the provider's own exchange). The sweep does not perform that
	// exchange, so [New] refuses such a block rather than search as
	// whatever Application Default Credentials happen to be.
	ExternalCredentials bool

	// Marked names the arguments the block sets to a marked value that
	// [ProviderAttrs] therefore left unread. [New] refuses a client when
	// one of them would change who the sweep authenticates as or which
	// endpoint it calls ([Attrs.refusedMarks]).
	Marked []string
}

// ProviderAttrs reads the arguments the GCP sweep understands off the
// evaluated hashicorp/google provider block, mirroring
// internal/live/substrate's KubernetesSweepAttrs: a marked value is left
// unread rather than unmarked, the same rule internal/command's
// projectionProviders.region applies to a sensitive region, EXCEPT for the
// two arguments that are themselves the credential, credentials and
// access_token, which are unmarked and read. GitHub issue #1527 is what
// leaving a credential unread cost on Kubernetes: every ordinary way of
// supplying one marks it (a key from a `sensitive = true` variable, a
// token from a data source the provider declares sensitive), and the
// sweep then called the API as someone other than the provider did, while
// the provider, handed the same value unmarked over RPC, read it fine.
// They go into an OAuth token source and out over TLS; nothing here
// renders them, and [Credentials] keeps only which source was chosen.
//
// Every other marked argument is named in [Attrs.Marked], so that one
// whose absence would change the sweep's identity or target refuses the
// client instead of being silently dropped.
func ProviderAttrs(val cty.Value, ok bool) Attrs {
	var a Attrs
	if !ok || val == cty.NilVal || val.IsNull() || !val.IsKnown() || !val.Type().IsObjectType() {
		return a
	}
	str := func(name string) string {
		if !val.Type().HasAttribute(name) {
			return ""
		}
		v := val.GetAttr(name)
		if v.IsMarked() {
			a.Marked = append(a.Marked, name)
			return ""
		}
		if v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
			return ""
		}
		return v.AsString()
	}
	boolean := func(name string) bool {
		if !val.Type().HasAttribute(name) {
			return false
		}
		v := val.GetAttr(name)
		if v.IsMarked() {
			a.Marked = append(a.Marked, name)
			return false
		}
		if v.IsNull() || !v.IsKnown() || v.Type() != cty.Bool {
			return false
		}
		return v.True()
	}
	list := func(name string) []string {
		if !val.Type().HasAttribute(name) {
			return nil
		}
		v := val.GetAttr(name)
		if v.IsMarked() {
			a.Marked = append(a.Marked, name)
			return nil
		}
		if v.IsNull() || !v.IsKnown() || !v.CanIterateElements() {
			return nil
		}
		var out []string
		for it := v.ElementIterator(); it.Next(); {
			_, e := it.Element()
			if e.IsMarked() {
				a.Marked = append(a.Marked, name)
				return nil
			}
			if e.IsNull() || !e.IsKnown() || e.Type() != cty.String {
				continue
			}
			out = append(out, e.AsString())
		}
		return out
	}
	// secret is str for the two arguments that ARE the credential; see
	// the function comment.
	secret := func(name string) string {
		if !val.Type().HasAttribute(name) {
			return ""
		}
		v, _ := val.GetAttr(name).Unmark()
		if v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
			return ""
		}
		return v.AsString()
	}
	a.Credentials = secret("credentials")
	a.AccessToken = secret("access_token")
	a.ImpersonateServiceAccount = str("impersonate_service_account")
	a.ImpersonateDelegates = list("impersonate_service_account_delegates")
	a.Scopes = list("scopes")
	a.Project = str("project")
	a.Region = str("region")
	a.Zone = str("zone")
	a.BillingProject = str("billing_project")
	a.UserProjectOverride = boolean("user_project_override")
	a.UniverseDomain = str("universe_domain")
	a.CloudAssetEndpoint = str("cloud_asset_custom_endpoint")
	a.IAMCredentialsEndpoint = str("iam_credentials_custom_endpoint")
	if val.Type().HasAttribute("external_credentials") {
		// A nested block of at most one: a list of zero or one object.
		// A mark on it is a declared block all the same.
		v, _ := val.GetAttr("external_credentials").Unmark()
		if !v.IsNull() && (!v.IsKnown() || !v.CanIterateElements() || v.LengthInt() > 0) {
			a.ExternalCredentials = true
		}
	}
	return a
}

// envDefaults are the environment variables the provider reads for an
// argument its block leaves unset, in the provider's order: the first one
// set wins. They are hashicorp/google's own (its provider configuration's
// SDK defaults), so that a sweep run in the same shell as the provider
// authenticates as the provider does.
var envDefaults = []struct {
	arg   string
	field func(*Attrs) *string
	names []string
}{
	{"credentials", func(a *Attrs) *string { return &a.Credentials }, []string{"GOOGLE_CREDENTIALS", "GOOGLE_CLOUD_KEYFILE_JSON", "GCLOUD_KEYFILE_JSON"}},
	{"access_token", func(a *Attrs) *string { return &a.AccessToken }, []string{"GOOGLE_OAUTH_ACCESS_TOKEN"}},
	{"impersonate_service_account", func(a *Attrs) *string { return &a.ImpersonateServiceAccount }, []string{"GOOGLE_IMPERSONATE_SERVICE_ACCOUNT"}},
	{"project", func(a *Attrs) *string { return &a.Project }, []string{"GOOGLE_PROJECT", "GOOGLE_CLOUD_PROJECT", "GCLOUD_PROJECT", "CLOUDSDK_CORE_PROJECT"}},
	{"region", func(a *Attrs) *string { return &a.Region }, []string{"GOOGLE_REGION", "GCLOUD_REGION", "CLOUDSDK_COMPUTE_REGION"}},
	{"zone", func(a *Attrs) *string { return &a.Zone }, []string{"GOOGLE_ZONE", "GCLOUD_ZONE", "CLOUDSDK_COMPUTE_ZONE"}},
	{"billing_project", func(a *Attrs) *string { return &a.BillingProject }, []string{"GOOGLE_BILLING_PROJECT"}},
	{"cloud_asset_custom_endpoint", func(a *Attrs) *string { return &a.CloudAssetEndpoint }, []string{"GOOGLE_CLOUD_ASSET_CUSTOM_ENDPOINT"}},
	{"iam_credentials_custom_endpoint", func(a *Attrs) *string { return &a.IAMCredentialsEndpoint }, []string{"GOOGLE_IAM_CREDENTIALS_CUSTOM_ENDPOINT"}},
}

// EnvNames are every environment variable [Attrs.WithEnv] reads, so a
// test can clear them all.
func EnvNames() []string {
	out := []string{"USER_PROJECT_OVERRIDE"}
	for _, d := range envDefaults {
		out = append(out, d.names...)
	}
	return out
}

// WithEnv fills every argument a left unset from the environment variable
// the provider reads for it, looked up through getenv (os.Getenv). An
// argument the block sets, or left unread because it was marked, is never
// replaced: the block wins, as it does in the provider, and a marked
// value is not to be stood in for by a different one. Note what that
// means for precedence: GOOGLE_OAUTH_ACCESS_TOKEN fills access_token even
// when the block sets credentials, and access_token wins
// ([Attrs.Auth]), exactly as in the provider.
func (a Attrs) WithEnv(getenv func(string) string) Attrs {
	marked := map[string]bool{}
	for _, m := range a.Marked {
		marked[m] = true
	}
	for _, d := range envDefaults {
		p := d.field(&a)
		if *p != "" || marked[d.arg] {
			continue
		}
		for _, n := range d.names {
			if v := getenv(n); v != "" {
				*p = v
				break
			}
		}
	}
	if !a.UserProjectOverride && !marked["user_project_override"] {
		switch getenv("USER_PROJECT_OVERRIDE") {
		case "1", "true", "TRUE", "True":
			a.UserProjectOverride = true
		}
	}
	return a
}
