resource "aws_docdb_subnet_group" "main" {
  name       = "prod-docdb-subnets"
  subnet_ids = ["subnet-aaaa", "subnet-bbbb"]
}

resource "aws_docdb_cluster_parameter_group" "main" {
  name   = "prod-docdb-pg"
  family = "docdb5.0"
}

resource "aws_docdb_cluster" "main" {
  cluster_identifier              = "prod-docdb"
  db_subnet_group_name            = aws_docdb_subnet_group.main.name
  db_cluster_parameter_group_name = aws_docdb_cluster_parameter_group.main.name
  master_username                 = "root"
  skip_final_snapshot             = true
}

resource "aws_docdb_cluster_instance" "nodes" {
  count = 2

  identifier         = "prod-docdb-${count.index}"
  cluster_identifier = aws_docdb_cluster.main.cluster_identifier
  instance_class     = "db.r5.large"
}
