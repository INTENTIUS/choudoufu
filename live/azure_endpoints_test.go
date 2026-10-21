// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/intentius/choudoufu/internal/live/azsweep"
)

// An azure-lane run talks only to floci-az (GitHub issue #2133, the
// endpoint lock). A gcp-lane run sent traffic to real Google because two
// endpoint arguments were unset (live/gcp_endpoints_test.go on gcp-lane).
// On the maintainer's Mac the Azure equivalent could do more: azurerm 5.10.0
// turns use_cli on by default, so a provider whose endpoints fell back to
// the public cloud would sign in with a real `az login` and create real
// resources. These tests render every provider block the lane writes (the
// smoke fixtures' azure_terraform_block, reference-azure's versions_block,
// corpus-azapi-role-assignment's delta 2, and the azurerm record store's
// connection arguments) and the environment both harnesses export, and fail
// when any endpoint or credential setting is missing or points anywhere but
// the emulator. live/e2e/lib/floci-az.sh's comments cite the provider
// source for each setting.
//
// Proven red (2026-10-20): floci_az_azapi_args without its
// disable_instance_discovery line fails every azapi block with "is unset";
// floci_az_provider_env without its export -n loop fails
// TestAzureHarnessEnvIsLocked naming each scrubbed variable left exported.

const (
	azTestHost   = "localhost:45770"
	azTestSub    = "00000000-0000-0000-0000-000000000001"
	azTestTenant = "00000000-0000-0000-0000-000000000002"
	azTestClient = "00000000-0000-0000-0000-000000000003"
	azTestSecret = "floci-az-does-not-check-this"
)

// azRealHosts are hosts no Azure-lane setting may name.
var azRealHosts = []string{
	"management.azure.com", "management.core.windows.net", "login.microsoftonline.com",
	"login.microsoft.com", "graph.microsoft.com", "core.windows.net", "vault.azure.net",
	"azure.us", "chinacloudapi.cn", "usgovcloudapi.net",
}

// azWant is what one provider type's block must state, argument by
// argument, as the HCL text after the "=".
var azWant = map[string]map[string]string{
	"azurerm": {
		"subscription_id":           `"` + azTestSub + `"`,
		"tenant_id":                 `"` + azTestTenant + `"`,
		"client_id":                 `"` + azTestClient + `"`,
		"client_secret":             `"` + azTestSecret + `"`,
		"use_cli":                   "false",
		"use_msi":                   "false",
		"use_oidc":                  "false",
		"use_aks_workload_identity": "false",
		// metadata_host is checked on its own: a literal loopback host, or
		// var.floci_az_host with the variable's loopback validation.
	},
	"azapi": {
		"subscription_id":            `"` + azTestSub + `"`,
		"tenant_id":                  `"` + azTestTenant + `"`,
		"client_id":                  `"` + azTestClient + `"`,
		"client_secret":              `"` + azTestSecret + `"`,
		"environment":                `"custom"`,
		"use_cli":                    "false",
		"use_msi":                    "false",
		"use_oidc":                   "false",
		"use_aks_workload_identity":  "false",
		"disable_instance_discovery": "true",
	},
}

// azForbidden are arguments that would add a credential or move an
// endpoint, which no lane block may set. azapi's endpoint block is among
// them: with environment = "custom" it makes azapi 2.13.0 refuse to
// configure (provider.go:404-424, 635-646), and with any other environment
// it leaves data-plane services at the public cloud.
var azForbidden = map[string][]string{
	"azurerm": {"environment", "client_certificate", "client_certificate_path", "client_certificate_password",
		"client_id_file_path", "client_secret_file_path", "oidc_token", "oidc_token_file_path", "oidc_request_url",
		"oidc_request_token", "ado_pipeline_service_connection_id", "msi_endpoint", "auxiliary_tenant_ids"},
	"azapi": {"endpoint", "client_certificate", "client_certificate_path", "client_certificate_password",
		"client_id_file_path", "client_secret_file_path", "oidc_token", "oidc_token_file_path", "oidc_request_url",
		"oidc_request_token", "oidc_azure_service_connection_id", "auxiliary_tenant_ids"},
}

var hclPos = hcl.Pos{Line: 1, Column: 1}

var (
	azProviderStart = regexp.MustCompile(`^provider "(azurerm|azapi)" \{$`)
	azArgLine       = regexp.MustCompile(`^\s*([a-z_]+)\s*=\s*(.+?)\s*$`)
	azBlockOpen     = regexp.MustCompile(`^\s*([a-z_]+)\s*(=\s*)?[\[{]\s*$`)
)

