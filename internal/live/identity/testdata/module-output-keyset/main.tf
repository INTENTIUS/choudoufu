# #2142: a for_each or count whose KEY SET is a child module's output. The
# first two calls are corpus-k8s-infra-gcp-gcve's own iam.tf (the call to
# terraform-google-modules/iam//modules/projects_iam, bindings abridged to
# three roles), once authoritative as gcve calls it and once additive; see
# projects_iam/ and helper/ for what each carries over verbatim. The rest are
# the two other shapes the same rule covers: count over a module output, and
# a module call ranging over a sibling module's output. Stock OpenTofu plans
# all of it; before #2142 every block refused with "Module output not
# supported in static context".

variable "project_id" {
  type    = string
  default = "k8s-infra-gcp-gcve"
}

# gcve's shape: one project, so the helper keys every binding by "default".
module "iam" {
  source   = "./projects_iam"
  projects = [var.project_id]
  mode     = "authoritative"
  bindings = {
    "roles/admin" = [
      "group:sig-k8s-infra-leads@kubernetes.io",
      "group:k8s-infra-gcp-gcve-admins@kubernetes.io",
    ]
    "roles/viewer" = [
      "serviceAccount:datadog@k8s-infra-seed.iam.gserviceaccount.com",
    ]
  }
}

# Additive, over two projects, so the keys carry the project and the member.
module "iam_additive" {
  source   = "./projects_iam"
  projects = ["alpha", "beta"]
  mode     = "additive"
  bindings = {
    "roles/viewer" = ["user:a@example.com"]
  }
}

module "helper" {
  source   = "./helper"
  entities = ["ops", "dev"]
  mode     = "authoritative"
  bindings = {
    "read"  = ["alice"]
    "write" = ["bob"]
  }
}

module "counter" {
  source = "./counter"
  items  = ["ops", "dev"]
}

# count over a module output.
resource "aws_iam_role_policy" "counted" {
  count = module.counter.n
  role  = ["ops", "dev"][count.index]
  name  = "counted"
}

# A module call whose for_each is a sibling module's output.
module "consumer" {
  source   = "./consumer"
  for_each = module.helper.set_authoritative
  role     = module.helper.bindings_authoritative[each.key].name
  name     = "consumer-${module.helper.bindings_authoritative[each.key].role}"
}
