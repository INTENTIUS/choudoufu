// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"fmt"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #2204. The id each composite and association type derives
// has to be byte-identical to what azurerm writes to state. The expected
// strings below are built with the fmtString of each part's ID() method in
// azurerm 5.10.0 (b24dfdc1), joined the way commonids.CompositeResourceID.ID()
// joins them (fmt.Sprintf("%s|%s", first.ID(), second.ID())), never with the
// artifact's templates, so a template that disagreed with azurerm's ID()
// would fail here.
const (
	// internal/services/web ... webapps.HostNameBindingId, certificates.CertificateId
	fmtHostNameBinding = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Web/sites/%s/hostNameBindings/%s"
	fmtCertificate     = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Web/certificates/%s"
	// automation schedule.ScheduleId, runbook.RunbookId
	fmtSchedule = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Automation/automationAccounts/%s/schedules/%s"
	fmtRunbook  = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Automation/automationAccounts/%s/runbooks/%s"
	// communication
	fmtCommService = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Communication/communicationServices/%s"
	fmtEmailDomain = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Communication/emailServices/%s/domains/%s"
	// network: commonids and the network parse / sdk packages
	fmtNatGateway   = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/natGateways/%s"
	fmtPublicIP     = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/publicIPAddresses/%s"
	fmtPublicPrefix = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/publicIPPrefixes/%s"
	fmtNIC          = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/networkInterfaces/%s"
	fmtNICIPConfig  = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/networkInterfaces/%s/ipConfigurations/%s"
	fmtAppGWPool    = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/applicationGateways/%s/backendAddressPools/%s"
	fmtASG          = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/applicationSecurityGroups/%s"
	fmtLBPool       = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/loadBalancers/%s/backendAddressPools/%s"
	fmtLBNatRule    = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/loadBalancers/%s/inboundNatRules/%s"
	fmtNSG          = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/networkSecurityGroups/%s"
	fmtSubnet       = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/virtualNetworks/%s/subnets/%s"
	// postgres virtualendpoints.VirtualEndpointId, servers.FlexibleServerId
	fmtVirtualEndpoint = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.DBforPostgreSQL/flexibleServers/%s/virtualEndpoints/%s"
	fmtFlexibleServer  = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.DBforPostgreSQL/flexibleServers/%s"
)

const (
	fxSub = "11111111-2222-3333-4444-555555555555"
	fxRG  = "rg-one"
)

func compositeID(first, second string) string { return fmt.Sprintf("%s|%s", first, second) }

// renderDerived is the components rendered with the arguments' values and
// the provider's subscription, the string the identity layer builds.
func renderDerived(t *testing.T, got SynthesizedIdentity, args map[string]string) string {
	t.Helper()
	var b strings.Builder
	for _, c := range got.Components {
		switch {
		case c.Cloud == azurermCloudSubscription:
			b.WriteString(fxSub)
		case c.Cloud != "":
			t.Fatalf("unexpected cloud value %q", c.Cloud)
		case len(c.Attrs) > 0:
			v, ok := args[c.Attrs[0]]
			if !ok {
				t.Fatalf("component reads %s, which the fixture does not set", c.Attrs[0])
			}
			b.WriteString(v)
		default:
			b.WriteString(c.Literal)
		}
	}
	return b.String()
}

