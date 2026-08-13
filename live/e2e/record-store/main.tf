# GitHub issue #73's record-backed fixture: the four RECORD_ADMITTED types
# a "record_store" block admits, run through the completely stock provider
# lifecycle against the LOCAL backend. No AWS, no floci - null, time and
# random are all cloud-free providers, so this fixture proves the full
# create / clean re-plan / replace / destroy lifecycle with nothing but a
# local directory. See run.sh in this directory.

terraform {
  live {
    estate = "record-store-e2e"

    record_store "local" {
      path = ".tofu-records"
    }
  }
}

variable "trigger_input" {
  type    = string
  default = "v1"
}

variable "replace_trigger" {
  type    = string
  default = "v1"
}

resource "null_resource" "trigger" {
  triggers = {
    input = var.trigger_input
  }
}

resource "terraform_data" "replacement" {
  input = "static"
  triggers_replace = [var.replace_trigger]
}

resource "time_static" "created" {}

resource "random_pet" "name" {
  length = 2
}

# Deliberately no output blocks: a stateless run's prior state is rebuilt
# from the record store on every plan and never carries computed output
# values across runs (there is no state file to remember them in), so an
# output here would show as a spurious "+" every single plan regardless of
# whether anything about the four resources actually changed. That is a
# pre-existing, out-of-scope property of stateless mode in general, not
# something this fixture is testing - run.sh verifies resource stability by
# reading the persisted record files directly instead.
