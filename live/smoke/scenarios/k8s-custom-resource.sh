# k8s-custom-resource
# CLAIM 24 - A custom resource binds by its natural key, carries the estate label and is swept by it, a block whose CRD the cluster does not serve is refused by name, and the plan carries the API server's own dry-run verdict on every planned object: a kubernetes_manifest block is found again by the apiVersion, kind, namespace and name written inside its manifest, with no state file, its object created with tofu-estate in metadata.labels; before the CRD is installed the plan refuses the block naming the kind, the apiVersion and the CRD to install; the plan submits the planned object to the server with dryRun=All and prints its acceptance, and a manifest the server rejects refuses the plan by name in the server's words; a label stripped out of band takes the object out of the estate and the next plan refuses it by name, an object deleted out of band walks back in as a create, and an object whose block is removed is found by the sweep and proposed for removal; and a custom resource stock created and recorded in a terraform.tfstate is adopted by live-import, which writes that same label as one API merge patch whose dry run is diffed against the live object so a write that would change anything beyond the labels map is refused and records the metadata keys the stock configuration declared, so that a label removed from the configuration once the state file is deleted is removed from the live object; and a migration of label-carrying objects under an estate name no Kubernetes label value can hold is refused once, at the read-only run, naming the count, with no per-object line and nothing written. ~5 min.
#
# The first unit of #1079 (ruled 2026-09-12): every custom resource is
# declared through kubernetes_manifest, whose whole object is one dynamic
# argument, and the natural key #1016 ruled on is four keys inside that
# argument's object constructor. Identity resolution reads them without
# evaluating the manifest (internal/live/identity/manifest.go) and renders
# the provider's own import id, so a replan with nothing stored anywhere
# re-binds the object. The second unit (the projection's stamp,
# internal/live/projection/nodestamp_manifest.go) writes the one
# tofu-estate label into manifest.metadata.labels on create, the same label
# every built-in type carries in its metadata block, so the object is
# inside the estate's boundary the way claim 23 draws it. The third unit
# (internal/live/kubesweep) lists every kind the cluster serves, CRDs
# included, under kubernetes_manifest, so an object whose block is removed
# is found by that label and proposed for removal at
# kubernetes_manifest.orphan_<kind>_<namespace>_<name>. The fourth unit
# (internal/live/discovery/kubernetes.go, refuseUnservedManifests) asks the
# cluster, at the plan's first contact with it, whether it serves the
# apiVersion and kind each manifest block names, and refuses a block whose
# pair it does not - by address, kind, apiVersion and the CRD that would
# have to be installed - before the provider fails at that block with its
# own error; step 1 plans before the CRD exists and requires exactly that
# refusal. live-check is offline and cannot ask. Item 3 of #1081 is the
# dry run: once the plan exists, every planned create or update of a
# kubernetes_manifest instance is the API object itself, label included,
# and the plan sends it to the server with dryRun=All
# (internal/command/live_plan_kubernetes_dryrun.go,
# internal/live/discovery/kubernetes_dryrun.go) - the server validates it
# against the CRD's schema, defaults it and runs admission, and persists
# nothing. Step 3 shows an object whose namespace the same plan creates
# reported rather than submitted, and step 4 requires the acceptance
# line above the plan. BREAK=1 first writes spec.replicas = 0, which the
# CRD's schema bounds at minimum 1 - a rule the provider does not check
# and the server does - and requires the replan refused by name with the
# server's message quoted and no plan produced; then strips the label
# with kubectl and
# requires the replan to propose the update that restores it; then strips
# it again, removes the block, and requires the replan NOT to list the
# object (the label is the boundary both ways); then deletes the object
# and requires the replan to propose creating it. If any of those plans
# read the other way, the label, the natural key or the dry run was
# scenery. Its last control is step 12's: it migrates the stock CronTab
# cleanly and then cuts manifest_metadata_keys out of the estate's record,
# which is the record a build without #1391's migrate-time seed writes, and
# requires the identical label deletion to plan "No changes." with the
# label still on the object.
#
# Step 12 is GitHub issue #1391, and it is the one thing a migration owes
# an estate beyond the label. A stock state file holds the last-applied
# manifest, so stock knows which metadata keys the configuration asked for
# and removes one that is deleted from it. The adopt page's next
# instruction after a migration is to delete that file. So live-import
# records the declared key set into the estate's own record
# (internal/live/liveimport's seedManifestKeys, through the same
# projection.ManifestDeclaredKeys the apply write-back uses), taking it
# from the STATE's recorded object rather than from the live read, and
# #1211's removal analysis reads it from there. Without the seed the
# removal is proposed by nothing, which no refusal and no warning says.
#
# Step 14 is GitHub issue #1396 (#1432, measured here for #1434). An
# estate name is up to 128 characters of [a-z0-9-] and a label value is
# capped at 63, so a name every AWS tag accepts can be one no Kubernetes
# object can carry. Ratify counts the entries whose carrier is a label or
# a manifest while it builds them and refuses once, after the loop, when
# that count is above zero and the name is not a legal label value -
# returning no ratification, so no report prints and -approve cannot run.
# The step migrates two ConfigMaps stock made under a 64-character name
# and requires exactly one diagnostic naming "2 resource instances", the
# name and the clause it broke, exit 1, no per-object line, and both
# objects at the same resourceVersion with no label before and after.
# Before #1432 the read-only run said nothing and -approve printed one
# identical FAILED line per object. BREAK=1's seventh control runs the
# same migration under a 60-character name and requires the report, one
# line per ConfigMap and 2 of 2 eligible, and exit 0: the refusal is the
# cap and not the fixture.

SMOKE_WORK="$SMOKE_WORKROOT/k8s-custom-resource"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK

# The CRD is the one the Kubernetes documentation uses to explain custom
# resources (crontabs.stable.example.com), installed with kubectl before
# the estate is planned: the provider reads a custom kind's schema from the
# cluster at plan time, which is why a manifest whose CRD is not served is
# refused by name rather than defaulted (#1079's fourth ruling).
cat > "$SMOKE_WORK/crd.yaml" <<'YAML'
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: crontabs.stable.example.com
spec:
  group: stable.example.com
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                cronSpec:
                  type: string
                image:
                  type: string
                replicas:
                  type: integer
                  minimum: 1
  scope: Namespaced
  names:
    plural: crontabs
    singular: crontab
    kind: CronTab
    shortNames:
      - ct
YAML
cat > "$SMOKE_WORK/versions.tf" <<'TF'
terraform {
  required_version = ">= 1.5.0"

  live {
    estate = "smoke-crd"
  }

  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "= 3.2.1"
    }
  }
}

provider "kubernetes" {}
TF
cat > "$SMOKE_WORK/main.tf" <<'TF'
resource "kubernetes_namespace" "crd" {
  metadata {
    name = "smoke-crd"
  }
}

resource "kubernetes_manifest" "crontab" {
  manifest = {
    apiVersion = "stable.example.com/v1"
    kind       = "CronTab"
    metadata = {
      name      = "my-crontab"
      namespace = "smoke-crd"
    }
    spec = {
      cronSpec = "* * * * */5"
      image    = "my-awesome-cron-image"
    }
  }
  depends_on = [kubernetes_namespace.crd]
}
TF

cp "$SMOKE_WORK/main.tf" "$SMOKE_WORK/main.tf.full"
sed '/^resource "kubernetes_manifest" "crontab"/,$d' "$SMOKE_WORK/main.tf.full" > "$SMOKE_WORK/main.tf.namespace-only"

cluster_up

# migrate_fixture_up stands up the adoption fixture that step 11 and the
# mutating-policy BREAK control both work from: a second namespace and a
# CronTab created by PLAIN stock terraform and recorded in a real
# terraform.tfstate, and beside it the identical source with a live block
# on it, initialised. It is a function rather than a step because the
# BREAK arm exits before step 11 and still has to migrate something; the
# once-only guard makes "at most one caller" a property of this function
# rather than of the control flow above it.
#
# The stock manifest declares two labels of its own, team and tier, which
# step 12 needs (#1391): the state file is the only record that this
# configuration ever asked for them, and the adopt page's next instruction
# is to delete it. Two rather than one so the removal below takes one of a
# pair rather than emptying the map, which is a different shape and is
# claim 27's to measure.
MIGRATE_FIXTURE_UP=0
migrate_fixture_up() {
[ "$MIGRATE_FIXTURE_UP" = "1" ] && return 0
MIGRATE_FIXTURE_UP=1
command -v terraform >/dev/null 2>&1 \
  || fail "k8s-custom-resource" "the terraform binary is not on PATH - this step needs the stock oracle to write the state file being adopted"
mkdir -p "$SMOKE_WORK/stock" "$SMOKE_WORK/migrated"
cat > "$SMOKE_WORK/stock/main.tf" <<'TF'
terraform {
  required_version = ">= 1.5.0"

  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "= 3.2.1"
    }
  }
}

