// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

// Command importer-probe asks a real provider plugin, over the plugin
// protocol, whether a resource type has an Importer at all.
//
// The question matters because a wire identity schema is not evidence that a
// type can be imported: issue #331 found aws_iam_policy_attachment admitted
// off its identity schema with no Importer behind it, so a real migrate hits
// "resource ... doesn't support import" the moment
// internal/live/projection calls ImportResourceState. Nothing static in this
// repository can see that - the provider's own documentation is the only
// other source, and it is silent for 6 types that do have Importers.
//
// The measurement needs no cloud and no credentials. helper/schema's
// Provider.ImportState checks Importer == nil and terraform-plugin-framework
// answers "Resource Import Not Implemented" before either makes an API call,
// so a syntactically invalid import ID separates the two populations
// cleanly: a type WITH an Importer answers with a parse error, or succeeds
// outright where the Importer is a passthrough.
//
// Usage: -exe names the provider binary (a plugin cache entry will do),
// -all sweeps every type the provider serves into -out. Without -all it
// prints the wire-only bucket and four controls.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	goPlugin "github.com/hashicorp/go-plugin"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/logging"
	tfplugin "github.com/intentius/choudoufu/internal/plugin"
	"github.com/intentius/choudoufu/internal/providers"
)

var types = []string{
	// the identity_schema_wire_only bucket
	"aws_acm_certificate_validation",
	"aws_acmpca_certificate_authority_certificate",
	"aws_codegurureviewer_repository_association",
	"aws_iam_policy_attachment",
	"aws_inspector_resource_group",
	"aws_shield_application_layer_automatic_response",
	// positive controls: types with a documented Import section
	"aws_iam_role",
	"aws_s3_bucket",
	"aws_iam_role_policy_attachment",
	"aws_security_group",
}

var (
	exeFlag = flag.String("exe", "", "path to the terraform-provider-aws binary")
	all     = flag.Bool("all", false, "sweep every resource type the provider serves")
	outFlag = flag.String("out", "", "sweep result file")
)

// sweep asks every resource type the same question and records the answer.
// The discriminator is helper/schema Provider.ImportState's own message for
// Importer == nil, which is returned before any AWS call is made.
func sweep(ctx context.Context, p *tfplugin.GRPCProvider, schema providers.GetProviderSchemaResponse) error {
	names := make([]string, 0, len(schema.ResourceTypes))
	for t := range schema.ResourceTypes {
		names = append(names, t)
	}
	sort.Strings(names)

	f, err := os.Create(*outFlag)
	if err != nil {
		return err
	}
	defer f.Close()

	deadline := time.Now().Add(12 * time.Minute)
	for i, t := range names {
		if time.Now().After(deadline) {
			fmt.Fprintf(os.Stderr, "importerprobe: deadline reached after %d of %d types\n", i, len(names))
			break
		}
		ir := p.ImportResourceState(ctx, providers.ImportResourceStateRequest{
			TypeName: t,
			Target:   providers.ImportTarget{ID: "probe-dummy-id"},
		})
		msg := "OK"
		if ir.Diagnostics.HasErrors() {
			msg = strings.ReplaceAll(ir.Diagnostics.Err().Error(), "\n", " | ")
		}
		verdict := "HAS_IMPORTER"
		if strings.Contains(msg, "doesn't support import") || strings.Contains(msg, "Import Not Implemented") {
			verdict = "NO_IMPORTER"
		}
		_, inTable := identity.DefaultTable[t]
		_, vetoed := identity.MarkerlessTypes[t]
		_, synth := identity.SynthesizeTypeIdentity(t, schema.ResourceTypes, nil)
		fmt.Fprintf(f, "%s\t%s\tin_table=%v\tmarkerless=%v\tsynth=%v\t%s\n", verdict, t, inTable, vetoed, synth, msg)
		if err := f.Sync(); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	flag.Parse()
	if err := run(*exeFlag); err != nil {
		fmt.Fprintf(os.Stderr, "importerprobe: %v\n", err)
		os.Exit(1)
	}
}

func run(exe string) error {
	ctx := context.Background()

	client := goPlugin.NewClient(&goPlugin.ClientConfig{
		HandshakeConfig:  tfplugin.Handshake,
		AllowedProtocols: []goPlugin.Protocol{goPlugin.ProtocolGRPC},
		VersionedPlugins: tfplugin.VersionedPlugins,
		Managed:          true,
		Cmd:              exec.Command(exe), //nolint:gosec // an operator-supplied path
		Logger:           logging.NewProviderLogger(""),
	})
	defer client.Kill()

	rpcClient, err := client.Client()
	if err != nil {
		return fmt.Errorf("starting the provider: %w", err)
	}
	raw, err := rpcClient.Dispense(tfplugin.ProviderPluginName)
	if err != nil {
		return fmt.Errorf("dispensing the provider: %w", err)
	}
	p, ok := raw.(*tfplugin.GRPCProvider)
	if !ok {
		return fmt.Errorf("expected a protocol 5 provider handle, got %T", raw)
	}
	p.PluginClient = client
	p.SchemaCache = providers.NewSchemaCache()

	schema := p.GetProviderSchema(ctx)
	if schema.Diagnostics.HasErrors() {
		return fmt.Errorf("GetProviderSchema: %w", schema.Diagnostics.Err())
	}
	fmt.Printf("provider serves %d resource types\n\n", len(schema.ResourceTypes))

	block := listclient.TypeSchema{TypeName: "provider", Config: schema.Provider.Block}
	cfg, diags := block.BuildConfig(map[string]cty.Value{
		"skip_credentials_validation": cty.True,
		"skip_metadata_api_check":     cty.True,
		"skip_requesting_account_id":  cty.True,
		"skip_region_validation":      cty.True,
		"region":                      cty.StringVal("us-east-1"),
		"access_key":                  cty.StringVal("probe"),
		"secret_key":                  cty.StringVal("probe"),
	})
	if diags.HasErrors() {
		return fmt.Errorf("building the provider configuration: %w", diags.Err())
	}
	resp := p.ConfigureProvider(ctx, providers.ConfigureProviderRequest{
		TerraformVersion: "1.6.0",
		Config:           cfg,
	})
	if resp.Diagnostics.HasErrors() {
		return fmt.Errorf("ConfigureProvider: %w", resp.Diagnostics.Err())
	}

	if *all {
		return sweep(ctx, p, schema)
	}

	for _, t := range types {
		sch, hasSchema := schema.ResourceTypes[t]
		hasIdentity := sch.IdentitySchema != nil
		_, admits := identity.SynthesizeTypeIdentity(t, schema.ResourceTypes, nil)
		refusal := identity.SchemaRefusal(t, schema.ResourceTypes, nil)

		ir := p.ImportResourceState(ctx, providers.ImportResourceStateRequest{
			TypeName: t,
			Target:   providers.ImportTarget{ID: "probe-dummy-id"},
		})
		msg := "OK (no diagnostics)"
		if ir.Diagnostics.HasErrors() {
			msg = strings.ReplaceAll(ir.Diagnostics.Err().Error(), "\n", " | ")
		}
		fmt.Printf("%-50s schema=%v identity_schema=%v synth_admits=%v imported=%d\n  import  -> %s\n  refusal -> %q\n",
			t, hasSchema, hasIdentity, admits, len(ir.ImportedResources), msg, refusal)
	}
	return nil
}
