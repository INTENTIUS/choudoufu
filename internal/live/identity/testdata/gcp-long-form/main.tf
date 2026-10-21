# Arguments hashicorp/google 8.6.0 rewrites before they become part of the
# id (#2144). An IAM type's parent is parsed with GetImportIdQualifiers and
# rebuilt into the canonical name; a type whose create uses
# ReplaceVarsForId keeps only the last path segment; pubsub's subscription
# IAM places a short name into projects/{p}/subscriptions/{s}. Each is
# composed the way the provider composes it. A value only an apply can
# know, or one the rewrite cannot use, refuses the instance by name.

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

resource "google_secret_manager_secret" "s" {
  secret_id = "db-pass"
  replication {
    auto {}
  }
}

# The secret's id is its long form: parsed back into the canonical parent.
resource "google_secret_manager_secret_iam_member" "byid" {
  secret_id = google_secret_manager_secret.s.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "group:a@example.com"
}

# Its secret_id is the short form: the same parent as byid.
resource "google_secret_manager_secret_iam_member" "byname" {
  secret_id = google_secret_manager_secret.s.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "group:b@example.com"
}

# A literal long form naming another project: the provider uses that
# project, and so does the id.
resource "google_secret_manager_secret_iam_member" "literal_long" {
  secret_id = "projects/other/secrets/db-pass"
  role      = "roles/secretmanager.secretAccessor"
  member    = "group:a@example.com"
}

resource "google_compute_network_firewall_policy" "p" {
  name = "pol"
}

# ReplaceVarsForId keeps "pol", the last segment of the policy's id.
resource "google_compute_network_firewall_policy_rule" "r" {
  firewall_policy = google_compute_network_firewall_policy.p.id
  priority        = 1000
  action          = "allow"
  direction       = "INGRESS"
  match {
    src_ip_ranges = ["10.0.0.0/8"]
    layer4_configs {
      ip_protocol = "tcp"
    }
  }
}

# A type the provider substitutes as written, with a "/" in its argument:
# unchanged.
resource "google_securityposture_posture" "org" {
  parent     = "organizations/123"
  location   = "global"
  posture_id = "baseline"
  state      = "ACTIVE"
  policy_sets {
    policy_set_id = "ps"
    policies {
      policy_id = "p"
      constraint {
        org_policy_constraint {
          canned_constraint_id = "storage.uniformBucketLevelAccess"
          policy_rules {
            enforce = true
          }
        }
      }
    }
  }
}

# The provider mints the key's name, so its id is known only after apply,
# and the rewrite reads the value's text: refused.
resource "google_recaptcha_enterprise_key" "site" {
  display_name = "site"
  web_settings {
    integration_type  = "SCORE"
    allow_all_domains = true
  }
}

resource "google_secret_manager_secret_iam_member" "minted" {
  secret_id = google_recaptcha_enterprise_key.site.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "group:a@example.com"
}

# A trailing "/" leaves ReplaceVarsForId an empty last segment, an id that
# names nothing: refused.
resource "google_compute_network_firewall_policy_rule" "trailing" {
  firewall_policy = "pol/"
  priority        = 2000
  action          = "allow"
  direction       = "INGRESS"
  match {
    src_ip_ranges = ["10.0.0.0/8"]
    layer4_configs {
      ip_protocol = "tcp"
    }
  }
}

# A regional rule: "projects/" is trimmed off the project, and the region's
# self link and the policy's id are each cut to their last segment.
resource "google_compute_region_network_firewall_policy" "rp" {
  name   = "rpol"
  region = "europe-west1"
}

resource "google_compute_region_network_firewall_policy_rule" "rr" {
  project         = "projects/estate-prod"
  region          = "https://www.googleapis.com/compute/v1/projects/estate-prod/regions/europe-west1"
  firewall_policy = google_compute_region_network_firewall_policy.rp.id
  priority        = 1000
  action          = "allow"
  direction       = "INGRESS"
  match {
    src_ip_ranges = ["10.0.0.0/8"]
    layer4_configs {
      ip_protocol = "tcp"
    }
  }
}

# pubsub's subscription IAM keeps a full name whole and places a short one
# into projects/{project}/subscriptions/{name} (GetComputedSubscriptionName).
resource "google_pubsub_subscription" "orders" {
  name  = "orders"
  topic = "projects/estate-prod/topics/orders"
}

resource "google_pubsub_subscription_iam_member" "short" {
  subscription = "orders"
  role         = "roles/pubsub.subscriber"
  member       = "group:a@example.com"
}

resource "google_pubsub_subscription_iam_member" "other_project" {
  project      = "other"
  subscription = "orders"
  role         = "roles/pubsub.subscriber"
  member       = "group:a@example.com"
}

resource "google_pubsub_subscription_iam_member" "byid" {
  subscription = google_pubsub_subscription.orders.id
  role         = "roles/pubsub.subscriber"
  member       = "group:b@example.com"
}

# A long form naming a project other than the one the resource sets: the
# updater writes the long form's project back over the argument, so stock
# plans a replacement on every run. Refused.
resource "google_secret_manager_secret_iam_member" "project_conflict" {
  project   = "estate-dr"
  secret_id = "projects/other/secrets/db-pass"
  role      = "roles/secretmanager.secretAccessor"
  member    = "group:a@example.com"
}

# The same for a location: refused.
resource "google_cloud_run_v2_service_iam_member" "location_conflict" {
  location = "us-central1"
  name     = "projects/estate-prod/locations/europe-west1/services/api"
  role     = "roles/run.invoker"
  member   = "group:a@example.com"
}

# The long form agrees with the location the resource sets: composed.
resource "google_cloud_run_v2_service_iam_member" "location_agrees" {
  location = "europe-west1"
  name     = "projects/estate-prod/locations/europe-west1/services/api"
  role     = "roles/run.invoker"
  member   = "group:a@example.com"
}

# The middle qualifier, {location}/{name}, over the provider's project.
resource "google_cloud_run_v2_service_iam_member" "location_short" {
  name   = "us-east1/api"
  role   = "roles/run.invoker"
  member = "group:a@example.com"
}