provider "kubernetes" {}

resource "kubernetes_namespace" "adopted" {
  metadata {
    name = "smoke-crd-stock"
  }
}

resource "kubernetes_manifest" "crontab" {
  manifest = {
    apiVersion = "stable.example.com/v1"
    kind       = "CronTab"
    metadata = {
      name      = "adopted-crontab"
      namespace = "smoke-crd-stock"
      labels = {
        team = "a"
        tier = "batch"
      }
    }
    spec = {
      cronSpec = "* * * * */5"
      image    = "my-awesome-cron-image"
    }
  }
  depends_on = [kubernetes_namespace.adopted]
}
TF
cmd "terraform apply -auto-approve   # plain stock, no live block, a real terraform.tfstate"
( cd "$SMOKE_WORK/stock" && terraform init -input=false -no-color >/dev/null 2>&1 ) \
  || fail "k8s-custom-resource" "stock init failed"
STOCK_APPLY="$(cd "$SMOKE_WORK/stock" && terraform apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-custom-resource" "stock apply failed: $(tail -5 <<< "$STOCK_APPLY")"
grep -qF "Apply complete! Resources: 2 added" <<< "$STOCK_APPLY" \
  || fail "k8s-custom-resource" "stock did not create exactly the namespace and the CronTab: $(grep -E 'Apply complete' <<< "$STOCK_APPLY")"
[ -f "$SMOKE_WORK/stock/terraform.tfstate" ] || fail "k8s-custom-resource" "stock left no terraform.tfstate"
PRE_LABEL="$(kc get crontab adopted-crontab -n smoke-crd-stock -o jsonpath='{.metadata.labels.tofu-estate}' 2>/dev/null || true)"
[ -z "$PRE_LABEL" ] || fail "k8s-custom-resource" "the stock-made CronTab already carries tofu-estate=$PRE_LABEL; this step would prove nothing"
grep -E 'Apply complete!' <<< "$STOCK_APPLY" | evidence
note "no tofu-estate label on the CronTab stock made (kubectl reads nothing)"

# The same configuration with a live block on it, which is all adoption
# changes in the source.
{ sed 's/^terraform {/terraform {\n\n  live {\n    estate = "smoke-crd-stock"\n  }\n/' "$SMOKE_WORK/stock/main.tf"; } > "$SMOKE_WORK/migrated/main.tf"
cp "$SMOKE_WORK/migrated/main.tf" "$SMOKE_WORK/migrated/main.tf.full"
sed '/^resource "kubernetes_manifest" "crontab"/,$d' "$SMOKE_WORK/migrated/main.tf.full" > "$SMOKE_WORK/migrated/main.tf.namespace-only"
( cd "$SMOKE_WORK/migrated" && chdf init -input=false -no-color >/dev/null 2>&1 ) \
  || fail "k8s-custom-resource" "init of the migrated root failed"
}

# The two estate names step 14 and its BREAK control run under (#1396,
# #1434). An estate name is up to 128 characters of [a-z0-9-]; a Kubernetes
# label value is capped at 63. So the 60-character name is legal on both
# counts and the 64-character one - the same name with four more digits -
# is a legal estate name that no object on the label surface can carry.
# Both lengths are asserted rather than trusted, because a fixture whose
# "long" name were 63 characters would make the whole step scenery.
LABEL_OK_ESTATE="$(printf 'smoke-crd-cm-%047d' 0)"
LABEL_LONG_ESTATE="${LABEL_OK_ESTATE}0000"
[ "${#LABEL_OK_ESTATE}" = "60" ] || fail "k8s-custom-resource" "the legal estate name is ${#LABEL_OK_ESTATE} characters, not 60"
[ "${#LABEL_LONG_ESTATE}" = "64" ] || fail "k8s-custom-resource" "the over-long estate name is ${#LABEL_LONG_ESTATE} characters, not 64"

# label_fixture_up stands up step 14's fixture, which the BREAK control for
# the same step also works from: two ConfigMaps in the default namespace,
# created by PLAIN stock terraform and recorded in a real terraform.tfstate,
# and beside it the identical source with a live block on it, initialised.
# Its own fixture rather than step 11's, because step 12 deletes that state
# file and the objects it names carry the label by then; two objects rather
# than one so the refusal's count is a count (#1432's own test uses two
# ConfigMaps for the same reason); the default namespace so the state
# holds exactly the two label carriers and nothing else. Same once-only
# guard as migrate_fixture_up, for the same reason.
LABEL_FIXTURE_UP=0
label_fixture_up() {
[ "$LABEL_FIXTURE_UP" = "1" ] && return 0
LABEL_FIXTURE_UP=1
command -v terraform >/dev/null 2>&1 \
  || fail "k8s-custom-resource" "the terraform binary is not on PATH - this step needs the stock oracle to write the state file being adopted"
mkdir -p "$SMOKE_WORK/stock-cm" "$SMOKE_WORK/migrated-cm"
cat > "$SMOKE_WORK/stock-cm/configmaps.tf" <<'TF'
resource "kubernetes_config_map" "a" {
  metadata {
    name      = "smoke-crd-cm-a"
    namespace = "default"
  }
  data = {
    key = "a"
  }
}

resource "kubernetes_config_map" "b" {
  metadata {
    name      = "smoke-crd-cm-b"
    namespace = "default"
  }
  data = {
    key = "b"
  }
}
TF
cat > "$SMOKE_WORK/stock-cm/main.tf" <<'TF'
terraform {
  required_version = ">= 1.5.0"

  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "= 3.2.1"
    }
  }
}

provider "kubernetes" {}
TF
cmd "terraform apply -auto-approve   # plain stock, no live block, two ConfigMaps in a real terraform.tfstate"
( cd "$SMOKE_WORK/stock-cm" && terraform init -input=false -no-color >/dev/null 2>&1 ) \
  || fail "k8s-custom-resource" "stock init of the ConfigMap root failed"
CM_APPLY="$(cd "$SMOKE_WORK/stock-cm" && terraform apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-custom-resource" "stock apply of the two ConfigMaps failed: $(tail -5 <<< "$CM_APPLY")"
grep -qF "Apply complete! Resources: 2 added" <<< "$CM_APPLY" \
  || fail "k8s-custom-resource" "stock did not create exactly the two ConfigMaps: $(grep -E 'Apply complete' <<< "$CM_APPLY")"
[ -f "$SMOKE_WORK/stock-cm/terraform.tfstate" ] || fail "k8s-custom-resource" "stock left no terraform.tfstate for the ConfigMaps"
grep -E 'Apply complete!' <<< "$CM_APPLY" | evidence

# The same source with a live block on it, under the name that IS a legal
# label value. -estate is what a migration runs under; the block is what
# the root would plan under once migrated.
cp "$SMOKE_WORK/stock-cm/configmaps.tf" "$SMOKE_WORK/migrated-cm/configmaps.tf"
cat > "$SMOKE_WORK/migrated-cm/main.tf" <<TF
terraform {
  required_version = ">= 1.5.0"

  live {
    estate = "$LABEL_OK_ESTATE"
  }

  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "= 3.2.1"
    }
  }
}

provider "kubernetes" {}
TF
( cd "$SMOKE_WORK/migrated-cm" && chdf init -input=false -no-color >/dev/null 2>&1 ) \
  || fail "k8s-custom-resource" "init of the migrated ConfigMap root failed"
}

# cm_state is what "nothing on the cluster changed" is measured against:
# each ConfigMap's name, resourceVersion and tofu-estate label, one per line.
cm_state() {
  kc get configmap smoke-crd-cm-a smoke-crd-cm-b -n default \
    -o jsonpath='{range .items[*]}{.metadata.name} resourceVersion={.metadata.resourceVersion} tofu-estate={.metadata.labels.tofu-estate}{"\n"}{end}'
}

step "1. before the CRD exists, the block is refused by name"
explain \
  "The cluster does not serve stable.example.com/v1 CronTab yet. The" \
  "provider reads a custom kind's schema from the cluster when it plans" \
  "and would fail at that block with its own error; choudoufu asks the" \
  "cluster first, at the plan's first contact with it, and refuses the" \
  "block by name - the address, the kind, the apiVersion, and the CRD" \
  "that would have to be installed. Nothing is planned, nothing applied." \
  "live-check is offline and cannot ask a cluster; the plan can."
