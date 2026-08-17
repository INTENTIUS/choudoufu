// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/pins"
	"github.com/intentius/choudoufu/internal/live/pluginschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// acquireAWSSchemas reads hashicorp/aws's resource-type schemas at
// [pins.AWSProviderVersion], the same pin tools/refusal-probe forces for the
// same provider and internal/live/identity's TestLocatedTypePopulation
// measures against.
//
// This tool only ever asks identity.LocatedType about AWS types -
// [locatedAll] is reached solely from the markerless-type cause set, and
// live/survey-full.json describes exactly one provider - so a single pinned
// acquisition is enough; tools/refusal-probe's multi-provider acquirer
// exists for a different job (measuring an arbitrary corpus entry's own
// requirements) and would be the wrong tool to copy here.
func acquireAWSSchemas(initBin string) (map[string]providers.Schema, error) {
	dir, err := os.MkdirTemp("", "estate-plan-schemas")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	schemas, err := pluginschema.ResourceTypes(context.Background(), pluginschema.Request{
		InitBin:  initBin,
		WorkDir:  dir,
		Source:   "hashicorp/aws",
		Version:  pins.AWSProviderVersion,
		Provider: addrs.NewDefaultProvider("aws"),
	})
	if err != nil {
		return nil, fmt.Errorf("acquiring hashicorp/aws %s: %w", pins.AWSProviderVersion, err)
	}
	return schemas, nil
}
