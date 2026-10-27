// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package azsweep

import (
	"strconv"
	"strings"

	"github.com/zclconf/go-cty/cty"
)

// The two provider types the Azure family covers (#1122): one Attrs shape
// reads either block, since azapi's connection arguments are azurerm's
// with two exceptions, named on the fields they affect.
const (
	ProviderAzureRM = "azurerm"
	ProviderAzAPI   = "azapi"
)

// Attrs are the azurerm or azapi provider block arguments the sweep
// understands, read off its evaluated configuration by [ProviderAttrs].
// The names are the providers' own (hashicorp/azurerm 5.10.0 and
// azure/azapi 2.13.0 provider schemas); an empty string or a nil bool is
// an argument the block leaves unset.
type Attrs struct {
	// Provider is the provider type the block configures,
	// [ProviderAzureRM] or [ProviderAzAPI]. It decides which environment
	// variables apply ([Attrs.WithEnv]) and which endpoint arguments are
	// read.
	Provider string

	// SubscriptionID is the subscription the block defaults to: the one a
	// client covers when its caller names no other ([Client.Subscription]).
	SubscriptionID string
	TenantID       string

	ClientID             string
	ClientIDFilePath     string
	ClientSecret         string
	ClientSecretFilePath string

	ClientCertificate         string
	ClientCertificatePath     string
	ClientCertificatePassword string

	// UseCLI, UseMSI, UseOIDC and UseAKSWorkloadIdentity are the four
	// switches. Nil is unset, which both providers default to true for
	// use_cli and false for the rest.
	UseCLI                 *bool
	UseMSI                 *bool
	UseOIDC                *bool
	UseAKSWorkloadIdentity *bool
	// MSIEndpoint is azurerm's msi_endpoint; azapi has none.
	MSIEndpoint string

	OIDCToken         string
	OIDCTokenFilePath string
	OIDCRequestURL    string
	OIDCRequestToken  string
	// ADOServiceConnectionID is azurerm's ado_pipeline_service_connection_id
	// or azapi's oidc_azure_service_connection_id: the same Azure DevOps
	// service connection under two names.
	ADOServiceConnectionID string

	// Environment is the cloud: public (the default), usgovernment or
	// china.
	Environment string
	// MetadataHost is azurerm's metadata_host, a host serving
	// /metadata/endpoints that names the cloud's endpoints in place of
	// Environment. azapi has none.
	MetadataHost string
	// ResourceManagerEndpoint, ResourceManagerAudience and
	// ActiveDirectoryAuthorityHost are azapi's endpoint block, each
	// overriding Environment's value. azurerm has no endpoint block.
	ResourceManagerEndpoint      string
	ResourceManagerAudience      string
	ActiveDirectoryAuthorityHost string
	// DisableInstanceDiscovery is azapi's disable_instance_discovery (azapi
	// 2.13.0 provider.go:566-576, ARM_DISABLE_INSTANCE_DISCOVERY when
	// unset): the token request skips Microsoft's instance discovery
	// service. azurerm has none; its go-azure-sdk credentials never ask.
	DisableInstanceDiscovery *bool

	// Marked names the arguments the block sets to a marked value that
	// [ProviderAttrs] therefore left unread. [New] refuses a client when
	// any is set ([Attrs.refusedMarks]).
	Marked []string
}

