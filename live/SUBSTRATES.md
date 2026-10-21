# Substrates

What each provider family uses for identity, markers, writes, sweep and
fence, generated from the substrate implementations in
`internal/live/substrate`. The fence and harness notes are kept by hand in
`tools/substrates-gen`, because the fence is never code here (#1118).

Rendered from `live/substrates.json` by `go run ./tools/substrates-gen -render`
(or `go run ./tools/gauntlet render`). Edit the text around the generated
markers, never inside them.

<!-- substrates-gen:begin substrates -->
| | AWS | GOOGLE | KUBERNETES |
|---|---|---|---|
| Identity | the block address is a key of the marker map itself, beside tofu-estate | the block address is a key of the marker map itself, beside tofu-estate | the block address is carried outside the marker map, in its own annotation |
| Marker surfaces | `tags`: tags attribute (tag) | `google-labels`: labels map (label) | `labels`: metadata.labels map (label)<br>`manifest`: manifest.metadata.labels map (label) |
| Writes (create / adopt / post-create) | `tags`: in-create / tags-only-plan / tagging-api | `google-labels`: in-create / google-labels-plan / never-needed | `labels`: in-create / labels-only-plan / never-needed<br>`manifest`: in-create / api-patch / never-needed |
| Sweep | tagging-index: runs through the configured provider itself; no separate client is built from the provider block | asset-search: a Cloud Asset Inventory client built from the provider block's own credential arguments (Substrate.NewSweeper), one search per project the estate's resources name; the index lags creates and deletes | label-list: a cluster client built from the provider block's own connection arguments (Substrate.NewSweeper) |
| Controller-held | the resource's own tags carry one of the fixed controller tag keys (markers.ControllerTagKeys: ACK, Crossplane) | a Compute Engine instance or disk carrying a label key GKE, Dataproc or Batch reserves for the VMs and disks they create, or a Cloud Run service whose goog-managed-by label names a product; never a goog- prefix, which the provider writes on everything it creates | the object's Helm release annotation (kubesweep.HelmReleaseNameAnnotation), read off the object itself rather than off the label marker map |
| Untaggable types | 852 (declaration-carried: 379; excluded by design: 2; record-carried: 471) | 1167 (declaration-carried: 917; record-carried: 250) | 0 |
| Fence | IAM conditions on aws:ResourceTag, reads and writes, per principal (live/MARKERS.md). | Project-scoped, restated: an IAM condition cannot match a label, so one estate per project, granted by an ordinary project IAM binding. Resource Manager tag bindings, which a condition can match, are ruled later (#1121). | One ValidatingAdmissionPolicy, writes only, enforced per principal through the cluster's authorizer. |
| Harness | floci, pinned in `live/floci-image` to `ghcr.io/lex00/floci@sha256:5ec92ae8046445d62c0009590dd09f04560886e5a5ef31248cb3fcc09b2961ec`. GHCR image pinned by digest; live/floci-capabilities.json probes per-AWS-service coverage against it. | floci-gcp, pinned in `live/floci-gcp-image` to `ghcr.io/lex00/floci-gcp@sha256:ed1a178190dad7f3a43f40dffbc1e8a2d70d28453b0d4bc3c27a53238ad9ac60`. the lex00/floci-gcp fork's GHCR image pinned by digest; the fork's issues for Resource Manager tags, Cloud Asset search, matchTag and IAM enforce mode (lex00/floci-gcp#4 to #9) are closed, and no gcp-lane run on it has passed yet. | kind, pinned in `live/kind-node-image` to `kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5`. a kind cluster on the pinned node image; no per-service capability probe like floci's exists for it. |
<!-- substrates-gen:end substrates -->