cmd "choudoufu init && choudoufu plan   # no CRD installed yet"
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null ) || fail "k8s-custom-resource" "init failed"
if kc get crd crontabs.stable.example.com >/dev/null 2>&1; then
  fail "k8s-custom-resource" "the CRD is already installed on a fresh cluster; this step measures nothing"
fi
if PRE_OUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)"; then
  fail "k8s-custom-resource" "plan succeeded with the CRD absent: $(grep -E '^Plan:|No changes|will be' <<< "$PRE_OUT" | head -3)"
fi
grep -E 'Error: Kubernetes kind not served|declares kind CronTab|spec\.group is' <<< "$PRE_OUT" | head -3 | evidence
# The renderer wraps a diagnostic's detail at the terminal width, so the
# phrases are matched on the output with its line breaks folded to spaces.
PRE_FLAT="$(tr '\n' ' ' <<< "$PRE_OUT" | tr -s ' ')"
grep -q 'Error: Kubernetes kind not served by the cluster' <<< "$PRE_FLAT" \
  || fail "k8s-custom-resource" "the plan failed, but not with the refusal by name: $PRE_OUT"
grep -q 'kubernetes_manifest.crontab declares kind CronTab at apiVersion stable.example.com/v1' <<< "$PRE_FLAT" \
  || fail "k8s-custom-resource" "the refusal does not name the block, kind and apiVersion: $PRE_OUT"
grep -q 'spec.group is "stable.example.com" and spec.names.kind is "CronTab", with version "v1" served' <<< "$PRE_FLAT" \
  || fail "k8s-custom-resource" "the refusal does not say which CRD to install: $PRE_OUT"
grep -q 'on main.tf line' <<< "$PRE_FLAT" || fail "k8s-custom-resource" "the refusal does not point at the block's declaration: $PRE_OUT"
if grep -qE '^Plan:|to add,' <<< "$PRE_OUT"; then
  fail "k8s-custom-resource" "a plan was produced alongside the refusal: $PRE_OUT"
fi
proof "refused by name before the provider was asked: kubernetes_manifest.crontab, kind CronTab, apiVersion stable.example.com/v1, and the CRD (group stable.example.com, kind CronTab, version v1) that has to be installed. Non-zero exit, no plan."

step "2. a CRD the cluster serves, installed with kubectl"
explain \
  "The provider reads a custom kind's schema from the cluster when it" \
  "plans, so the CRD comes first, through kubectl, the way an operator's" \
  "install step or a platform team would put it there. Nothing about it" \
  "is this tool's; it is the CronTab from the Kubernetes documentation."
cmd "kubectl apply -f crd.yaml"
kc apply -f "$SMOKE_WORK/crd.yaml" >/dev/null || fail "k8s-custom-resource" "could not install the CRD"
# Not a bare `kubectl wait --for=condition=Established`: the CRD the server
# has just accepted is served with `"conditions": null` until a controller
# fills it in, and kubectl's accessor errors on that instead of retrying
# (#1278). k8s_wait_condition waits for the conditions to exist first, and
# tells the two failures apart.
k8s_wait_condition "k8s-custom-resource" crd/crontabs.stable.example.com Established
kc get crd crontabs.stable.example.com -o jsonpath='{.metadata.name}{" "}{.spec.scope}{"\n"}' | evidence
proof "crontabs.stable.example.com is served and namespaced. The estate below declares one CronTab through kubernetes_manifest."

step "3. the namespace first: the server cannot judge an object in a namespace this same plan creates"
explain \
  "The plan is computed locally, as every plan is. Then every planned" \
  "create or update of a kubernetes_manifest object is sent to the API" \
  "server with dryRun=All. The CronTab's namespace is created by this" \
  "same plan, so the server would answer 404 - the apply's order, not" \
  "the object's validity - and the plan says so instead of submitting" \
  "it. The namespace is applied on its own first."
cmd "choudoufu plan   # both blocks; then apply with the namespace block only"
NS_PLAN="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "k8s-custom-resource" "plan failed with the CRD installed: $NS_PLAN"
grep -E 'Server-side dry run:|\[NOT SUBMITTED\]|^Plan:' <<< "$NS_PLAN" | head -3 | evidence
grep -q 'kubernetes_manifest.crontab \[NOT SUBMITTED\] its namespace smoke-crd is created by this same plan' <<< "$NS_PLAN" \
  || fail "k8s-custom-resource" "the plan does not say the CronTab's namespace is its own to create: $NS_PLAN"
grep -q 'Plan: 2 to add, 0 to change, 0 to destroy' <<< "$NS_PLAN" || fail "k8s-custom-resource" "the plan is not the 2 creates: $(grep -E '^Plan:' <<< "$NS_PLAN")"
cp "$SMOKE_WORK/main.tf.namespace-only" "$SMOKE_WORK/main.tf"
NS_APPLY="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-custom-resource" "the namespace apply failed: $NS_APPLY"
grep -qE 'Apply complete! Resources: 1 added' <<< "$NS_APPLY" || fail "k8s-custom-resource" "the namespace apply did not report 1 added: $NS_APPLY"
grep -E 'Apply complete!' <<< "$NS_APPLY" | evidence
proof "an object in a namespace the same plan creates is reported, not submitted; the namespace exists now, a built-in type the dry run never covers (its object shape is the provider's own)."

step "4. the plan asks the server first: the planned CronTab, dry run, nothing written"
explain \
  "With the namespace live, the planned CronTab - the manifest as the" \
  "apply would write it, tofu-estate label included - goes to the API" \
  "server with dryRun=All: the server validates it against the CRD's" \
  "schema, applies its defaults and runs every admission policy, and" \
  "persists nothing. AWS has no equivalent. The answer prints above the" \
  "plan, one line per object; a rejection would refuse the plan by name."
cmd "choudoufu plan   # the CronTab block is back; its object does not exist yet"
cp "$SMOKE_WORK/main.tf.full" "$SMOKE_WORK/main.tf"
DRY_OUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "k8s-custom-resource" "plan failed with the namespace live: $DRY_OUT"
grep -E 'Server-side dry run:|\[ACCEPTED\]|^Plan:' <<< "$DRY_OUT" | head -3 | evidence
grep -q "kubernetes_manifest.crontab \[ACCEPTED\] CronTab smoke-crd/my-crontab: create accepted by the server's admission, dry run, nothing written" <<< "$DRY_OUT" \
  || fail "k8s-custom-resource" "the plan carries no dry-run acceptance for kubernetes_manifest.crontab: $DRY_OUT"
grep -q 'Server-side dry run: 1 of 1 planned Kubernetes object accepted by the API server' <<< "$DRY_OUT" \
  || fail "k8s-custom-resource" "the dry-run heading does not count the one object: $DRY_OUT"
grep -q 'Plan: 1 to add, 0 to change, 0 to destroy' <<< "$DRY_OUT" || fail "k8s-custom-resource" "the plan is not the one create: $(grep -E '^Plan:' <<< "$DRY_OUT")"
if kc get crontab my-crontab -n smoke-crd >/dev/null 2>&1; then
  fail "k8s-custom-resource" "the dry run persisted the CronTab"
fi
proof "the API server accepted the planned CronTab - validated, defaulted, admitted - before anything was applied, and kubectl confirms nothing was written."

step "5. the estate applies: the custom resource, no state file"
explain \
  "One kubernetes_manifest block. Its identity is the natural key written" \
  "inside the manifest - apiVersion, kind, metadata.namespace," \
  "metadata.name - which identity resolution reads out of the object" \
  "constructor without evaluating the manifest, rendered as the provider's" \
  "own import id. choudoufu keeps only a disposable cache."
cmd "choudoufu apply -auto-approve"
APPLY_OUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-custom-resource" "apply failed: $APPLY_OUT"
grep -E 'Apply complete!' <<< "$APPLY_OUT" | evidence
grep -qE 'Apply complete! Resources: 1 added' <<< "$APPLY_OUT" || fail "k8s-custom-resource" "apply did not report 1 added: $APPLY_OUT"
[ ! -f "$SMOKE_WORK/terraform.tfstate" ] || fail "k8s-custom-resource" "a terraform.tfstate appeared"
cmd "kubectl get crontab my-crontab -n smoke-crd"
CT="$(kc get crontab my-crontab -n smoke-crd -o jsonpath='{.spec.cronSpec}{" "}{.spec.image}{" tofu-estate="}{.metadata.labels.tofu-estate}{"\n"}' 2>&1)" \
  || fail "k8s-custom-resource" "kubectl cannot read the CronTab: $CT"
