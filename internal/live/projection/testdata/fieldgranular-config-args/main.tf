# Fixture for fieldgranular_configargs_test.go (#1885, reference-k8s-
# shared-objects' and corpus-govuk-cluster-services' test_plan): two
# field-granular blocks whose provider reads depend on arguments only
# configuration and state ever hold - field_manager, force, container, and
# for an importable type the metadata block and the env names.

resource "stub_labels" "ns" {
  api_version = "v1"
  kind        = "Namespace"
  force       = true
  metadata {
    name = "shared"
  }
  labels = {
    "app.shared/team" = "app"
  }
}

resource "stub_env" "web" {
  api_version = "apps/v1"
  kind        = "Deployment"
  container   = "web"
  metadata {
    name      = "web"
    namespace = "shared"
  }
  env {
    name  = "APP_MODE"
    value = "shared"
  }
}
