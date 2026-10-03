# an-estate-reads-another-by-declaring-it
# CLAIM 44 (aws) - An estate reads another estate's outputs only by declaring the read: the plan says how old the value is, a destroyed producer's values are gone, and without the grant the plan refuses naming the other estate. ~3 min.

W="$SMOKE_WORKROOT/estateoutputs"; PRODUCER="$W/network"; CONSUMER="$W/app"
mkdir -p "$PRODUCER" "$CONSUMER"
SMOKE_WORK="$W"; export SMOKE_WORK
BUCKET="smoke-estate-outputs"
# The published renderer, the same file bucket-iam.sh names. That file is
# not sourced: it is the real-AWS scenarios' harness.
POLICY_RENDERER="$ROOT/examples/record-store-bucket/iam/render-policy.sh"
# flat undoes the CLI's word wrap before a sentence is matched.
flat() { tr '\n' ' ' | sed 's/│/ /g' | tr -s ' '; }

# The emulator's IAM filter is off by default. This claim's refusal is the
# platform's, so it is on here. The harness's own "test" key still bypasses
# it, so the bucket is set up as the account and only the two roles below
# are governed.
export FLOCI_IAM_ENFORCEMENT=true

cat > "$PRODUCER/main.tf" <<TFEOF
terraform {
  live {
    estate = "network"

    record_store "s3" {
      bucket = "$BUCKET"
    }
  }
}

resource "terraform_data" "anchor" {
  input = "network"
}

# A value no live resource holds: a name this estate chose.
output "cluster_services_namespace" {
  value = "cluster-services"
}
TFEOF

cat > "$CONSUMER/main.tf" <<TFEOF
terraform {
  live {
    estate = "app"

    record_store "s3" {
      bucket = "$BUCKET"
    }
  }
}

# The declaration. It names the producer estate, as the consumer's policy
# does with render-policy.sh --reads-outputs-of network.
data "terraform_estate_outputs" "network" {
  estate = "network"
  names  = ["cluster_services_namespace"]
}

resource "terraform_data" "service" {
  input = data.terraform_estate_outputs.network.values.cluster_services_namespace
}
TFEOF

step "the claim"
explain \
  "Estates that share a record store bucket can read each other's root" \
  "outputs, and only those, when both sides say so. The consumer declares" \
  "the read with a data \"terraform_estate_outputs\" block naming the" \
  "producer estate, and its role is rendered with --reads-outputs-of" \
  "naming the same estate. The value is a copy as of the producer's last" \
  "apply, and the plan says so with the time. When the producer is" \
  "destroyed its copies go with it. When the grant is missing, the plan" \
  "refuses and names the estate it may not read, instead of evaluating" \
  "against nothing (#1371)."

step "1. one bucket, two roles"
stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
command -v jq >/dev/null 2>&1 || fail "estateoutputs" "jq is not installed; the policy renderer needs it"
awsl s3api create-bucket --bucket "$BUCKET" >/dev/null || fail "estateoutputs" "could not create the bucket"
# The bucket contract (claim 29), set once and not mentioned again.
awsl s3api put-bucket-versioning --bucket "$BUCKET" --versioning-configuration Status=Enabled >/dev/null \
  || fail "estateoutputs" "could not enable versioning"
awsl s3api put-bucket-lifecycle-configuration --bucket "$BUCKET" --lifecycle-configuration '{"Rules":[{"ID":"expire-noncurrent","Status":"Enabled","Filter":{"Prefix":""},"NoncurrentVersionExpiration":{"NoncurrentDays":30}}]}' >/dev/null \
  || fail "estateoutputs" "could not set the lifecycle rule"
awsl s3api put-public-access-block --bucket "$BUCKET" --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true >/dev/null \
  || fail "estateoutputs" "could not set the public-access block"

