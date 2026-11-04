resource "aws_db_subnet_group" "g" {
  name       = "${var.sfx}-dbi"
  subnet_ids = [aws_subnet.a.id, aws_subnet.b.id]
}
resource "aws_db_instance" "d" {
  identifier          = "${var.sfx}-dbi"
  engine              = "postgres"
  instance_class      = "db.t4g.micro"
  allocated_storage   = 20
  username            = "repro"
  password            = "Repro-not-secret-2309"
  db_subnet_group_name = aws_db_subnet_group.g.name
  skip_final_snapshot = false
}
