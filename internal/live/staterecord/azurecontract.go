// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
)

// The storage account contract: bucketcontract.go's three assertions (#1339),
// asserted about the storage account behind record_store "azurerm" (the
// Azure epic, #1120). The reasons are the bucket's, word for word: a record
// can be the only copy of what it says, so an overwrite or delete must be
// recoverable; a versioned store written on every apply grows forever
// without an expiry of noncurrent versions, and that expiry's age is the
// recovery window; and records hold secret material, so nothing may publish
// them.
//
// What differs is where Azure keeps each setting. All three are Azure
// Resource Manager properties of the storage account, not anything the Blob
// REST API serves, so they are read through ARM with a token credential, and
// a store opened with only an access key or a SAS token has no way to ask:
// every setting is then [Unreadable], named with the reason, and waivable
// like any other unreadable setting.
//
//   - blob_versioning: blobServices/default's isVersioningEnabled.
//   - lifecycle_management: the account's management policy (there is one
//     per account, named "default"). A rule filters by "<container>/<blob
//     prefix>", not by blob name alone, which is the one shape that differs
//     from S3's.
//   - public_access: the account's allowBlobPublicAccess. A container's own
//     access level only takes effect while the account allows it, so the
//     account setting is the one that keeps every container private, and the
//     container's level is reported beside it when it is public.
//
// Encryption at rest is not asserted, for the bucket's reason: Azure Storage
// encrypts every account and it cannot be turned off, so the check could not
// fail.

// The account's settings, in the shared [Setting] vocabulary. The names are
// distinct from the bucket's and the cluster's, because an allow_insecure
// name has to say which store's assertion it waives (internal/command pins
// that the vocabularies do not overlap).
const (
	AzureBlobVersioning Setting = "blob_versioning"
	AzureLifecycle      Setting = "lifecycle_management"
	AzurePublicAccess   Setting = "public_access"
)

// AzureSettings is every asserted setting, in the order findings are
// reported.
var AzureSettings = []Setting{AzureBlobVersioning, AzureLifecycle, AzurePublicAccess}

// AzureContractAPI is the four ARM reads the contract needs, and the actions
// they cost: Microsoft.Storage/storageAccounts/blobServices/read,
// .../managementPolicies/read, Microsoft.Storage/storageAccounts/read and
// .../blobServices/containers/read. [NewAzureContractAPI] is the one
// implementation outside tests.
type AzureContractAPI interface {
	BlobServiceProperties(ctx context.Context) (armstorage.BlobServiceProperties, error)
	ManagementPolicy(ctx context.Context) (armstorage.ManagementPolicy, error)
	Account(ctx context.Context) (armstorage.Account, error)
	Container(ctx context.Context) (armstorage.BlobContainer, error)
}

// NewAzureContractAPI reads one account's settings through armstorage
// clients made by factory, which the caller builds with its own credential
// and cloud.
func NewAzureContractAPI(factory *armstorage.ClientFactory, resourceGroup, account, containerName string) AzureContractAPI {
	return &armContractAPI{factory: factory, rg: resourceGroup, account: account, container: containerName}
}

type armContractAPI struct {
	factory                *armstorage.ClientFactory
	rg, account, container string
}

func (a *armContractAPI) BlobServiceProperties(ctx context.Context) (armstorage.BlobServiceProperties, error) {
	out, err := a.factory.NewBlobServicesClient().GetServiceProperties(ctx, a.rg, a.account, nil)
	return out.BlobServiceProperties, err
}

func (a *armContractAPI) ManagementPolicy(ctx context.Context) (armstorage.ManagementPolicy, error) {
	out, err := a.factory.NewManagementPoliciesClient().Get(ctx, a.rg, a.account, armstorage.ManagementPolicyNameDefault, nil)
	return out.ManagementPolicy, err
}

func (a *armContractAPI) Account(ctx context.Context) (armstorage.Account, error) {
	out, err := a.factory.NewAccountsClient().GetProperties(ctx, a.rg, a.account, nil)
	return out.Account, err
}

func (a *armContractAPI) Container(ctx context.Context) (armstorage.BlobContainer, error) {
	out, err := a.factory.NewBlobContainersClient().Get(ctx, a.rg, a.account, a.container, nil)
	return out.BlobContainer, err
}