ACCOUNT="$(awsl sts get-caller-identity --query Account --output text)" || fail "estateoutputs" "the emulator did not answer sts get-caller-identity"
TRUST="{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Principal\":{\"AWS\":\"arn:aws:iam::$ACCOUNT:root\"},\"Action\":\"sts:AssumeRole\"}]}"
# role <name> <policy>: an emulator role carrying one inline policy, with
# two edits the pinned emulator needs and real AWS does not.
#
#   - The conditions come off the ALLOW statements. The emulator does not
#     evaluate s3:prefix or s3:RequestObjectTag (claim 35 is the real-AWS
#     measurement of those), and an Allow whose condition it cannot evaluate
#     never matches.
#   - ListOwnNamespaces is granted on "*". choudoufu's S3 client sends
#     virtual-hosted requests (bucket.localhost), and the emulator does not
#     match a ListObjectsV2 sent that way against the bucket's ARN; the AWS
#     CLI's path-style list of the same prefix, under the same statement,
#     is allowed. Measured 2026-09-26.
#
# Both only widen what each role may list and write. What this claim rests on
# is untouched: every GetObject Allow keeps its Resource, and the one that
# reaches tofu-outputs/network/ is ReadDeclaredDependenciesOutputs, which the
# renderer writes only for --reads-outputs-of network.
role() {
  local doc
  doc="$(jq '.Statement |= map(if .Effect == "Allow" then del(.Condition) else . end)
             | (.Statement[] | select(.Sid == "ListOwnNamespaces") | .Resource) = "*"' <<< "$2")" || return 1
  awsl iam create-role --role-name "$1" --assume-role-policy-document "$TRUST" >/dev/null || return 1
  awsl iam put-role-policy --role-name "$1" --policy-name estate --policy-document "$doc"
}
# as <role> <dir> <choudoufu args...>: one run under the role's session
# credentials, in a subshell so nothing leaks into the account-level steps.
as() {
  local r="$1" d="$2" c; shift 2
  c="$(awsl sts assume-role --role-arn "arn:aws:iam::$ACCOUNT:role/$r" --role-session-name "$r" \
        --query 'Credentials.[AccessKeyId,SecretAccessKey,SessionToken]' --output text)" || { echo "  could not assume $r" >&2; return 1; }
  ( cd "$d" && export AWS_ACCESS_KEY_ID="$(cut -f1 <<< "$c")" AWS_SECRET_ACCESS_KEY="$(cut -f2 <<< "$c")" AWS_SESSION_TOKEN="$(cut -f3 <<< "$c")"
    chdf "$@" )
}

cmd "render-policy.sh network $BUCKET --account $ACCOUNT"
role network "$("$POLICY_RENDERER" network "$BUCKET" --account "$ACCOUNT" 2>/dev/null)" || fail "estateoutputs" "could not create the producer's role"
if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - the consumer's role without the grant"
  explain \
    "The consumer's configuration is unchanged: it still declares the" \
    "read. Its role is rendered WITHOUT --reads-outputs-of network, so" \
    "the platform refuses the GET on network's outputs prefix. The plan" \
    "must stop, and the refusal must name estate network. A plan that" \
    "reads the value anyway means something other than this grant is" \
    "letting it through."
  cmd "render-policy.sh app $BUCKET --account $ACCOUNT   # no --reads-outputs-of"
  role app "$("$POLICY_RENDERER" app "$BUCKET" --account "$ACCOUNT" 2>/dev/null)" || fail "estateoutputs" "could not create the consumer's role"
else
  cmd "render-policy.sh app $BUCKET --account $ACCOUNT --reads-outputs-of network"
  role app "$("$POLICY_RENDERER" app "$BUCKET" --account "$ACCOUNT" --reads-outputs-of network 2>/dev/null)" || fail "estateoutputs" "could not create the consumer's role"
fi
awsl iam get-role-policy --role-name app --policy-name estate --query 'PolicyDocument.Statement[?Effect==`Allow`].Sid' --output text | sed 's/^/app may: /' | evidence
proof "two estates, one bucket, each with the role the renderer writes for it."

step "2. the producer applies and records its output"
logged an-estate-reads-another-by-declaring-it-producer-init "estateoutputs" "init failed in the producer" -- in_dir "$PRODUCER" chdf init -input=false -no-color
logged an-estate-reads-another-by-declaring-it-consumer-init "estateoutputs" "init failed in the consumer" -- in_dir "$CONSUMER" chdf init -input=false -no-color
cmd "choudoufu apply -auto-approve   # in network/, as the network role"
P_OUT="$(as network "$PRODUCER" apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "estateoutputs" "the producer could not apply: $P_OUT"
grep -q "Resources: 1 added" <<< "$P_OUT" || fail "estateoutputs" "the producer's apply: $P_OUT"
KEYS="$(awsl s3api list-objects-v2 --bucket "$BUCKET" --prefix "tofu-outputs/network/" --query 'Contents[].Key' --output text | tr '\t' '\n' | grep -v '^None$')"
[ "$(grep -c . <<< "$KEYS")" = "1" ] || fail "estateoutputs" "network should hold exactly one recorded output, and holds: [$KEYS]"
echo "$KEYS" | evidence
proof "network's root output is one object under tofu-outputs/network/."

step "3. the consumer plans with the value"
explain \
  "The consumer runs in its own directory with its own role. The value it" \
  "plans with is read from network's record, and the plan says the value" \
  "is as of network's last apply."
