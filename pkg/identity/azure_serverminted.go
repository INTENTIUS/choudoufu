// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// This file is the Azure half of GitHub issue #2205 (the 2026-10-23 ruling
// on #2201 item 4): the 17 azurerm types whose id the server mints get both
// of AWS's answers.
//
//   - Record-located, for all 17. [LocatedType] admits a member as it admits
//     a [MarkerlessTypes] one, when the estate declares a record_store and
//     the provider's schema gives an identity a record can hold in full.
//   - Marker discovery, for the members that carry the ARM tags map
//     ([AzureServerMinted.Taggable]). Without a record_store such a member
//     resolves [ClassNeedsDiscovery] ([DiscoveryServerAssigned]) and is found
//     again by its tofu-address tag, the way AWS finds a taggable
//     server-assigned type. The control-plane members are listed by Resource
//     Graph. The Key Vault ones are data-plane objects Resource Graph does
//     not hold; internal/live/discovery's DataPlaneSweep lists them.
//
// The roster is held by hand because no committed artifact says per type
// which azurerm ids the server mints: the classification is #2147's trace of
// azurerm 5.10.0's Create functions (the evidence is in that issue). A new
// azurerm version can only add or remove a member by someone re-reading the
// Create call, so TestAzureServerMintedRosterIsServed holds the roster to
// the committed schema-side facts instead (the type exists, the taggability
// flag agrees with armtypes' azurerm tags table, no member has a table row).

// AzureServerMinted is one roster entry.
type AzureServerMinted struct {
	// Mints is the part of the id the server supplies, in #2147's words.
	Mints string
	// Taggable: the type's `tags` argument is the ARM tags map, so the
	// ownership marker can be written and a listing can find the object.
	Taggable bool
	// DataPlane: the object lives on a data plane Resource Graph does not
	// index, so marker discovery needs a data-plane listing.
	DataPlane bool
}

// AzureServerMintedTypes is every azurerm type whose id the server mints
// (17 at azurerm 5.10.0, #2147).
var AzureServerMintedTypes = map[string]AzureServerMinted{
	"azurerm_active_directory_domain_service":                     {Mints: "the id ends in /initialReplicaSetId/<id>, which the service mints", Taggable: true},
	"azurerm_active_directory_domain_service_replica_set":         {Mints: "the replica set id is read back from the domain service"},
	"azurerm_application_insights_analytics_item":                 {Mints: "the id is read from the create response"},
	"azurerm_key_vault_key":                                       {Mints: "the versioned id comes from the create response", Taggable: true, DataPlane: true},
	"azurerm_key_vault_secret":                                    {Mints: "the versioned id comes from the create response", Taggable: true, DataPlane: true},
	"azurerm_key_vault_certificate":                               {Mints: "the versioned id comes from the create response", Taggable: true, DataPlane: true},
	"azurerm_sentinel_threat_intelligence_indicator":              {Mints: "the id comes from the create response", DataPlane: true},
	"azurerm_sentinel_alert_rule_anomaly_duplicate":               {Mints: "uuid.New() on every create"},
	"azurerm_security_center_assessment_policy":                   {Mints: "uuid.New() on every create"},
	"azurerm_synapse_role_assignment":                             {Mints: "a UUID is generated inside create"},
	"azurerm_storage_object_replication":                          {Mints: "the policy id is assigned by the destination account"},
	"azurerm_app_service_managed_certificate":                     {Mints: "the resource group is read from the web app's plan at apply", Taggable: true},
	"azurerm_backup_protected_file_share":                         {Mints: "the share's system name is known only to Azure Backup"},
	"azurerm_site_recovery_hyperv_network_mapping":                {Mints: "fabric, network or container ids are looked up by friendly name at apply"},
	"azurerm_site_recovery_hyperv_replication_policy_association": {Mints: "fabric, network or container ids are looked up by friendly name at apply"},
	"azurerm_site_recovery_vmware_replicated_vm":                  {Mints: "fabric, network or container ids are looked up by friendly name at apply"},
	"azurerm_site_recovery_vmware_replication_policy_association": {Mints: "fabric, network or container ids are looked up by friendly name at apply"},
}

// IsAzureServerMinted reports whether typeName is one of the 17.
func IsAzureServerMinted(typeName string) bool {
	_, ok := AzureServerMintedTypes[typeName]
	return ok
}

// AzureServerMintedTaggable reports whether typeName is a roster member that
// carries the ARM tags map, so marker discovery can find it.
func AzureServerMintedTaggable(typeName string) bool {
	m, ok := AzureServerMintedTypes[typeName]
	return ok && m.Taggable
}

// AzureServerMintedMarkerless reports whether typeName is a roster member
// with nowhere to carry a marker: the Azure counterpart of
// [MarkerlessTypes], and the only members whose sole route is the record.
func AzureServerMintedMarkerless(typeName string) bool {
	m, ok := AzureServerMintedTypes[typeName]
	return ok && !m.Taggable
}

// AzureMarkerlessReason is why an untaggable roster member is vetoed
// without a record_store, the Azure wording of [MarkerlessReason].
const AzureMarkerlessReason = "the Azure service mints part of this type's id (a create-response value, a UUID or a lookup at apply) " +
	"and the type has no ARM tags argument, so every instance would need marker discovery to be found again and there is nowhere to write the marker"
