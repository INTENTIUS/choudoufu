#!/bin/bash
# One small image pushed from outside OpenTofu.
acct=$(aws sts get-caller-identity --query Account --output text)
reg="${acct}.dkr.ecr.us-east-2.amazonaws.com"
aws ecr get-login-password --region us-east-2 | docker login --username AWS --password-stdin "$reg" >/dev/null
docker pull -q public.ecr.aws/docker/library/busybox:latest >/dev/null
docker tag public.ecr.aws/docker/library/busybox:latest "$reg/${SFX}-fd-repo:outside"
docker push -q "$reg/${SFX}-fd-repo:outside"
