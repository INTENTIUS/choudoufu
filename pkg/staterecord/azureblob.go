// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
)

// AzureBlobStore is a [Store] backed by blobs in one Azure Storage container,
// for record_store "azurerm" (the Azure epic, GitHub issue #1120, ruled on
// #1122). It is [S3Store] on Azure's primitives, and every choice below is
// the S3 store's choice made again where Azure lets it be made the same way.
//
// # What is genuinely atomic
//
// A version is the blob's ETag, unmodified, and every conditional operation
// is one Blob REST request carrying the condition:
//
//   - [AzureBlobStore.PutIfAbsent] and a "" [AzureBlobStore.PutIfVersion]
//     send Put Blob with If-None-Match: *. Azure answers 409 BlobAlreadyExists
//     when a blob is there (some paths answer 412 ConditionNotMet; both are
//     read as the conflict).
//   - A non-"" PutIfVersion sends If-Match: <ETag>, refused 412
//     ConditionNotMet on a mismatch, or 404 BlobNotFound when the blob is gone.
//   - [AzureBlobStore.Delete] sends Delete Blob with If-Match: <ETag>.
//
// No lease is taken and nothing is locked, which is the ruling on #1122: a
// lease is a lock with a timer, and a killed run would hold the record until
// the timer ran out, where a conditional write holds nothing.
//
// # The estate is a blob index tag, in the same request
//
// Every Put Blob carries x-ms-tags with tofu-estate (and a record's
// tofu-address chunks), so no blob exists untagged even for the length of a
// second request. That is what the ABAC read fence in
// live/azure/record-store-abac.condition conditions on: a role assignment
// condition can read a blob's index tags, and nothing else on a blob is
// readable to it. See [AzureBlobConfig.BaseTags].
//
// # What this store does not manage
//
// The storage account, the container and their settings are the operator's;
// this store reads and writes blobs in a container it is handed and builds no
// client of its own. internal/live/projection builds the container client
// with stock's azurerm backend auth (internal/backend/remote-state/azure/auth),
// so the authentication precedence is stock's and nowhere else.
// AzureEstateTag is the blob index tag that carries the estate, spelled as the
// marker spec spells it (live/MARKERS.md). Declared here rather than imported,
// for the reason [KubernetesEstateLabel] is: this package imports nothing that
// knows what an estate is (doc.go).
const AzureEstateTag = "tofu-estate"

type AzureBlobStore struct {
	container     *container.Client
	account       string
	resourceGroup string
	containerName string
	keyPrefix     string

	getAllParallelism int

	baseTags map[string]string

	// contract reads the account settings the opening checks assert. Nil
	// when no Azure Resource Manager credential could be built, and then
	// contractUnavailable says why; see azurecontract.go.
	contract            AzureContractAPI
	contractUnavailable string
}

// AzureBlobConfig configures an [AzureBlobStore].
type AzureBlobConfig struct {
	// Container is the Blob REST client for the container every record
	// lives in, already authenticated. The caller builds it.
	Container *container.Client

	// Account and ContainerName name the storage account and container, for
	// what an error or a refusal says. They are not used to build requests;
	// Container already carries its URL.
	Account       string
	ContainerName string

	// ResourceGroup is the account's resource group, for the az lines a
	// refusal prints.
	ResourceGroup string

	// KeyPrefix is joined ahead of every key, as [S3Config.KeyPrefix] is.
	KeyPrefix string

	// GetAllParallelism bounds how many Get Blob calls [AzureBlobStore.GetAll]
	// has in flight at once. Zero or negative takes
	// [DefaultS3GetAllParallelism], the same unmeasured eight.
	GetAllParallelism int

	// BaseTags go on every blob this store writes, as blob index tags. The
	// record store sets tofu-estate here, and it wins over a per-write tag of
	// the same key, so a blob can never name a different estate from the one
	// the store was opened for. [S3Config.BaseTags] has the reasoning.
	BaseTags map[string]string

	// Contract reads the storage account's settings for the opening checks.
	// Nil is allowed: ContractUnavailable then says why, and every setting is
	// reported [Unreadable] rather than passed.
	Contract            AzureContractAPI
	ContractUnavailable string
}

