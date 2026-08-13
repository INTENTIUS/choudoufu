// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package cloudcontrol

import "context"

// GetResourceByIdentity fetches one resource by identity, falling back to a
// full ListResources plus an identifier match when GetResource itself
// refuses with CodeUnsupportedOperation - floci's exact answer for some
// types while ListResources on the same type works fine (see errors.go).
//
// This is chant's proven readByIdentity (identity-observe.ts) ported to Go:
// every other refusal from GetResource propagates unchanged, because the
// caller - not this function - decides what a failed refinement means; only
// UnsupportedOperation specifically degrades to the list-and-match path
// rather than surfacing as an error. A miss in the fallback list (the
// identifier is genuinely not present) is reported as (nil, nil): a
// confirmed absence, not a failure.
func GetResourceByIdentity(ctx context.Context, c *Client, typeName, identifier string) (*ResourceDescription, error) {
	desc, err := c.GetResource(ctx, typeName, identifier)
	if err == nil {
		return desc, nil
	}
	if !HasCode(err, CodeUnsupportedOperation) {
		return nil, err
	}

	listed, listErr := c.ListResources(ctx, typeName)
	if listErr != nil {
		return nil, listErr
	}
	for i := range listed {
		if listed[i].Identifier == identifier {
			return &listed[i], nil
		}
	}
	return nil, nil
}
