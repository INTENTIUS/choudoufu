#!/usr/bin/env bash
# minimal greenfield repro for [gauntlet:reference-ec2-vpc/greenfield]
set -uo pipefail
ROOT="/Users/alex/Documents/checkouts/intentius/wt/wrong-marker"
PORT="${FLOCI_PORT:-5420}"
NAME="wrongmarker-floci-$PORT"
IMAGE="$(cat "$ROOT/live/floci-image")"
ENDPOINT="http://127.0.0.1:$PORT"
TOFU="${TOFU_BIN:-$ROOT/.bin/choudoufu}"
WORK="${WORK:-$ROOT/.repro/work}"
KEEP="${KEEP:-0}"
ESTATE="ec2-reference"
REGION="us-east-1"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" AWS_ENDPOINT_URL="$ENDPOINT"

if [ "${FRESH:-1}" = "1" ]; then
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  docker run -d --rm -p "$PORT:4566" --name "$NAME" "$IMAGE" >/dev/null || { echo "docker run failed"; exit 2; }
  for _ in $(seq 1 45); do
    H="$(curl -fs "$ENDPOINT/_localstack/health" 2>/dev/null)" || true
    grep -q '"ec2"' <<< "${H:-}" && break
    sleep 2
  done
  grep -q '"ec2"' <<< "${H:-}" || { echo "floci unhealthy"; exit 2; }
fi
echo "floci up on $PORT"

rm -rf "$WORK"; mkdir -p "$WORK"
cat > "$WORK/main.tf" <<EOF
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
  live {
    estate = "$ESTATE"
    record_store "local" {
      path = ".tofu-records"
    }
  }
}

provider "aws" {
  region                      = "us-east-1"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true
}

resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
  tags = { Name = "ec2-reference-vpc" }
}

resource "aws_subnet" "main" {
  vpc_id                  = aws_vpc.main.id
  cidr_block              = "10.0.1.0/24"
  availability_zone       = "us-east-1a"
  map_public_ip_on_launch = true
  tags = { Name = "ec2-reference-subnet" }
}

resource "aws_internet_gateway" "main" {
  vpc_id = aws_vpc.main.id
  tags = { Name = "ec2-reference-igw" }
}

resource "aws_security_group" "main" {
  name        = "ec2-reference-sg"
  description = "Allow SSH"
  vpc_id      = aws_vpc.main.id
  ingress {
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
  tags = { Name = "ec2-reference-sg" }
}

resource "aws_instance" "main" {
  ami                    = "ami-12345678"
  instance_type          = "t3.micro"
  subnet_id              = aws_subnet.main.id
  vpc_security_group_ids = [aws_security_group.main.id]
  tags = { Name = "ec2-reference-instance" }
}
EOF

cd "$WORK" || exit 2
"$TOFU" init -input=false -no-color > init.log 2>&1 || { tail -20 init.log; exit 3; }
"$TOFU" apply -input=false -auto-approve -no-color > apply.log 2>&1; ARC=$?
echo "apply rc=$ARC: $(grep -E 'Apply complete' apply.log | head -1)"
[ "$ARC" -eq 0 ] || { grep -E '^Error|^│' apply.log | head -30; exit 3; }

echo "--- ENI tags via AWS CLI (no tofu in the loop) ---"
aws --endpoint-url "$ENDPOINT" --region "$REGION" ec2 describe-network-interfaces \
  --query 'NetworkInterfaces[].{id:NetworkInterfaceId,desc:Description,att:Attachment.InstanceId,tags:TagSet}' --output json
echo "--- all tofu-address tags in the account ---"
aws --endpoint-url "$ENDPOINT" --region "$REGION" ec2 describe-tags \
  --filters "Name=key,Values=tofu-address" --output json

echo "--- plan ---"
"$TOFU" plan -input=false -no-color > plan.log 2>&1; PRC=$?
echo "plan rc=$PRC"
tail -40 plan.log
