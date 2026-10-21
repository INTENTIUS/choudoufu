# Types whose id embeds a name the API returns, where the create sends the
# name's last segment itself as the request's resource id field
# (tools/gcp-row-gen createname.go, hashicorp/google 8.6.0, #2145 item 3).
# One per shape, and one that sends no id.

terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "8.6.0"
    }
  }
}

provider "google" {
  project = "estate-prod"
  region  = "europe-west1"
}

# The encoder sends templateId in the body; the id is the parent and the
# name's last segment.
resource "google_data_loss_prevention_inspect_template" "pii" {
  parent      = "projects/estate-prod/locations/global"
  template_id = "pii"
}

# The create URL sends agentId; the id is configuration's path and the
# name's last segment.
resource "google_ces_agent" "triage" {
  location     = "us"
  app          = "support"
  agent_id     = "triage"
  display_name = "Triage"
}

# The create URL sends generatorId, and the API writes the whole name back.
# Google documents dialogflow's names with the project ID.
resource "google_dialogflow_generator" "summary" {
  location     = "global"
  generator_id = "summary"
}

# The whole name with no project in it.
resource "google_scc_v2_organization_notification_config" "findings" {
  organization = "123456789"
  location     = "global"
  config_id    = "findings"
  pubsub_topic = "projects/estate-prod/topics/findings"
}

# No templateId: the API names the template, so this run cannot.
resource "google_data_loss_prevention_inspect_template" "unnamed" {
  parent = "projects/estate-prod/locations/global"
}
