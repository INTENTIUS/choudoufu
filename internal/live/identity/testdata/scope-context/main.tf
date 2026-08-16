// Two projects, named by literals, so a reference to either resolves to a
// literal through the sibling's own resolved identity.
resource "google_project" "one" {
  project_id = "proj-one"
}

resource "google_project" "two" {
  project_id = "proj-two"
}

// GitHub issue #200's shape: the same API enabled in two projects, each
// named only by a reference to a sibling managed resource. Two objects.
resource "google_project_service" "sibling_one" {
  project = google_project.one.project_id
  service = "iam.googleapis.com"
}

resource "google_project_service" "sibling_two" {
  project = google_project.two.project_id
  service = "iam.googleapis.com"
}

// The same, with the projects written out. Two objects.
resource "google_project_service" "literal_one" {
  project = "proj-three"
  service = "run.googleapis.com"
}

resource "google_project_service" "literal_two" {
  project = "proj-four"
  service = "run.googleapis.com"
}

// One project, one service, two blocks. One object with two owners, and
// the refusal has to fire.
resource "google_project_service" "dup_a" {
  project = "proj-five"
  service = "storage.googleapis.com"
}

resource "google_project_service" "dup_b" {
  project = "proj-five"
  service = "storage.googleapis.com"
}

// One side's project cannot be read at all: "number" is Computed and is no
// identity attribute of a google_project. An unresolved scope is a wildcard
// and must not rule the pair out (#217's direction), so this pair still
// has to be refused.
resource "google_project_service" "known_project" {
  project = "proj-six"
  service = "logging.googleapis.com"
}

resource "google_project_service" "unknown_project" {
  project = google_project.one.number
  service = "logging.googleapis.com"
}
