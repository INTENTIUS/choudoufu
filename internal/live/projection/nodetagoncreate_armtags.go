// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"

	"github.com/intentius/choudoufu/internal/live/substrate"
)

// The Azure family's post-create writer (#1120 unit 12,
// [substrate.WriteARMTagsAPI]), kept in its own file beside
// [TaggingAPIWriter] so the two families' writers merge independently.

// ARMTagger is ARM's Tags API merge onto one ARM id.
// *internal/live/azsweep.Tagger implements it; [ARMTagsWriter] adapts it
// to [MarkerWriter].
type ARMTagger interface {
	MergeTags(ctx context.Context, id string, tags map[string]string) error
}

// errNoARMScope is the ARM writer's refusal of an object it cannot address.
var errNoARMScope = errors.New("the object the provider returned carries no ARM id the Tags API can address (its id attribute, or for a tenant-level object its subscription_id)")

// ARMTagsWriter merges the withheld markers onto the created object's tag
// scope, [substrate.ARMTagScope]: the ARM id the provider returned, which
// is the id attribute of every azurerm and azapi resource.
type ARMTagsWriter struct {
	Tagger ARMTagger
}

// WriteMarkers implements [MarkerWriter].
func (w ARMTagsWriter) WriteMarkers(ctx context.Context, created CreatedInstance, tags map[string]string) error {
	scope := substrate.ARMTagScope(created.Object)
	if scope == "" {
		return errNoARMScope
	}
	if w.Tagger == nil {
		return errors.New("this run has no ARM tags client")
	}
	return w.Tagger.MergeTags(ctx, scope, tags)
}

// writeFoldsKeys reports whether a post-create write matches tag keys
// case-insensitively, so that what it stores has to be merged the same way
// ([mergeMarkersAt]). True for ARM's Tags API ([substrate.WriteARMTagsAPI]):
// its Merge replaces a key the object already carries in another spelling
// under that spelling (internal/live/azsweep's Tagger.MergeTags). The
// Tagging API's TagResources and a label patch match keys exactly.
func writeFoldsKeys(write substrate.Write) bool {
	return write == substrate.WriteARMTagsAPI
}