echo "$CT" | evidence
grep -q 'tofu-estate=smoke-crd$' <<< "$CT" || fail "k8s-custom-resource" "the CronTab does not carry tofu-estate=smoke-crd: $CT"
proof "the CronTab exists with the spec the configuration declared and the one label the configuration never wrote, tofu-estate=smoke-crd; no terraform.tfstate exists."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - a manifest the CRD's schema rejects; the server must refuse it and the plan must be refused by name"
  explain \
    "You asked for proof the dry run is load-bearing. The CRD bounds" \
    "spec.replicas at minimum 1; this writes replicas = 0. The provider" \
    "checks the field's type against the CRD and nothing more, so the" \
    "plan is an in-place update as far as it can tell. If the server's" \
    "answer were scenery, the plan would print and exit 0 and the apply" \
    "would be the first to fail."
  cmd "sed 's/image    = \"my-awesome-cron-image\"/&\n      replicas = 0/' main.tf ; choudoufu plan"
  sed_i "$SMOKE_WORK/main.tf" -e 's/^\(      image    = "my-awesome-cron-image"\)$/\1\
      replicas = 0/'
  grep -q 'replicas = 0' "$SMOKE_WORK/main.tf" || fail "k8s-custom-resource" "BREAK: the manifest edit did not land"
  if ROUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)"; then
    fail "k8s-custom-resource" "BREAK: the plan succeeded with a manifest the server rejects: $(grep -E '^Plan:|No changes|will be' <<< "$ROUT" | head -3)"
  fi
  grep -E 'Error: Kubernetes API server rejected|\[REJECTED\]|spec.replicas' <<< "$ROUT" | head -3 | evidence
  RFLAT="$(tr '\n' ' ' <<< "$ROUT" | tr -s ' ')"
  grep -q 'Error: Kubernetes API server rejected the planned object' <<< "$RFLAT" \
    || fail "k8s-custom-resource" "BREAK: the plan failed, but not with the refusal by name: $ROUT"
  grep -q 'refused the update kubernetes_manifest.crontab plans' <<< "$RFLAT" \
    || fail "k8s-custom-resource" "BREAK: the refusal does not name the instance and the verb: $ROUT"
  grep -q 'spec.replicas: Invalid value: 0: spec.replicas in body should be greater than or equal to 1' <<< "$RFLAT" \
    || fail "k8s-custom-resource" "BREAK: the refusal does not quote the server's message about spec.replicas: $ROUT"
  grep -q 'kubernetes_manifest.crontab \[REJECTED\] CronTab smoke-crd/my-crontab: update refused by the server' <<< "$ROUT" \
    || fail "k8s-custom-resource" "BREAK: the evidence section does not mark the object rejected: $ROUT"
  if grep -qE '^Plan:|to add,' <<< "$ROUT"; then
    fail "k8s-custom-resource" "BREAK: a plan was produced alongside the refusal: $ROUT"
  fi
  cp "$SMOKE_WORK/main.tf.full" "$SMOKE_WORK/main.tf"
  LIVE_REPLICAS="$(kc get crontab my-crontab -n smoke-crd -o jsonpath='{.spec.replicas}')"
  [ -z "$LIVE_REPLICAS" ] || fail "k8s-custom-resource" "BREAK: the dry run wrote spec.replicas=$LIVE_REPLICAS to the live object"
  proof "caught: the server refused replicas 0 under the CRD's minimum of 1 - a rule only the server checks - the plan was refused by name in the server's words, no plan was produced and nothing was written. The edit is reverted."

  step "BREAK control - strip the label out of band; the replan must refuse the object by name"
  explain \
    "You asked for proof the assertions can fail. This removes the" \
    "tofu-estate label with kubectl, behind choudoufu's back. The provider" \
    "treats metadata.labels as a computed field by default and would accept" \
    "the stripped object as the truth; if the next plan is empty, the" \
    "label is decoration and nothing holds the object inside the boundary." \
    "What the plan must do is what it does with an unlabelled ConfigMap and" \
    "with a stripped tofu-estate tag on AWS (#1108): an object carrying no" \
    "marker is nobody's, so the plan refuses it by name and falls back to" \
    "creating what the block declares - and the server's own dry run then" \
    "refuses that create, because the object it would not adopt is still" \
    "holding the name. Adopting it back is a label write an operator makes," \
    "which is what the next line does with kubectl."
  cmd "kubectl label crontab my-crontab -n smoke-crd tofu-estate-"
  kc label crontab my-crontab -n smoke-crd tofu-estate- >/dev/null || fail "k8s-custom-resource" "BREAK: could not strip the label"
  SOUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1 || true)"
  SFLAT="$(tr '\n' ' ' <<< "$SOUT" | tr -s ' ')"
  if grep -q "No changes." <<< "$SOUT"; then
    fail "k8s-custom-resource" "BREAK: the plan is still empty after the label was stripped - the label is not what the plan holds the object by"
  fi
  grep -E 'kubernetes_manifest.crontab \[UNOWNED\]|API server rejected the planned object' <<< "$SOUT" | head -2 | evidence
  grep -q 'kubernetes_manifest.crontab \[UNOWNED\]' <<< "$SOUT" \
    || fail "k8s-custom-resource" "BREAK: the plan does not refuse the unlabelled custom resource by name - it was adopted in silence: $(grep -E '^Plan:|will be' <<< "$SOUT" | head -3)"
  grep -q 'carries no tofu-estate label, so this estate does not own it' <<< "$SFLAT" \
    || fail "k8s-custom-resource" "BREAK: the refusal does not say the object carries no marker for this estate: $SOUT"
  grep -q 'The API server refused the create kubernetes_manifest.crontab plans' <<< "$SFLAT" \
    || fail "k8s-custom-resource" "BREAK: the refused object was not followed by the server refusing the create it fell back to: $SOUT"
  grep -q 'crontabs.stable.example.com "my-crontab" already exists' <<< "$SFLAT" \
    || fail "k8s-custom-resource" "BREAK: the server's answer is not that the unowned object still holds the name: $SOUT"
  STILL="$(kc get crontab my-crontab -n smoke-crd -o jsonpath='{.metadata.labels.tofu-estate}')"
  [ -z "$STILL" ] || fail "k8s-custom-resource" "BREAK: the plan put the label back on an object it does not own (tofu-estate=$STILL)"
  cmd "kubectl label crontab my-crontab -n smoke-crd tofu-estate=smoke-crd   # the operator adopts it back"
  kc label crontab my-crontab -n smoke-crd tofu-estate=smoke-crd >/dev/null || fail "k8s-custom-resource" "BREAK: could not adopt the object back"
  AOUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1 || true)"
  grep -q "No changes." <<< "$AOUT" \
    || fail "k8s-custom-resource" "BREAK: the replan after the operator's own label write is not empty: $(grep -E '^Plan:|will be' <<< "$AOUT" | head -3)"
  proof "caught: with its label gone the CronTab is nobody's. The plan refuses it by name rather than relabelling it, falls back to the create the block declares, and the server's dry run refuses that too because the unowned object still holds the name. The label stays off until an operator writes it - after which the replan is empty again."

  step "BREAK control - strip the label and remove the block; the replan must not list the object"
  explain \
    "The label is the boundary both ways. With the label gone AND the block" \
    "gone, nothing says the object is this estate's: the sweep must not" \
    "find it and the plan must not propose destroying it. If it did, the" \
    "sweep would be selecting on something other than the label."
  cmd "kubectl label crontab my-crontab -n smoke-crd tofu-estate- && (remove the block) && choudoufu plan"
  kc label crontab my-crontab -n smoke-crd tofu-estate- >/dev/null || fail "k8s-custom-resource" "BREAK: could not strip the label again"
  cp "$SMOKE_WORK/main.tf.namespace-only" "$SMOKE_WORK/main.tf"
  NOUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1 || true)"
  if grep -q "orphan_crontab" <<< "$NOUT"; then
    fail "k8s-custom-resource" "BREAK: an unlabelled object was swept into the plan: $(grep -E 'orphan_crontab|^Plan:' <<< "$NOUT" | head -2)"
  fi
  grep -q "No changes." <<< "$NOUT" || fail "k8s-custom-resource" "BREAK: the plan is not empty with the label and the block both gone: $(grep -E '^Plan:|will be' <<< "$NOUT" | head -3)"
  grep -E 'No changes\.' <<< "$NOUT" | head -1 | evidence
  cp "$SMOKE_WORK/main.tf.full" "$SMOKE_WORK/main.tf"
  kc label crontab my-crontab -n smoke-crd tofu-estate=smoke-crd >/dev/null || fail "k8s-custom-resource" "BREAK: could not put the label back"
  proof "caught: an object with no label is nobody's, and the sweep left it alone."

  step "BREAK control - delete the custom resource out of band; the replan must propose creating it"
  explain \
    "Now the CronTab itself is deleted with kubectl. If the next plan is" \
    "still empty, nothing was reading the cluster by the natural key and" \
    "every empty replan in this scenario is scenery."
  cmd "kubectl delete crontab my-crontab -n smoke-crd"
  kc delete crontab my-crontab -n smoke-crd >/dev/null || fail "k8s-custom-resource" "BREAK: could not delete the CronTab"
  BOUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1 || true)"
  if grep -q "No changes." <<< "$BOUT"; then
    fail "k8s-custom-resource" "BREAK: the plan is still empty after the CronTab was deleted - the natural key is not what the plan reads"
  fi
  grep -E '^Plan:|will be created' <<< "$BOUT" | head -2 | evidence
  grep -q 'kubernetes_manifest.crontab will be created' <<< "$BOUT" \
    || fail "k8s-custom-resource" "BREAK: the plan changed but does not propose creating kubernetes_manifest.crontab: $BOUT"
  proof "caught. The deleted object is exactly what the plan proposes to create, so the empty replans below are real checks and the natural key is load-bearing."

  step "BREAK control - something rewrites the object on the way past; the migration must refuse rather than write"
  explain \
    "A MutatingAdmissionPolicy is installed that rewrites spec.image on" \
    "every update to a CronTab. The label patch itself names one key" \
    "under metadata.labels and can reach nothing else - but the server" \
    "can, and \"the request is small\" is an assertion, not a check. The" \
    "dry run is what turns it into one: the object the server says it" \
    "would store is compared with the object it holds, and a difference" \
    "outside the labels map refuses the write with nothing sent. A" \
    "migration that can silently rewrite a custom resource's spec is" \
    "worse than one that does nothing."
  migrate_fixture_up
  cat > "$SMOKE_WORK/mutator.yaml" <<'YAML'
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingAdmissionPolicy
metadata:
  name: crontab-image-rewriter