// azProviderBlocks parses the provider blocks out of rendered HCL: per
// block, its top-level arguments and the names of its nested blocks.
func azProviderBlocks(t *testing.T, hcl string) []struct {
	typ    string
	args   map[string]string
	nested []string
} {
	t.Helper()
	var out []struct {
		typ    string
		args   map[string]string
		nested []string
	}
	lines := strings.Split(hcl, "\n")
	for i := 0; i < len(lines); i++ {
		m := azProviderStart.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		b := struct {
			typ    string
			args   map[string]string
			nested []string
		}{typ: m[1], args: map[string]string{}}
		depth := 1
		for i++; i < len(lines) && depth > 0; i++ {
			l := lines[i]
			if depth == 1 {
				if o := azBlockOpen.FindStringSubmatch(l); o != nil {
					b.nested = append(b.nested, o[1])
					depth++
					continue
				}
				if strings.TrimSpace(l) == "}" {
					depth--
					break
				}
				if a := azArgLine.FindStringSubmatch(l); a != nil {
					if strings.HasSuffix(a[2], "{}") {
						b.nested = append(b.nested, a[1])
						continue
					}
					b.args[a[1]] = a[2]
				}
				continue
			}
			depth += strings.Count(l, "{") + strings.Count(l, "[") - strings.Count(l, "}") - strings.Count(l, "]")
		}
		out = append(out, b)
	}
	return out
}

// checkAzureBlocks holds rendered HCL to the lock: both provider types
// present, every wanted argument at its value, nothing forbidden, metadata
// host loopback, and no real Azure host anywhere in the text.
func checkAzureBlocks(t *testing.T, hcl string, wantTypes ...string) {
	t.Helper()
	if _, diags := hclsyntax.ParseConfig([]byte(hcl), "rendered.tf", hclPos); diags.HasErrors() {
		t.Errorf("the rendered configuration is not valid HCL: %s\n%s", diags.Error(), hcl)
	}
	blocks := azProviderBlocks(t, hcl)
	seen := map[string]bool{}
	for _, b := range blocks {
		seen[b.typ] = true
		for arg, want := range azWant[b.typ] {
			got, ok := b.args[arg]
			switch {
			case !ok:
				t.Errorf("provider %q: %s is unset, so the provider reads it from the operator's shell or its default", b.typ, arg)
			case got != want:
				t.Errorf("provider %q: %s = %s, want %s", b.typ, arg, got, want)
			}
		}
		for _, arg := range azForbidden[b.typ] {
			if _, ok := b.args[arg]; ok {
				t.Errorf("provider %q sets %s", b.typ, arg)
			}
			for _, n := range b.nested {
				if n == arg {
					t.Errorf("provider %q has a %s block", b.typ, arg)
				}
			}
		}
		if b.typ == "azurerm" {
			host, ok := b.args["metadata_host"]
			switch {
			case !ok:
				t.Errorf(`provider "azurerm": metadata_host is unset, so azurerm builds the public cloud from environment = "public"`)
			case host == "var.floci_az_host":
				if !strings.Contains(hcl, `variable "floci_az_host" {`) ||
					!strings.Contains(hcl, `condition     = can(regex("^(localhost|127\\.0\\.0\\.1):[0-9]+$", var.floci_az_host))`) {
					t.Errorf("metadata_host is var.floci_az_host, but the configuration does not declare it with its loopback validation:\n%s", hcl)
				}
			case host != `"`+azTestHost+`"`:
				t.Errorf(`provider "azurerm": metadata_host = %s, not the emulator's "%s"`, host, azTestHost)
			}
		}
	}
	for _, typ := range wantTypes {
		if !seen[typ] {
			t.Errorf("no provider %q block rendered:\n%s", typ, hcl)
		}
	}
	for _, h := range azRealHosts {
		if strings.Contains(hcl, h) {
			t.Errorf("the rendered configuration names %s:\n%s", h, hcl)
		}
	}
}

func runBash(t *testing.T, script string, env ...string) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(), append([]string{"ROOT=" + root}, env...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash failed: %v\n%s", err, out)
	}
	return string(out)
}