// ProviderAttrs reads the arguments the Azure sweep understands off the
// evaluated azurerm or azapi provider block (providerType names which),
// following pkg/gcpsweep's ProviderAttrs: a marked value is left
// unread rather than unmarked, except for the arguments that are
// themselves the credential (client_secret, client_certificate and its
// password, oidc_token, oidc_request_token), which are unmarked and read.
// GitHub issue #1527 is what leaving a credential unread cost on
// Kubernetes: every ordinary way of supplying one marks it, and the sweep
// then called the API as someone other than the provider did. They go
// into a token request and out over TLS; nothing here renders them.
func ProviderAttrs(providerType string, val cty.Value, ok bool) Attrs {
	a := Attrs{Provider: providerType}
	if !ok || val == cty.NilVal || val.IsNull() || !val.IsKnown() || !val.Type().IsObjectType() {
		return a
	}
	str := func(v cty.Value, name string) string {
		if !v.Type().IsObjectType() || !v.Type().HasAttribute(name) {
			return ""
		}
		e := v.GetAttr(name)
		if e.IsMarked() {
			a.Marked = append(a.Marked, name)
			return ""
		}
		if e.IsNull() || !e.IsKnown() || e.Type() != cty.String {
			return ""
		}
		return e.AsString()
	}
	boolean := func(name string) *bool {
		if !val.Type().HasAttribute(name) {
			return nil
		}
		e := val.GetAttr(name)
		if e.IsMarked() {
			a.Marked = append(a.Marked, name)
			return nil
		}
		if e.IsNull() || !e.IsKnown() || e.Type() != cty.Bool {
			return nil
		}
		b := e.True()
		return &b
	}
	secret := func(name string) string {
		if !val.Type().HasAttribute(name) {
			return ""
		}
		e, _ := val.GetAttr(name).Unmark()
		if e.IsNull() || !e.IsKnown() || e.Type() != cty.String {
			return ""
		}
		return e.AsString()
	}

	a.SubscriptionID = str(val, "subscription_id")
	a.TenantID = str(val, "tenant_id")
	a.ClientID = str(val, "client_id")
	a.ClientIDFilePath = str(val, "client_id_file_path")
	a.ClientSecret = secret("client_secret")
	a.ClientSecretFilePath = str(val, "client_secret_file_path")
	a.ClientCertificate = secret("client_certificate")
	a.ClientCertificatePath = str(val, "client_certificate_path")
	a.ClientCertificatePassword = secret("client_certificate_password")
	a.UseCLI = boolean("use_cli")
	a.UseMSI = boolean("use_msi")
	a.UseOIDC = boolean("use_oidc")
	a.UseAKSWorkloadIdentity = boolean("use_aks_workload_identity")
	a.OIDCToken = secret("oidc_token")
	a.OIDCTokenFilePath = str(val, "oidc_token_file_path")
	a.OIDCRequestURL = str(val, "oidc_request_url")
	a.OIDCRequestToken = secret("oidc_request_token")
	a.Environment = str(val, "environment")
	switch providerType {
	case ProviderAzAPI:
		a.ADOServiceConnectionID = str(val, "oidc_azure_service_connection_id")
		a.DisableInstanceDiscovery = boolean("disable_instance_discovery")
		if val.Type().HasAttribute("endpoint") {
			// A list nested attribute of at most one object.
			if e, marked := endpointBlock(val.GetAttr("endpoint")); marked {
				a.Marked = append(a.Marked, "endpoint")
			} else if e != cty.NilVal {
				a.ResourceManagerEndpoint = str(e, "resource_manager_endpoint")
				a.ResourceManagerAudience = str(e, "resource_manager_audience")
				a.ActiveDirectoryAuthorityHost = str(e, "active_directory_authority_host")
			}
		}
	default:
		a.ADOServiceConnectionID = str(val, "ado_pipeline_service_connection_id")
		a.MSIEndpoint = str(val, "msi_endpoint")
		a.MetadataHost = str(val, "metadata_host")
	}
	return a
}

// endpointBlock is azapi's endpoint attribute's one object, or cty.NilVal
// when the block sets none; marked reports a mark on the list or on its
// element, which leaves the endpoints unread.
func endpointBlock(ep cty.Value) (e cty.Value, marked bool) {
	if ep.IsMarked() {
		return cty.NilVal, true
	}
	if ep.IsNull() || !ep.IsKnown() || !ep.CanIterateElements() {
		return cty.NilVal, false
	}
	for it := ep.ElementIterator(); it.Next(); {
		_, el := it.Element()
		if el.IsMarked() {
			return cty.NilVal, true
		}
		if el.IsNull() || !el.IsKnown() {
			continue
		}
		return el, false
	}
	return cty.NilVal, false
}

// envDefault is one argument's environment fallback: names in the
// provider's order, the first one set winning, for the provider types
// that read them (nil is both).
type envDefault struct {
	arg       string
	field     func(*Attrs) *string
	names     []string
	providers []string
}