func TestAzurermCompositeIDsAreAzurermsFormat(t *testing.T) {
	nic := fmt.Sprintf(fmtNIC, fxSub, fxRG, "nic1")
	nat := fmt.Sprintf(fmtNatGateway, fxSub, fxRG, "nat1")
	subnet := fmt.Sprintf(fmtSubnet, fxSub, "rg-net", "vnet1", "snet1")
	srcSrv := fmt.Sprintf(fmtFlexibleServer, fxSub, fxRG, "pg-src")
	repSrv := fmt.Sprintf(fmtFlexibleServer, fxSub, "rg-two", "pg-rep")
	ipcfg := func() string { return fmt.Sprintf(fmtNICIPConfig, fxSub, fxRG, "nic1", "ipconfig1") }

	cases := []struct {
		typ  string
		args map[string]string
		want string
	}{
		{"azurerm_app_service_certificate_binding",
			map[string]string{
				"hostname_binding_id": fmt.Sprintf(fmtHostNameBinding, fxSub, fxRG, "site1", "www.example.com"),
				"certificate_id":      fmt.Sprintf(fmtCertificate, fxSub, fxRG, "cert1"),
			},
			compositeID(fmt.Sprintf(fmtHostNameBinding, fxSub, fxRG, "site1", "www.example.com"), fmt.Sprintf(fmtCertificate, fxSub, fxRG, "cert1"))},
		// The four names are plain arguments here; the subscription is the provider's.
		{"azurerm_automation_job_schedule",
			map[string]string{"resource_group_name": fxRG, "automation_account_name": "acct1", "schedule_name": "sched1", "runbook_name": "rb1"},
			compositeID(fmt.Sprintf(fmtSchedule, fxSub, fxRG, "acct1", "sched1"), fmt.Sprintf(fmtRunbook, fxSub, fxRG, "acct1", "rb1"))},
		{"azurerm_communication_service_email_domain_association",
			map[string]string{
				"communication_service_id": fmt.Sprintf(fmtCommService, fxSub, fxRG, "comm1"),
				"email_service_domain_id":  fmt.Sprintf(fmtEmailDomain, fxSub, fxRG, "email1", "example.com"),
			},
			compositeID(fmt.Sprintf(fmtCommService, fxSub, fxRG, "comm1"), fmt.Sprintf(fmtEmailDomain, fxSub, fxRG, "email1", "example.com"))},
		{"azurerm_nat_gateway_public_ip_association",
			map[string]string{"nat_gateway_id": nat, "public_ip_address_id": fmt.Sprintf(fmtPublicIP, fxSub, fxRG, "pip1")},
			compositeID(nat, fmt.Sprintf(fmtPublicIP, fxSub, fxRG, "pip1"))},
		{"azurerm_nat_gateway_public_ip_prefix_association",
			map[string]string{"nat_gateway_id": nat, "public_ip_prefix_id": fmt.Sprintf(fmtPublicPrefix, fxSub, fxRG, "pfx1")},
			compositeID(nat, fmt.Sprintf(fmtPublicPrefix, fxSub, fxRG, "pfx1"))},
		// The interface's first part is its ip configuration, built from the
		// interface id's segments and ip_configuration_name.
		{"azurerm_network_interface_application_gateway_backend_address_pool_association",
			map[string]string{"network_interface_id": nic, "ip_configuration_name": "ipconfig1", "backend_address_pool_id": fmt.Sprintf(fmtAppGWPool, fxSub, fxRG, "agw1", "pool1")},
			compositeID(ipcfg(), fmt.Sprintf(fmtAppGWPool, fxSub, fxRG, "agw1", "pool1"))},
		{"azurerm_network_interface_application_security_group_association",
			map[string]string{"network_interface_id": nic, "application_security_group_id": fmt.Sprintf(fmtASG, fxSub, fxRG, "asg1")},
			compositeID(nic, fmt.Sprintf(fmtASG, fxSub, fxRG, "asg1"))},
		{"azurerm_network_interface_backend_address_pool_association",
			map[string]string{"network_interface_id": nic, "ip_configuration_name": "ipconfig1", "backend_address_pool_id": fmt.Sprintf(fmtLBPool, fxSub, fxRG, "lb1", "pool1")},
			compositeID(ipcfg(), fmt.Sprintf(fmtLBPool, fxSub, fxRG, "lb1", "pool1"))},
		{"azurerm_network_interface_nat_rule_association",
			map[string]string{"network_interface_id": nic, "ip_configuration_name": "ipconfig1", "nat_rule_id": fmt.Sprintf(fmtLBNatRule, fxSub, fxRG, "lb1", "rule1")},
			compositeID(ipcfg(), fmt.Sprintf(fmtLBNatRule, fxSub, fxRG, "lb1", "rule1"))},
		{"azurerm_network_interface_security_group_association",
			map[string]string{"network_interface_id": nic, "network_security_group_id": fmt.Sprintf(fmtNSG, fxSub, fxRG, "nsg1")},
			compositeID(nic, fmt.Sprintf(fmtNSG, fxSub, fxRG, "nsg1"))},
		// One endpoint id on each server, both named by `name`.
		{"azurerm_postgresql_flexible_server_virtual_endpoint",
			map[string]string{"source_server_id": srcSrv, "replica_server_id": repSrv, "name": "ve1"},
			compositeID(fmt.Sprintf(fmtVirtualEndpoint, fxSub, fxRG, "pg-src", "ve1"), fmt.Sprintf(fmtVirtualEndpoint, fxSub, "rg-two", "pg-rep", "ve1"))},
		// Subnet associations: d.SetId(subnetId.ID()), no join.
		{"azurerm_subnet_nat_gateway_association", map[string]string{"subnet_id": subnet}, subnet},
		{"azurerm_subnet_network_security_group_association", map[string]string{"subnet_id": subnet}, subnet},
		{"azurerm_subnet_route_table_association", map[string]string{"subnet_id": subnet}, subnet},
	}
	if len(cases) != 14 {
		t.Fatalf("%d cases, the ruling names 14 types", len(cases))
	}
	for _, c := range cases {
		var names []string
		for a := range c.args {
			names = append(names, a)
		}
		schema := providers.Schema{Block: azurermArgsBlock(names...)}
		got, ok := Azure.SynthesizeIdentity(c.typ, schema)
		if !ok || got.FromIdentitySchema || got.TemplateWithheld != "" || len(got.Components) == 0 {
			t.Errorf("%s: %+v, %v; want the composite derivation", c.typ, got, ok)
			continue
		}
		if d := renderDerived(t, got, c.args); d != c.want {
			t.Errorf("%s: derived\n  %s\nazurerm writes\n  %s", c.typ, d, c.want)
		}
		// The import syntax azurerm accepts is the same shape.
		if strings.Count(got.ImportSyntax, "|") != strings.Count(c.want, "|") {
			t.Errorf("%s: import syntax %q does not join as the id does", c.typ, got.ImportSyntax)
		}
	}
}

// TestAzurermCompositeRefusedWhenAnArgumentIsNotSettable shows the guard can
// fail: the derivation stands only while every argument it reads is one the
// served schema lets a configuration set.
func TestAzurermCompositeRefusedWhenAnArgumentIsNotSettable(t *testing.T) {
	schema := providers.Schema{Block: azurermArgsBlock("network_interface_id")} // no network_security_group_id
	got, ok := Azure.SynthesizeIdentity("azurerm_network_interface_security_group_association", schema)
	if !ok || got.TemplateWithheld == "" || !strings.Contains(got.TemplateWithheld, "network_security_group_id") {
		t.Errorf("got %+v, %v; want the template withheld naming network_security_group_id", got, ok)
	}
}