// TestAzureSmokeProviderBlocksAreLocked renders live/smoke/lib.sh's
// azure_terraform_block, the provider blocks every az-*.sh scenario writes.
func TestAzureSmokeProviderBlocksAreLocked(t *testing.T) {
	out := runBash(t, `source smoke/lib.sh >/dev/null 2>&1; . e2e/lib/floci-az.sh && AZURE_HOST="$H" && azure_terraform_block smoke-az-test`,
		"H="+azTestHost)
	checkAzureBlocks(t, out, "azurerm", "azapi")
}

// TestAzureReferenceProviderBlocksAreLocked renders reference-azure's own
// versions_block, read out of its run.sh, in both modes. The live mode's
// record store is record_store "azurerm" on container A's blob storage (the
// maintainer's ruling of 2026-10-21: records live in the record store, the
// state file is a cache), aimed at A's host with the endpoint lock's
// arguments, never a local directory.
func TestAzureReferenceProviderBlocksAreLocked(t *testing.T) {
	for _, mode := range []string{"stock", "live"} {
		t.Run(mode, func(t *testing.T) {
			out := runBash(t, `source e2e/lib/gauntlet.sh
eval "$(awk '/^versions_block\(\) \{/{on=1} /^# resource_block writes/{on=0} on' e2e/reference-azure/run.sh)"
ESTATE=reference-azure AZURERM_REQUIRED_PROVIDER="" AZAPI_REQUIRED_PROVIDER=""
REC_RG=refaz-records-rg REC_ACCOUNT=refazrecords
versions_block "$MODE"`, "MODE="+mode, "HOST_A="+azTestHost)
			checkAzureBlocks(t, out, "azurerm", "azapi")
			if strings.Contains(out, `record_store "local"`) || strings.Contains(out, ".tofu-records") {
				t.Errorf("reference-azure keeps its records in a local directory:\n%s", out)
			}
			if mode == "stock" {
				if strings.Contains(out, "record_store") {
					t.Errorf("the stock configuration names a record store:\n%s", out)
				}
				return
			}
			_, block, found := strings.Cut(out, `record_store "azurerm" {`)
			if !found {
				t.Fatalf("the live configuration has no record_store \"azurerm\" block:\n%s", out)
			}
			block, _, _ = strings.Cut(block, "\n    }")
			got := map[string]string{}
			for _, l := range strings.Split(block, "\n") {
				if a := azArgLine.FindStringSubmatch(l); a != nil {
					got[a[1]] = a[2]
				}
			}
			for k, v := range map[string]string{
				"storage_account_name": `"refazrecords"`, "container_name": `"adopted"`,
				"resource_group_name": `"refaz-records-rg"`, "metadata_host": `"` + azTestHost + `"`,
				"client_secret": `"` + azTestSecret + `"`, "use_cli": "false", "use_msi": "false",
			} {
				if got[k] != v {
					t.Errorf("record_store %s = %q, want %s:\n%s", k, got[k], v, out)
				}
			}
		})
	}
}

// corpusRootHeader is the published root's terraform and provider blocks
// (Azure/terraform-provider-azapi v2.13.0, examples/Microsoft.Authorization_
// roleAssignments@2022-04-01/main.tf), which delta 2 must match exactly.
// The fetched root is used instead when `just corpus-fetch` has run.
const corpusRootHeader = `terraform {
  required_providers {
    azapi = {
      source = "Azure/azapi"
    }
    azurerm = {
      source = "hashicorp/azurerm"
    }
  }
}

provider "azurerm" {
  features {
  }
}

provider "azapi" {
  skip_provider_registration = false
}
`