cmd "choudoufu plan   # in app/, as the app role"
C_OUT="$(as app "$CONSUMER" plan -input=false -no-color 2>&1)" && C_RC=0 || C_RC=$?
C_FLAT="$(flat <<< "$C_OUT")"

if [ "${BREAK:-0}" = "1" ]; then
  [ "$C_RC" != "0" ] || fail "estateoutputs" "the consumer planned without the grant, so the grant is not what this claim measures: $C_OUT"
  grep -q '"cluster-services"' <<< "$C_OUT" && fail "estateoutputs" "the consumer's plan shows network's value without the grant: $C_OUT"
  grep -q "This estate may not read another estate's outputs" <<< "$C_FLAT" \
    || fail "estateoutputs" "the plan failed, but not with the denial refusal, so nothing here shows the missing grant stopped it: $C_OUT"
  grep -q "reading estate \"network\"'s output" <<< "$C_FLAT" \
    || fail "estateoutputs" "the refusal does not name estate network as the one it may not read: $C_OUT"
  grep -q "\-\-reads-outputs-of network" <<< "$C_FLAT" \
    || fail "estateoutputs" "the refusal does not say which grant to add: $C_OUT"
  grep -oE "Error: This estate may not read another estate's outputs|Estate \"app\"'s identity was refused reading estate \"network\"'s output \"[a-z_]+\"" <<< "$C_FLAT" | evidence
  proof "caught - with the grant missing and the read still declared, the plan refuses and names estate network and the render-policy.sh flag that grants the read."
  exit 0
fi

[ "$C_RC" = "0" ] || fail "estateoutputs" "the consumer could not plan with the grant: $C_OUT"
grep -qE '\+ input += "cluster-services"' <<< "$C_OUT" \
  || fail "estateoutputs" "the consumer's plan does not use the value network recorded: $C_OUT"
grep -q "Values from another estate are as of its last apply" <<< "$C_FLAT" \
  || fail "estateoutputs" "the plan does not say the value is as of network's last apply: $C_OUT"
grep -qE "read from estate \"network\" as recorded by its last apply, at [0-9]{4}-[0-9]{2}-[0-9]{2}T" <<< "$C_FLAT" \
  || fail "estateoutputs" "the as-of warning does not name estate network and the time of its record: $C_OUT"
grep -E '# terraform_data.service will be created|\+ input += "cluster-services"' <<< "$C_OUT" | sed 's/^ *//' | evidence
grep -oE "Warning: Values from another estate are as of its last apply|The value of \"[a-z_]+\" was read from estate \"network\" as recorded by its last apply, at [0-9TZ:-]+" <<< "$C_FLAT" | evidence
proof "the consumer plans with network's value, and the plan says how old it is."

step "4. the producer is destroyed, and its values go with it"
explain \
  "A destroy of the whole estate deletes what it recorded under" \
  "tofu-outputs/network/. The consumer's same plan then refuses by name," \
  "instead of planning with the last value of an estate that is gone."
cmd "choudoufu apply -destroy -auto-approve   # in network/, as the network role"
D_OUT="$(as network "$PRODUCER" apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "estateoutputs" "the producer's destroy failed: $D_OUT"
destroyed_exactly estateoutputs 1 "$D_OUT"
LEFT="$(awsl s3api list-objects-v2 --bucket "$BUCKET" --prefix "tofu-outputs/network/" --query 'length(Contents || `[]`)' --output text)"
[ "$LEFT" = "0" ] || fail "estateoutputs" "network was destroyed and $LEFT object(s) remain under tofu-outputs/network/"
cmd "choudoufu plan   # in app/, as the app role"
G_OUT="$(as app "$CONSUMER" plan -input=false -no-color 2>&1)" \
  && fail "estateoutputs" "the consumer planned against a destroyed estate's outputs: $G_OUT"
G_FLAT="$(flat <<< "$G_OUT")"
grep -q "Another estate has not recorded this output" <<< "$G_FLAT" \
  || fail "estateoutputs" "the consumer's plan failed, but not because network's output is no longer recorded: $G_OUT"
echo "objects under tofu-outputs/network/ after the destroy: $LEFT" | evidence
grep -oE "Error: Another estate has not recorded this output|Estate \"network\" has no recorded value for its output \"[a-z_]+\"" <<< "$G_FLAT" | evidence
proof "network's destroy deleted its recorded output, and the consumer is told so by name."

echo "  What you watched: a producer recording a root output, a consumer"
echo "  declaring the read and planning with the value under a role granted"
echo "  exactly that read, the plan saying the value is as of the producer's"
echo "  last apply, and the producer's destroy taking its values with it."