// envDefaults are the environment variables the providers read for an
// argument the block leaves unset, taken from their own configuration
// code: azurerm 5.10.0's internal/provider/provider.go (the schema's
// EnvDefaultFunc and MultiEnvDefaultFunc) and azapi 2.13.0's
// internal/provider/provider.go (Configure's model.X.IsNull() fallbacks).
var envDefaults = []envDefault{
	{"subscription_id", func(a *Attrs) *string { return &a.SubscriptionID }, []string{"ARM_SUBSCRIPTION_ID"}, nil},
	{"tenant_id", func(a *Attrs) *string { return &a.TenantID }, []string{"ARM_TENANT_ID"}, nil},
	{"client_id", func(a *Attrs) *string { return &a.ClientID }, []string{"ARM_CLIENT_ID"}, []string{ProviderAzureRM}},
	{"client_id", func(a *Attrs) *string { return &a.ClientID }, []string{"ARM_CLIENT_ID", "AZURE_CLIENT_ID"}, []string{ProviderAzAPI}},
	{"client_id_file_path", func(a *Attrs) *string { return &a.ClientIDFilePath }, []string{"ARM_CLIENT_ID_FILE_PATH"}, nil},
	{"client_secret", func(a *Attrs) *string { return &a.ClientSecret }, []string{"ARM_CLIENT_SECRET"}, nil},
	{"client_secret_file_path", func(a *Attrs) *string { return &a.ClientSecretFilePath }, []string{"ARM_CLIENT_SECRET_FILE_PATH"}, nil},
	{"client_certificate", func(a *Attrs) *string { return &a.ClientCertificate }, []string{"ARM_CLIENT_CERTIFICATE"}, nil},
	{"client_certificate_path", func(a *Attrs) *string { return &a.ClientCertificatePath }, []string{"ARM_CLIENT_CERTIFICATE_PATH"}, nil},
	{"client_certificate_password", func(a *Attrs) *string { return &a.ClientCertificatePassword }, []string{"ARM_CLIENT_CERTIFICATE_PASSWORD"}, nil},
	{"oidc_token", func(a *Attrs) *string { return &a.OIDCToken }, []string{"ARM_OIDC_TOKEN"}, nil},
	{"oidc_token_file_path", func(a *Attrs) *string { return &a.OIDCTokenFilePath }, []string{"ARM_OIDC_TOKEN_FILE_PATH"}, nil},
	{"oidc_request_token", func(a *Attrs) *string { return &a.OIDCRequestToken }, []string{"ARM_OIDC_REQUEST_TOKEN", "ACTIONS_ID_TOKEN_REQUEST_TOKEN", "SYSTEM_ACCESSTOKEN"}, nil},
	{"oidc_request_url", func(a *Attrs) *string { return &a.OIDCRequestURL }, []string{"ARM_OIDC_REQUEST_URL", "ACTIONS_ID_TOKEN_REQUEST_URL", "SYSTEM_OIDCREQUESTURI"}, []string{ProviderAzureRM}},
	{"oidc_request_url", func(a *Attrs) *string { return &a.OIDCRequestURL }, []string{"ARM_OIDC_REQUEST_URL", "ACTIONS_ID_TOKEN_REQUEST_URL"}, []string{ProviderAzAPI}},
	{"ado_pipeline_service_connection_id", func(a *Attrs) *string { return &a.ADOServiceConnectionID }, []string{"ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID", "ARM_OIDC_AZURE_SERVICE_CONNECTION_ID"}, []string{ProviderAzureRM}},
	{"oidc_azure_service_connection_id", func(a *Attrs) *string { return &a.ADOServiceConnectionID }, []string{"ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID", "ARM_OIDC_AZURE_SERVICE_CONNECTION_ID", "AZURESUBSCRIPTION_SERVICE_CONNECTION_ID"}, []string{ProviderAzAPI}},
	{"environment", func(a *Attrs) *string { return &a.Environment }, []string{"ARM_ENVIRONMENT"}, nil},
	{"metadata_host", func(a *Attrs) *string { return &a.MetadataHost }, []string{"ARM_METADATA_HOSTNAME"}, []string{ProviderAzureRM}},
	{"msi_endpoint", func(a *Attrs) *string { return &a.MSIEndpoint }, []string{"ARM_MSI_ENDPOINT"}, []string{ProviderAzureRM}},
	// azapi reads these three only when the block has no endpoint block
	// at all; one set in the block is not replaced, which for a block
	// that sets one of the three and not another reads one more
	// variable than azapi does.
	{"endpoint", func(a *Attrs) *string { return &a.ResourceManagerEndpoint }, []string{"ARM_RESOURCE_MANAGER_ENDPOINT"}, []string{ProviderAzAPI}},
	{"endpoint", func(a *Attrs) *string { return &a.ResourceManagerAudience }, []string{"ARM_RESOURCE_MANAGER_AUDIENCE"}, []string{ProviderAzAPI}},
	{"endpoint", func(a *Attrs) *string { return &a.ActiveDirectoryAuthorityHost }, []string{"ARM_ACTIVE_DIRECTORY_AUTHORITY_HOST"}, []string{ProviderAzAPI}},
}