// NewAzureBlobStore builds an [AzureBlobStore] from cfg.
func NewAzureBlobStore(cfg AzureBlobConfig) (*AzureBlobStore, error) {
	if cfg.Container == nil {
		return nil, fmt.Errorf("staterecord: azurerm: Container must not be nil")
	}
	if cfg.ContainerName == "" {
		return nil, fmt.Errorf("staterecord: azurerm: ContainerName must not be empty")
	}
	return &AzureBlobStore{
		container:           cfg.Container,
		account:             cfg.Account,
		resourceGroup:       cfg.ResourceGroup,
		containerName:       cfg.ContainerName,
		keyPrefix:           cfg.KeyPrefix,
		getAllParallelism:   cfg.GetAllParallelism,
		baseTags:            cfg.BaseTags,
		contract:            cfg.Contract,
		contractUnavailable: cfg.ContractUnavailable,
	}, nil
}

// blobName joins s.keyPrefix and key into the blob name sent to Azure.
func (s *AzureBlobStore) blobName(key string) string {
	if s.keyPrefix == "" {
		return key
	}
	return strings.TrimSuffix(s.keyPrefix, "/") + "/" + key
}

// BlobName is the blob this store reads and writes key at, so a caller can
// say where a record is in words an operator can paste into `az storage blob`.
func (s *AzureBlobStore) BlobName(key string) string { return s.blobName(key) }

func (s *AzureBlobStore) keyFromBlobName(name string) string {
	if s.keyPrefix == "" {
		return name
	}
	return strings.TrimPrefix(name, strings.TrimSuffix(s.keyPrefix, "/")+"/")
}

// azureStatus is the HTTP status and Azure error code of err, or ok false
// when err is not a response from Azure at all.
func azureStatus(err error) (status int, code string, ok bool) {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.StatusCode, respErr.ErrorCode, true
	}
	return 0, "", false
}

// azureMissingBlob reports whether err is Azure saying the BLOB is not there.
// [missingKey]'s reasoning holds here unchanged (GitHub issue #1383): a 404
// for the container is not the absence of a record, and reading it as one
// would make a deleted container an empty estate. Azure always names which
// it is, BlobNotFound or ContainerNotFound; an uncoded 404 is read as the
// blob's, as the S3 store reads one.
func azureMissingBlob(err error) bool {
	status, code, ok := azureStatus(err)
	if !ok || status != http.StatusNotFound {
		return false
	}
	return code == "BlobNotFound" || code == ""
}

// azureConflict reports whether err is a failed write condition: 412
// ConditionNotMet for If-Match, and for If-None-Match: * either that or 409
// BlobAlreadyExists, which is what Put Blob answers when a blob exists.
func azureConflict(err error) bool {
	status, code, ok := azureStatus(err)
	if !ok {
		return false
	}
	return status == http.StatusPreconditionFailed || (status == http.StatusConflict && code == "BlobAlreadyExists")
}

// notTheBlob is the sentence a 404 gets when it named the container or
// anything else that is not this record.
func (s *AzureBlobStore) notTheBlob(err error) string {
	_, code, _ := azureStatus(err)
	return fmt.Sprintf("container %q in storage account %q answered %s, so the 404 is the container's own and not a missing key", s.containerName, s.account, code)
}

func (s *AzureBlobStore) opError(doing, key string, err error) error {
	return fmt.Errorf("staterecord: azurerm: %s %q in container %q: %w", doing, key, s.containerName, err)
}

// Get implements [Store].
func (s *AzureBlobStore) Get(ctx context.Context, key string) ([]byte, string, bool, error) {
	if err := validateKey(key); err != nil {
		return nil, "", false, err
	}
	rec, exists, err := s.download(ctx, key)
	if err != nil {
		return nil, "", false, fmt.Errorf("staterecord: azurerm: %w", err)
	}
	if !exists {
		return nil, "", false, nil
	}
	return rec.Payload, rec.Version, true, nil
}