// TestAzureCorpusProviderBlocksAreLocked runs corpus-azapi-role-assignment's
// write_root, read out of its run.sh, on the published root and renders
// what its delta 2 writes.
func TestAzureCorpusProviderBlocksAreLocked(t *testing.T) {
	src := "../.corpus/azapi/examples/Microsoft.Authorization_roleAssignments@2022-04-01"
	if _, err := os.Stat(filepath.Join(src, "main.tf")); err != nil {
		src = t.TempDir()
		if err := os.WriteFile(filepath.Join(src, "main.tf"), []byte(corpusRootHeader), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	src, _ = filepath.Abs(src)
	dst := filepath.Join(t.TempDir(), "root")
	runBash(t, `source e2e/lib/gauntlet.sh
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
eval "$(awk '/^write_root\(\) \{/{on=1} /^# ── substrate helpers/{on=0} on' e2e/corpus-azapi-role-assignment/run.sh)"
SRC="$CSRC" RA_GUID=6faae21a-0cd6-4536-8c23-a278823d12ed ESTATE=corpus-azapi-role-assignment
AZAPI_REQUIRED_PROVIDER="$(gauntlet_azapi_required_provider)" AZURERM_REQUIRED_PROVIDER="$(gauntlet_azurerm_required_provider)"
RECORD_STORE_HCL="$(gauntlet_floci_az_record_store "$H" choudoufu-records choudoufuazrarecords adopted)" || exit 1
write_root "$DST" live`, "CSRC="+src, "DST="+dst, "H="+azTestHost)
	// The live block's records go to the blob container on floci-az, the
	// lane's ruling, through the endpoint lock's connection arguments.
	live := readFile(t, filepath.Join(dst, "live.tf"))
	if !strings.Contains(live, `record_store "azurerm" {`) || strings.Contains(live, `record_store "local"`) {
		t.Errorf("write_root's live.tf does not keep records in record_store \"azurerm\":\n%s", live)
	}
	got := map[string]string{}
	for _, l := range strings.Split(live, "\n") {
		if a := azArgLine.FindStringSubmatch(l); a != nil {
			got[a[1]] = a[2]
		}
	}
	for k, v := range map[string]string{
		"metadata_host": `"` + azTestHost + `"`, "client_secret": `"` + azTestSecret + `"`,
		"use_cli": "false", "use_msi": "false", "use_oidc": "false", "use_aks_workload_identity": "false",
		"storage_account_name": `"choudoufuazrarecords"`, "container_name": `"adopted"`, "resource_group_name": `"choudoufu-records"`,
	} {
		if got[k] != v {
			t.Errorf("live.tf's record_store %s = %q, want %s", k, got[k], v)
		}
	}
	var all strings.Builder
	for _, f := range []string{"main.tf", "floci-az.tf"} {
		b, err := os.ReadFile(filepath.Join(dst, f))
		if err != nil {
			t.Fatalf("write_root wrote no %s: %v", f, err)
		}
		all.Write(b)
		all.WriteString("\n")
	}
	checkAzureBlocks(t, all.String(), "azurerm", "azapi")
	if !strings.Contains(all.String(), "skip_provider_registration = false") {
		t.Error("delta 2 dropped the root's own skip_provider_registration = false")
	}
}

// TestAzureRecordStoreArgsAreLocked renders the azurerm record store's
// connection arguments (az-records-in-a-storage-account.sh), which the
// stock backend's schema reads.
func TestAzureRecordStoreArgsAreLocked(t *testing.T) {
	out := runBash(t, `. e2e/lib/floci-az.sh && floci_az_record_store_args "$H" "$S" "$T" "$C"`,
		"H="+azTestHost, "S="+azTestSub, "T="+azTestTenant, "C="+azTestClient)
	got := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if a := azArgLine.FindStringSubmatch(l); a != nil {
			got[a[1]] = a[2]
		}
	}
	want := map[string]string{
		"metadata_host": `"` + azTestHost + `"`, "subscription_id": `"` + azTestSub + `"`,
		"tenant_id": `"` + azTestTenant + `"`, "client_id": `"` + azTestClient + `"`,
		"client_secret": `"` + azTestSecret + `"`, "use_cli": "false", "use_msi": "false",
		"use_oidc": "false", "use_aks_workload_identity": "false",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("record_store %s = %q, want %s", k, got[k], v)
		}
	}
	scenario := readFile(t, "smoke/scenarios/az-records-in-a-storage-account.sh")
	if !strings.Contains(scenario, `floci_az_record_store_args "$AZURE_HOST"`) {
		t.Error(`az-records-in-a-storage-account.sh's record_store "azurerm" does not take floci_az_record_store_args`)
	}
}