// CheckContract implements [ContractChecker]. opts.Namespaces are
// store-relative; [AzureBlobConfig.KeyPrefix] is joined ahead of each.
func (s *AzureBlobStore) CheckContract(ctx context.Context, opts ContractOptions) ([]Finding, error) {
	if s.contract == nil {
		findings := make([]Finding, 0, len(AzureSettings))
		for _, setting := range AzureSettings {
			findings = append(findings, Finding{Setting: setting, Outcome: Unreadable, Found: s.contractUnavailable})
		}
		return findings, nil
	}
	blobNamespaces := make([]string, 0, len(opts.Namespaces))
	for _, ns := range opts.Namespaces {
		blobNamespaces = append(blobNamespaces, s.blobName(ns))
	}
	return CheckAzureContract(ctx, s.contract, s.containerName, blobNamespaces)
}

// ContractSubject implements [ContractChecker]: a container is named with its
// account, since a container name alone is not unique.
func (s *AzureBlobStore) ContractSubject() (label, value string) {
	return "Container", s.account + "/" + s.containerName
}

// ContractRefusal implements [ContractChecker].
func (s *AzureBlobStore) ContractRefusal(f Finding) (summary, detail string) {
	return AzureContractRefusal(s.account, s.resourceGroup, s.containerName, f)
}

// ContractCheckFailed implements [ContractChecker].
func (s *AzureBlobStore) ContractCheckFailed(err error) (summary, detail string) {
	return "Cannot check the record store storage account",
		fmt.Sprintf("Before applying, storage account %q is checked for the settings its records depend on, and that check could not be made: %s. Nothing has been applied.", s.account, err)
}

// ContractRefusalClosing implements [ContractChecker]; none is needed, for
// the bucket's reason.
func (s *AzureBlobStore) ContractRefusalClosing([]Setting) string { return "" }

// CheckAzureContract reads the account's settings and reports one finding
// per setting, always all three, in [AzureSettings] order. namespaces are the
// blob-name prefixes the estate writes under; the lifecycle assertion needs
// them, for [CheckBucketContract]'s reason. A denied read is a finding, not
// an error.
func CheckAzureContract(ctx context.Context, api AzureContractAPI, containerName string, namespaces []string) ([]Finding, error) {
	findings := make([]Finding, 0, len(AzureSettings))
	for _, check := range []func() (Finding, error){
		func() (Finding, error) { return checkAzureVersioning(ctx, api) },
		func() (Finding, error) { return checkAzureLifecycle(ctx, api, containerName, namespaces) },
		func() (Finding, error) { return checkAzurePublicAccess(ctx, api, containerName) },
	} {
		f, err := check()
		if err != nil {
			return nil, err
		}
		findings = append(findings, f)
	}
	return findings, nil
}

func checkAzureVersioning(ctx context.Context, api AzureContractAPI) (Finding, error) {
	f := Finding{Setting: AzureBlobVersioning}
	props, err := api.BlobServiceProperties(ctx)
	if err != nil {
		return azureSettingReadFailure(f, "Microsoft.Storage/storageAccounts/blobServices/read", err)
	}
	if p := props.BlobServiceProperties; p != nil && p.IsVersioningEnabled != nil && *p.IsVersioningEnabled {
		f.Outcome = Passed
		f.Found = "blob versioning is enabled"
		return f, nil
	}
	f.Found = "blob versioning is not enabled"
	return f, nil
}

