#!/bin/bash
z=$(aws route53 list-hosted-zones-by-name --dns-name "${SFX}-fd.example.internal" --query 'HostedZones[0].Id' --output text)
aws route53 change-resource-record-sets --hosted-zone-id "$z" --change-batch "{\"Changes\":[{\"Action\":\"CREATE\",\"ResourceRecordSet\":{\"Name\":\"outside.${SFX}-fd.example.internal\",\"Type\":\"TXT\",\"TTL\":60,\"ResourceRecords\":[{\"Value\":\"\\\"x\\\"\"}]}}]}" --query ChangeInfo.Status --output text
