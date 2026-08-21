# GitHub issue #353's provisioner fixture: a create-time provisioner that
# fails on a MARKER-TRACKED CLOUD RESOURCE, and the one bit that has to
# survive it.
#
# What makes this a measurement rather than an exercise. Every resource here
# is an ordinary taggable AWS type whose prior state is read back out of the
# cloud on every run - not a record-backed logical type, which has carried
# its tainted bit inside its own record since issue #216. That distinction
# IS the feature: internal/live/stamp writes tofu-estate/tofu-address before
# the create request goes out, so the instant the bucket exists it is fully
# marked - before any provisioner has run. Without the tofu-provisioned
# namespace, a failed provisioner leaves a live, fully-marked, unprovisioned
# object that the next plan reads back as healthy. run.sh asserts the object
# is live AND marked AND proposed for replacement, so a run that lost the
# bit fails on the third even though the first two are perfect.
#
# The provisioner writes a line to an append-only log outside the estate, so
# "the provisioner re-ran" is measured by counting real executions rather
# than by trusting a plan verdict. A replace that proposed itself and then
# did not actually run the command would pass every plan-level assertion and
# fail here.
#
# Four shapes, one fixture:
#
#   aws_s3_bucket.app       a create-time provisioner whose success is
#                           controlled by var.app_command. The feature.
#   aws_s3_bucket.tolerant  a create-time provisioner that ALWAYS fails with
#                           on_failure = continue. Stock does not taint for
#                           one of these, so neither may this: the negative
#                           control against a mechanism that taints on any
#                           provisioner error at all.
#   aws_s3_bucket.control   no provisioner. Nothing may ever be written for
#                           it, and the store must not even be consulted.
#   aws_s3_bucket.shrinker  a `when = destroy` provisioner behind a count,
#                           for the destroy-time half - which needs no
#                           storage at all, because a failed destroy
#                           provisioner leaves the object alive WITH ITS
#                           MARKER, and the marker's continued existence
#                           already is the "still needs destroying" signal.
#
# The depends_on chain is not decoration. It forces a single deterministic
# apply order (control, then shrinker, then tolerant, then app) so that the
# run whose LAST action is a provisioner failure has already created
# everything else. Without it the graph creates all four concurrently and a
# failure part-way through leaves an indeterminate set of buckets, which
# would make every count below flaky.

terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.59.0"
    }
  }

  live {
    estate = "provisioner-taint-e2e"

    # The declaration that admits the provisioners below. Without it
    # internal/live/lint's RuleProvisioner refuses every one of them, and
    # correctly: a failed create-time provisioner would have nowhere to be
    # remembered.
    record_store "local" {
      path = ".tofu-records"
    }
  }
}

provider "aws" {
  region                      = "us-east-1"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true
}

variable "app_command" {
  type        = string
  description = "The shell command aws_s3_bucket.app's create-time provisioner ends with. `exit 3` makes it fail."
  default     = "exit 3"
}

variable "app_marker" {
  type        = string
  description = "The word app's provisioner writes to the run log. Changing it changes the provisioner's command text and nothing else, which is how the plan's indifference to that change is measured."
  default     = "run-1"
}

variable "app_count" {
  type        = number
  description = "How many aws_s3_bucket.app instances exist. Dropping it to 0 destroys the instance through the ordinary declared path, and raising it again re-creates it - which is how a SECOND create-time provisioner failure is arranged, and how the taint record's cleanup on destroy is measured, without editing the store by hand."
  default     = 1
}

variable "shrink_count" {
  type        = number
  description = "How many aws_s3_bucket.shrinker instances exist. Dropping it from 2 to 1 is what runs the destroy-time provisioner."
  default     = 2
}

# There is deliberately no variable controlling the destroy-time
# provisioner, and no var reference inside it. Stock refuses one outright
# ("Destroy-time provisioners and their connection configurations may only
# reference attributes of the related resource, via 'self', 'count.index',
# or 'each.key'"), which is a parity fact worth stating rather than working
# around: whether that provisioner fails is decided by whether run.sh has
# created the sentinel file it tests for.

resource "aws_s3_bucket" "control" {
  bucket = "pt-e2e-control"
}

resource "aws_s3_bucket" "shrinker" {
  count      = var.shrink_count
  bucket     = "pt-e2e-shrinker-${count.index}"
  depends_on = [aws_s3_bucket.control]

  provisioner "local-exec" {
    when    = destroy
    command = "echo destroy-${count.index} >> ./provisioner-runs.txt && test ! -f ./fail-destroy"
  }
}

resource "aws_s3_bucket" "tolerant" {
  bucket     = "pt-e2e-tolerant"
  depends_on = [aws_s3_bucket.shrinker]

  # Always fails, always tolerated. Stock records nothing for this, because
  # on_failure = continue means the failure was not an error at all - so
  # nothing may be recorded here either, and no plan may ever propose
  # replacing this bucket.
  provisioner "local-exec" {
    on_failure = continue
    command    = "echo tolerant >> ./provisioner-runs.txt && exit 9"
  }
}

resource "aws_s3_bucket" "app" {
  count      = var.app_count
  bucket     = "pt-e2e-app"
  depends_on = [aws_s3_bucket.tolerant]

  provisioner "local-exec" {
    command = "echo ${var.app_marker} >> ./provisioner-runs.txt && ${var.app_command}"
  }
}