func checkAzureLifecycle(ctx context.Context, api AzureContractAPI, containerName string, namespaces []string) (Finding, error) {
	f := Finding{Setting: AzureLifecycle}
	policy, err := api.ManagementPolicy(ctx)
	if err != nil {
		// Any 404 here is "no policy", the reading azurerm's own
		// storage_management_policy resource makes of it. A missing account
		// was already an error at the versioning read before this one.
		if status, _, ok := azureStatus(err); ok && status == http.StatusNotFound {
			f.Found = "the storage account has no lifecycle management policy"
			return f, nil
		}
		return azureSettingReadFailure(f, "Microsoft.Storage/storageAccounts/managementPolicies/read", err)
	}
	var rules []*armstorage.ManagementPolicyRule
	if p := policy.Properties; p != nil && p.Policy != nil {
		rules = p.Policy.Rules
	}

	// What a lifecycle rule matches is "<container>/<blob name>", so every
	// target is spelled that way. With no namespaces, the whole container.
	targets := make([]string, 0, len(namespaces))
	for _, ns := range namespaces {
		targets = append(targets, containerName+"/"+NamespacePrefix(ns))
	}
	if len(targets) == 0 {
		targets = []string{containerName + "/"}
	}

	// A rule that deletes or archives CURRENT blobs outranks everything
	// else, for #1377's reason: a converged estate does not rewrite its
	// records, so they age, and such a rule removes them on a timer. Archive
	// is here too because an archived blob cannot be read until it is
	// rehydrated, which takes hours, and a plan reads every record.
	var deleting []string
	for _, rule := range rules {
		if rule == nil || !azureRuleEnabled(rule) || !azureRuleTouchesBlockBlobs(rule) {
			continue
		}
		what := azureRuleHarmsCurrent(rule)
		if what == "" || !azureRuleMayReach(rule, targets) {
			continue
		}
		deleting = append(deleting, fmt.Sprintf("rule %q %s, and a record is a current blob", azureRuleName(rule), what))
	}
	if len(deleting) > 0 {
		f.Unwaivable = true
		f.Found = strings.Join(deleting, "; ")
		return f, nil
	}

	var near []string
	var covering []*armstorage.ManagementPolicyRule
	for _, rule := range rules {
		if rule == nil || azureNoncurrentDays(rule) <= 0 {
			continue
		}
		switch {
		case !azureRuleEnabled(rule):
			near = append(near, fmt.Sprintf("rule %q deletes previous versions but is disabled", azureRuleName(rule)))
		case !azureRuleTouchesBlockBlobs(rule):
			near = append(near, fmt.Sprintf("rule %q deletes previous versions but not of block blobs, which is what a record is", azureRuleName(rule)))
		case azureRuleFilters(rule).BlobIndexMatch != nil:
			near = append(near, fmt.Sprintf("rule %q deletes previous versions but is filtered by blob index tag, which this check does not rely on", azureRuleName(rule)))
		default:
			covering = append(covering, rule)
		}
	}
	var used, uncovered []string
	for _, target := range targets {
		found := false
		for _, rule := range covering {
			if azureRuleCovers(rule, target) {
				if d := fmt.Sprintf("rule %q deletes previous versions after %d day(s)", azureRuleName(rule), azureNoncurrentDays(rule)); !slices.Contains(used, d) {
					used = append(used, d)
				}
				found = true
				break
			}
		}
		if !found {
			uncovered = append(uncovered, target)
		}
	}
	if len(uncovered) == 0 && len(covering) > 0 {
		f.Outcome = Passed
		f.Found = strings.Join(used, "; ")
		return f, nil
	}
	for _, rule := range covering {
		for _, target := range uncovered {
			near = append(near, fmt.Sprintf("rule %q deletes previous versions but only under %s, which does not contain %q", azureRuleName(rule), joinQuotedPrefixes(azureRuleFilters(rule).PrefixMatch), target))
		}
	}
	switch {
	case len(near) > 0:
		f.Found = strings.Join(near, "; ")
	case len(rules) == 0:
		f.Found = "the lifecycle management policy has no rules"
	default:
		f.Found = fmt.Sprintf("none of the lifecycle management policy's %d rule(s) deletes previous versions", len(rules))
	}
	return f, nil
}

func azureRuleName(rule *armstorage.ManagementPolicyRule) string {
	if rule.Name != nil && *rule.Name != "" {
		return *rule.Name
	}
	return "(unnamed rule)"
}

// azureRuleEnabled: a rule's enabled flag is optional and true when absent.
func azureRuleEnabled(rule *armstorage.ManagementPolicyRule) bool {
	return rule.Enabled == nil || *rule.Enabled
}

func azureRuleFilters(rule *armstorage.ManagementPolicyRule) armstorage.ManagementPolicyFilter {
	if rule.Definition == nil || rule.Definition.Filters == nil {
		return armstorage.ManagementPolicyFilter{}
	}
	return *rule.Definition.Filters
}

func azureRuleActions(rule *armstorage.ManagementPolicyRule) armstorage.ManagementPolicyAction {
	if rule.Definition == nil || rule.Definition.Actions == nil {
		return armstorage.ManagementPolicyAction{}
	}
	return *rule.Definition.Actions
}

// azureRuleTouchesBlockBlobs: blobTypes is required by Azure, and a record is
// a block blob, so a rule that names only appendBlob cannot reach one.
func azureRuleTouchesBlockBlobs(rule *armstorage.ManagementPolicyRule) bool {
	types := azureRuleFilters(rule).BlobTypes
	if len(types) == 0 {
		return true
	}
	for _, t := range types {
		if t != nil && strings.EqualFold(*t, "blockBlob") {
			return true
		}
	}
	return false
}

