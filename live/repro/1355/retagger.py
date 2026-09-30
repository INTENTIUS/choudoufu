#!/usr/bin/env python3
"""retagger.py <endpoint> <bucket> <seconds> <counts-file>

Out-of-band traffic against a record bucket while the loops run: lists the
record namespace and re-tags every record object (put-object-tagging, which
replaces the tag set), and writes/deletes noise objects under an unrelated
prefix, as fast as it can for <seconds>. This is #1355's out-of-band re-tag
made continuous, so it lands inside a destroy's LIST-to-GET window.
"""
import sys, time, random, boto3
ep, bucket, secs, out = sys.argv[1], sys.argv[2], float(sys.argv[3]), sys.argv[4]
s3 = boto3.client("s3", endpoint_url=ep, aws_access_key_id="test", aws_secret_access_key="test", region_name="us-east-1")
end = time.time() + secs
n = {"tag": 0, "tagerr": 0, "noise": 0}
while time.time() < end:
    try:
        r = s3.list_objects_v2(Bucket=bucket, Prefix="tofu-records/")
    except Exception:
        time.sleep(0.05); continue
    for o in r.get("Contents", []):
        k = o["Key"]
        if "/terraform_data/" not in k:
            continue
        try:
            s3.put_object_tagging(Bucket=bucket, Key=k, Tagging={"TagSet": [{"Key": "probe", "Value": str(random.random())}]})
            n["tag"] += 1
        except Exception:
            n["tagerr"] += 1
    k = "noise/%d" % random.randint(0, 50)
    try:
        s3.put_object(Bucket=bucket, Key=k, Body=b"x" * random.randint(1, 4096))
        if random.random() < 0.5:
            s3.delete_object(Bucket=bucket, Key=k)
        n["noise"] += 1
    except Exception:
        pass
    open(out, "w").write(repr(n) + "\n")
