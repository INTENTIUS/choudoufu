# Substrates

What each provider family uses for identity, markers, writes, sweep and
fence, generated from the substrate implementations in
`internal/live/substrate`. The fence and harness notes are kept by hand in
`tools/substrates-gen`, because the fence is never code here (#1118).

Rendered from `live/substrates.json` by `go run ./tools/substrates-gen -render`
(or `go run ./tools/gauntlet render`). Edit the text around the generated
markers, never inside them.

<!-- substrates-gen:begin substrates -->
| | AWS | AZURERM | KUBERNETES |
|---|---|---|---|
| Identity | the block address is a key of the marker map itself, beside tofu-estate | the block address is a key of the marker map itself, beside tofu-estate | the block address is carried outside the marker map, in its own annotation |
| Marker surfaces | `tags`: tags attribute (tag) | `arm-tags`: tags attribute (tag) | `labels`: metadata.labels map (label)<br>`manifest`: manifest.metadata.labels map (label) |
| Writes (create / adopt / post-create) | `tags`: in-create / tags-only-plan / tagging-api | `arm-tags`: in-create / tags-only-plan / arm-tags-api | `labels`: in-create / labels-only-plan / never-needed<br>`manifest`: in-create / api-patch / never-needed |
| Sweep | tagging-index: runs through the configured provider itself; no separate client is built from the provider block | resource-graph: one Azure Resource Graph query per subscription the estate's resources resolve to, matching the marker case-insensitively, through clients built from the azurerm and azapi provider blocks' own connection arguments (Substrate.NewSweeper; GitHub issue #1120) | label-list: a cluster client built from the provider block's own connection arguments (Substrate.NewSweeper) |
| Controller-held | the resource's own tags carry one of the fixed controller tag keys (markers.ControllerTagKeys: ACK, Crossplane) | the object's ARM managedBy property, or a tag AKS writes on its node resource group (aks-managed- prefix, case-insensitive; AKS FAQ) | the object's Helm release annotation (kubesweep.HelmReleaseNameAnnotation), read off the object itself rather than off the label marker map |
| Untaggable types | 852 (declaration-carried: 379; excluded by design: 2; record-carried: 471) | 719 (declaration-carried: 488; record-carried: 231) | 0 |
| Fence | IAM conditions on aws:ResourceTag, reads and writes, per principal (live/MARKERS.md). | Resource-group RBAC per principal (a custom role or Contributor on the estate's groups), plus Azure Policy on the tofu-estate tag: deny a write that lacks it or changes it, denyAction on deleting a tagged resource. Template files an operator applies, never plan resources. | One ValidatingAdmissionPolicy, writes only, enforced per principal through the cluster's authorizer. |
| Harness | floci, pinned in `live/floci-image` to `ghcr.io/lex00/floci@sha256:5ec92ae8046445d62c0009590dd09f04560886e5a5ef31248cb3fcc09b2961ec`. GHCR image pinned by digest; live/floci-capabilities.json probes per-AWS-service coverage against it. | floci-az, pinned in `live/floci-az-image` to `ghcr.io/lex00/floci-az@sha256:d1efbcc9b669e1a82c8933d29749379b2ce6745fe54b6aad84e981f09982e09d`. the lex00/floci-az fork, pinned by digest; no lane runs on it yet, and live/FLOCI.md lists the surfaces it lacks. | kind, pinned in `live/kind-node-image` to `kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5`. a kind cluster on the pinned node image; no per-service capability probe like floci's exists for it. |
<!-- substrates-gen:end substrates -->