// download is one Get Blob. exists is false with no error only for a 404 that
// named the blob; see [azureMissingBlob].
func (s *AzureBlobStore) download(ctx context.Context, key string) (Record, bool, error) {
	resp, err := s.container.NewBlobClient(s.blobName(key)).DownloadStream(ctx, nil)
	if err != nil {
		if azureMissingBlob(err) {
			return Record{}, false, nil
		}
		if status, _, ok := azureStatus(err); ok && status == http.StatusNotFound {
			return Record{}, false, fmt.Errorf("getting %q: %s: %w", key, s.notTheBlob(err), err)
		}
		return Record{}, false, fmt.Errorf("getting %q in container %q: %w", key, s.containerName, err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return Record{}, false, fmt.Errorf("reading %q: %w", key, err)
	}
	return Record{Payload: payload, Version: etagString(resp.ETag)}, true, nil
}

func etagString(e *azcore.ETag) string {
	if e == nil {
		return ""
	}
	return string(*e)
}

// conflictError is [S3Store.conflictError] for this store: a best-effort read
// to name the version the store holds now, keeping the refusal that brought
// us here when that read fails too.
func (s *AzureBlobStore) conflictError(ctx context.Context, key, expectedVersion string, cause error) error {
	_, actual, exists, err := s.Get(ctx, key)
	if err != nil {
		if cause == nil {
			return fmt.Errorf("staterecord: azurerm: %q failed its version condition, and the re-read that would name the version the store now holds failed too: %w", key, err)
		}
		return fmt.Errorf("staterecord: azurerm: %q failed its version condition, and the re-read that would name the version the store now holds failed too: %w (the condition failure: %w)", key, err, cause)
	}
	av := ""
	if exists {
		av = actual
	}
	return &VersionConflictError{Key: key, ExpectedVersion: expectedVersion, ActualVersion: av}
}

// PutIfAbsent implements [Store].
func (s *AzureBlobStore) PutIfAbsent(ctx context.Context, key string, payload []byte) (string, error) {
	return s.PutIfVersion(ctx, key, payload, "")
}

// PutIfVersion implements [Store]: one Put Blob with If-None-Match: * or
// If-Match: <expectedVersion>, and the blob's index tags in the same request.
func (s *AzureBlobStore) PutIfVersion(ctx context.Context, key string, payload []byte, expectedVersion string) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	cond := &blob.ModifiedAccessConditions{}
	if expectedVersion == "" {
		star := azcore.ETagAny
		cond.IfNoneMatch = &star
	} else {
		etag := azcore.ETag(expectedVersion)
		cond.IfMatch = &etag
	}
	opts := &blockblob.UploadOptions{
		Tags:             azureBlobTags(ObjectTags(ctx), s.baseTags),
		AccessConditions: &blob.AccessConditions{ModifiedAccessConditions: cond},
	}
	resp, err := s.container.NewBlockBlobClient(s.blobName(key)).Upload(ctx, streaming.NopCloser(bytes.NewReader(payload)), opts)
	if err != nil {
		if azureConflict(err) {
			return "", s.conflictError(ctx, key, expectedVersion, err)
		}
		// An update whose blob is gone is the same conflict from the
		// caller's side, as it is on S3 (#1344): the version it read is not
		// the version the store holds, because it holds none.
		if azureMissingBlob(err) && expectedVersion != "" {
			return "", s.conflictError(ctx, key, expectedVersion, err)
		}
		if status, _, ok := azureStatus(err); ok && status == http.StatusNotFound {
			return "", fmt.Errorf("staterecord: azurerm: writing %q: %s: %w", key, s.notTheBlob(err), err)
		}
		return "", s.opError("writing", key, err)
	}
	return etagString(resp.ETag), nil
}

// azureBlobTags merges tag sets for x-ms-tags, later sets winning on a shared
// key as [encodeObjectTagging] does, so [AzureBlobConfig.BaseTags] (passed
// last) always carries the estate.
//
// One difference from S3, and it is a charset. A blob index tag value takes
// only letters, digits, space and + - . / : = _ (Azure's "Manage and find
// data with blob index tags" page), and an escaped address can carry "@"
// (markers.EscapeKey doubles it and uses it to escape "." and ":" in an
// instance key). Azure refuses the whole Put Blob with 400 InvalidTag for
// one such character. So when any address tag's value is outside that
// charset, the address tags are left off THAT blob and tofu-estate is
// written alone. The estate tag is what the fence conditions on and is never
// dropped (an estate name is [a-z][a-z0-9-]*, always inside the charset);
// the address tags are provenance, and a missing one says nothing wrong
// where a rewritten one would say something false.
func azureBlobTags(sets ...map[string]string) map[string]string {
	merged := map[string]string{}
	for _, set := range sets {
		for k, v := range set {
			merged[k] = v
		}
	}
	if len(merged) == 0 {
		return nil
	}
	for k, v := range merged {
		if k == AzureEstateTag {
			continue
		}
		if !azureTagValueOK(v) {
			for k2 := range merged {
				if k2 != AzureEstateTag {
					delete(merged, k2)
				}
			}
			break
		}
	}
	return merged
}