// azureRuleHarmsCurrent says what rule does to current blobs that makes a
// record unreadable, or "".
func azureRuleHarmsCurrent(rule *armstorage.ManagementPolicyRule) string {
	base := azureRuleActions(rule).BaseBlob
	if base == nil {
		return ""
	}
	if d := dateAfterModificationDays(base.Delete); d != "" {
		return "deletes current blobs " + d
	}
	if d := dateAfterModificationDays(base.TierToArchive); d != "" {
		return "moves current blobs to the archive tier " + d + ", where a blob cannot be read until it is rehydrated"
	}
	return ""
}

func dateAfterModificationDays(d *armstorage.DateAfterModification) string {
	if d == nil {
		return ""
	}
	for _, v := range []struct {
		days *float32
		what string
	}{
		{d.DaysAfterModificationGreaterThan, "since last modification"},
		{d.DaysAfterCreationGreaterThan, "since creation"},
		{d.DaysAfterLastAccessTimeGreaterThan, "since last access"},
		{d.DaysAfterLastTierChangeGreaterThan, "since the last tier change"},
	} {
		if v.days != nil {
			return fmt.Sprintf("after %g day(s) %s", *v.days, v.what)
		}
	}
	return ""
}

// azureNoncurrentDays is the age at which rule deletes previous versions, or
// 0 when it does not.
func azureNoncurrentDays(rule *armstorage.ManagementPolicyRule) int {
	v := azureRuleActions(rule).Version
	if v == nil || v.Delete == nil || v.Delete.DaysAfterCreationGreaterThan == nil {
		return 0
	}
	return int(*v.Delete.DaysAfterCreationGreaterThan)
}

// azureRuleCovers: a rule with no prefixMatch covers every blob; otherwise
// one of its prefixes must be a string prefix of target.
func azureRuleCovers(rule *armstorage.ManagementPolicyRule, target string) bool {
	prefixes := azureRuleFilters(rule).PrefixMatch
	if len(prefixes) == 0 {
		return true
	}
	for _, p := range prefixes {
		if p != nil && strings.HasPrefix(target, *p) {
			return true
		}
	}
	return false
}

// azureRuleMayReach is [ruleMayReach] for a lifecycle rule: conservative the
// other way, so only a prefix that provably lies outside every target lets a
// harmful rule through. A blob index filter is no reassurance; records carry
// tags.
func azureRuleMayReach(rule *armstorage.ManagementPolicyRule, targets []string) bool {
	prefixes := azureRuleFilters(rule).PrefixMatch
	if len(prefixes) == 0 {
		return true
	}
	for _, p := range prefixes {
		if p == nil {
			continue
		}
		for _, target := range targets {
			if strings.HasPrefix(target, *p) || strings.HasPrefix(*p, target) {
				return true
			}
		}
	}
	return false
}

func joinQuotedPrefixes(prefixes []*string) string {
	var q []string
	for _, p := range prefixes {
		if p != nil {
			q = append(q, fmt.Sprintf("%q", *p))
		}
	}
	return strings.Join(q, ", ")
}

func checkAzurePublicAccess(ctx context.Context, api AzureContractAPI, containerName string) (Finding, error) {
	f := Finding{Setting: AzurePublicAccess}
	account, err := api.Account(ctx)
	if err != nil {
		return azureSettingReadFailure(f, "Microsoft.Storage/storageAccounts/read", err)
	}
	allow := (*bool)(nil)
	if account.Properties != nil {
		allow = account.Properties.AllowBlobPublicAccess
	}
	if allow != nil && !*allow {
		f.Outcome = Passed
		f.Found = "the storage account disallows anonymous blob access"
		return f, nil
	}
	if allow == nil {
		f.Found = "the storage account does not set allowBlobPublicAccess, which accounts created before 2023 read as allowed"
	} else {
		f.Found = "the storage account allows anonymous blob access"
	}
	// The container's own level is what is public right now. It is said when
	// it is readable and public; the assertion is about the account either
	// way, because any container in it can be made public.
	if c, err := api.Container(ctx); err == nil && c.ContainerProperties != nil && c.ContainerProperties.PublicAccess != nil {
		if level := *c.ContainerProperties.PublicAccess; level != armstorage.PublicAccessNone {
			f.Found += fmt.Sprintf(", and container %q is already readable anonymously at level %q", containerName, level)
		}
	}
	return f, nil
}

