// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package configs

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
)

// The record store and estate a root gets from what it already has (GitHub
// issue #2293, ruling 2026-10-27: "Where we can derive, I want it.").
//
// A live root that declares no record_store block used to get the implied
// local store, which on a CI runner is empty every run. Most roots moving to
// live already name a bucket in their backend block, and since #2213 that
// block stays beside the live block and is kept as [Module.IgnoredBackend].
// So the store is derived from it: s3 to s3, gcs to gcs, azurerm to azurerm,
// under the records prefix. An explicit record_store always wins, and a
// backend with no record store counterpart leaves the implied local store in
// place with [Live.RecordStoreNote] saying why.

// deriveRecordStore maps a backend block onto the record store it implies.
// It returns nil and the reason when the backend has no counterpart or does
// not set what the store needs as a literal in the configuration (a value
// passed with -backend-config is not known here).
func deriveRecordStore(be *Backend) (*LiveRecordStore, string) {
	if be == nil {
		return nil, ""
	}
	where := fmt.Sprintf("backend %q at %s", be.Type, rangeFileLine(be.DeclRange))
	rs := &LiveRecordStore{
		Type:      be.Type,
		TypeRange: be.TypeRange,
		DeclRange: be.DeclRange,
		Derived:   true,
	}
	switch be.Type {
	case "s3":
		bucket, ok := BackendLiteral(be, "bucket")
		if !ok || bucket == "" {
			return nil, where + " sets no literal bucket; values passed with -backend-config are not read"
		}
		rs.Bucket, rs.BucketSet = bucket, true
		if region, ok := BackendLiteral(be, "region"); ok && region != "" {
			rs.Region, rs.RegionSet = region, true
		}
	case "gcs":
		bucket, ok := BackendLiteral(be, "bucket")
		if !ok || bucket == "" {
			return nil, where + " sets no literal bucket; values passed with -backend-config are not read"
		}
		rs.Bucket, rs.BucketSet = bucket, true
		for _, f := range []struct {
			name string
			val  *string
		}{
			{"credentials", &rs.GCS.Credentials},
			{"access_token", &rs.GCS.AccessToken},
			{"impersonate_service_account", &rs.GCS.ImpersonateServiceAccount},
			{"encryption_key", &rs.GCS.EncryptionKey},
			{"kms_encryption_key", &rs.GCS.KMSEncryptionKey},
			{"storage_custom_endpoint", &rs.GCS.StorageCustomEndpoint},
			{"universe_domain", &rs.GCS.UniverseDomain},
		} {
			if v, ok := BackendLiteral(be, f.name); ok {
				*f.val = v
			}
		}
	case "azurerm":
		for _, f := range recordStoreAzureRMStrings(&rs.AzureRM) {
			if v, ok := BackendLiteral(be, f.name); ok {
				*f.val = v
			}
		}
		for _, f := range recordStoreAzureRMBools(&rs.AzureRM) {
			if v, ok := backendLiteralBool(be, f.name); ok {
				b := v
				*f.val = &b
			}
		}
		for _, req := range []struct{ name, val string }{
			{"storage_account_name", rs.AzureRM.StorageAccountName},
			{"container_name", rs.AzureRM.ContainerName},
			{"resource_group_name", rs.AzureRM.ResourceGroupName},
		} {
			if req.val == "" {
				return nil, fmt.Sprintf("%s sets no literal %s, which the azurerm record store needs", where, req.name)
			}
		}
	case "local":
		return nil, ""
	default:
		return nil, where + " has no record store counterpart"
	}
	return rs, ""
}

// DeriveRecordStore is [deriveRecordStore] for a caller outside this
// package: live-check reports the store a root not yet converted would get
// from its active backend block.
func DeriveRecordStore(be *Backend) (*LiveRecordStore, string) {
	return deriveRecordStore(be)
}

