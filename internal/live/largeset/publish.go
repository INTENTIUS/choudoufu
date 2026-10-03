// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package largeset

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"sort"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
)

// The artifact and layer types stock's module getter accepts
// (internal/getmodules/oci_getter.go): an OCI image manifest whose
// artifactType is the OpenTofu module package type, carrying one archive/zip
// layer.
const (
	ModulePackageArtifactType = "application/vnd.opentofu.modulepkg"
	ModulePackageLayerType    = "archive/zip"
)

// fixedTime is the timestamp every zip entry and the manifest's created
// annotation carry, so a version's package and manifest digests are the same
// on every publish.
var fixedTime = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

// ModuleZip is the module package at version v as a deterministic zip.
func ModuleZip(v Version) ([]byte, error) {
	files := ModulePackage(v)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Deflate, Modified: fixedTime})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(files[n]); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Publish pushes the module package at version v to registry (host:port),
// tagged v.OCITag(), and returns the manifest digest. caFile is the PEM the
// registry's certificate chains to; stock's getter speaks HTTPS only, so the
// registry the fixture uses always has one.
func Publish(ctx context.Context, registry, caFile string, v Version) (string, error) {
	repo, err := remote.NewRepository(registry + "/" + OCIRepository)
	if err != nil {
		return "", err
	}
	pool := x509.NewCertPool()
	pem, err := os.ReadFile(caFile) //nolint:gosec // operator-supplied path
	if err != nil {
		return "", fmt.Errorf("reading the registry CA: %w", err)
	}
	if !pool.AppendCertsFromPEM(pem) {
		return "", fmt.Errorf("%s holds no PEM certificate", caFile)
	}
	repo.Client = &auth.Client{
		Client: &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}},
	}

	zipped, err := ModuleZip(v)
	if err != nil {
		return "", err
	}
	layer := content.NewDescriptorFromBytes(ModulePackageLayerType, zipped)
	layer.Annotations = map[string]string{ocispec.AnnotationTitle: "largeset-shared-" + v.OCITag() + ".zip"}
	if err := pushIfAbsent(ctx, repo, layer, zipped); err != nil {
		return "", fmt.Errorf("pushing the module package: %w", err)
	}
	desc, err := oras.PackManifest(ctx, repo, oras.PackManifestVersion1_1, ModulePackageArtifactType, oras.PackManifestOptions{
		Layers:              []ocispec.Descriptor{layer},
		ManifestAnnotations: map[string]string{ocispec.AnnotationCreated: fixedTime.Format(time.RFC3339)},
	})
	if err != nil {
		return "", fmt.Errorf("packing the manifest: %w", err)
	}
	if err := repo.Tag(ctx, desc, v.OCITag()); err != nil {
		return "", fmt.Errorf("tagging %s: %w", v.OCITag(), err)
	}
	return desc.Digest.String(), nil
}

func pushIfAbsent(ctx context.Context, repo *remote.Repository, desc ocispec.Descriptor, b []byte) error {
	ok, err := repo.Exists(ctx, desc)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	return repo.Push(ctx, desc, bytes.NewReader(b))
}
