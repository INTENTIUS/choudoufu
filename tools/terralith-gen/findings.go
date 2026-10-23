// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "fmt"

// The -findings layer (issue #2199). Off by default, and when off nothing in
// this file is called, so the default output stays byte-identical and every
// count that measures it (gen_test.go, live-cert's terralith-scale.sh, the
// discovery benches, gauntlet scale records) stays where it is.
//
// On, it adds one fixed layer that does not scale with -scale: the things a
// person asks about when they review a stranger's estate. behold's "ask your
// agent about a Terraform estate" demo (INTENTIUS/behold#912) asks three of
// them, and each has a definite answer in this layer:
//
//   - an instance reachable over SSH from the internet: aws_instance.bastion
//     is launched from aws_launch_template.bastion, whose security group has
//     an ingress rule for tcp/22 from 0.0.0.0/0, into aws_subnet.public,
//     whose route table sends 0.0.0.0/0 to aws_internet_gateway.main;
//   - a database a one-line change replaces: aws_db_instance.orders carries
//     storage_encrypted = false, and turning it on forces replacement. On
//     provider 6.59.0, changing identifier or engine_version plans an
//     in-place update instead, measured against floci;
//   - a role that reaches a data store: aws_iam_role.app_data's attached
//     policy names aws_dynamodb_table.sessions' ARN, and the bastion's
//     instance profile carries that role.
//
// Two lines exist only because of floci, the emulator these estates apply
// against, and both are measured (issue #2199):
//
//   - floci's DescribeInstances does not return the aws:ec2launchtemplate:id
//     tag the provider reads an instance's launch_template back from, so
//     without ignore_changes every plan replaces the instance;
//   - floci returns a launch template's instance profile by ARN only, so the
//     template names the profile by arn rather than name, or every plan
//     updates it.
//
// No aws_s3_bucket: on provider 6.59.0 a bucket's create reads its tags back
// through S3 Control at <account>.<endpoint host>, which does not resolve for
// a 127.0.0.1 endpoint, and the apply fails. The data store is a DynamoDB
// table instead.

// findingsResources is how many resources the layer adds, by file.
const (
	findingsBastionResources = 8 // igw, subnet, route table, association, sg, ingress rule, launch template, instance
	findingsDataResources    = 9 // 2 subnets, subnet group, db instance, table, role, policy, attachment, instance profile

	// findingsIAMRoles is the layer's aws_iam_role instances, for -iam-roles.
	findingsIAMRoles = 1
)

func bastionTF(prefix string) string {
	return header("bastion.tf", "the -findings layer's internet-facing instance (issue #2199): an IGW-routed subnet and an instance launched from a template whose security group opens SSH to 0.0.0.0/0") +
		fmt.Sprintf(`resource "aws_internet_gateway" "main" {
  vpc_id = aws_vpc.main.id

  tags = {
    Name = "%[1]s-igw"
  }
}

resource "aws_subnet" "public" {
  vpc_id                  = aws_vpc.main.id
  cidr_block              = "10.42.2.0/24"
  availability_zone       = "us-east-1a"
  map_public_ip_on_launch = true

  tags = {
    Name = "%[1]s-public"
  }
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.main.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.main.id
  }

  tags = {
    Name = "%[1]s-public"
  }
}

resource "aws_route_table_association" "public" {
  subnet_id      = aws_subnet.public.id
  route_table_id = aws_route_table.public.id
}

resource "aws_security_group" "bastion" {
  name        = "%[1]s-bastion-sg"
  description = "terralith-gen: the bastion's security group"
  vpc_id      = aws_vpc.main.id
}

resource "aws_vpc_security_group_ingress_rule" "bastion_ssh" {
  security_group_id = aws_security_group.bastion.id
  description       = "ssh from anywhere"
  ip_protocol       = "tcp"
  from_port         = 22
  to_port           = 22
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_launch_template" "bastion" {
  name                   = "%[1]s-bastion"
  image_id               = "ami-12345678"
  instance_type          = "t3.micro"
  vpc_security_group_ids = [aws_security_group.bastion.id]

  # By arn, not name: floci returns the profile by ARN only (issue #2199).
  iam_instance_profile {
    arn = aws_iam_instance_profile.app_data.arn
  }
}

resource "aws_instance" "bastion" {
  subnet_id = aws_subnet.public.id

  launch_template {
    id      = aws_launch_template.bastion.id
    version = "$Latest"
  }

  tags = {
    Name = "%[1]s-bastion"
  }

  # floci does not return the aws:ec2launchtemplate:id tag the provider
  # reads this block back from, so without this every plan replaces the
  # instance (issue #2199).
  lifecycle {
    ignore_changes = [launch_template]
  }
}
`, prefix)
}

func dataTF(prefix string) string {
	return header("data.tf", "the -findings layer's data stores (issue #2199): an unencrypted postgres instance and a DynamoDB table, and the role whose attached policy reaches the table") +
		fmt.Sprintf(`resource "aws_subnet" "db_a" {
  vpc_id            = aws_vpc.main.id
  cidr_block        = "10.42.10.0/24"
  availability_zone = "us-east-1a"

  tags = {
    Name = "%[1]s-db-a"
  }
}

resource "aws_subnet" "db_b" {
  vpc_id            = aws_vpc.main.id
  cidr_block        = "10.42.11.0/24"
  availability_zone = "us-east-1b"

  tags = {
    Name = "%[1]s-db-b"
  }
}

resource "aws_db_subnet_group" "orders" {
  name       = "%[1]s-orders"
  subnet_ids = [aws_subnet.db_a.id, aws_subnet.db_b.id]
}

# storage_encrypted = true forces replacement; identifier and
# engine_version update in place on provider 6.59.0 (issue #2199).
resource "aws_db_instance" "orders" {
  identifier           = "%[1]s-orders"
  engine               = "postgres"
  engine_version       = "16.3"
  instance_class       = "db.t3.micro"
  allocated_storage    = 20
  db_name              = "orders"
  username             = "orders_admin"
  password             = "emulator-only-not-a-secret"
  db_subnet_group_name = aws_db_subnet_group.orders.name
  storage_encrypted    = false
  skip_final_snapshot  = true
}

resource "aws_dynamodb_table" "sessions" {
  name         = "%[1]s-sessions"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "id"

  attribute {
    name = "id"
    type = "S"
  }
}

resource "aws_iam_role" "app_data" {
  name               = "%[1]s-app-data-role"
  assume_role_policy = %[2]s
}

resource "aws_iam_policy" "app_data" {
  name = "%[1]s-app-data"
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:Query", "dynamodb:DeleteItem"]
      Resource = aws_dynamodb_table.sessions.arn
    }]
  })
}

resource "aws_iam_role_policy_attachment" "app_data" {
  role       = aws_iam_role.app_data.name
  policy_arn = aws_iam_policy.app_data.arn
}

resource "aws_iam_instance_profile" "app_data" {
  name = "%[1]s-app-data"
  role = aws_iam_role.app_data.name
}
`, prefix, assumeRolePolicyHCL("ec2.amazonaws.com"))
}
