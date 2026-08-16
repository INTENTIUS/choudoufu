variable "suffix" {
  type    = string
  default = "prod"
}

resource "aws_docdb_cluster" "first" {
  cluster_identifier  = "docdb-${var.suffix}"
  master_username     = "root"
  skip_final_snapshot = true
}

resource "aws_docdb_cluster" "second" {
  cluster_identifier  = "docdb-prod"
  master_username     = "root"
  skip_final_snapshot = true
}
