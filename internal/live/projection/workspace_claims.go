// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/pkg/staterecord"
)

// This file is GitHub issue #2221's shared-estate notice. Under a live block
// a selected workspace is ignored: the estate named in the block owns the
// records, so two stacks that differ only by workspace (the Atmos default,
// one workspace per stack) read and write the same records. That is correct
// when it is meant and a trap when it is not, so the second stack is told,
// naming both, instead of sharing silently.
//
// The registry is one small key beside the estate's hint, in the hint's own
// namespace so orphan discovery never lists it. It is a notice and never
// authority: every failure to read or write it is a warning at most, and the
// records themselves are untouched.

const workspaceClaimsFormatVersion = "tofu-live-workspace-claims-v1"

// WorkspaceClaimsKey is where one estate's workspace registry lives.
func WorkspaceClaimsKey(estate string) string {
	return HintKeyPrefix(estate) + "workspaces"
}

type workspaceClaims struct {
	FormatVersion string `json:"formatVersion"`
	Estate        string `json:"estate"`
	// Workspaces maps a workspace name to when an apply under it last ran.
	Workspaces map[string]string `json:"workspaces"`
}

// NoteWorkspaceClaim reads the estate's registry, warns when a workspace other
// than workspace has applied to this estate, and, when record is true, adds
// workspace to the registry. Plans read and never write.
func NoteWorkspaceClaim(ctx context.Context, store staterecord.Store, estate, workspace string, record bool, clock func() time.Time) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	key := WorkspaceClaimsKey(estate)
	for attempt := 0; attempt < 3; attempt++ {
		payload, version, exists, err := store.Get(ctx, key)
		if err != nil {
			log.Printf("[WARN] live/workspaces: reading the workspace registry for estate %q: %s", estate, err)
			return diags
		}
		claims := workspaceClaims{FormatVersion: workspaceClaimsFormatVersion, Estate: estate, Workspaces: map[string]string{}}
		if exists {
			var read workspaceClaims
			if json.Unmarshal(payload, &read) == nil && read.FormatVersion == workspaceClaimsFormatVersion && read.Workspaces != nil {
				claims = read
			}
		}
		if attempt == 0 {
			var others []string
			for name := range claims.Workspaces {
				if name != workspace {
					others = append(others, fmt.Sprintf("%q", name))
				}
			}
			if len(others) > 0 {
				sort.Strings(others)
				diags = diags.Append(tfdiags.Sourceless(
					tfdiags.Warning,
					"Workspaces share this estate's records",
					fmt.Sprintf("Workspace %q and workspace %s both run against estate %q. Workspaces are ignored under live resource markers, so they read and write the same records and the same marked resources. "+
						"If they are meant to be separate environments, give each its own estate name in its own live block.",
						workspace, strings.Join(others, ", "), estate),
				))
			}
		}
		if !record {
			return diags
		}
		if _, seen := claims.Workspaces[workspace]; seen {
			return diags
		}
		claims.Workspaces[workspace] = clock().UTC().Format(time.RFC3339)
		out, err := json.Marshal(claims)
		if err != nil {
			return diags
		}
		_, err = store.PutIfVersion(ctx, key, out, version)
		if err == nil {
			return diags
		}
		var conflict *staterecord.VersionConflictError
		if errors.As(err, &conflict) {
			continue
		}
		log.Printf("[WARN] live/workspaces: writing the workspace registry for estate %q: %s", estate, err)
		return diags
	}
	return diags
}
