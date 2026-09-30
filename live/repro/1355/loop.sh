#!/usr/bin/env bash
# loop.sh <bin> <chdf-endpoint> <direct-endpoint> <bucket> <workdir> <iters> <tag>
# The #1355 sequence, repeated. Each iteration is its own estate, so one
# iteration's leftovers can never feed the next.
#   1 apply (2 added)
#   2 out-of-band put-object-tagging on a.b's record (variant-dependent)
#   3 change input, apply (2 changed)
#   4 apply -destroy (must be 2 destroyed, 0 records left)
# Variants rotate by iteration: 0 retag-replace, 1 retag-add, 2 retag+rm cache,
# 3 no retag. RM_CACHE_ALWAYS=1 deletes the state cache before every apply.
# CONTROL=1 deletes plain's record out of band before the destroy, which must
# be reported as an anomaly (1 destroyed): the check that this loop can fail.
set -uo pipefail
BIN="$1" EP="$2" DIRECT="$3" B="$4" W="$5" N="$6" TAG="$7"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
mkdir -p "$W"; cd "$W" || exit 2
RES="$W/results.tsv"
A() { AWS_ENDPOINT_URL="$DIRECT" aws "$@"; }
C() { AWS_ENDPOINT_URL="$EP" "$BIN" "$@"; }
tf() {
  cat > main.tf <<EOF
terraform {
  live {
    estate = "$1"
    record_store "s3" {
      bucket = "$B"
      region = "us-east-1"
    }
  }
}
resource "terraform_data" "effect" {
  for_each = toset(["a.b", "plain"])
  input    = "\${each.key}$2"
}
EOF
}
tf "init-$TAG" ""
C init -input=false -no-color > init.log 2>&1 || { echo "init failed"; cat init.log; exit 2; }
for i in $(seq 1 "$N"); do
  est="e1355-$TAG-$i"; v=$((i % 4)); d="$W/it-$i"; mkdir -p "$d"
  tf "$est" ""
  [ "${RM_CACHE_ALWAYS:-0}" = 1 ] && rm -f .terraform/choudoufu-cache.tfstate
  C apply -auto-approve -input=false -no-color > "$d/1-apply.log" 2>&1; r1=$?
  ab="tofu-records/$est/terraform_data/dGVycmFmb3JtX2RhdGEuZWZmZWN0WyJhLmIiXQ"
  case $v in
    0) A s3api put-object-tagging --bucket "$B" --key "$ab" --tagging 'TagSet=[{Key=probe,Value=x}]' > "$d/2-tag.log" 2>&1 ;;
    1) cur="$(A s3api get-object-tagging --bucket "$B" --key "$ab" --output json 2>/dev/null | python3 -c 'import json,sys;t=json.load(sys.stdin)["TagSet"];t.append({"Key":"probe","Value":"x"});print(json.dumps({"TagSet":t}))')"
       A s3api put-object-tagging --bucket "$B" --key "$ab" --tagging "$cur" > "$d/2-tag.log" 2>&1 ;;
    2) A s3api put-object-tagging --bucket "$B" --key "$ab" --tagging 'TagSet=[{Key=probe,Value=x}]' > "$d/2-tag.log" 2>&1
       rm -f .terraform/choudoufu-cache.tfstate ;;
    3) : ;;
  esac
  tf "$est" "-v2"
  C apply -auto-approve -input=false -no-color > "$d/3-apply.log" 2>&1; r3=$?
  [ "${RM_CACHE_ALWAYS:-0}" = 1 ] && rm -f .terraform/choudoufu-cache.tfstate
  [ "${CONTROL:-0}" = 1 ] && A s3api delete-object --bucket "$B" --key "tofu-records/$est/terraform_data/dGVycmFmb3JtX2RhdGEuZWZmZWN0WyJwbGFpbiJd" >/dev/null
  C apply -destroy -auto-approve -input=false -no-color > "$d/4-destroy.log" 2>&1; r4=$?
  added="$(grep -oE 'Resources: [0-9]+ added' "$d/1-apply.log" | grep -oE '[0-9]+')"
  changed="$(grep -oE '[0-9]+ changed' "$d/3-apply.log" | head -1 | grep -oE '[0-9]+')"
  destroyed="$(grep -oE '[0-9]+ destroyed' "$d/4-destroy.log" | tail -1 | grep -oE '[0-9]+')"
  left="$(A s3api list-objects-v2 --bucket "$B" --prefix "tofu-records/$est/terraform_data/" --query 'Contents[].Key' --output text 2>/dev/null | tr '\t' '\n' | grep -c terraform_data)"
  ok=1
  [ "$r1$r3$r4" = "000" ] || ok=0
  [ "$added" = 2 ] && [ "$changed" = 2 ] && [ "$destroyed" = 2 ] && [ "$left" = 0 ] || ok=0
  printf '%s\t%d\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$TAG" "$i" "$v" "$r1$r3$r4" "$added" "$changed" "$destroyed" "$left" "$ok" "$(date +%s)" >> "$RES"
  if [ "$ok" = 1 ]; then rm -rf "$d"; else echo "ANOMALY $TAG it=$i v=$v rc=$r1$r3$r4 added=$added changed=$changed destroyed=$destroyed left=$left"; fi
done
