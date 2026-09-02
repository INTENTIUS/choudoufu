# GitHub issue #349's sub-problem 2, in miniature: two root outputs, each
# reaching a data source that nothing else in the configuration reads.
#
# data.test_zone.current (in module.m) belongs to the "test" provider, which
# this configuration manages a real resource through, so the root-output read
# class reads it.
#
# data.external.archive belongs to a provider that manages nothing here and
# whose read runs a program named by its own arguments. It is the shape the
# class must never reach, and the boundary that stops it is derived - not a
# type name - see dataread.LiveProviders.

resource "test_thing" "a" {
  name = "thing-a"
}

data "external" "archive" {
  program = ["python3", "package.py", "prepare"]
}

module "m" {
  source = "./m"
  suffix = "simple"
}

output "arn_static" {
  value = module.m.arn_static
}

output "local_filename" {
  value = data.external.archive.result
}
