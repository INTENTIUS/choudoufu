# The producer estate.
#
# An estate is the unit of ownership (live/MARKERS.md): everything carrying
# this `tofu-estate` value is one management domain, matched by one live
# config block. This root owns exactly one thing, a VPC, and the service
# estate beside it owns everything inside that VPC.
#
# Declared as a sidecar rather than a `live` block inside terraform{}, so
# stock tooling - `tofu validate`, tflint, editors - reads main.tf unchanged.
# The whole body is the live block's content, with no wrapper block.
estate = "cross-estate-network"

# No `record_store`: both this root's VPC and the service root's subnet are
# taggable, so their markers live on the resources themselves and the implied
# local record store stays empty. A root with a record-only resource in it
# would declare an `ssm` or `s3` store here, the way
# examples/ci-pipelines does, because a CI runner is fresh every run.
