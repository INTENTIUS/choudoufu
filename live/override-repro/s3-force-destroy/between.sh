#!/bin/bash
acct=$(aws sts get-caller-identity --query Account --output text)
echo x | aws s3 cp - "s3://${SFX}-fd-${acct}/made-outside.txt" --region us-east-2