// TestAzureLaneWritesNoOtherProviderBlock: every Azure provider block the
// lane writes comes from floci-az.sh. A literal block anywhere else in the
// lane's scripts would be one the tests above never render.
func TestAzureLaneWritesNoOtherProviderBlock(t *testing.T) {
	files := []string{"smoke/lib.sh", "e2e/lib/gauntlet.sh", "e2e/reference-azure/run.sh"}
	scenarios, err := filepath.Glob("smoke/scenarios/az-*.sh")
	if err != nil || len(scenarios) == 0 {
		t.Fatalf("found no az-*.sh scenarios: %v", err)
	}
	files = append(files, scenarios...)
	lit := regexp.MustCompile(`provider "(azurerm|azapi)" \{|backend "azurerm"`)
	for _, f := range files {
		if lit.MatchString(readFile(t, f)) {
			t.Errorf("%s writes an Azure provider or backend block of its own; take it from live/e2e/lib/floci-az.sh", f)
		}
	}
	// The corpus estate edits the published root's blocks; its two edits
	// are the ones TestAzureCorpusProviderBlocksAreLocked renders.
	corpus := readFile(t, "e2e/corpus-azapi-role-assignment/run.sh")
	if n := len(lit.FindAllString(corpus, -1)); n != 4 {
		t.Errorf("corpus-azapi-role-assignment/run.sh names an Azure provider block %d times; delta 2's two matches and two replacements are 4", n)
	}
}

// TestAzureHarnessEnvIsLocked runs floci_az_provider_env in a shell set up
// as a logged-in operator's might be, and reads back what a child process
// would see.
func TestAzureHarnessEnvIsLocked(t *testing.T) {
	nologin := filepath.Join(t.TempDir(), "no-login")
	hostile := []string{
		"ARM_USE_CLI=true", "ARM_USE_MSI=true", "ARM_USE_OIDC=true", "ARM_USE_AKS_WORKLOAD_IDENTITY=true",
		"ARM_CLIENT_ID=real-client", "ARM_CLIENT_SECRET=real-secret", "ARM_TENANT_ID=real-tenant",
		"ARM_SUBSCRIPTION_ID=real-sub", "ARM_CLIENT_CERTIFICATE_PATH=/real/cert.pfx", "ARM_OIDC_TOKEN=real",
		"ARM_METADATA_HOSTNAME=management.azure.com", "ARM_METADATA_HOST=management.azure.com",
		"ARM_ENVIRONMENT=public", "ARM_RESOURCE_MANAGER_ENDPOINT=https://management.azure.com",
		"ARM_ACTIVE_DIRECTORY_AUTHORITY_HOST=https://login.microsoftonline.com/",
		"ARM_DISABLE_INSTANCE_DISCOVERY=false", "AZURE_CLIENT_ID=real-client", "AZURE_CLIENT_SECRET=real-secret",
		"AZURE_TENANT_ID=real-tenant", "AZURE_AUTHORITY_HOST=https://login.microsoftonline.com/",
		"AZURE_FEDERATED_TOKEN_FILE=/real/token", "AZURE_REGIONAL_AUTHORITY_NAME=eastus",
		"AZURE_CONFIG_DIR=/Users/someone/.azure", "IDENTITY_ENDPOINT=http://169.254.169.254",
		"TF_VAR_floci_az_host=management.azure.com:443",
	}
	out := runBash(t, `. e2e/lib/floci-az.sh && floci_az_provider_env "$H" "$S" "$T" "$C" "$N" && printf '%s\n' "$FLOCI_AZ_SCRUBBED" > "$N.scrubbed" && env`,
		append(hostile, "H="+azTestHost, "S="+azTestSub, "T="+azTestTenant, "C="+azTestClient, "N="+nologin)...)
	env := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			env[k] = v
		}
	}
	want := map[string]string{
		"ARM_SUBSCRIPTION_ID": azTestSub, "ARM_TENANT_ID": azTestTenant, "ARM_CLIENT_ID": azTestClient,
		"ARM_CLIENT_SECRET": azTestSecret, "ARM_USE_CLI": "false", "ARM_USE_MSI": "false",
		"ARM_USE_OIDC": "false", "ARM_USE_AKS_WORKLOAD_IDENTITY": "false",
		"ARM_METADATA_HOSTNAME": azTestHost, "ARM_METADATA_HOST": azTestHost, "ARM_ENVIRONMENT": "custom",
		"ARM_ACTIVE_DIRECTORY_AUTHORITY_HOST": "https://" + azTestHost + "/",
		"ARM_RESOURCE_MANAGER_ENDPOINT":       "https://" + azTestHost,
		"ARM_RESOURCE_MANAGER_AUDIENCE":       "https://" + azTestHost + "/",
		"ARM_DISABLE_INSTANCE_DISCOVERY":      "true",
		"AZURE_CONFIG_DIR":                    nologin,
		"TF_VAR_floci_az_host":                azTestHost,
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("after floci_az_provider_env, %s=%q, want %q", k, env[k], v)
		}
	}
	scrubbedRaw, err := os.ReadFile(nologin + ".scrubbed")
	if err != nil {
		t.Fatal(err)
	}
	scrubbed := strings.Fields(string(scrubbedRaw))
	for _, k := range scrubbed {
		if v, ok := env[k]; ok {
			t.Errorf("%s=%q is still exported to child processes", k, v)
		}
	}
	// Every variable the sweep's credential chain reads (it mirrors both
	// providers', internal/live/azsweep's envDefaults) is either set here
	// or scrubbed.
	handled := map[string]bool{}
	for k := range want {
		handled[k] = true
	}
	for _, k := range scrubbed {
		handled[k] = true
	}
	for _, k := range azsweep.EnvNames() {
		if !handled[k] {
			t.Errorf("%s, which azurerm or azapi reads (internal/live/azsweep's envDefaults), is neither set nor scrubbed by floci_az_provider_env", k)
		}
	}
	for k, v := range env {
		for _, h := range azRealHosts {
			if strings.Contains(v, h) {
				t.Errorf("%s=%s still names %s", k, v, h)
			}
		}
	}
	if fi, err := os.Stat(nologin); err != nil || !fi.IsDir() {
		t.Errorf("the no-login Azure CLI directory %s was not made: %v", nologin, err)
	}
}

