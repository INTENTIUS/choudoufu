// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	pid "github.com/intentius/choudoufu/pkg/identity"
)

// The per-type identity knowledge - the tables, the generated rows, and the
// rules that read a provider's identity schema - lives in pkg/identity, so
// that another module (plunge) can import it. GitHub issue #2244. This file
// re-declares that package's names here, so the configuration-reading layer
// in this package and every caller of it keep one import and read the same
// values: every name below is an alias, never a copy.

// Types.
type (
	Admission              = pid.Admission
	AzureServerMinted      = pid.AzureServerMinted
	CloudContext           = pid.CloudContext
	CloudValue             = pid.CloudValue
	Component              = pid.Component
	ContentMatchBinding    = pid.ContentMatchBinding
	DocumentedImportID     = pid.DocumentedImportID
	DocumentedImportIDPart = pid.DocumentedImportIDPart
	GoogleAssetImport      = pid.GoogleAssetImport
	GoogleIdentityImport   = pid.GoogleIdentityImport
	GoogleImportFormat     = pid.GoogleImportFormat
	LongFormRule           = pid.LongFormRule
	ParentLink             = pid.ParentLink
	ServiceOf              = pid.ServiceOf
	TypeIdentity           = pid.TypeIdentity
	UniqueName             = pid.UniqueName
)

// Constants.
const (
	AdmitConfigDeclined            = pid.AdmitConfigDeclined
	AdmitConfigSignal              = pid.AdmitConfigSignal
	AdmitNeedsConfigSignal         = pid.AdmitNeedsConfigSignal
	AdmitSchema                    = pid.AdmitSchema
	AzureMarkerlessReason          = pid.AzureMarkerlessReason
	AzureRecordCarriedReason       = pid.AzureRecordCarriedReason
	CloudAccountID                 = pid.CloudAccountID
	CloudAppConfigurationDNSSuffix = pid.CloudAppConfigurationDNSSuffix
	CloudEnvironment               = pid.CloudEnvironment
	CloudKeyVaultDNSSuffix         = pid.CloudKeyVaultDNSSuffix
	CloudLocation                  = pid.CloudLocation
	CloudManagedHSMDNSSuffix       = pid.CloudManagedHSMDNSSuffix
	CloudNone                      = pid.CloudNone
	CloudProject                   = pid.CloudProject
	CloudRegion                    = pid.CloudRegion
	CloudStorageDNSSuffix          = pid.CloudStorageDNSSuffix
	CloudSubscriptionID            = pid.CloudSubscriptionID
	CloudZone                      = pid.CloudZone
	DiscoverableFallbackReason     = pid.DiscoverableFallbackReason
	GoogleMarkerlessReason         = pid.GoogleMarkerlessReason
	LongFormComputedName           = pid.LongFormComputedName
	LongFormEnsurePrefix           = pid.LongFormEnsurePrefix
	LongFormIAMParent              = pid.LongFormIAMParent
	LongFormLastSegment            = pid.LongFormLastSegment
	LongFormTrimPrefix             = pid.LongFormTrimPrefix
	MarkerlessReason               = pid.MarkerlessReason
	NotImportableReason            = pid.NotImportableReason
	PerElementShrinkCaveat         = pid.PerElementShrinkCaveat
	SameNameIdentity               = pid.SameNameIdentity
)

// Tables. Each is the same map as pkg/identity's, not a copy.
var (
	AzureRecordCarriedTypes       = pid.AzureRecordCarriedTypes
	AzureServerMintedTypes        = pid.AzureServerMintedTypes
	ContentMatchTypes             = pid.ContentMatchTypes
	DefaultTable                  = pid.DefaultTable
	DiscoverableFallbackTypes     = pid.DiscoverableFallbackTypes
	DocumentedImportIDs           = pid.DocumentedImportIDs
	GoogleAssetImports            = pid.GoogleAssetImports
	GoogleAssetUnknown            = pid.GoogleAssetUnknown
	GoogleCompositeImports        = pid.GoogleCompositeImports
	GoogleCompositeRefused        = pid.GoogleCompositeRefused
	GoogleIdentityImportRefused   = pid.GoogleIdentityImportRefused
	GoogleIdentityImports         = pid.GoogleIdentityImports
	GoogleMarkerlessTypes         = pid.GoogleMarkerlessTypes
	GoogleTable                   = pid.GoogleTable
	IDNotProvenWholeTypes         = pid.IDNotProvenWholeTypes
	MarkerlessTypes               = pid.MarkerlessTypes
	NotImportableTypes            = pid.NotImportableTypes
	VariadicTrailingImportIDTypes = pid.VariadicTrailingImportIDTypes
)

// Functions.
var (
	AdmittedTypes               = pid.AdmittedTypes
	AzureDNSSuffix              = pid.AzureDNSSuffix
	AzureServerMintedMarkerless = pid.AzureServerMintedMarkerless
	AzureServerMintedTaggable   = pid.AzureServerMintedTaggable
	FoldParentOf                = pid.FoldParentOf
	FoldParentTypes             = pid.FoldParentTypes
	GoogleAssetImportID         = pid.GoogleAssetImportID
	GoogleCompositeImportID     = pid.GoogleCompositeImportID
	GoogleIdentityImportValues  = pid.GoogleIdentityImportValues
	GuessArgNameFromCFNProperty = pid.GuessArgNameFromCFNProperty
	IsAzureServerMinted         = pid.IsAzureServerMinted
	IsMarkerless                = pid.IsMarkerless
	LookupType                  = pid.LookupType
	MarkerlessReasonFor         = pid.MarkerlessReasonFor
	NormalizeDocName            = pid.NormalizeDocName
	NotImportable               = pid.NotImportable
	ParentByConvention          = pid.ParentByConvention
	ParentListRecovered         = pid.ParentListRecovered
	ParentOf                    = pid.ParentOf
	ParentReadRemovable         = pid.ParentReadRemovable
	SameServiceAffinity         = pid.SameServiceAffinity
	SingleParentComponent       = pid.SingleParentComponent
)

// The unexported names the resolver and its tests use for the same
// helpers.
var (
	normalizeDocName          = pid.NormalizeDocName
	preferSynthesized         = pid.PreferSynthesized
	canonicalAzureEnvironment = pid.CanonicalAzureEnvironment
	azureSuffixService        = pid.AzureSuffixService
	attr                      = pid.Attr
	inAttr                    = pid.InAttr
	schemaReproducesRow       = pid.SchemaReproducesRow
)

// containsString reports whether s appears anywhere in ss.
func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
