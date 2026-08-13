// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// resolveDigest returns image's bare "sha256:<hex>" digest. image already
// pinned by digest (repo@sha256:...) is returned as-is, split off the "@" -
// no docker call needed. Otherwise image is a mutable tag or bare name, and
// this shells out to `docker inspect` for the digest of the image docker
// actually has locally under that ref - the same image a container started
// from that ref is running - refusing rather than guessing when docker has
// no RepoDigest for it (an image built or loaded locally with no registry
// digest of its own, e.g. `docker build`, has none - only a `docker pull`ed
// or `docker push`ed image does).
func resolveDigest(image string) (string, error) {
	if i := strings.Index(image, "@sha256:"); i >= 0 {
		return image[i+1:], nil
	}

	out, err := exec.Command("docker", "inspect", "--format", "{{index .RepoDigests 0}}", image).Output() //nolint:gosec // caller-supplied image ref, a local CLI tool's own argument
	if err != nil {
		return "", fmt.Errorf("`docker inspect %s` failed (image ref not pinned by digest, and docker could not resolve one locally - pull it by digest, or pass -image a digest-pinned ref directly): %w", image, err)
	}
	digest := strings.TrimSpace(string(out))
	i := strings.Index(digest, "@sha256:")
	if i < 0 {
		return "", fmt.Errorf("`docker inspect %s` returned %q, which has no @sha256: digest to extract", image, digest)
	}
	return digest[i+1:], nil
}