// TestAzureHarnessRefusesARealHost: the lock refuses a host that is not
// loopback rather than export it.
func TestAzureHarnessRefusesARealHost(t *testing.T) {
	for _, host := range []string{"management.azure.com:443", "localhost", "localhost:x", "10.0.0.1:4577", ""} {
		cmd := exec.Command("bash", "-c", `. e2e/lib/floci-az.sh && floci_az_provider_env "$H" s t c "$N" && env`)
		cmd.Env = append(os.Environ(), "H="+host, "N="+filepath.Join(t.TempDir(), "n"))
		if out, err := cmd.CombinedOutput(); err == nil {
			t.Errorf("floci_az_provider_env accepted %q:\n%s", host, out)
		}
		cmd = exec.Command("bash", "-c", `. e2e/lib/floci-az.sh && floci_az_provider_blocks "$H" s t c`)
		cmd.Env = append(os.Environ(), "H="+host)
		if out, err := cmd.CombinedOutput(); err == nil {
			t.Errorf("floci_az_provider_blocks accepted %q:\n%s", host, out)
		}
	}
}

// TestAzureHarnessesTakeTheLock: both harnesses aim their processes through
// floci_az_provider_env, and each gauntlet estate saves the variables the
// lock adds into the per-container environment its commands run under, so
// a command run on container A never reads B's host.
func TestAzureHarnessesTakeTheLock(t *testing.T) {
	gauntlet := readFile(t, "e2e/lib/gauntlet.sh")
	i := strings.Index(gauntlet, "\ngauntlet_floci_az_up() {")
	if i < 0 {
		t.Fatal("gauntlet.sh has no gauntlet_floci_az_up")
	}
	body := gauntlet[i:]
	body = body[:strings.Index(body, "\n}\n")]
	if !strings.Contains(body, "floci_az_provider_env ") {
		t.Error("gauntlet_floci_az_up does not call floci_az_provider_env")
	}
	if strings.Contains(body, "export ARM_") {
		t.Error("gauntlet_floci_az_up exports ARM_* itself; the set belongs to floci_az_provider_env")
	}
	smoke := readFile(t, "smoke/lib.sh")
	j := strings.Index(smoke, "\nazure_provider_env() {")
	if j < 0 || !strings.Contains(smoke[j:j+400], "floci_az_provider_env ") {
		t.Error("live/smoke/lib.sh's azure_provider_env does not call floci_az_provider_env")
	}
	for _, f := range []string{"e2e/reference-azure/run.sh", "e2e/corpus-azapi-role-assignment/run.sh"} {
		s := readFile(t, f)
		k := strings.Index(s, "\nsave_env() {")
		if k < 0 {
			t.Errorf("%s has no save_env", f)
			continue
		}
		line := s[k : k+strings.Index(s[k+1:], "\n")+1]
		for _, v := range []string{"ARM_[A-Z_]+", "AZURE_CONFIG_DIR", "TF_VAR_floci_az_host"} {
			if !strings.Contains(line, v) {
				t.Errorf("%s's save_env does not keep %s, so a command on one container runs with the other's", f, v)
			}
		}
	}
}