// azureTagValueOK is the blob index tag value charset.
func azureTagValueOK(v string) bool {
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune(" +-./:=_", r):
		default:
			return false
		}
	}
	return len(v) <= 256
}

// Delete implements [Store]: Delete Blob with If-Match. expectedVersion == ""
// against an absent key is a no-op, checked by a read first, as on S3.
func (s *AzureBlobStore) Delete(ctx context.Context, key string, expectedVersion string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if expectedVersion == "" {
		_, _, exists, err := s.Get(ctx, key)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		return s.conflictError(ctx, key, expectedVersion, nil)
	}
	etag := azcore.ETag(expectedVersion)
	_, err := s.container.NewBlobClient(s.blobName(key)).Delete(ctx, &blob.DeleteOptions{
		AccessConditions: &blob.AccessConditions{ModifiedAccessConditions: &blob.ModifiedAccessConditions{IfMatch: &etag}},
	})
	if err != nil {
		if azureConflict(err) || azureMissingBlob(err) {
			return s.conflictError(ctx, key, expectedVersion, err)
		}
		if status, _, ok := azureStatus(err); ok && status == http.StatusNotFound {
			return fmt.Errorf("staterecord: azurerm: deleting %q: %s: %w", key, s.notTheBlob(err), err)
		}
		return s.opError("deleting", key, err)
	}
	return nil
}

// List implements [Store] by paging List Blobs with prefix set, which is an
// ordinary string prefix on Azure as on S3, so the trailing delimiter
// [NamespacePrefix] adds does the same isolating work here (#1335). Only
// current blobs are listed: no include=versions, so a versioned container's
// noncurrent versions and a soft-deleted blob are not records.
func (s *AzureBlobStore) List(ctx context.Context, keyPrefix string) ([]string, error) {
	if err := validateKeyPrefix(keyPrefix); err != nil {
		return nil, err
	}
	prefix := s.blobName(keyPrefix)
	pager := s.container.NewListBlobsFlatPager(&container.ListBlobsFlatOptions{Prefix: &prefix})
	var keys []string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if status, _, ok := azureStatus(err); ok && status == http.StatusNotFound {
				return nil, fmt.Errorf("staterecord: azurerm: listing %q: %s: %w", keyPrefix, s.notTheBlob(err), err)
			}
			return nil, s.opError("listing", keyPrefix, err)
		}
		if page.Segment == nil {
			continue
		}
		for _, item := range page.Segment.BlobItems {
			if item == nil || item.Name == nil {
				continue
			}
			keys = append(keys, s.keyFromBlobName(*item.Name))
		}
	}
	sort.Strings(keys)
	return keys, nil
}

var _ BulkReader = (*AzureBlobStore)(nil)

// GetAll implements [BulkReader] exactly as [S3Store.GetAll] does, for the
// same reason: Blob REST has no batch read, so it is one listing and one Get
// Blob per key, bounded, and the result is complete or it fails. A key the
// listing named and two reads did not find fails the whole read
// (GitHub issue #1355).
func (s *AzureBlobStore) GetAll(ctx context.Context, keyPrefix string) (map[string]Record, error) {
	keys, err := s.List(ctx, keyPrefix)
	if err != nil {
		return nil, err
	}
	workers := s.getAllParallelism
	if workers < 1 {
		workers = DefaultS3GetAllParallelism
	}
	if workers > len(keys) {
		workers = len(keys)
	}
	found := make([]*Record, len(keys))
	err = boundedFanOut(ctx, len(keys), workers, func(ctx context.Context, i int) error {
		rec, exists, getErr := s.download(ctx, keys[i])
		if getErr != nil {
			return getErr
		}
		if !exists {
			rec, exists, getErr = s.download(ctx, keys[i])
			if getErr != nil {
				return getErr
			}
		}
		if exists {
			found[i] = &rec
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("staterecord: azurerm: reading everything under %q: %w", keyPrefix, err)
	}
	var vanished []string
	for i, rec := range found {
		if rec == nil {
			vanished = append(vanished, keys[i])
		}
	}
	if len(vanished) > 0 {
		return nil, fmt.Errorf("staterecord: azurerm: reading everything under %q: %s, twice over: the listing and the reads that follow it disagree about what this namespace holds, so what came back is not a snapshot of it and is refused rather than returned short (GitHub issue #1355)", keyPrefix, namesVanished(vanished))
	}
	out := make(map[string]Record, len(keys))
	for i, rec := range found {
		out[keys[i]] = *rec
	}
	return out, nil
}
