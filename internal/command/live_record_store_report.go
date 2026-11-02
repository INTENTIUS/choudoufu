// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"path/filepath"

	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/projection"
)

// GitHub issue #2293: where records go and which estate they belong to,
// derived from what a root already has and printed on every init and plan,
// and carried in live-check -json for callers that read no HCL (terragucci's
// init, chant's local-store lint). The JSON field names are the contract
// posted on the issue before the build.

// The values of estate_source.
const (
	liveEstateSourceExplicit   = "explicit"
	liveEstateSourceTags       = "tags"
	liveEstateSourceBackendKey = "backend_key"
	liveEstateSourceRootPath   = "root_path"
	liveEstateSourceNone       = "none"
)

// The values of record_store.source.
const (
	liveStoreSourceRecordStore = "record_store"
	liveStoreSourceBackend     = "backend"
	liveStoreSourceImplied     = "implied"
)

// liveDerivedEstate is the estate name a module with no declared name and no
// tofu-estate tags gets: its backend block's state key, else its path in the
// repository. Under a live block the backend is [configs.Module.IgnoredBackend];
// on a root not yet converted it is the active one.
func liveDerivedEstate(mod *configs.Module) (string, string) {
	if mod == nil {
		return "", ""
	}
	be := mod.IgnoredBackend
	if be == nil {
		be = mod.Backend
	}
	if key, ok := configs.BackendStateKey(be); ok {
		if name := configs.EstateFromStateKey(key); name != "" {
			return name, liveEstateSourceBackendKey
		}
	}
	dir := mod.SourceDir
	if dir == "" {
		dir = "."
	}
	if name := configs.EstateFromRootPath(dir); name != "" {
		return name, liveEstateSourceRootPath
	}
	return "", ""
}

// liveSetupFor resolves the estate and record store of one root. It makes no
// cloud call. estateFlag is an explicit name from the command line, or "".
//
// A root with a live block reports the store its live block resolved to
// (declared, derived from the ignored backend, or implied). A root with no
// live block reports what it would get once one is added: the store its
// active backend block implies, and the estate from its tags, backend key or
// path.
func liveSetupFor(ctx context.Context, estateFlag string, config *configs.Config) views.LiveSetup {
	var out views.LiveSetup
	if config == nil || config.Module == nil {
		out.EstateSource = liveEstateSourceNone
		return out
	}
	mod := config.Module

	estate, source, found, diags := liveResolveEstate(ctx, estateFlag, config)
	if !diags.HasErrors() && estate == "" && len(found) == 0 && mod.Live == nil {
		estate, source = liveDerivedEstate(mod)
		if estate == "" {
			source = liveEstateSourceNone
		}
	}
	out.Estate, out.EstateSource = estate, source

	var rs *configs.LiveRecordStore
	note := ""
	if mod.Live != nil {
		rs, note = mod.Live.RecordStore, mod.Live.RecordStoreNote
	} else if mod.Backend != nil {
		rs, note = configs.DeriveRecordStore(mod.Backend)
	}
	out.RecordStore = liveSetupStore(rs, estate)
	out.RecordStore.Note = note
	return out
}

func liveSetupStore(rs *configs.LiveRecordStore, estate string) views.LiveSetupStore {
	if rs == nil {
		return views.LiveSetupStore{
			Kind:   "local",
			Bucket: ".tofu-records",
			Prefix: liveSetupPrefix(nil, estate),
			Source: liveStoreSourceImplied,
		}
	}
	out := views.LiveSetupStore{
		Kind:   rs.Type,
		Prefix: liveSetupPrefix(rs, estate),
	}
	switch {
	case rs.Derived:
		out.Source = liveStoreSourceBackend
		out.DeclaredAt = configs.RangeFileLine(rs.DeclRange)
	case rs.Implied:
		out.Source = liveStoreSourceImplied
	default:
		out.Source = liveStoreSourceRecordStore
		out.DeclaredAt = configs.RangeFileLine(rs.DeclRange)
	}
	switch rs.Type {
	case "s3", "gcs":
		out.Bucket = rs.Bucket
	case "azurerm":
		out.Bucket = rs.AzureRM.StorageAccountName + "/" + rs.AzureRM.ContainerName
	case "kubernetes":
		out.Bucket = rs.Namespace
	case "local":
		out.Bucket = ".tofu-records"
		if rs.PathSet {
			out.Bucket = filepath.ToSlash(rs.Path)
		}
	}
	return out
}

func liveSetupPrefix(rs *configs.LiveRecordStore, estate string) string {
	if rs == nil || !rs.KeyPrefixSet {
		if estate == "" {
			return ""
		}
	}
	return projection.RecordStoreKeyPrefix(rs, estate)
}

// printLiveSetup writes the one line every init and plan prints under a live
// block, on stderr so a -json stdout stays one document.
func printLiveSetup(view *views.View, setup views.LiveSetup) {
	if view == nil {
		return
	}
	views.NewLiveSetup(view).Print(setup)
}

// liveSetupLine is the printed form, kept here so a test can read it without
// a view.
func liveSetupLine(setup views.LiveSetup) string {
	return views.LiveSetupLine(setup)
}