spec:
  matchConstraints:
    resourceRules:
      - apiGroups:   ["stable.example.com"]
        apiVersions: ["v1"]
        operations:  ["UPDATE"]
        resources:   ["crontabs"]
  failurePolicy: Fail
  reinvocationPolicy: Never
  mutations:
    - patchType: ApplyConfiguration
      applyConfiguration:
        expression: >
          Object{ spec: Object.spec{ image: "rewritten-by-the-policy" } }
---
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingAdmissionPolicyBinding
metadata:
  name: crontab-image-rewriter
spec:
  policyName: crontab-image-rewriter
YAML
  cmd "kubectl apply -f mutator.yaml && choudoufu live-import -approve"
  kc apply -f "$SMOKE_WORK/mutator.yaml" >/dev/null \
    || fail "k8s-custom-resource" "BREAK: could not install the mutating policy (it needs a cluster serving admissionregistration.k8s.io/v1 MutatingAdmissionPolicy)"
  sleep 5
  MIG_BOUT="$(cd "$SMOKE_WORK/migrated" && chdf_bounded live-import -state="$SMOKE_WORK/stock/terraform.tfstate" -estate=smoke-crd-stock -approve -no-color 2>&1)" \
    || fail "k8s-custom-resource" "BREAK: live-import -approve exited non-zero: $MIG_BOUT"
  grep -E 'kubernetes_manifest.crontab|failed, ' <<< "$MIG_BOUT" | tail -2 | evidence
  grep -q 'would also change spec.image' <<< "$MIG_BOUT" \
    || fail "k8s-custom-resource" "BREAK: the migration did not refuse the rewritten write by name: $(grep -A2 'kubernetes_manifest.crontab' <<< "$MIG_BOUT" | tail -3)"
  grep -q '1 failed' <<< "$MIG_BOUT" \
    || fail "k8s-custom-resource" "BREAK: the summary does not count the refusal: $(grep 'newly stamped' <<< "$MIG_BOUT")"
  B_LABEL="$(kc get crontab adopted-crontab -n smoke-crd-stock -o jsonpath='{.metadata.labels.tofu-estate}' 2>/dev/null || true)"
  [ -z "$B_LABEL" ] || fail "k8s-custom-resource" "BREAK: the label was written anyway (tofu-estate=$B_LABEL)"
  B_IMAGE="$(kc get crontab adopted-crontab -n smoke-crd-stock -o jsonpath='{.spec.image}')"
  [ "$B_IMAGE" = "my-awesome-cron-image" ] \
    || fail "k8s-custom-resource" "BREAK: the object's spec.image is now $B_IMAGE; the refusal did not stop the write"
  kc delete -f "$SMOKE_WORK/mutator.yaml" >/dev/null 2>&1 || true
  sleep 5
  proof "with a policy rewriting spec.image in the path, live-import refused the label write by name (\"would also change spec.image\"), counted it as 1 failed, and left the object exactly as it was - no label, the original image. The main run, with no such policy, makes the identical write and it lands, so the refusal is the dry run's and not the tool's dislike of the type."

  step "BREAK control - a migration that recorded no declared key set; the label removal must NOT plan"
  explain \
    "You asked for proof that step 12's removal is the SEEDED record and" \
    "not something the plan could work out from the object. The policy is" \
    "gone, so this migration lands as the main run's does. Then the one" \
    "member live-import seeds - manifest_metadata_keys - is cut out of" \
    "the estate's record with jq, which leaves exactly the record a build" \
    "without #1391's fix writes: same object, same label, same stock" \
    "state, same edit. If the plan still removes the label, the record is" \
    "not what the removal reads and step 12 is scenery."
  cmd "choudoufu live-import -approve   # no policy in the way this time"
  MIG_OK="$(cd "$SMOKE_WORK/migrated" && chdf live-import -state="$SMOKE_WORK/stock/terraform.tfstate" -estate=smoke-crd-stock -approve -no-color 2>&1)" \
    || fail "k8s-custom-resource" "BREAK: the second live-import -approve exited non-zero: $MIG_OK"
  grep -q '0 failed, 0 skipped' <<< "$MIG_OK" \
    || fail "k8s-custom-resource" "BREAK: the control's own migration did not land cleanly: $(grep 'newly stamped' <<< "$MIG_OK")"

  cmd "jq 'del(.residue.manifest_metadata_keys)' over the estate's records"
  RECDIR="$SMOKE_WORK/migrated/.tofu-records"
  [ -d "$RECDIR" ] || fail "k8s-custom-resource" "BREAK: no record store at $RECDIR, so there is nothing to cut the key set out of"
  STRIPPED=0
  REMOVED=0
  while IFS= read -r f; do
    jq -e '.residue.manifest_metadata_keys' "$f" >/dev/null 2>&1 || continue
    # The whole residue member goes when the key set was all of it, which
    # is what a kubernetes_manifest has: nothing else about it classifies
    # as residue. A residue member left behind holding {} is a record no
    # build ever wrote, and the reader refuses it by name.
    jq 'if (.residue | keys) == ["manifest_metadata_keys"] then del(.residue) else del(.residue.manifest_metadata_keys) end' "$f" > "$f.stripped" \
      || fail "k8s-custom-resource" "BREAK: jq could not rewrite $f"
    mv "$f.stripped" "$f"
    STRIPPED=$((STRIPPED + 1))
    # And the file itself goes when nothing but the envelope's bookkeeping
    # is left. That is what a pre-#1391 build leaves behind: the store
    # deletes a record with no payload rather than keeping an empty one,
    # and a manifest-shaped instance has no flat identity to keep its
    # record alive. Leaving the husk would make this control measure the
    # reader refusing a shape no build produces.
    if ! jq -e '[keys[] | select(. != "format_version" and . != "address" and . != "kind" and . != "provider")] | length > 0' "$f" >/dev/null 2>&1; then
      rm -f "$f"
      REMOVED=$((REMOVED + 1))
    fi
  done < <(find "$RECDIR" -type f)
  [ "$STRIPPED" -ge 1 ] \
    || fail "k8s-custom-resource" "BREAK: no record carried manifest_metadata_keys, so the migration never seeded one and step 12's green run proves nothing"
  echo "  stripped manifest_metadata_keys from $STRIPPED record(s), $REMOVED of which held nothing else" | evidence

  cmd "(delete team = \"a\" from the manifest) && choudoufu plan"
  sed_i "$SMOKE_WORK/migrated/main.tf" '/team *= *"a"/d'
  if grep -q 'team' "$SMOKE_WORK/migrated/main.tf"; then
    fail "k8s-custom-resource" "BREAK: the label is still in the configuration; this control would measure nothing"
  fi
  BRM="$(cd "$SMOKE_WORK/migrated" && chdf plan -input=false -no-color 2>&1)" \
    || fail "k8s-custom-resource" "BREAK: the plan after deleting the label failed: $BRM"
  grep -E '^No changes|^Plan:|team' <<< "$BRM" | head -3 | evidence
  grep -q "No changes." <<< "$BRM" \
    || fail "k8s-custom-resource" "BREAK: the removal planned with no recorded key set, so step 12 is not measuring the seed: $(grep -E '^Plan:|will be|team' <<< "$BRM" | head -5)"
  BLBL="$(kc get crontab adopted-crontab -n smoke-crd-stock -o jsonpath='{.metadata.labels}')"
  grep -q '"team":"a"' <<< "$BLBL" \
    || fail "k8s-custom-resource" "BREAK: the label is off the object already, so the empty plan above says nothing: $BLBL"
  proof "caught: with manifest_metadata_keys cut out of the record - the record a pre-#1391 live-import writes - the identical edit plans \"No changes.\" and the label stays on the object. Step 12's in-place update is the seeded key set and nothing else."

  step "BREAK control - the same migration under a 60-character name; the report must print and the run must exit 0"
  explain \
    "Step 14 refuses a migration of two ConfigMaps under a 64-character" \
    "estate name. That refusal is about the name and nothing else, or it" \
    "is scenery: here the same state, the same root and the same read-only" \
    "run go under a name four characters shorter - 60, a legal label" \
    "value - and the ratification report must print, one line per" \
    "ConfigMap and 2 of 2 eligible, with no Estate name diagnostic and" \
    "exit 0. If this run were refused too, step 14 would be measuring the" \
    "fixture and not the cap."
  label_fixture_up
  cmd "choudoufu live-import -state=../stock-cm/terraform.tfstate -estate=<60 characters>   # read-only, no -approve"
  OK_BEFORE="$(cm_state)"
  OK_RC=0
  OK_OUT="$(cd "$SMOKE_WORK/migrated-cm" && chdf live-import -state="$SMOKE_WORK/stock-cm/terraform.tfstate" -estate="$LABEL_OK_ESTATE" -no-color 2>"$SMOKE_WORK/ok-name.stderr")" || OK_RC=$?
  OK_ERR="$(cat "$SMOKE_WORK/ok-name.stderr")"
  [ "$OK_RC" = "0" ] \
    || fail "k8s-custom-resource" "BREAK: the read-only run under a 60-character name exited $OK_RC: $(tail -5 <<< "$OK_OUT$OK_ERR")"
  if grep -q 'Estate name cannot be written as a Kubernetes label' <<< "$OK_ERR"; then
    fail "k8s-custom-resource" "BREAK: a 60-character name was refused as a label value, so step 14's refusal is not the 63-character cap: $OK_ERR"
  fi
  grep -E '^  kubernetes_config_map\.|eligible for stamping' <<< "$OK_OUT" | head -3 | evidence || true
  OK_LINES="$(grep -cE '^  kubernetes_config_map\.(a|b) ' <<< "$OK_OUT" || true)"
  [ "$OK_LINES" = "2" ] \
    || fail "k8s-custom-resource" "BREAK: the report does not carry one line per ConfigMap (got $OK_LINES): $OK_OUT"
  grep -q '2 of 2 resource instance(s) are eligible for stamping' <<< "$OK_OUT" \
    || fail "k8s-custom-resource" "BREAK: the report under a legal name does not find both ConfigMaps eligible: $(grep 'eligible for stamping' <<< "$OK_OUT")"
  OK_AFTER="$(cm_state)"
  [ "$OK_BEFORE" = "$OK_AFTER" ] \
    || fail "k8s-custom-resource" "BREAK: a read-only run changed the cluster: before: $OK_BEFORE after: $OK_AFTER"
  proof "caught: the identical read-only run under a 60-character name prints the report - one line per ConfigMap, 2 of 2 eligible - and exits 0 with no diagnostic and nothing written. Step 14's refusal is the 63-character cap on a label value and nothing else."
  kc delete configmap smoke-crd-cm-a smoke-crd-cm-b -n default >/dev/null 2>&1 || true

  ( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  ( cd "$SMOKE_WORK/migrated" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  exit 0
fi

step "6. the replan - prior state rebuilt from the cluster by the natural key"
explain \
  "With no state file, the next plan asks the cluster for the object the" \
  "manifest names: apiVersion=stable.example.com/v1,kind=CronTab," \
  "namespace=smoke-crd,name=my-crontab, the provider's own import id. If" \
  "binding is right, the plan is empty."
cmd "choudoufu plan"
PLAN_OUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "k8s-custom-resource" "plan failed: $PLAN_OUT"
grep -q "No changes." <<< "$PLAN_OUT" || fail "k8s-custom-resource" "replan is not empty: $(grep -E '^Plan:|will be' <<< "$PLAN_OUT" | head -3)"
grep -E 'No changes\.' <<< "$PLAN_OUT" | head -1 | evidence
proof "an empty plan, the custom resource found by the four keys written in its manifest and nothing else."

step "7. the cache is disposable"
cmd "rm .terraform/choudoufu-cache.tfstate && choudoufu plan"
CACHE="$SMOKE_WORK/.terraform/choudoufu-cache.tfstate"
[ -f "$CACHE" ] || fail "k8s-custom-resource" "no cache at $CACHE after a plain apply"
rm -f "$CACHE"
PLAN2="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "k8s-custom-resource" "plan without the cache failed"
grep -q "No changes." <<< "$PLAN2" || fail "k8s-custom-resource" "deleting the cache changed the plan: $(grep -E '^Plan:|will be' <<< "$PLAN2" | head -3)"
grep -E 'No changes\.' <<< "$PLAN2" | head -1 | evidence
proof "the cache was there and its loss changed nothing."

step "8. the block is removed - the sweep finds the object by its label and the plan removes it"
explain \
  "The kubernetes_manifest block is deleted from the configuration and" \
  "nothing else changes. No state file remembers the CronTab; the sweep" \
  "lists every kind the cluster serves, CRDs included, selected on the" \
  "estate label, and files the object under kubernetes_manifest at an" \
  "address that says what it is. The plan proposes destroying exactly it."
cmd "(remove the kubernetes_manifest block) && choudoufu plan && choudoufu apply -auto-approve"
cp "$SMOKE_WORK/main.tf.namespace-only" "$SMOKE_WORK/main.tf"
ORPHAN_PLAN="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "k8s-custom-resource" "plan after removing the block failed: $ORPHAN_PLAN"
grep -E 'orphan_crontab|^Plan:' <<< "$ORPHAN_PLAN" | head -2 | evidence
grep -q 'kubernetes_manifest.orphan_crontab_smoke-crd_my-crontab will be destroyed' <<< "$ORPHAN_PLAN" \
  || fail "k8s-custom-resource" "the plan does not propose destroying the swept CronTab: $(grep -E '^Plan:|will be|No changes' <<< "$ORPHAN_PLAN" | head -3)"
grep -q 'Plan: 0 to add, 0 to change, 1 to destroy' <<< "$ORPHAN_PLAN" \
  || fail "k8s-custom-resource" "the plan proposes more than the one orphan: $(grep -E '^Plan:' <<< "$ORPHAN_PLAN")"
ORPHAN_APPLY="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-custom-resource" "the orphan apply failed: $ORPHAN_APPLY"
grep -E 'Apply complete!' <<< "$ORPHAN_APPLY" | evidence
if kc get crontab my-crontab -n smoke-crd >/dev/null 2>&1; then
  fail "k8s-custom-resource" "the CronTab still exists after the orphan apply"
fi
proof "the CronTab is gone: found by its label under a kind the provider has no type for, and destroyed through kubernetes_manifest."

step "9. the block returns - the object is created again"
cmd "(restore the block) && choudoufu apply -auto-approve"
cp "$SMOKE_WORK/main.tf.full" "$SMOKE_WORK/main.tf"
BACK="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-custom-resource" "re-apply failed: $BACK"
grep -qE 'Apply complete! Resources: 1 added, 0 changed, 0 destroyed' <<< "$BACK" || fail "k8s-custom-resource" "re-apply did not add exactly the CronTab: $BACK"
grep -E 'Apply complete!' <<< "$BACK" | evidence
proof "1 added, the same object at the same key, labelled again."

step "10. destroy - exactly what was made"
cmd "choudoufu apply -destroy -auto-approve"
DESTROY_OUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-custom-resource" "apply -destroy failed: $DESTROY_OUT"
grep -E 'Destroy complete|Apply complete' <<< "$DESTROY_OUT" | head -1 | evidence
grep -qE "Resources: 0 added, 0 changed, 2 destroyed" <<< "$DESTROY_OUT" \
  || fail "k8s-custom-resource" "destroy did not remove exactly the 2 created resources: $DESTROY_OUT"
if kc get crontab my-crontab -n smoke-crd >/dev/null 2>&1; then
  fail "k8s-custom-resource" "the CronTab still exists after destroy"
fi
kc get crd crontabs.stable.example.com >/dev/null 2>&1 || fail "k8s-custom-resource" "the CRD is gone; destroy reached past the estate"
proof "2 destroyed, 0 added, 0 changed. The custom resource is gone and the CRD, which nothing declared, stands."

step "11. migrate: a custom resource stock made, adopted by live-import"
explain \
  "Everything above was choudoufu's from the first apply. This is the" \
  "other door: a CronTab that stock terraform created and recorded in a" \
  "real terraform.tfstate, with no label on it, adopted into an estate." \
  "live-import reads the state once, finds the live object by the" \
  "apiVersion, kind, namespace and name inside the manifest the state" \
  "recorded, and writes the one tofu-estate label as an API merge patch" \
  "under the caller's own credential - the type has no metadata block to" \
  "write into, so a labels-only write through the provider would be a" \
  "re-apply of the whole manifest. The patch goes first with dryRun=All," \
  "and the object the server answers with is compared with the object it" \
  "holds: if anything outside the labels map moved, nothing is sent."
migrate_fixture_up

cmd "choudoufu live-import -state=../stock/terraform.tfstate -estate=smoke-crd-stock   # read-only first"
DRY="$(cd "$SMOKE_WORK/migrated" && chdf live-import -state="$SMOKE_WORK/stock/terraform.tfstate" -estate=smoke-crd-stock -no-color 2>&1)" \
  || fail "k8s-custom-resource" "live-import (read-only) failed: $DRY"
grep -E 'kubernetes_manifest.crontab|eligible for stamping' <<< "$DRY" | head -2 | evidence
if grep -q 'UNTAGGABLE' <<< "$DRY"; then
  fail "k8s-custom-resource" "live-import still reads something as UNTAGGABLE: $(grep -A2 'UNTAGGABLE' <<< "$DRY" | head -4)"
fi
grep -q 'live id: apiVersion=stable.example.com/v1,kind=CronTab,namespace=smoke-crd-stock,name=adopted-crontab' <<< "$DRY" \
  || fail "k8s-custom-resource" "the CronTab's live id is not the natural key out of its manifest: $(grep 'kubernetes_manifest.crontab' <<< "$DRY")"
grep -q '2 of 2 resource instance(s) are eligible for stamping' <<< "$DRY" \
  || fail "k8s-custom-resource" "not everything in the stock state is eligible: $(grep 'eligible for stamping' <<< "$DRY")"


cmd "choudoufu live-import -approve"
APPROVE="$(cd "$SMOKE_WORK/migrated" && chdf live-import -state="$SMOKE_WORK/stock/terraform.tfstate" -estate=smoke-crd-stock -approve -no-color 2>&1)" \
  || fail "k8s-custom-resource" "live-import -approve failed: $APPROVE"
SUMMARY="$(grep 'newly stamped' <<< "$APPROVE" | tail -1 || true)"
grep -E 'kubernetes_manifest.crontab .*Wrote the tofu-estate label|newly stamped' <<< "$APPROVE" | tail -2 | evidence
grep -q '0 failed, 0 skipped' <<< "$SUMMARY" \
  || fail "k8s-custom-resource" "the migration skipped or failed something: ${SUMMARY:-no summary line}"
grep -q 'kubernetes_manifest.crontab .*Wrote the tofu-estate label' <<< "$APPROVE" \
  || fail "k8s-custom-resource" "live-import did not write the CronTab's label: $(grep -A1 'kubernetes_manifest.crontab' <<< "$APPROVE" | tail -2)"
grep -q '2 resource(s) newly stamped' <<< "$SUMMARY" \
  || fail "k8s-custom-resource" "the migration did not stamp both objects: ${SUMMARY:-no summary line}"
ADOPTED_LABEL="$(kc get crontab adopted-crontab -n smoke-crd-stock -o jsonpath='{.metadata.labels.tofu-estate}')"
[ "$ADOPTED_LABEL" = "smoke-crd-stock" ] \
  || fail "k8s-custom-resource" "the CronTab does not carry the estate label after the migration (tofu-estate=${ADOPTED_LABEL:-none})"
ADOPTED_IMAGE="$(kc get crontab adopted-crontab -n smoke-crd-stock -o jsonpath='{.spec.image}')"
[ "$ADOPTED_IMAGE" = "my-awesome-cron-image" ] \
  || fail "k8s-custom-resource" "the label write moved the object's spec.image to $ADOPTED_IMAGE"
kc get crontab adopted-crontab -n smoke-crd-stock -o jsonpath='{.metadata.labels}{"\n"}' | evidence
proof "2 newly stamped, 0 failed, 0 skipped; kubectl reads tofu-estate=smoke-crd-stock on the custom resource and its spec is untouched. Before #1109 this line read \"1 newly stamped ... 1 skipped\" and the CronTab carried no label: the manifest shape was not a live-import carrier, so a migrated custom resource was bound and counted but left outside the boundary."

step "12. a label the stock configuration declared, removed after the migration, is removed from the object"
explain \
  "The CronTab stock made carries two labels its configuration asked" \
  "for, team and tier. Which keys a configuration DECLARED is not on the" \
  "object - the object holds the label and no memory of who asked for" \
  "it - and it is not in the configuration once the key is deleted from" \
  "it either. A stock run reads it out of the last-applied manifest in" \
  "its state file, and the adopt page's next instruction after a" \
  "migration is to delete that file. So live-import records the declared" \
  "key set into the estate's own record, the same set an apply records" \
  "(#1211), and the removal set is (recorded) minus (currently" \
  "declared). Without that seed the state file's deletion takes the" \
  "answer with it and the label stays on the object for ever, which no" \
  "refusal and no warning would have said (#1391)."
cmd "choudoufu plan   # nothing removed yet"
KEEP="$(cd "$SMOKE_WORK/migrated" && chdf plan -input=false -no-color 2>&1)" \
  || fail "k8s-custom-resource" "the first plan after the migration failed: $KEEP"
grep -q "No changes." <<< "$KEEP" \
  || fail "k8s-custom-resource" "the first plan after the migration is not empty, so the seeded key set moved what a converged estate plans: $(grep -E '^Plan:|will be' <<< "$KEEP" | head -3)"
grep -E 'No changes\.' <<< "$KEEP" | head -1 | evidence

cmd "rm ../stock/terraform.tfstate   # what the adopt page says to do next"
rm -f "$SMOKE_WORK/stock/terraform.tfstate" "$SMOKE_WORK/stock/terraform.tfstate.backup"
[ ! -f "$SMOKE_WORK/stock/terraform.tfstate" ] \
  || fail "k8s-custom-resource" "the stock state file is still there, so nothing below is evidence about a migrated estate that has none"

cmd "(delete team = \"a\" from the manifest) && choudoufu plan"
sed_i "$SMOKE_WORK/migrated/main.tf" '/team *= *"a"/d'
sed_i "$SMOKE_WORK/migrated/main.tf.full" '/team *= *"a"/d'
if grep -q 'team' "$SMOKE_WORK/migrated/main.tf"; then
  fail "k8s-custom-resource" "the label is still in the configuration; this step would measure nothing"
fi
RM_PLAN="$(cd "$SMOKE_WORK/migrated" && chdf plan -input=false -no-color 2>&1)" \
  || fail "k8s-custom-resource" "the plan after deleting the label failed: $RM_PLAN"
grep -E 'team|^Plan:|^No changes' <<< "$RM_PLAN" | head -3 | evidence
grep -qE '^Plan: 0 to add, 1 to change, 0 to destroy' <<< "$RM_PLAN" \
  || fail "k8s-custom-resource" "deleting a declared label from a MIGRATED estate did not plan one in-place update - this is #1391: $(grep -E '^Plan:|No changes' <<< "$RM_PLAN" | head -2)"
grep -qE '^ +- +"?team"? +=' <<< "$RM_PLAN" \
  || fail "k8s-custom-resource" "the plan changes something, but it is not the deleted label: $(grep -E 'will be|team|tier' <<< "$RM_PLAN" | head -5)"

cmd "choudoufu apply -auto-approve && kubectl get crontab adopted-crontab -n smoke-crd-stock -o jsonpath='{.metadata.labels}'"
RM_APPLY="$(cd "$SMOKE_WORK/migrated" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-custom-resource" "the removal apply failed: $RM_APPLY"
grep -qE 'Apply complete! Resources: 0 added, 1 changed, 0 destroyed' <<< "$RM_APPLY" \
  || fail "k8s-custom-resource" "the removal apply did not report exactly one change: $(grep -E 'Apply complete' <<< "$RM_APPLY")"
RM_LABELS="$(kc get crontab adopted-crontab -n smoke-crd-stock -o jsonpath='{.metadata.labels}')"
echo "$RM_LABELS" | evidence
if grep -q '"team"' <<< "$RM_LABELS"; then
  fail "k8s-custom-resource" "the apply reported success and the label is still on the object: $RM_LABELS"
fi
grep -q '"tier":"batch"' <<< "$RM_LABELS" \
  || fail "k8s-custom-resource" "the label that was NOT removed from the configuration is gone too: $RM_LABELS"
grep -q '"tofu-estate":"smoke-crd-stock"' <<< "$RM_LABELS" \
  || fail "k8s-custom-resource" "the estate's own marker was removed along with the label: $RM_LABELS"
SETTLE="$(cd "$SMOKE_WORK/migrated" && chdf plan -input=false -no-color 2>&1)" \
  || fail "k8s-custom-resource" "the replan after the removal failed: $SETTLE"
grep -q "No changes." <<< "$SETTLE" \
  || fail "k8s-custom-resource" "the estate does not settle after the removal: $(grep -E '^Plan:|will be' <<< "$SETTLE" | head -3)"
grep -E 'No changes\.' <<< "$SETTLE" | head -1 | evidence
proof "a label stock declared and the migration inherited is removed from the live object when it is removed from the configuration, with the state file deleted and nothing but the estate's own record remembering it was ever declared. tier, which nobody removed, is untouched; so is the marker; and the estate settles."

step "13. the migrated estate replans empty, and the sweep can now see the adopted object"
cmd "choudoufu plan   # no state file; then remove the block and plan again"
MPLAN="$(cd "$SMOKE_WORK/migrated" && chdf plan -input=false -no-color 2>&1)" \
  || fail "k8s-custom-resource" "the plan after the migration failed: $MPLAN"
grep -q "No changes." <<< "$MPLAN" \
  || fail "k8s-custom-resource" "the plan after the migration is not empty: $(grep -E '^Plan:|will be' <<< "$MPLAN" | head -3)"
grep -E 'No changes\.' <<< "$MPLAN" | head -1 | evidence
cp "$SMOKE_WORK/migrated/main.tf.namespace-only" "$SMOKE_WORK/migrated/main.tf"
MORPHAN="$(cd "$SMOKE_WORK/migrated" && chdf plan -input=false -no-color 2>&1)" \
  || fail "k8s-custom-resource" "the plan after removing the adopted block failed: $MORPHAN"
grep -E 'orphan_crontab|^Plan:' <<< "$MORPHAN" | head -2 | evidence
grep -q 'kubernetes_manifest.orphan_crontab_smoke-crd-stock_adopted-crontab will be destroyed' <<< "$MORPHAN" \
  || fail "k8s-custom-resource" "the sweep does not find the adopted CronTab: $(grep -E '^Plan:|will be|No changes' <<< "$MORPHAN" | head -3)"
( cd "$SMOKE_WORK/migrated" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) \
  || fail "k8s-custom-resource" "the migrated estate would not destroy"
if kc get namespace smoke-crd-stock >/dev/null 2>&1; then
  kc delete namespace smoke-crd-stock --wait=false >/dev/null 2>&1 || true
fi
proof "the migrated estate replans empty with no state file, and deleting the adopted block proposes destroying exactly that object at kubernetes_manifest.orphan_crontab_smoke-crd-stock_adopted-crontab - the sweep finds it because the migration put the label on it. That is the whole difference the label makes: without it the object plans empty too, and is invisible to the sweep, to the admission policy and to live-ls."

step "14. an estate name no Kubernetes label can hold is refused once, at the read-only run, with nothing written"
explain \
  "An estate name may be 128 characters of [a-z0-9-]; a Kubernetes label" \
  "value may be 63. So a name the bucket, the IAM policy and every AWS" \
  "tag accept can be one no object on the label surface can carry." \
  "Before #1396 the read-only run - whose whole job is to say what" \
  "-approve will do - said nothing about it, and -approve then printed" \
  "one identical FAILED line per object. The state here is two" \
  "ConfigMaps stock made, the name is 64 characters, and the run must" \
  "refuse exactly once, count the objects, print no per-object line," \
  "exit 1 and change nothing on the cluster."
label_fixture_up

cmd "choudoufu live-import -state=../stock-cm/terraform.tfstate -estate=<64 characters>   # read-only, no -approve"
LONG_BEFORE="$(cm_state)"
[ "$(grep -c 'tofu-estate=$' <<< "$LONG_BEFORE")" = "2" ] \
  || fail "k8s-custom-resource" "the ConfigMaps already carry a tofu-estate label, so this step would prove nothing: $LONG_BEFORE"
LONG_RC=0
LONG_OUT="$(cd "$SMOKE_WORK/migrated-cm" && chdf live-import -state="$SMOKE_WORK/stock-cm/terraform.tfstate" -estate="$LABEL_LONG_ESTATE" -no-color 2>"$SMOKE_WORK/long-name.stderr")" || LONG_RC=$?
LONG_ERR="$(cat "$SMOKE_WORK/long-name.stderr")"
LONG_FLAT="$(tr '\n' ' ' <<< "$LONG_ERR" | tr -s ' ')"
LONG_COUNT="$(grep -c 'Estate name cannot be written as a Kubernetes label' <<< "$LONG_ERR" || true)"
grep -E 'Estate name cannot be written as a Kubernetes label' <<< "$LONG_ERR" | head -1 | evidence || true
grep -oE '[0-9]+ resource instances? in this state|it is [0-9]+ characters long and a Kubernetes label value is capped at [0-9]+|Nothing was ratified and nothing was written' <<< "$LONG_FLAT" | evidence || true
echo "diagnostics: $LONG_COUNT, exit $LONG_RC" | evidence
[ "$LONG_RC" = "1" ] \
  || fail "k8s-custom-resource" "the read-only run under a 64-character name exited $LONG_RC, not 1: $(tail -5 <<< "$LONG_OUT$LONG_ERR")"
[ "$LONG_COUNT" = "1" ] \
  || fail "k8s-custom-resource" "expected exactly one 'Estate name cannot be written as a Kubernetes label' diagnostic, got $LONG_COUNT: $LONG_ERR"
grep -q '2 resource instances in this state carry their ownership marker as a Kubernetes label' <<< "$LONG_FLAT" \
  || fail "k8s-custom-resource" "the refusal does not count both ConfigMaps: $LONG_FLAT"
grep -qF "$LABEL_LONG_ESTATE" <<< "$LONG_FLAT" \
  || fail "k8s-custom-resource" "the refusal does not name the estate: $LONG_FLAT"
grep -q 'it is 64 characters long and a Kubernetes label value is capped at 63' <<< "$LONG_FLAT" \
  || fail "k8s-custom-resource" "the refusal does not name the clause the name broke: $LONG_FLAT"
if grep -qE 'live id:|eligible for stamping|FAILED|newly stamped' <<< "$LONG_OUT$LONG_ERR"; then
  fail "k8s-custom-resource" "the refused run still printed a per-object line or a report: $(grep -E 'live id:|eligible for stamping|FAILED|newly stamped' <<< "$LONG_OUT$LONG_ERR" | head -3)"
fi
LONG_AFTER="$(cm_state)"
echo "$LONG_AFTER" | evidence
[ "$LONG_BEFORE" = "$LONG_AFTER" ] \
  || fail "k8s-custom-resource" "the refused run changed the cluster: before: $LONG_BEFORE after: $LONG_AFTER"
kc delete configmap smoke-crd-cm-a smoke-crd-cm-b -n default >/dev/null 2>&1 || true
proof "one refusal, exit 1: \"2 resource instances in this state carry their ownership marker as a Kubernetes label\", the name, and \"64 characters long ... capped at 63\"; no per-object line and no report, and both ConfigMaps read the same resourceVersion with no label before and after. Before #1396 this run printed the report and said nothing about the name, and -approve then printed the same FAILED line once per object; BREAK=1 runs the same migration under a 60-character name and requires the report and exit 0."

echo "  What you watched: a custom resource refused by name while its CRD was"
echo "  missing, accepted by the API server's own dry run before it was"
echo "  applied, then live its whole life without a state file, found again"
echo "  each time by the apiVersion, kind, namespace and name written inside"
echo "  its manifest, carrying the one tofu-estate label the configuration"
echo "  never wrote, and found by that label once its block was gone. A custom"
echo "  resource is inside the estate the way a ConfigMap is. And one that"
echo "  stock made, with a real state file behind it, walks in through"
echo "  live-import and is inside it too - by one label, written as one"
echo "  patch the server was asked about first. And a migration under a name"
echo "  that label cannot hold is refused once, before it reads as a report"
echo "  and long before it writes."
