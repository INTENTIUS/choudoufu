# composed-root-three-cases
# One plan and one apply across estates, the three cases #2208 names, on floci. ~4 min.
#
# This run: a hand-written composed root (root/, calling net, app and svc)
# over one S3 record store bucket, with a fourth estate (bystander) in the
# same bucket and outside the root. GitHub issue #2208, ruled: composed root
# only, no joint Terragrunt run.
#
#   case 2  a composed apply that fails part way (svc's provisioner), then a
#           re-run that plans and applies only what is left
#   case 1  each estate run alone after the composed run: no changes, and
#           its records unchanged
#   case 3  a destroy of the composed root: each estate's records gone from
#           its own prefixes, the bystander's untouched
#
# Case 2 runs first because it is what brings the estates up. The plan texts
# and every record listing are kept under $SMOKE_WORKROOT/composed/artifacts,
# which is what the maintainer copies into the issue (or live/smoke/evidence/).
# It proves no claim: a draft, run with `just smoke composed-root-three-cases`.

W="$SMOKE_WORKROOT/composed"
SMOKE_WORK="$W"; export SMOKE_WORK
BUCKET="smoke-composed-records"
ART="$W/artifacts"
mkdir -p "$W/root" "$W/net" "$W/app" "$W/svc" "$W/bystander" "$ART"
flat() { tr '\n' ' ' | sed 's/│/ /g' | tr -s ' '; }

# live_block <estate>: the block every estate here carries.
live_block() {
  cat <<TFEOF
terraform {
  live {
    estate = "$1"

    record_store "s3" {
      bucket = "$BUCKET"
    }
  }
TFEOF
}

{ live_block cr-net; cat <<'TFEOF'

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
}

provider "aws" {
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}

output "vpc_id" {
  value = aws_vpc.main.id
}
TFEOF
} > "$W/net/main.tf"

{ live_block cr-app; cat <<'TFEOF'
}

data "terraform_estate_outputs" "net" {
  estate = "cr-net"
  names  = ["vpc_id"]
}

resource "terraform_data" "app" {
  input = data.terraform_estate_outputs.net.values.vpc_id
}

output "app_id" {
  value = terraform_data.app.id
}
TFEOF
} > "$W/app/main.tf"

# svc reads both, so it applies last; its provisioner fails while $W/FAIL
# exists, which is how case 2 fails part way.
{ live_block cr-svc; cat <<TFEOF
}

data "terraform_estate_outputs" "net" {
  estate = "cr-net"
  names  = ["vpc_id"]
}

data "terraform_estate_outputs" "app" {
  estate = "cr-app"
  names  = ["app_id"]
}

resource "terraform_data" "gate" {
  input = [data.terraform_estate_outputs.net.values.vpc_id, data.terraform_estate_outputs.app.values.app_id]

  provisioner "local-exec" {
    command = "test ! -e '$W/FAIL'"
  }
}
TFEOF
} > "$W/svc/main.tf"

{ live_block cr-bystander; cat <<'TFEOF'
}

resource "terraform_data" "keep" {
  input = "bystander"
}

output "kept" {
  value = terraform_data.keep.id
}
TFEOF
} > "$W/bystander/main.tf"

cat > "$W/root/main.tf" <<'TFEOF'
# A composed root: module calls only, each a directory with its own live block.
module "net" {
  source = "../net"
}

module "app" {
  source = "../app"
}

module "svc" {
  source = "../svc"
}
TFEOF