// boolDefaults are the four switches' environment variables, read by both
// providers.
var boolDefaults = []struct {
	arg   string
	field func(*Attrs) **bool
	name  string
}{
	{"use_cli", func(a *Attrs) **bool { return &a.UseCLI }, "ARM_USE_CLI"},
	{"use_msi", func(a *Attrs) **bool { return &a.UseMSI }, "ARM_USE_MSI"},
	{"use_oidc", func(a *Attrs) **bool { return &a.UseOIDC }, "ARM_USE_OIDC"},
	{"use_aks_workload_identity", func(a *Attrs) **bool { return &a.UseAKSWorkloadIdentity }, "ARM_USE_AKS_WORKLOAD_IDENTITY"},
}

// EnvNames are every environment variable [Attrs.WithEnv] can read, so a
// test can clear them all.
func EnvNames() []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range envDefaults {
		for _, n := range d.names {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	for _, d := range boolDefaults {
		out = append(out, d.name)
	}
	return append(out, "ARM_DISABLE_INSTANCE_DISCOVERY")
}

// WithEnv fills every argument a left unset from the environment variable
// its provider reads for it, looked up through getenv (os.Getenv). An
// argument the block sets, or left unread because it was marked, is never
// replaced: the block wins, as it does in the provider.
//
// A switch's variable is parsed with strconv.ParseBool, which is
// terraform-plugin-sdk's reading for azurerm; azapi compares with "true"
// alone, so "1" turns a switch on here that azapi would leave off. No
// switch here widens what the sweep can read beyond the credential the
// provider would also find.
func (a Attrs) WithEnv(getenv func(string) string) Attrs {
	marked := map[string]bool{}
	for _, m := range a.Marked {
		marked[m] = true
	}
	provider := a.Provider
	if provider == "" {
		provider = ProviderAzureRM
	}
	for _, d := range envDefaults {
		if d.providers != nil && d.providers[0] != provider {
			continue
		}
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
	for _, d := range boolDefaults {
		p := d.field(&a)
		if *p != nil || marked[d.arg] {
			continue
		}
		if b, err := strconv.ParseBool(getenv(d.name)); err == nil {
			*p = &b
		}
	}
	// azapi alone reads this one, comparing case-insensitively with "true"
	// (provider.go:566-576).
	if provider == ProviderAzAPI && a.DisableInstanceDiscovery == nil && !marked["disable_instance_discovery"] {
		if v := getenv("ARM_DISABLE_INSTANCE_DISCOVERY"); v != "" {
			b := strings.EqualFold(v, "true")
			a.DisableInstanceDiscovery = &b
		}
	}
	return a
}

// EnvSubscriptionID is the subscription the environment gives an azurerm or
// azapi provider block that sets no subscription_id: the one envDefaults
// entry both providers share, ARM_SUBSCRIPTION_ID (azurerm 5.10.0
// internal/provider/provider.go:115-120, schema.EnvDefaultFunc; azapi
// 2.13.0 internal/provider/provider.go:363-368, Configure's IsNull
// fallback), looked up through getenv. It is [Attrs.WithEnv]'s answer for
// a block with no subscription_id, so the sweep's subscription and the one
// internal/live/identity builds ARM ids under (its
// CloudContext.SubscriptionID, which the run fills from this) cannot read
// the environment differently (GitHub issue #2133, finding 4). "" when
// unset.
//
// Neither provider reads any other variable for it. Both fall back further,
// to the Azure CLI's default subscription when use_cli is on (azurerm
// provider.go:449, AzureCliSubscriptionIDHint), which needs a process call
// and is not answered here.
func EnvSubscriptionID(getenv func(string) string) string {
	return Attrs{}.WithEnv(getenv).SubscriptionID
}

// EnvEnvironment is the Azure cloud the environment gives an azurerm or
// azapi provider block that sets no `environment`, for
// internal/live/identity's CloudContext.Environment (GitHub issue #2206):
// ARM_ENVIRONMENT through getenv, and "public" when that is unset, the
// default both providers apply. "" when ARM_METADATA_HOSTNAME names a
// metadata host, because azurerm then takes its cloud from that host and
// not from either argument, and nothing here reads it.
func EnvEnvironment(getenv func(string) string) string {
	if getenv("ARM_METADATA_HOSTNAME") != "" {
		return ""
	}
	if env := (Attrs{}).WithEnv(getenv).Environment; env != "" {
		return env
	}
	return "public"
}

// refusedMarks are the arguments the block marked, which [New] refuses
// rather than stand in for: every argument read here decides who the
// sweep authenticates as or which endpoint it calls.
func (a Attrs) refusedMarks() []string { return a.Marked }

func on(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}
