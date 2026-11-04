resource "aws_db_subnet_group" "g" {
  name       = "${var.sfx}-sg"
  subnet_ids = [aws_subnet.a.id, aws_subnet.b.id]
}
resource "aws_rds_cluster" "c" {
  cluster_identifier   = "${var.sfx}-c"
  engine               = "aurora-postgresql"
  master_username      = "repro"
  master_password      = "Repro-not-secret-2309"
  db_subnet_group_name = aws_db_subnet_group.g.name
  skip_final_snapshot  = false
}