// azureSettingReadFailure is [settingReadFailure] for an ARM read: a 403 is a
// finding the role could not read, anything else an error.
func azureSettingReadFailure(f Finding, action string, err error) (Finding, error) {
	if status, _, ok := azureStatus(err); ok && status == http.StatusForbidden {
		f.Outcome = Unreadable
		f.Found = action + " was denied"
		return f, nil
	}
	return f, fmt.Errorf("staterecord: azurerm: reading the storage account's %s setting: %w", f.Setting, err)
}

// isAzureForbidden reports whether err is Azure refusing this identity, on
// the Blob REST API or ARM: a 403 of any code (AuthorizationFailure,
// AuthorizationPermissionMismatch, an ABAC condition's refusal, ARM's
// AuthorizationFailed).
func isAzureForbidden(err error) bool {
	var respErr *azcore.ResponseError
	return errors.As(err, &respErr) && respErr.StatusCode == http.StatusForbidden
}

// AzureContractRefusal is the headline and paragraph for one failed finding.
// Empty for a finding that passed.
func AzureContractRefusal(account, resourceGroup, containerName string, f Finding) (summary, detail string) {
	if f.OK() {
		return "", ""
	}
	why, fix := "", ""
	switch f.Setting {
	case AzureBlobVersioning:
		why = "A record in this container can be the only copy of what it says: a record-backed resource carries no marker and cannot be imported under a live block, so without blob versioning an overwrite or a delete is final."
		fix = fmt.Sprintf("Enable it: az storage account blob-service-properties update --account-name %s --resource-group %s --enable-versioning true", account, resourceGroup)
	case AzureLifecycle:
		if f.Unwaivable {
			return "The record store storage account's lifecycle policy removes records",
				fmt.Sprintf("Storage account %q, container %q: %s.\n\nThat rule removes records from reach. An estate that has converged does not rewrite its records, so they age, and once one is gone the next plan reads an estate with that resource missing and proposes creating what already exists. For a record-backed resource the record was the only copy.\n\nRemove the baseBlob delete or tierToArchive action from that rule, or limit the rule to a prefix outside this store's blobs. Only a version delete belongs on a rule that reaches them. The allow_insecure waiver does not cover this.", account, containerName, f.Found)
		}
		why = "Blob versioning keeps a previous version on every overwrite, and every apply writes records, so without a rule that deletes previous versions the account keeps every version of every record forever. The rule's age is also the recovery window: how long a deleted or overwritten record can still be brought back. Choose it deliberately."
		fix = "Add an enabled lifecycle management rule for blockBlob with a version delete (daysAfterCreationGreaterThan) and no prefix filter, or a prefixMatch the store's blobs sit under (it starts with the container name). examples/record-store-azurerm/CONTRACT.md has one."
	case AzurePublicAccess:
		why = "Records hold secret material, protected only by the account's encryption and by its role assignments. An account that allows anonymous access lets one container setting publish them."
		fix = fmt.Sprintf("Disallow it: az storage account update --name %s --resource-group %s --allow-blob-public-access false", account, resourceGroup)
	}
	if f.Outcome == Unreadable {
		return fmt.Sprintf("The record store storage account's %s setting could not be read", f.Setting),
			fmt.Sprintf("Storage account %q: %s, so whether the account satisfies this setting is unknown, and an account nobody could check is refused the same as one that failed.\n\n%s\n\nGrant the identity that read action, or have someone who holds it confirm the setting and name it in allow_insecure.", account, f.Found, why)
	}
	return fmt.Sprintf("The record store storage account fails its %s assertion", f.Setting),
		fmt.Sprintf("Storage account %q, container %q: %s.\n\n%s\n\n%s", account, containerName, f.Found, why, fix)
}

// AzureWaiverCost says what an estate gives up by waiving setting, as a
// clause that completes "is waived, so ..." (#1340).
func AzureWaiverCost(setting Setting) string {
	switch setting {
	case AzureBlobVersioning:
		return "an overwritten or deleted record cannot be brought back, and a record can be the only copy of what it says"
	case AzureLifecycle:
		return "nothing is known to delete previous versions: the account may keep every version of every record forever, and nobody has chosen how long a record destroyed by mistake stays recoverable"
	case AzurePublicAccess:
		return "nothing is known to stop a container from being made readable anonymously, and the records hold secret material"
	}
	return "that assertion is not made"
}
