// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// extractRegistrySchemas mirrors tools/registry-gen/registry.go's own
// extractSchemas: raw per-type JSON schema bytes keyed by typeName. See
// detect.go's registryZipURL comment for why this is duplicated here
// instead of imported - registry-gen is package main.
func extractRegistrySchemas(zipData []byte) (map[string][]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("opening the registry zip: %w", err)
	}

	schemas := make(map[string][]byte, len(r.File))
	for _, f := range r.File {
		if f.FileInfo().IsDir() || !strings.HasSuffix(f.Name, ".json") {
			continue
		}
		data, err := readZipFile(f)
		if err != nil {
			return nil, fmt.Errorf("reading %s in the registry zip: %w", f.Name, err)
		}
		var probe struct {
			TypeName string `json:"typeName"`
		}
		if err := json.Unmarshal(data, &probe); err != nil || probe.TypeName == "" {
			continue
		}
		schemas[probe.TypeName] = data
	}
	if len(schemas) == 0 {
		return nil, fmt.Errorf("the registry zip carried no schemas with a typeName")
	}
	return schemas, nil
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close() //nolint:errcheck // a read-only zip entry
	return io.ReadAll(rc)
}

// registryContentDigest mirrors tools/registry-gen/pin.go's ContentDigest:
// sorted typeName -> sha256(schema), hashed in order - stable against the
// archive being repackaged, moves when a schema's bytes change or a type is
// added or removed.
func registryContentDigest(schemas map[string][]byte) string {
	names := make([]string, 0, len(schemas))
	for name := range schemas {
		names = append(names, name)
	}
	sort.Strings(names)

	h := sha256.New()
	for _, name := range names {
		h.Write([]byte(name))
		sum := sha256.Sum256(schemas[name])
		h.Write(sum[:])
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// diffTypeSets reports which type names in schemas aren't in pinnedNames
// (added) and which names in pinnedNames have no schema (removed), both
// sorted.
func diffTypeSets(schemas map[string][]byte, pinnedNames map[string]bool) (added, removed []string) {
	for name := range schemas {
		if !pinnedNames[name] {
			added = append(added, name)
		}
	}
	for name := range pinnedNames {
		if _, ok := schemas[name]; !ok {
			removed = append(removed, name)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}