// BackendLiteral reads one top-level argument of a backend block as a literal
// string. ok is false when the argument is absent or is not a known string.
func BackendLiteral(be *Backend, name string) (string, bool) {
	val, ok := backendLiteralValue(be, name)
	if !ok || val.Type() != cty.String {
		return "", false
	}
	return val.AsString(), true
}

func backendLiteralBool(be *Backend, name string) (bool, bool) {
	val, ok := backendLiteralValue(be, name)
	if !ok || val.Type() != cty.Bool {
		return false, false
	}
	return val.True(), true
}

func backendLiteralValue(be *Backend, name string) (cty.Value, bool) {
	if be == nil || be.Config == nil {
		return cty.NilVal, false
	}
	content, _, _ := be.Config.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{{Name: name}},
	})
	if content == nil {
		return cty.NilVal, false
	}
	attr, exists := content.Attributes[name]
	if !exists {
		return cty.NilVal, false
	}
	val, diags := attr.Expr.Value(nil)
	if diags.HasErrors() || val.IsNull() || !val.IsWhollyKnown() {
		return cty.NilVal, false
	}
	return val, true
}

// BackendStateKey is where a backend block keeps its state, as written: the
// s3 and azurerm "key", or the gcs "prefix". ok is false for any other
// backend or when the argument is not a literal.
func BackendStateKey(be *Backend) (string, bool) {
	if be == nil {
		return "", false
	}
	switch be.Type {
	case "s3", "azurerm":
		return BackendLiteral(be, "key")
	case "gcs":
		return BackendLiteral(be, "prefix")
	}
	return "", false
}

// EstateFromStateKey turns a backend state key into an estate name:
// prod/network/terraform.tfstate gives prod-network. The trailing *.tfstate
// segment is dropped and the rest is normalized to the marker grammar by
// [NormalizeEstateName]. It returns "" when nothing usable is left.
func EstateFromStateKey(key string) string {
	key = strings.Trim(filepath.ToSlash(key), "/")
	if strings.HasSuffix(key, ".tfstate") {
		key = path.Dir(key)
		if key == "." {
			return ""
		}
	}
	return NormalizeEstateName(key)
}

// EstateFromRootPath turns a root module directory into an estate name: its
// path from the repository top (the nearest ancestor holding .git),
// normalized by [NormalizeEstateName]. A Terragrunt cache tail
// (".terragrunt-cache/<hash>/<hash>") is stripped first, so a unit gets the
// name of its own directory. The repository root itself, or a directory
// outside any repository, gives its base name.
func EstateFromRootPath(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	slashed := filepath.ToSlash(abs)
	if i := strings.Index(slashed, "/.terragrunt-cache/"); i >= 0 {
		abs = filepath.FromSlash(slashed[:i])
	}
	rel := filepath.Base(abs)
	for d := abs; ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			if r, err := filepath.Rel(d, abs); err == nil && r != "." {
				rel = r
			}
			break
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return NormalizeEstateName(rel)
}

// NormalizeEstateName maps a key or path onto the marker grammar
// (internal/live/markers' ^[a-z][a-z0-9-]{0,127}$): lowercase, every run of
// characters outside [a-z0-9] becomes one "-", and leading and trailing "-"
// are trimmed. It returns "" when the result does not start with a letter.
func NormalizeEstateName(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if len(out) > 128 {
		out = strings.TrimRight(out[:128], "-")
	}
	if out == "" || out[0] < 'a' || out[0] > 'z' {
		return ""
	}
	return out
}

// rangeFileLine is "file:line" for a range, the short form the record store
// line prints.
func rangeFileLine(r hcl.Range) string {
	if r.Filename == "" {
		return "?"
	}
	return fmt.Sprintf("%s:%d", filepath.Base(r.Filename), r.Start.Line)
}

// RangeFileLine is [rangeFileLine] for internal/command.
func RangeFileLine(r hcl.Range) string { return rangeFileLine(r) }
