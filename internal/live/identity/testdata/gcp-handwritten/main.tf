# Hand-written hashicorp/google 8.6.0 types whose create builds its id in
# code rather than from one ReplaceVars template (#2145 item 1). One
# resource per way the code builds it, each rendered as the provider's
# create renders it.

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
  zone    = "europe-west1-b"
}

# A variable bound from tpgresource.GetProject: the provider's project.
resource "google_app_engine_application" "app" {
  location_id = "europe-west"
}

# d.Get("project") as written, with no provider default.
resource "google_compute_shared_vpc_host_project" "host" {
  project = "host-prj"
}

# fmt.Sprintf over two arguments.
resource "google_compute_shared_vpc_service_project" "svc" {
  host_project    = "host-prj"
  service_project = "svc-prj"
}

# A package helper (containerClusterFullName) over GetLocation, which keeps
# the location's last segment...
resource "google_container_cluster" "regional" {
  name     = "gke"
  location = "regions/us-east1"
}

# ...and falls back to the provider's region.
resource "google_container_cluster" "defaulted" {
  name = "gke-default"
}

# A struct method (CloudFunctionId) over GetProject and GetRegion.
resource "google_cloudfunctions_function" "fn" {
  name    = "fn"
  runtime = "python312"
}

# A struct built by a package function, read through its canonicalId.
resource "google_logging_project_sink" "audit" {
  name        = "audit"
  destination = "storage.googleapis.com/audit-logs"
}

# The same, with resourcemanager.ParseFolderId trimming "folders/".
resource "google_logging_folder_sink" "long" {
  folder      = "folders/123"
  name        = "f"
  destination = "storage.googleapis.com/audit-logs"
}

resource "google_logging_folder_sink" "short" {
  folder      = "456"
  name        = "g"
  destination = "storage.googleapis.com/audit-logs"
}

# The parent comes from an updater the registration passes in.
resource "google_logging_project_exclusion" "noisy" {
  name   = "noisy"
  filter = "severity < WARNING"
}

# A DCL resource's ID(), over urlNormalized's SelfLinkToName.
resource "google_dataproc_workflow_template" "wt" {
  name     = "wt"
  location = "europe-west1"
}

# ReplaceVarsForId in the file that defines the resource, not the one that
# registers it.
resource "google_gke_hub_feature_membership" "fm" {
  location   = "global"
  feature    = "configmanagement"
  membership = "m1"
}

# fmt.Sprintf over d.Get with no type assertion.
resource "google_project_organization_policy" "serial" {
  project    = "estate-prod"
  constraint = "compute.disableSerialPortAccess"
}

# A ruled importer: splits the id once on "/".
resource "google_organization_policy" "org" {
  org_id     = "1234"
  constraint = "constraints/compute.skipDefaultNetworkCreation"
}