# listing <estate> <file>: every object version under the estate's prefixes
# (records, published outputs, provisioned bits), one per line, sorted.
listing() {
  local e="$1" out="$2" p
  : > "$out"
  for p in "tofu-records/$e/" "tofu-outputs/$e/" "tofu-provisioned/$e/"; do
    awsl s3api list-object-versions --bucket "$BUCKET" --prefix "$p" \
      --query '[Versions[].[Key,VersionId,IsLatest], DeleteMarkers[].[Key,VersionId,`marker`]][]' --output text 2>/dev/null \
      | grep -v '^None$' >> "$out" || true
  done
  sort -o "$out" "$out"
}
# live_keys <estate>: the keys currently present under its record prefix.
live_keys() {
  awsl s3api list-objects-v2 --bucket "$BUCKET" --prefix "tofu-records/$1/" --query 'Contents[].Key' --output text 2>/dev/null \
    | tr '\t' '\n' | grep -v -e '^None$' -e '^$' || true
}
snapshot() { # <label>: listings of all four estates
  local e
  for e in cr-net cr-app cr-svc cr-bystander; do listing "$e" "$ART/$1.$e.txt"; done
}

step "setup: one bucket, four estates, the bystander applied alone"
stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
awsl s3api create-bucket --bucket "$BUCKET" >/dev/null || fail "composed" "could not create the bucket"
awsl s3api put-bucket-versioning --bucket "$BUCKET" --versioning-configuration Status=Enabled >/dev/null \
  || fail "composed" "could not enable versioning"
awsl s3api put-public-access-block --bucket "$BUCKET" --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true >/dev/null
for d in bystander net app svc root; do
  logged "composed-root-three-cases-$d-init" "composed" "init failed in $d" -- in_dir "$W/$d" chdf init -input=false -no-color
done
B_OUT="$(cd "$W/bystander" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "composed" "the bystander's apply failed: $B_OUT"
[ -n "$(live_keys cr-bystander)" ] || fail "composed" "the bystander wrote no record"
listing cr-bystander "$ART/0-start.cr-bystander.txt"
evidence < "$ART/0-start.cr-bystander.txt"
proof "the bystander estate holds its record in the shared bucket."

step "case 2. a composed apply that fails part way, then a re-run"
explain \
  "svc reads net and app, so it applies after them, and its provisioner" \
  "fails while a file named FAIL exists. The estates that applied must" \
  "keep their records, and the re-run must plan only svc's resources."
: > "$W/FAIL"
cmd "choudoufu apply -auto-approve   # in root/, FAIL present"
F_RC=0
F_OUT="$(cd "$W/root" && chdf apply -auto-approve -input=false -no-color 2>&1)" || F_RC=$?
printf '%s\n' "$F_OUT" > "$ART/2a-failed-apply.txt"
[ "$F_RC" != "0" ] || fail "composed" "[case 2] the composed apply with FAIL present exited 0, so nothing failed part way: $F_OUT"
grep -q "local-exec" <<< "$(flat <<< "$F_OUT")" || fail "composed" "[case 2] the composed apply failed, but not on svc's provisioner: $F_OUT"
snapshot 2a-after-failed-apply
[ -n "$(live_keys cr-net)" ] || fail "composed" "[case 2] net has no record after the failed apply; what applied was not kept"
[ -n "$(live_keys cr-app)" ] || fail "composed" "[case 2] app has no record after the failed apply; what applied was not kept"
for e in cr-net cr-app cr-svc; do echo "--- $e after the failed apply ---"; cat "$ART/2a-after-failed-apply.$e.txt"; done | evidence
rm -f "$W/FAIL"
cmd "choudoufu plan   # in root/, FAIL removed"
R_RC=0
R_PLAN="$(cd "$W/root" && chdf plan -input=false -no-color 2>&1)" || R_RC=$?
printf '%s\n' "$R_PLAN" > "$ART/2b-rerun-plan.txt"
[ "$R_RC" = "0" ] || fail "composed" "[case 2] the re-run's plan failed: $R_PLAN"
R_LINES="$(grep -E '^  # ' <<< "$R_PLAN" || true)"
echo "$R_LINES" | evidence
[ -n "$R_LINES" ] || fail "composed" "[case 2] the re-run's plan names no resource, so svc's unapplied gate is not in it: $R_PLAN"
grep -q "terraform_data.gate" <<< "$R_LINES" || fail "composed" "[case 2] the re-run's plan does not name svc's terraform_data.gate: $R_PLAN"
if grep -qE "aws_vpc\.main|terraform_data\.app" <<< "$R_LINES"; then
  fail "composed" "[case 2] the re-run's plan names a resource of an estate that already applied: $R_LINES"
