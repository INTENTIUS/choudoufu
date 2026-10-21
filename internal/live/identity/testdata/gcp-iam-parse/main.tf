# IAM types whose parent hashicorp/google 8.6.0 parses in a shape the
# generator read only from #2145 on. Each parent is the template the
# updater's GetResourceId builds, which the framework's create sets as the
# id before the binding:
#
#   - a generated updater whose qualifier groups are camelCase
#     (`(?P<serviceId>`), respelled as the argument the producer copies in;
#   - a hand-written IdParseFunc over tpgresource.ParseImportId;
#   - one over tpgresource.ParseProjectFieldValue or
#     ParseRegionalFieldValue, whose RelativeLink is the id;
#   - one over a parse helper whose id struct's method is the id.

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

# Generated, camelCase groups: the short database name.
resource "google_dataproc_metastore_database_iam_member" "short" {
  service_id = "meta"
  database   = "sales"
  role       = "roles/metastore.user"
  member     = "group:a@example.com"
}

# The same parent as a long form naming its own project and location:
# the updater's captured groups win over the provider's defaults.
resource "google_dataproc_metastore_database_iam_member" "long" {
  service_id = "meta"
  database   = "projects/other/locations/us-east1/services/meta/databases/sales"
  role       = "roles/metastore.user"
  member     = "group:a@example.com"
}

# Generated, camelCase app_id group, with a literal inside a segment.
resource "google_iap_app_engine_service_iam_member" "svc" {
  app_id  = "estate-prod"
  service = "default"
  role    = "roles/iap.httpsResourceAccessor"
  member  = "group:a@example.com"
}

# Hand-written, ParseImportId: {project}/{instance}/{database}.
resource "google_spanner_database_iam_member" "db" {
  instance = "core"
  database = "ledger"
  role     = "roles/spanner.databaseReader"
  member   = "group:a@example.com"
}

# Hand-written, a parse helper whose SpannerInstanceId.TerraformId() is
# the id.
resource "google_spanner_instance_iam_member" "inst" {
  project  = "estate-data"
  instance = "core"
  role     = "roles/spanner.viewer"
  member   = "group:a@example.com"
}

# Hand-written, ParseProjectFieldValue("datasets", ...), from a parse
# function another file of the package defines.
resource "google_bigquery_dataset_iam_member" "ds" {
  dataset_id = "analytics"
  role       = "roles/bigquery.dataViewer"
  member     = "group:a@example.com"
}

# Hand-written, ParseRegionalFieldValue("clusters", ...).
resource "google_dataproc_cluster_iam_member" "cl" {
  cluster = "batch"
  role    = "roles/dataproc.viewer"
  member  = "group:a@example.com"
}

# resourcemanager.CanonicalFolderId adds "folders/" to a folder that lacks
# it, so the short and the long spelling name one parent, "folders/<id>",
# while the folder argument keeps the value as written.
resource "google_folder_iam_member" "short" {
  folder = "123"
  role   = "roles/viewer"
  member = "group:a@example.com"
}

resource "google_folder_iam_member" "long" {
  folder = "folders/456"
  role   = "roles/viewer"
  member = "group:a@example.com"
}

# cloudbilling.canonicalBillingAccountId returns its argument unchanged.
resource "google_billing_account_iam_member" "ba" {
  billing_account_id = "012345-6789AB-CDEF01"
  role               = "roles/billing.viewer"
  member             = "group:a@example.com"
}
