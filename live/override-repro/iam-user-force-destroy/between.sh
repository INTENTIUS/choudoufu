#!/bin/bash
aws iam create-access-key --user-name "${SFX}-fd-user" --query AccessKey.AccessKeyId --output text