fi
cmd "choudoufu apply -auto-approve   # in root/"
A_OUT="$(cd "$W/root" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "composed" "[case 2] the re-run's apply failed: $A_OUT"
printf '%s\n' "$A_OUT" > "$ART/2c-rerun-apply.txt"
grep -E '^Apply complete' <<< "$A_OUT" | evidence
[ -n "$(live_keys cr-svc)" ] || fail "composed" "[case 2] svc has no record after the re-run's apply"
proof "the failed apply kept net's and app's records, and the re-run planned and applied only svc's gate."

step "case 1. each estate run alone after the composed run"
explain \
  "A resource's marker and record key are the address it has in its own" \
  "directory, so the lone run of an estate sees what the composed run" \
  "applied as its own, with nothing to change, and writes nothing."
snapshot 1a-before-lone
for e in net app svc; do
  cmd "choudoufu plan   # in $e/, alone"
  L_RC=0
  L_OUT="$(cd "$W/$e" && chdf plan -input=false -no-color 2>&1)" || L_RC=$?
  printf '%s\n' "$L_OUT" > "$ART/1b-lone-plan.$e.txt"
  [ "$L_RC" = "0" ] || fail "composed" "[case 1] the lone plan of $e failed: $L_OUT"
  grep -q "No changes." <<< "$L_OUT" || fail "composed" "[case 1] the lone plan of $e is not clean after the composed run: $L_OUT"
  grep -E 'No changes\.' <<< "$L_OUT" | head -1 | sed "s/^/$e: /" | evidence
done
snapshot 1c-after-lone
for e in cr-net cr-app cr-svc cr-bystander; do
  diff -u "$ART/1a-before-lone.$e.txt" "$ART/1c-after-lone.$e.txt" > "$ART/1d-diff.$e.txt" \
    || fail "composed" "[case 1] $e's record listing changed across the lone runs: $(cat "$ART/1d-diff.$e.txt")"
done
proof "net, app and svc each planned alone with no changes, and no estate's listing moved."

step "case 3. destroy the composed root"
explain \
  "Each estate's records must leave its own prefixes, and the bystander," \
  "which shares the bucket and is not in the root, must be untouched."
snapshot 3a-before-destroy
cmd "choudoufu apply -destroy -auto-approve   # in root/"
D_OUT="$(cd "$W/root" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "composed" "[case 3] the composed destroy failed: $D_OUT"
printf '%s\n' "$D_OUT" > "$ART/3b-destroy.txt"
destroyed_exactly composed 3 "$D_OUT"
snapshot 3c-after-destroy
for e in cr-net cr-app cr-svc; do
  LEFT="$(live_keys "$e")"
  [ -z "$LEFT" ] || fail "composed" "[case 3] $e still has records after the composed destroy: $LEFT"
done
diff -u "$ART/3a-before-destroy.cr-bystander.txt" "$ART/3c-after-destroy.cr-bystander.txt" > "$ART/3d-diff.cr-bystander.txt" \
  || fail "composed" "[case 3] the bystander's listing changed across the composed destroy: $(cat "$ART/3d-diff.cr-bystander.txt")"
[ -n "$(live_keys cr-bystander)" ] || fail "composed" "[case 3] the bystander's record is gone"
evidence < "$ART/3c-after-destroy.cr-bystander.txt"
proof "3 destroyed; net's, app's and svc's record prefixes are empty, and the bystander's listing is identical."

step "teardown"
B_D="$(cd "$W/bystander" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "composed" "the bystander's destroy failed: $B_D"
destroyed_exactly composed 1 "$B_D"
proof "gone."

echo "  The plan texts and record listings for #2208 are in $ART."
echo "  Commit the run's commit sha with them: $(git -C "$ROOT" rev-parse HEAD 2>/dev/null || echo '?')"
