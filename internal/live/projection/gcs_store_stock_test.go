// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection_test

import (
	"fmt"
	"slices"
	"sort"
	"testing"

	"github.com/intentius/choudoufu/internal/backend/remote-state/gcs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/encryption"
	"github.com/intentius/choudoufu/internal/live/projection"
)

// stockGCSSchema is stock's gcs backend's argument schema, read from stock's
// own package. This file imports it from the outside, which is the only
// direction the import may go (see gcs_store.go's gcsConnection).
func stockGCSSchema(t *testing.T) *gcs.Backend {
	t.Helper()
	b, ok := gcs.New(encryption.StateEncryptionDisabled()).(*gcs.Backend)
	if !ok {
		t.Fatal("gcs.New no longer returns a *gcs.Backend")
	}
	return b
}

// TestGCSStoreTakesStocksArguments: record_store "gcs" takes every argument
// of stock's gcs backend except "prefix", which every record store spells
// "key_prefix", and nothing stock does not have. An argument stock adds on an
// upstream sync fails this until the record store takes it or says why not.
func TestGCSStoreTakesStocksArguments(t *testing.T) {
	var stock []string
	for name := range stockGCSSchema(t).Schema {
		if name == "bucket" || name == "prefix" {
			continue
		}
		stock = append(stock, name)
	}
	ours := slices.Clone(configs.RecordStoreGCSSettings)
	sort.Strings(stock)
	sort.Strings(ours)
	if !slices.Equal(stock, ours) {
		t.Errorf("stock gcs takes %v besides bucket and prefix; record_store \"gcs\" takes %v", stock, ours)
	}
}

// TestGCSStoreDefaultsFromStocksEnvironment: every argument stock's schema
// defaults from the environment is defaulted from the same variables in the
// same order here. Each variable is set to its own value, then unset one at a
// time from the front, so an order that differs from stock's shows.
func TestGCSStoreDefaultsFromStocksEnvironment(t *testing.T) {
	schema := stockGCSSchema(t).Schema
	for name, s := range schema {
		if s.DefaultFunc == nil {
			continue
		}
		if _, ok := projection.GCSEnvDefaultsForTest[name]; !ok {
			t.Errorf("stock defaults %q from the environment and record_store \"gcs\" does not", name)
		}
	}
	for name, vars := range projection.GCSEnvDefaultsForTest {
		s, ok := schema[name]
		if !ok || s.DefaultFunc == nil {
			t.Errorf("record_store \"gcs\" defaults %q from %v and stock does not default it at all", name, vars)
			continue
		}
		for _, v := range vars {
			t.Setenv(v, "")
		}
		for i := range vars {
			for j, v := range vars {
				if j >= i {
					t.Setenv(v, fmt.Sprintf("%s-%d", v, j))
				}
			}
			want, err := s.DefaultFunc()
			if err != nil {
				t.Fatal(err)
			}
			got := projection.GCSConnectionEnvForTest(&configs.LiveRecordStore{Type: "gcs"})[name]
			if got != want {
				t.Errorf("%s with %v set from index %d: record_store reads %q, stock reads %v", name, vars, i, got, want)
			}
			t.Setenv(vars[i], "")
		}
	}
}
