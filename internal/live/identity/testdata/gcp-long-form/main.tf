# Arguments hashicorp/google 8.6.0 rewrites before they become part of the
# id (#2144). An IAM type's parent is parsed with GetImportIdQualifiers and
# rebuilt into the canonical name; a type whose create uses
# ReplaceVarsForId keeps only the last path segment. Until those rewrites
# are composed, a long-form value refuses the instance by name rather than
# render the whole value into a template that already spells its path.

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

# The secret's id is its long form: refused.
resource "google_secret_manager_secret_iam_member" "byid" {
  secret_id = google_secret_manager_secret.s.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "group:a@example.com"
}

# Its secret_id is the short form: resolves as before.
resource "google_secret_manager_secret_iam_member" "byname" {
  secret_id = google_secret_manager_secret.s.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "group:a@example.com"
}

# A literal long form naming another project: refused.
resource "google_secret_manager_secret_iam_member" "literal_long" {
  secret_id = "projects/other/secrets/db-pass"
  role      = "roles/secretmanager.secretAccessor"
  member    = "group:a@example.com"
}

resource "google_compute_network_firewall_policy" "p" {
  name = "pol"
}

# ReplaceVarsForId would keep "pol" out of the policy's id: refused.
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
# and nothing before then says which form it takes: refused.
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
