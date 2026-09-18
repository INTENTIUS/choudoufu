#!/usr/bin/env bash
# reference-k8s-stateful: the kubernetes lane's stateful estate (#1175,
# surface 2 of #1107, under #1016's ruling and #1067's shape), crossed on
# the kind substrate.
#
# Hand-written and named so. #1107's research pass 2 fetched and ran the
# published field for this surface and nothing cleared the bar: the
# recommended corpus-mastodon has two of its four StatefulSets commented
# out and no headless Service at all, toy-data-platform pins images that
# no longer exist, Wazuh's are deleted from Docker Hub, and of 152
# headless-Service code-search hits the roots that stay on
# hashicorp/kubernetes alone are two-to-seven-resource modules. A
# hand-written root is #1107's explicit fallback for that case, and the
# "reference-" prefix says which it is, exactly as reference-k8s does. Do
# not re-run that search; it is in #1107's second research comment.
#
# Fourteen objects over eight kinds, typed resources only, all on the _v1
# names. No kubernetes_manifest anywhere: #1174's cert-manager estate owns
# the manifest and converted-bundle route, and keeping the two
# non-overlapping is #1107's own rule for admitting an estate.
#
#   Namespace, ServiceAccount, Secret (POSTGRES_PASSWORD), ConfigMap
#   (postgres init SQL), ConfigMap (api config), headless Service +
#   StatefulSet for postgres (1 replica, postgres:17-alpine), headless
#   Service + StatefulSet for redis (2 replicas, redis:7-alpine), a
#   ClusterIP Service + Deployment that reads both, a PodDisruptionBudget,
#   and a two-instance count ConfigMap for day2_count.
#
# Deliberately NO StorageClass. kind's own `standard` (rancher.io/local-path,
# WaitForFirstConsumer, reclaim Delete) is the point; declaring one hides
# the first-consumer binding this estate exists to watch.
#
# ── what this estate is FOR ──────────────────────────────────────────────
#
# A StatefulSet's volume_claim_template produces PVCs the configuration
# never declares and no state file holds. Measured here (see day2_remove):
#
#   * they carry labels, and the labels are NOT the claim template's
#     verbatim. The StatefulSet controller merges spec.selector.matchLabels
#     OVER the template's own metadata.labels, so a key present in both is
#     the selector's. #1107's second research comment reports "the claim
#     template's labels verbatim"; its probe could not tell the two apart
#     because its template labels and its selector agreed. This estate's
#     redis claim template disagrees with its selector on purpose, and the
#     PVC shows the selector's value. That correction is the reason the
#     two label sets are written to disagree.
#   * they carry the kubernetes.io/pvc-protection finalizer;
#   * they have NO metadata.ownerReferences, because a StatefulSet's
#     default persistentVolumeClaimRetentionPolicy is Retain;
#   * their metadata.managedFields name kube-scheduler and
#     kube-controller-manager and nobody else. This line read "on kind they
#     have NO metadata.managedFields either" until #1179 was fixed, and
#     that was an artefact of the instrument: kubectl's own get strips
#     managedFields from its output unless --show-managed-fields is passed,
#     which pvc_facts and inventory below now do. The API server has them
#     and so does the dynamic client kubesweep lists through.
#
# Those are the inputs to kubesweep.ControllerMade, the one test that keeps
# a controller's label copies out of the estate sweep - "what is excluded,
# and why it is the whole safety of this", in that package's own words,
# which names "a StatefulSet's volumeClaimTemplate labels reach its PVCs"
# as the case it exists for. The first signal is genuinely absent on such a
# PVC and the package comment's claim that it is what excludes them was
# false; the second is what actually excludes them. #1179 fixed both the
# comment and the rule, because the rule as written asked for managedFields
# naming ONLY control-plane managers and one `kubectl label` - the very
# thing day2_remove's probe does to manufacture the marker - added a
# kubectl-label manager and took the PVC out of the exclusion for good.
# ControllerMade now asks which managers wrote the object's own content.
#
# The consequence is precise, and it is why an ordinary teardown has never
# caught it: destroying the WHOLE root takes the PVCs with it, because the
# Namespace is in the root and its deletion cascades (day2_teardown proves
# that, and it is a pass). Removing only the StatefulSet block - which is
# exactly the day2_remove shape - leaves both PVCs Bound, labelled and
# unowned.
#
# ── the two clusters ─────────────────────────────────────────────────────
#
#   A  the estate. Stock terraform cold-deploys it (cold_deploy), choudoufu
#      adopts it from stock's state (migrate) and runs every day-2 stage on
#      it, tears it down, then applies the same shape fresh with a live
#      block (greenfield) and compares the cluster with what stock's cold
#      deploy left.
#   B  the oracle. Stock terraform cold-deploys the identical shape and
#      applies every day-2 change itself, so each stage's "what stock does
#      for the same change" is stock's own plan on its own cluster, never a
#      state file choudoufu has moved on from.
#
# What reads differently on this substrate, per tools/gauntlet/stages.go's
# Substrates notes: identities are NAMESPACE/NAME read with kubectl; the
# marker count is `kubectl get <kind> -A -l tofu-estate=<estate>` summed
# over the estate's eight kinds; the out-of-band mutation is a kubectl patch
# or label; the rename is the moved-block half only, since live-mv has no
# Kubernetes leg (#1066). day2_replace does not apply and is recorded n/a
# by the runner, not by this script. day2_crash's own create-before-destroy
# window does not exist here either, so what this script interrupts instead
# is an apply that creates several objects (#1110, part 4): the kill lands
# between one object's create committing and the next object's, and the
# next plan has to propose exactly the remainder. Every other stage
# applies.
#
# A stage this script cannot pass records fail with the reason and the run
# continues to the next; the runner, not the script, decides what "clear"
# means. The one exception is a fault in the substrate itself (a cluster
# that never came up), which is a fail on the stage being set up and an
# exit.
#
#   go run ./tools/gauntlet run reference-k8s-stateful   # one estate, local kind: no allow file
#   bash live/e2e/reference-k8s-stateful/run.sh
#
# Needs kind, kubectl, terraform (the stock binary) and Docker on PATH.
#
# Env overrides:
#   TOFU_BIN       path to a prebuilt choudoufu binary; skips the `go build`.
#   BREAK          set to 1 to run drift_reconverge's, day2_rename's and
#                  greenfield's negative controls instead of the real
#                  checks: a second object is tampered and the
#                  single-object assertion must fail; the ServiceAccount's
#                  own metadata.name is changed (a block rename without a
#                  moved block is zero churn on Kubernetes, because the
#                  block name is not part of the object's identity) and the
#                  zero-churn assertion must fail; the redis StatefulSet is
#                  dropped from greenfield's expected inventory and the
#                  object-by-object match must fail.
#   BREAK_REMOVE   set to 1 to keep the redis StatefulSet block and assert
#                  no destroy is proposed (day2_remove's own Break line).
#   BREAK_PVC      set to 1 to run day2_remove's PVC probe WITHOUT putting
#                  the estate label on the orphaned PVC and still expect
#                  the sweep to propose it; must fail, which is what proves
#                  the probe keys on the label and not on the kind.
#   BREAK_COUNT    set to 1 to assert the wrong instance was destroyed on
#                  the scale-down (day2_count's Break line); must fail.
#   BREAK_APPROVAL set to 1 to apply the saved plan after the world moved
#                  and expect success (plan_approval's Break line); must
#                  fail.
#   BREAK_CRASH    set to 1 to assert, after the same real interrupt, that
#                  nothing is proposed (day2_crash's own Break line); must
#                  fail, because a recovered run proposes the remainder.
#   BREAK_CRASH_UNBOUND
#                  set to 1 to strip the tofu-estate label off the object
#                  the interrupted apply did create before replanning - the
#                  unrecovered run that stage exists to catch. The real
#                  check must then fail to hold.
#   BREAK_STRICT   set to 1 to turn secrets back to "store" and require the
#                  refusal to vanish (strict's Break line).
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
source "$ROOT/live/e2e/lib/gauntlet.sh"
ESTATE="reference-k8s-stateful"
NS="refk8sst"
KINDS="namespaces serviceaccounts secrets configmaps services statefulsets deployments poddisruptionbudgets"
WORK="$(mktemp -d)"
STOCK="$WORK/stock"; ADOPTED="$WORK/adopted"; ORACLE="$WORK/oracle"; GREEN="$WORK/green"
KCA="$WORK/a.kubeconfig"; KCB="$WORK/b.kubeconfig"
CLUSTER_A="chdf-refk8sst-a-$$"; CLUSTER_B="chdf-refk8sst-b-$$"
export TF_PLUGIN_CACHE_DIR="$WORK/plugin-cache"; mkdir -p "$TF_PLUGIN_CACHE_DIR"
export TF_IN_AUTOMATION=1
log() { printf '%s\n' "$*"; }

CURRENT_STAGE=""
fail() {
  printf 'FAIL: %s\n' "$*" >&2
  if [ -n "$CURRENT_STAGE" ]; then gauntlet_stage "$CURRENT_STAGE" fail "$*"; fi
  exit 1
}
cleanup() {
  gauntlet_kind_down "$CLUSTER_A"
  gauntlet_kind_down "$CLUSTER_B"
  rm -rf "$WORK"
}
trap cleanup EXIT
gauntlet_begin

# ── 0. tools ─────────────────────────────────────────────────────────────
log "=== 0. tools ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v terraform >/dev/null 2>&1 || fail "the terraform binary is not on PATH - needed as the stock oracle"
command -v kind >/dev/null 2>&1 || fail "kind is not on PATH (brew install kind)"
command -v kubectl >/dev/null 2>&1 || fail "kubectl is not on PATH"
command -v python3 >/dev/null 2>&1 || fail "python3 is not on PATH"

if [ -n "${TOFU_BIN:-}" ]; then
  TOFU="$TOFU_BIN"
  [ -x "$TOFU" ] || fail "TOFU_BIN=$TOFU_BIN is not an executable file"
  log "  using TOFU_BIN=$TOFU"
else
  mkdir -p "$WORK/bin"
  TOFU="$WORK/bin/choudoufu"
  ( cd "$ROOT" && env -u PWD go build -o "$TOFU" ./cmd/choudoufu ) || fail "go build ./cmd/choudoufu failed"
  log "  built $TOFU"
fi

# day2_crash needs a build with e2eTestingFeatures set, the same ldflags
# gate TOFU_E2E_APPLY_RESOURCE_PANIC sits behind, so the engine's own
# TOFU_E2E_APPLY_RESOURCE_INTERRUPT hook (internal/command/
# apply_e2etesting_crash.go) is reachable. Built from THIS tree's source
# whatever $TOFU came from, and unconditionally, since the real check and
# the Break controls all need it; identical to $TOFU everywhere else,
# because the hook is a no-op unless the variable names an applied address.
mkdir -p "$WORK/bin"
TOFU_CRASH="$WORK/bin/choudoufu-e2e"
( cd "$ROOT" && env -u PWD go build -ldflags="-X 'main.e2eTestingFeatures=yes'" -o "$TOFU_CRASH" ./cmd/choudoufu ) \
  || fail "go build -ldflags e2eTestingFeatures ./cmd/choudoufu failed"
log "  built $TOFU_CRASH (e2eTestingFeatures=yes, for day2_crash's interrupt)"

# ── the shape ────────────────────────────────────────────────────────────
versions_block() { # $1 = "live" to include the live block, anything else for stock
  cat <<EOF
terraform {
  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "= 3.2.1"
    }
  }
EOF
  if [ "$1" = "live" ]; then cat <<EOF
  live {
    estate = "$ESTATE"
    record_store "local" {
      path = ".tofu-records"
    }
  }
EOF
  fi
  cat <<'EOF'
}

provider "kubernetes" {}
EOF
}

# resource_block writes the fourteen objects.
#   $1  the ServiceAccount's block name (app, then team after the rename)
#   $2  the shard ConfigMap's count
#   $3  an extra data line for api-config (plan_approval's reviewed = "yes")
#   $4  "drop_redis_sts" to leave the redis StatefulSet block out (day2_remove)
#
# NOTHING here carries a tofu-estate label. cold_deploy asserts that a plain
# stock apply leaves zero labelled objects, which is the whole source of
# genuinely unmarked infrastructure for migrate; a marker written into the
# configuration - into a claim template above all - would destroy that.
resource_block() {
  local sa="${1:-app}" shards="${2:-2}" extra="${3:-}" drop="${4:-}"
  cat <<EOF
resource "kubernetes_namespace_v1" "app" {
  metadata {
    name = "$NS"
  }
}

resource "kubernetes_service_account_v1" "$sa" {
  metadata {
    name      = "app"
    namespace = "$NS"
  }
  depends_on = [kubernetes_namespace_v1.app]
}

resource "kubernetes_secret_v1" "postgres" {
  metadata {
    name      = "postgres"
    namespace = "$NS"
  }
  data = {
    POSTGRES_PASSWORD = "reference-only-not-a-credential"
  }
  type       = "Opaque"
  depends_on = [kubernetes_namespace_v1.app]
}

resource "kubernetes_config_map_v1" "initsql" {
  metadata {
    name      = "postgres-init"
    namespace = "$NS"
  }
  data = {
    "00-schema.sql" = "CREATE TABLE IF NOT EXISTS reference (id integer PRIMARY KEY);\n"
  }
  depends_on = [kubernetes_namespace_v1.app]
}

resource "kubernetes_config_map_v1" "api" {
  metadata {
    name      = "api-config"
    namespace = "$NS"
  }
  data = {
    postgres_host = "postgres.$NS.svc.cluster.local"
    redis_host    = "redis.$NS.svc.cluster.local"
$extra
  }
  depends_on = [kubernetes_namespace_v1.app]
}

# Headless: cluster_ip = "None" is what gives a StatefulSet's pods their
# stable per-ordinal DNS names, and it is the shape the published field
# never had (#1107: zero of mastodon's Services set it).
resource "kubernetes_service_v1" "postgres" {
  metadata {
    name      = "postgres"
    namespace = "$NS"
    labels = {
      app = "postgres"
    }
  }
  spec {
    cluster_ip = "None"
    selector = {
      app = "postgres"
    }
    port {
      name = "postgres"
      port = 5432
    }
  }
  depends_on = [kubernetes_namespace_v1.app]
}

# The claim template's labels AGREE with the selector here, so that the
# redis StatefulSet below is the only place the two disagree and the
# merge is unambiguous about which one produced it.
resource "kubernetes_stateful_set_v1" "postgres" {
  metadata {
    name      = "postgres"
    namespace = "$NS"
    labels = {
      app = "postgres"
    }
  }
  spec {
    service_name = "postgres"
    replicas     = 1
    selector {
      match_labels = {
        app = "postgres"
      }
    }
    template {
      metadata {
        labels = {
          app = "postgres"
        }
      }
      spec {
        container {
          name  = "postgres"
          image = "postgres:17-alpine"
          port {
            name           = "postgres"
            container_port = 5432
          }
          env {
            name  = "PGDATA"
            value = "/var/lib/postgresql/data/pgdata"
          }
          env {
            name = "POSTGRES_PASSWORD"
            value_from {
              secret_key_ref {
                name = "postgres"
                key  = "POSTGRES_PASSWORD"
              }
            }
          }
          volume_mount {
            name       = "data"
            mount_path = "/var/lib/postgresql/data"
          }
          volume_mount {
            name       = "init"
            mount_path = "/docker-entrypoint-initdb.d"
          }
        }
        volume {
          name = "init"
          config_map {
            name = "postgres-init"
          }
        }
      }
    }
    volume_claim_template {
      metadata {
        name = "data"
        labels = {
          app  = "postgres"
          tier = "storage"
        }
      }
      spec {
        access_modes = ["ReadWriteOnce"]
        resources {
          requests = {
            storage = "64Mi"
          }
        }
      }
    }
  }
  depends_on = [kubernetes_service_v1.postgres, kubernetes_secret_v1.postgres, kubernetes_config_map_v1.initsql]
}

resource "kubernetes_service_v1" "redis" {
  metadata {
    name      = "redis"
    namespace = "$NS"
    labels = {
      app = "redis"
    }
  }
  spec {
    cluster_ip = "None"
    selector = {
      app = "redis"
    }
    port {
      name = "redis"
      port = 6379
    }
  }
  depends_on = [kubernetes_namespace_v1.app]
}
EOF
  if [ "$drop" != "drop_redis_sts" ]; then cat <<EOF

# The claim template's labels DISAGREE with the selector on purpose:
# app is "redis-data" here and "redis" in spec.selector.match_labels, and
# role exists only here. The StatefulSet controller merges the selector
# over the template, so the PVCs read app=redis (the selector's),
# tier=storage and role=cache-volume (the template's). day2_remove asserts
# exactly that, which is the correction to #1107's "the claim template's
# labels verbatim" - its probe had the two agreeing and could not tell.
resource "kubernetes_stateful_set_v1" "redis" {
  metadata {
    name      = "redis"
    namespace = "$NS"
    labels = {
      app = "redis"
    }
  }
  spec {
    service_name = "redis"
    replicas     = 2
    selector {
      match_labels = {
        app = "redis"
      }
    }
    template {
      metadata {
        labels = {
          app = "redis"
        }
      }
      spec {
        container {
          name  = "redis"
          image = "redis:7-alpine"
          args  = ["--save", "", "--appendonly", "no"]
          port {
            name           = "redis"
            container_port = 6379
          }
          volume_mount {
            name       = "data"
            mount_path = "/data"
          }
        }
      }
    }
    volume_claim_template {
      metadata {
        name = "data"
        labels = {
          app  = "redis-data"
          tier = "storage"
          role = "cache-volume"
        }
      }
      spec {
        access_modes = ["ReadWriteOnce"]
        resources {
          requests = {
            storage = "64Mi"
          }
        }
      }
    }
  }
  depends_on = [kubernetes_service_v1.redis]
}
EOF
  fi
  cat <<EOF

resource "kubernetes_service_v1" "api" {
  metadata {
    name      = "api"
    namespace = "$NS"
    labels = {
      app = "api"
    }
  }
  spec {
    selector = {
      app = "api"
    }
    port {
      name        = "http"
      port        = 80
      target_port = 8080
    }
  }
  depends_on = [kubernetes_namespace_v1.app]
}

# Not only StatefulSets: an ordinary Deployment reading both backends, so
# the root has a stateless half whose own controller-made copies
# (ReplicaSet, Pod) DO carry ownerReferences and are excluded from the
# sweep - the contrast day2_remove's PVC probe is measured against.
resource "kubernetes_deployment_v1" "api" {
  metadata {
    name      = "api"
    namespace = "$NS"
    labels = {
      app = "api"
    }
  }
  spec {
    replicas = 1
    selector {
      match_labels = {
        app = "api"
      }
    }
    template {
      metadata {
        labels = {
          app = "api"
        }
      }
      spec {
        container {
          name    = "api"
          image   = "redis:7-alpine"
          command = ["sh", "-c", "while true; do sleep 3600; done"]
          env {
            name = "POSTGRES_HOST"
            value_from {
              config_map_key_ref {
                name = "api-config"
                key  = "postgres_host"
              }
            }
          }
          env {
            name = "REDIS_HOST"
            value_from {
              config_map_key_ref {
                name = "api-config"
                key  = "redis_host"
              }
            }
          }
          env {
            name = "POSTGRES_PASSWORD"
            value_from {
              secret_key_ref {
                name = "postgres"
                key  = "POSTGRES_PASSWORD"
              }
            }
          }
        }
      }
    }
  }
  wait_for_rollout = false
  depends_on       = [kubernetes_config_map_v1.api, kubernetes_secret_v1.postgres]
}

resource "kubernetes_pod_disruption_budget_v1" "redis" {
  metadata {
    name      = "redis"
    namespace = "$NS"
    labels = {
      app = "redis"
    }
  }
  spec {
    max_unavailable = "1"
    selector {
      match_labels = {
        app = "redis"
      }
    }
  }
  depends_on = [kubernetes_namespace_v1.app]
}

resource "kubernetes_config_map_v1" "shard" {
  count = $shards
  metadata {
    name      = "shard-\${count.index}"
    namespace = "$NS"
  }
  data = {
    shard = tostring(count.index)
  }
  depends_on = [kubernetes_namespace_v1.app]
}
EOF
}
write_config() { # $1 dir, $2 live|stock, then resource_block's args
  local dir="$1" mode="$2"; shift 2
  { versions_block "$mode"; echo; resource_block "$@"; } > "$dir/main.tf"
}

# ── cluster helpers ──────────────────────────────────────────────────────
kca() { kubectl --kubeconfig "$KCA" "$@"; }
kcb() { kubectl --kubeconfig "$KCB" "$@"; }
# stock_b runs the stock binary in the oracle root against cluster B.
stock_b() { ( cd "$ORACLE" && KUBECONFIG="$KCB" KUBE_CONFIG_PATH="$KCB" terraform "$@" ); }
# count_a is the marker count on cluster A, the tagging index's equivalent.
# PVCs are deliberately NOT in KINDS: nothing declares one, so a PVC
# carrying the estate's label would be a finding, not a member of the
# count, and day2_remove asserts on it by name instead.
count_a() { KUBECONFIG="$KCA" gauntlet_kind_count "$ESTATE" $KINDS; }
# pvc_count_a is how many PVCs carry the estate label on cluster A - zero
# at every stage of a correct run.
pvc_count_a() { KUBECONFIG="$KCA" gauntlet_kind_count "$ESTATE" persistentvolumeclaims; }
# pvc_facts prints one normalised line per PVC in the namespace on the
# cluster $1 names: name, labels, whether ownerReferences is present, the
# managedFields managers and which of them wrote the PVC's spec, finalizers,
# phase. This is the measurement the estate exists to make, so it is read
# from the server and printed rather than summarised.
#
# --show-managed-fields is load-bearing (#1179). Without it kubectl strips
# managedFields from its own output, and this function reported
# "managedFields=0" for three PVCs that carry three entries each - which is
# the reading #1179 was filed on. The spec writer is printed separately
# because that, not the manager list, is what kubesweep.ControllerMade
# judges: a `kubectl label` adds a manager without writing any spec.
pvc_facts() {
  KUBECONFIG="$1" NS="$NS" python3 - <<'PY'
import json, os, subprocess
ns = os.environ["NS"]
out = subprocess.run(["kubectl", "get", "pvc", "-n", ns, "-o", "json", "--show-managed-fields"], capture_output=True, text=True)
if out.returncode != 0:
    print("ERROR: " + out.stderr.strip()); raise SystemExit(1)
items = json.loads(out.stdout).get("items", [])
for o in sorted(items, key=lambda x: x["metadata"]["name"]):
    m = o["metadata"]
    labels = ",".join("%s=%s" % kv for kv in sorted((m.get("labels") or {}).items()))
    entries = m.get("managedFields") or []
    managers = ",".join(sorted({e.get("manager", "?") for e in entries})) or "none"
    specw = sorted({e.get("manager", "?") for e in entries
                    if not e.get("subresource")
                    and any(k not in ("f:metadata", "f:status", "f:apiVersion", "f:kind")
                            for k in (e.get("fieldsV1") or {}))})
    print("%s labels=[%s] ownerReferences=%s managers=[%s] specWrittenBy=[%s] finalizers=%s storageClass=%s phase=%s" % (
        m["name"], labels,
        "present" if m.get("ownerReferences") else "absent",
        managers, ",".join(specw) or "none",
        ",".join(m.get("finalizers") or []) or "none",
        o["spec"].get("storageClassName"), o.get("status", {}).get("phase")))
PY
}
# inventory prints the estate's objects on the cluster $1 names, normalised:
# what the configuration declares and nothing the server set, labels
# included, so stock's cold deploy and choudoufu's greenfield apply compare
# object by object. The controller-made PVCs are in it too, because "the
# same objects stock's cold deploy produced" has to include what the
# StatefulSets' claim templates produced. $2 is a key to drop (BREAK's
# control).
inventory() {
  local cfg="$1" drop="${2:-}"
  KUBECONFIG="$cfg" DROP="$drop" NS="$NS" python3 - <<'PY'
import json, os, subprocess
ns = os.environ["NS"]; drop = os.environ.get("DROP", "")
def get(kind, name):
    out = subprocess.run(["kubectl", "get", kind, name, "-n", ns, "-o", "json"], capture_output=True, text=True)
    if out.returncode != 0:
        return None
    return json.loads(out.stdout)
def containers(spec):
    return [{"name": c["name"], "image": c["image"], "command": c.get("command"), "args": c.get("args"),
             "env": sorted([e["name"] for e in c.get("env", [])]),
             "mounts": sorted([m["mountPath"] for m in c.get("volumeMounts", []) if not m["mountPath"].startswith("/var/run/secrets")])}
            for c in spec["containers"]]
inv = {}
nsobj = subprocess.run(["kubectl", "get", "namespace", ns, "-o", "json"], capture_output=True, text=True)
inv["namespace/" + ns] = {"exists": nsobj.returncode == 0}
for name in ["postgres-init", "api-config", "shard-0", "shard-1"]:
    o = get("configmap", name)
    inv["configmap/" + name] = None if o is None else {"data": o.get("data", {})}
o = get("serviceaccount", "app")
inv["serviceaccount/app"] = None if o is None else {"exists": True}
o = get("secret", "postgres")
inv["secret/postgres"] = None if o is None else {"type": o.get("type"), "keys": sorted((o.get("data") or {}).keys()),
                                                 "data": o.get("data", {})}
for name in ["postgres", "redis", "api"]:
    o = get("service", name)
    if o is None:
        inv["service/" + name] = None; continue
    cip = o["spec"].get("clusterIP")
    inv["service/" + name] = {
        "type": o["spec"].get("type"),
        # a headless Service reads "None"; an allocated address differs per
        # cluster and per run, so only its headless-ness is comparable.
        "clusterIP": "None" if cip == "None" else "<allocated>",
        "selector": o["spec"].get("selector"),
        "ports": [{"name": p.get("name"), "port": p.get("port"), "targetPort": p.get("targetPort"), "protocol": p.get("protocol")} for p in o["spec"].get("ports", [])]}
for name in ["postgres", "redis"]:
    o = get("statefulset", name)
    if o is None:
        inv["statefulset/" + name] = None; continue
    sp = o["spec"]
    inv["statefulset/" + name] = {
        "replicas": sp.get("replicas"), "serviceName": sp.get("serviceName"),
        "selector": sp.get("selector", {}).get("matchLabels"),
        "podLabels": sp["template"]["metadata"].get("labels"),
        "containers": containers(sp["template"]["spec"]),
        "claimTemplates": [{"name": t["metadata"]["name"], "labels": t["metadata"].get("labels"),
                            "accessModes": t["spec"].get("accessModes"),
                            "storage": t["spec"].get("resources", {}).get("requests", {}).get("storage"),
                            "storageClassName": t["spec"].get("storageClassName")}
                           for t in sp.get("volumeClaimTemplates", [])],
        "retentionPolicy": sp.get("persistentVolumeClaimRetentionPolicy")}
o = get("deployment", "api")
inv["deployment/api"] = None if o is None else {
    "replicas": o["spec"].get("replicas"), "selector": o["spec"].get("selector", {}).get("matchLabels"),
    "containers": containers(o["spec"]["template"]["spec"])}
o = get("poddisruptionbudget", "redis")
inv["poddisruptionbudget/redis"] = None if o is None else {
    "maxUnavailable": o["spec"].get("maxUnavailable"), "minAvailable": o["spec"].get("minAvailable"),
    "selector": o["spec"].get("selector", {}).get("matchLabels")}
# --show-managed-fields for the same reason pvc_facts passes it (#1179):
# without it kubectl hides what this line claims to compare. The managers
# are compared as a set, not a count, because the count can pick up a
# transient entry the two clusters need not have written in the same order.
pvcs = subprocess.run(["kubectl", "get", "pvc", "-n", ns, "-o", "json", "--show-managed-fields"], capture_output=True, text=True)
if pvcs.returncode == 0:
    for o in json.loads(pvcs.stdout).get("items", []):
        m = o["metadata"]
        inv["persistentvolumeclaim/" + m["name"]] = {
            "labels": m.get("labels"),
            "ownerReferences": "present" if m.get("ownerReferences") else "absent",
            "managedFields": sorted({e.get("manager", "?") for e in (m.get("managedFields") or [])}),
            "finalizers": m.get("finalizers"),
            "accessModes": o["spec"].get("accessModes"),
            "storageClassName": o["spec"].get("storageClassName"),
            "storage": o["spec"].get("resources", {}).get("requests", {}).get("storage"),
            "phase": o.get("status", {}).get("phase")}
if drop:
    inv.pop(drop, None)
print(json.dumps(inv, sort_keys=True, indent=1))
PY
}
exists_a() { kca get "$1" "$2" -n "$NS" >/dev/null 2>&1; }
plan_line() { grep -E '^Plan:|^No changes' <<< "$1" | head -1 | sed 's/\.$//'; }

# ── 1. cold_deploy: stock stands the estate up on A (and B, the oracle) ──
gauntlet_begin_stage cold_deploy
log "=== 1. cold_deploy: two kind clusters, stock terraform applies the shape on each ==="
gauntlet_kind_up "$CLUSTER_A" "$KCA" || fail "kind cluster A ($CLUSTER_A) did not come up"
gauntlet_kind_up "$CLUSTER_B" "$KCB" || fail "kind cluster B ($CLUSTER_B) did not come up"
export KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA"
log "  cluster A: $(kca version 2>/dev/null | grep -i server | head -1); cluster B: $CLUSTER_B"
mkdir -p "$STOCK" "$ORACLE"
write_config "$STOCK" stock
write_config "$ORACLE" stock
( cd "$STOCK" && terraform init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed on A"
COLD_OUT="$(cd "$STOCK" && terraform apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$COLD_OUT" | tail -30; fail "stock cold deploy failed on A"; }
grep -qF "Apply complete! Resources: 14 added, 0 changed, 0 destroyed" <<< "$COLD_OUT" || { printf '%s\n' "$COLD_OUT" | tail -5; fail "stock cold deploy did not add exactly 14 objects on A"; }
[ -f "$STOCK/terraform.tfstate" ] || fail "stock left no terraform.tfstate on A"
STOCK_N="$(cd "$STOCK" && terraform state list | wc -l | tr -d ' ')"
[ "$STOCK_N" = "14" ] || fail "stock's state holds $STOCK_N instances, want 14"
UNMARKED="$(count_a)"
[ "$UNMARKED" = "0" ] || fail "$UNMARKED object(s) already carry tofu-estate=$ESTATE after a plain stock apply - this proves nothing"
[ "$(pvc_count_a)" = "0" ] || fail "a PVC already carries tofu-estate=$ESTATE after a plain stock apply; the configuration must declare no marker anywhere"
# The three PVCs nobody declared. Wait for them to bind: kind's standard
# class is WaitForFirstConsumer, so binding happens when each pod is
# scheduled, which stock's wait_for_rollout already waited for.
PVC_N="$(kca get pvc -n "$NS" -o name 2>/dev/null | wc -l | tr -d ' ')"
[ "$PVC_N" = "3" ] || { kca get pvc -n "$NS"; fail "$PVC_N PVC(s) in $NS after the cold deploy, want 3 (data-postgres-0, data-redis-0, data-redis-1)"; }
BOUND_N="$(kca get pvc -n "$NS" -o jsonpath='{range .items[*]}{.status.phase}{"\n"}{end}' | grep -c '^Bound$')"
[ "$BOUND_N" = "3" ] || { kca get pvc -n "$NS"; fail "$BOUND_N of 3 PVCs are Bound after the cold deploy"; }
STATE_PVC="$(cd "$STOCK" && terraform state list | grep -c persistent_volume_claim || true)"
[ "$STATE_PVC" = "0" ] || fail "stock's state holds $STATE_PVC persistent_volume_claim instance(s); the claim templates' PVCs must be in nobody's state"
log "  PVC facts on A after stock's cold deploy:"
pvc_facts "$KCA" | sed 's/^/    /'
inventory "$KCA" > "$WORK/inventory.stock.json" || fail "could not read the cold-deployed inventory on A"
( stock_b init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed on B"
( stock_b apply -auto-approve -input=false -no-color 2>&1 | grep -qF "Apply complete! Resources: 14 added" ) || fail "stock cold deploy failed on B"
gauntlet_stage cold_deploy pass "14 objects over eight kinds (namespace, ServiceAccount, Secret, 4 ConfigMaps, 2 headless Services and a ClusterIP one, 2 StatefulSets, a Deployment, a PodDisruptionBudget) from plain terraform against kind $(kca version 2>/dev/null | grep -io 'v1\.[0-9.]*' | head -1), a real terraform.tfstate with 14 instances, zero tofu-estate labels read back with kubectl. The two volume_claim_templates produced 3 PVCs nobody declared, all Bound on kind's own standard class (rancher.io/local-path, WaitForFirstConsumer, reclaim Delete) with no StorageClass in the root, and stock's state holds none of them; the identical shape cold-deployed by stock on a second cluster as every later stage's oracle"

# ── 2. migrate: choudoufu live-import against stock's state ──────────────
gauntlet_begin_stage migrate
log "=== 2. migrate: live-import against the stock state file, read-only then -approve ==="
mkdir -p "$ADOPTED"
write_config "$ADOPTED" live
( cd "$ADOPTED" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "adopted init failed"
IMPORT_OUT="$(cd "$ADOPTED" && "$TOFU" live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -no-color 2>&1)" || { printf '%s\n' "$IMPORT_OUT" | tail -30; fail "live-import (dry run) failed"; }
ELIGIBLE_LINE="$(grep -E 'resource instance\(s\) are eligible for stamping' <<< "$IMPORT_OUT" | head -1)"
log "  dry run: ${ELIGIBLE_LINE:-no eligibility line}"
APPROVE_OUT="$(cd "$ADOPTED" && "$TOFU" live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)" || { printf '%s\n' "$APPROVE_OUT" | tail -30; fail "live-import -approve failed"; }
SUMMARY_LINE="$(grep -E 'resource\(s\) newly stamped' <<< "$APPROVE_OUT" | head -1 | sed 's/\.$//')"
log "  approve: ${SUMMARY_LINE:-no summary line}"
LABELLED="$(count_a)"
if grep -qF "14 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 0 skipped" <<< "$APPROVE_OUT" && [ "$LABELLED" = "14" ]; then
  [ "$(pvc_count_a)" = "0" ] || fail "live-import labelled a PVC; nothing in the configuration declares one"
  gauntlet_stage migrate pass "14 of 14 stamped, 0 failed, 0 skipped ($SUMMARY_LINE); every object carries tofu-estate=$ESTATE, read back with kubectl over eight kinds, and none of the three controller-created PVCs does"
else
  gauntlet_stage migrate fail "live-import -approve did not stamp all 14: ${SUMMARY_LINE:-no summary line}; kubectl counts $LABELLED labelled object(s) over the estate's eight kinds"
fi

# ── 3. test_plan: replan from nothing ────────────────────────────────────
gauntlet_begin_stage test_plan
log "=== 3. test_plan: choudoufu plan with no state file, identities read with kubectl ==="
PLAN_OUT="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$PLAN_OUT" | tail -30; fail "the post-migration plan failed"; }
IDS_OK=1; MISSING=""
for spec in "namespace $NS" "serviceaccount app" "secret postgres" "configmap postgres-init" "configmap api-config" "configmap shard-0" "configmap shard-1" "service postgres" "service redis" "service api" "statefulset postgres" "statefulset redis" "deployment api" "poddisruptionbudget redis"; do
  read -r kind name <<< "$spec"
  if [ "$kind" = "namespace" ]; then kca get namespace "$name" >/dev/null 2>&1 || { IDS_OK=0; MISSING="$MISSING $kind/$name"; }
  else exists_a "$kind" "$name" || { IDS_OK=0; MISSING="$MISSING $NS/$name"; }; fi
done
if grep -q "No changes." <<< "$PLAN_OUT" && [ "$IDS_OK" = "1" ]; then
  gauntlet_stage test_plan pass "the plan with no state file is empty; all 14 identities (NAMESPACE/NAME) confirmed present with kubectl across eight kinds"
else
  gauntlet_stage test_plan fail "the plan with no state file is not empty: $(plan_line "$PLAN_OUT"); identities confirmed with kubectl: $IDS_OK (missing:${MISSING:- none})"
  log "  adopting through choudoufu's own apply so the day-2 stages below run on a labelled estate"
  ADOPT_OUT="$(cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$ADOPT_OUT" | tail -30; fail "the adopting apply failed"; }
  REPLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || fail "the replan after the adopting apply failed"
  grep -q "No changes." <<< "$REPLAN" || { printf '%s\n' "$REPLAN" | tail -30; fail "the replan after the adopting apply is not empty; nothing below would measure day-2 behaviour"; }
fi
[ "$(count_a)" = "14" ] || fail "$(count_a) object(s) carry tofu-estate=$ESTATE after adoption, want 14"

# ── 4. test_apply: no-op apply, marker count unchanged ───────────────────
gauntlet_begin_stage test_apply
log "=== 4. test_apply: apply the empty plan; the labelled-object count must not move ==="
BEFORE_N="$(count_a)"
NOOP_OUT="$(cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$NOOP_OUT" | tail -30; fail "the no-op apply failed"; }
grep -qF "Apply complete! Resources: 0 added, 0 changed, 0 destroyed" <<< "$NOOP_OUT" || { printf '%s\n' "$NOOP_OUT" | tail -5; fail "the no-op apply changed something"; }
AFTER_N="$(count_a)"
[ "$BEFORE_N" = "$AFTER_N" ] || fail "labelled-object count moved across a no-op apply: $BEFORE_N -> $AFTER_N"
[ "$(pvc_count_a)" = "0" ] || fail "a PVC gained tofu-estate=$ESTATE across a no-op apply"
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); objects carrying tofu-estate=$ESTATE unchanged at $BEFORE_N across the estate's eight kinds, and still zero of the three PVCs, counted with kubectl"

# ── 5. drift_reconverge: one object tampered out of band ─────────────────
gauntlet_begin_stage drift_reconverge
log "=== 5. drift_reconverge: kubectl patch api-config on A and on B; stock's plan on B is the oracle ==="
kca patch configmap api-config -n "$NS" --type merge -p '{"data":{"redis_host":"tampered"}}' >/dev/null || fail "could not tamper api-config on A"
kcb patch configmap api-config -n "$NS" --type merge -p '{"data":{"redis_host":"tampered"}}' >/dev/null || fail "could not tamper api-config on B"
if [ "${BREAK:-}" = "1" ]; then
  kca patch configmap shard-0 -n "$NS" --type merge -p '{"data":{"shard":"tampered"}}' >/dev/null || fail "BREAK: could not tamper shard-0 on A"
fi
ORACLE_PLAN="$(stock_b plan -detailed-exitcode -input=false -no-color 2>&1)"; ORACLE_RC=$?
[ "$ORACLE_RC" -eq 2 ] || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B after the tamper exited $ORACLE_RC, want 2 (changes)"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$ORACLE_PLAN" || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B does not propose exactly one change"; }
grep -q "kubernetes_config_map_v1.api" <<< "$ORACLE_PLAN" || fail "stock's plan on B does not name kubernetes_config_map_v1.api"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not reconverge B"
DRIFT_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$DRIFT_PLAN" | tail -30; fail "the plan after the tamper failed"; }
if [ "${BREAK:-}" = "1" ]; then
  if grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$DRIFT_PLAN"; then
    fail "BREAK=1: two objects were tampered but the plan still proposes exactly one change - the single-object assertion is not load-bearing"
  fi
  log "  BREAK=1: caught - with a second object tampered the plan is $(plan_line "$DRIFT_PLAN"), not one change; the real check below is skipped"
  ( cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: could not reconverge A"
  gauntlet_stage drift_reconverge pass "BREAK=1 control: with two objects tampered the single-object assertion correctly fails to hold ($(plan_line "$DRIFT_PLAN")); reconverged afterwards"
else
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$DRIFT_PLAN" || { printf '%s\n' "$DRIFT_PLAN" | tail -30; fail "the plan after one tamper does not propose exactly one change"; }
  grep -q "kubernetes_config_map_v1.api" <<< "$DRIFT_PLAN" || fail "the plan does not name kubernetes_config_map_v1.api"
  grep -q "shard" <<< "$DRIFT_PLAN" && fail "the plan touches a shard ConfigMap nobody tampered"
  grep -q "persistent_volume_claim" <<< "$DRIFT_PLAN" && fail "the plan touches a PVC nobody tampered or declared"
  RECONV="$(cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$RECONV" | tail -30; fail "the reconverging apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$RECONV" || fail "the reconverging apply did not change exactly one object"
  HOST="$(kca get configmap api-config -n "$NS" -o jsonpath='{.data.redis_host}')"
  [ "$HOST" = "redis.$NS.svc.cluster.local" ] || fail "api-config's redis_host reads $HOST after reconverging, want redis.$NS.svc.cluster.local"
  gauntlet_stage drift_reconverge pass "one ConfigMap tampered with kubectl patch; choudoufu proposed exactly kubernetes_config_map_v1.api (0 add, 1 change, 0 destroy), matching stock's own plan on the oracle cluster for the same tamper; apply changed 1 and the value reads back as configured. Neither the shard ConfigMaps nor any of the three undeclared PVCs was proposed. BREAK=1 tampers a second object and the single-object assertion correctly fails"
fi

# ── 6. plan_approval: plan -out, the world moves, apply refuses ──────────
gauntlet_begin_stage plan_approval
log "=== 6. plan_approval: a saved plan, an out-of-band label, a refusal; then the same file applies once the world is back ==="
write_config "$ADOPTED" live app 2 '    reviewed = "yes"'
write_config "$ORACLE" stock app 2 '    reviewed = "yes"'
P_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -out=approved.tfplan -input=false -no-color 2>&1)" || { printf '%s\n' "$P_PLAN" | tail -30; fail "plan -out failed"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$P_PLAN" || { printf '%s\n' "$P_PLAN" | tail -10; fail "the saved plan is not exactly one change"; }
kca label configmap shard-0 -n "$NS" stray=yes >/dev/null || fail "could not move the world (label shard-0) on A"
P_APPLY="$(cd "$ADOPTED" && "$TOFU" apply -input=false -no-color approved.tfplan 2>&1)"; P_RC=$?
if [ "${BREAK_APPROVAL:-}" = "1" ]; then
  [ "$P_RC" -eq 0 ] && fail "BREAK_APPROVAL=1: applying the saved plan after the world moved succeeded - the refusal is not load-bearing"
  log "  BREAK_APPROVAL=1: caught - the apply after the world moved exited $P_RC, so 'expect success' correctly fails"
  kca label configmap shard-0 -n "$NS" stray- >/dev/null
  ( cd "$ADOPTED" && "$TOFU" apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: the saved plan did not apply once the world was put back"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: stock could not apply the reviewed change on B"
  gauntlet_stage plan_approval pass "BREAK_APPROVAL=1 control: applying the saved plan after the world moved exited $P_RC (refused), so the stage's own Break line correctly fails; applied once the world was put back"
else
  [ "$P_RC" -eq 3 ] || { printf '%s\n' "$P_APPLY" | tail -30; fail "apply of the saved plan after the world moved exited $P_RC, want 3 (the refusal)"; }
  grep -qF "The approved plan no longer matches the live system" <<< "$P_APPLY" || { printf '%s\n' "$P_APPLY" | tail -30; fail "the refusal does not carry its documented sentence"; }
  REVIEWED="$(kca get configmap api-config -n "$NS" -o jsonpath='{.data.reviewed}')"
  [ -z "$REVIEWED" ] || fail "api-config gained reviewed=$REVIEWED despite the refusal"
  kca label configmap shard-0 -n "$NS" stray- >/dev/null || fail "could not put the world back"
  P_APPLY2="$(cd "$ADOPTED" && "$TOFU" apply -input=false -no-color approved.tfplan 2>&1)" || { printf '%s\n' "$P_APPLY2" | tail -30; fail "the saved plan did not apply once the world was put back"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$P_APPLY2" || fail "the saved plan's apply did not change exactly one object"
  [ "$(kca get configmap api-config -n "$NS" -o jsonpath='{.data.reviewed}')" = "yes" ] || fail "api-config does not read reviewed=yes after the saved plan applied"
  ( stock_b plan -out=approved.tfplan -input=false -no-color >/dev/null 2>&1 && stock_b apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "stock's own planfile did not apply on B"
  gauntlet_stage plan_approval pass "plan -out wrote one update (api-config gains reviewed=yes); the world then moved out of band (a stray label on shard-0, kubectl, never choudoufu) and apply of the saved plan refused with \"The approved plan no longer matches the live system\" at exit 3, nothing applied (kubectl reads no reviewed key); with the label removed the identical file applied, 0 added, 1 changed, 0 destroyed, and reviewed=yes reads back; stock's own planfile applied on the oracle cluster in the unchanged case. BREAK_APPROVAL=1 expects success after the move and correctly fails"
fi

# ── 7. day2_rename: a moved block, zero churn ────────────────────────────
gauntlet_begin_stage day2_rename
log "=== 7. day2_rename: kubernetes_service_account_v1.app becomes .team through a moved block ==="
moved_block() { cat <<'EOF'

moved {
  from = kubernetes_service_account_v1.app
  to   = kubernetes_service_account_v1.team
}
EOF
}
if [ "${BREAK:-}" = "1" ]; then
  # The AWS Break line (rename without a moved block, expect churn) cannot
  # fire here: with no address on the object the block name is not part of
  # its identity, and the plan after a bare block rename is empty too. The
  # control that can fire is a rename of the object's own metadata.name,
  # which is a replace.
  write_config "$ADOPTED" live app 2 '    reviewed = "yes"'
  python3 - "$ADOPTED/main.tf" <<'PYIN' || fail "BREAK: could not rename the ServiceAccount's metadata.name"
import sys
p = sys.argv[1]; s = open(p).read()
old = 'resource "kubernetes_service_account_v1" "app" {\n  metadata {\n    name      = "app"'
assert old in s
open(p, 'w').write(s.replace(old, old.replace('name      = "app"', 'name      = "app-renamed"'), 1))
PYIN
  R_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -30; fail "BREAK: the plan after renaming the object failed"; }
  if grep -qE "^No changes|Plan: 0 to add, 0 to change, 0 to destroy" <<< "$R_PLAN"; then
    fail "BREAK=1: renaming the object's own name still planned zero churn - the zero-churn assertion is not load-bearing"
  fi
  grep -q "1 to add" <<< "$R_PLAN" && grep -q "1 to destroy" <<< "$R_PLAN" || { printf '%s\n' "$R_PLAN" | tail -30; fail "BREAK=1: renaming the object's own name did not plan a destroy and a create: $(plan_line "$R_PLAN")"; }
  log "  BREAK=1: caught - renaming metadata.name plans $(plan_line "$R_PLAN"); the real moved-block check below is skipped"
  { write_config "$ADOPTED" live team 2 '    reviewed = "yes"'; moved_block >> "$ADOPTED/main.tf"; }
  ( cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: the moved-block apply failed"
  { write_config "$ORACLE" stock team 2 '    reviewed = "yes"'; moved_block >> "$ORACLE/main.tf"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: stock's moved-block apply failed on B"
  gauntlet_stage day2_rename pass "BREAK=1 control: renaming the object's own metadata.name plans a replace ($(plan_line "$R_PLAN")), so the zero-churn assertion correctly fails to hold; a bare block rename without a moved block is zero churn on this substrate because the block name is not part of the object's identity; the moved block then applied"
else
  { write_config "$ADOPTED" live team 2 '    reviewed = "yes"'; moved_block >> "$ADOPTED/main.tf"; }
  { write_config "$ORACLE" stock team 2 '    reviewed = "yes"'; moved_block >> "$ORACLE/main.tf"; }
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan failed on B"; }
  grep -qE "^No changes|Plan: 0 to add, 0 to change, 0 to destroy" <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan on B is not zero churn"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's moved-block apply failed on B"
  R_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -30; fail "the moved-block plan failed"; }
  grep -qE "^No changes|Plan: 0 to add, 0 to change, 0 to destroy" <<< "$R_PLAN" || { printf '%s\n' "$R_PLAN" | tail -30; fail "the moved-block plan is not zero churn"; }
  ( cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "the moved-block apply failed"
  exists_a serviceaccount app || fail "the ServiceAccount is gone after the rename"
  [ "$(count_a)" = "14" ] || fail "$(count_a) labelled objects after the rename, want 14"
  gauntlet_stage day2_rename pass "moved block: kubernetes_service_account_v1.app -> .team with zero churn (no add, no change, no destroy), the live object untouched and still labelled, read with kubectl; stock's plan for the same moved block on the oracle cluster is also zero churn. The moved-block half only: live-mv has no Kubernetes leg, because the object carries no address to rewrite (#1066). A bare block rename without a moved block is zero churn here too, since the block name is not part of the object's identity; BREAK=1 renames the object's own metadata.name instead, which plans a replace, and the zero-churn assertion correctly fails"
fi

# ── 8. day2_remove: the redis StatefulSet's block leaves the configuration
#
# The stage this estate was built for. Two things are measured: the block
# removal itself (which the sweep handles the way it handles any orphan),
# and what the removal leaves behind - two PVCs that nobody declared, that
# no state holds, that carry labels, and that kubesweep.ControllerMade
# cannot recognise as a controller's work.
gauntlet_begin_stage day2_remove
log "=== 8. day2_remove: the redis StatefulSet block leaves the configuration ==="
if [ "${BREAK_REMOVE:-}" = "1" ]; then
  K_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || fail "BREAK_REMOVE: the plan with the block kept failed"
  # "0 to destroy" in a zero-churn summary line is not a destroy.
  grep -qE "will be destroyed|[1-9][0-9]* to destroy" <<< "$K_PLAN" && fail "BREAK_REMOVE=1: with the block kept a destroy was still proposed - the destroy below would not be the block removal's doing"
  log "  BREAK_REMOVE=1: caught - with the block kept the plan proposes no destroy ($(plan_line "$K_PLAN")); the real check is skipped"
  gauntlet_stage day2_remove pass "BREAK_REMOVE=1 control: with the redis StatefulSet block kept, no destroy is proposed; the real check is skipped"
  write_config "$ADOPTED" live team 2 '    reviewed = "yes"' drop_redis_sts
  write_config "$ORACLE" stock team 2 '    reviewed = "yes"' drop_redis_sts
  ( cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: the removal apply failed afterwards"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: stock's removal apply failed on B afterwards"
else
  # The PVC facts BEFORE anything is removed, so that "unchanged by the
  # removal" below is a comparison and not an assertion about one reading.
  PVC_BEFORE="$(pvc_facts "$KCA")"
  log "  PVC facts before the removal:"; printf '%s\n' "$PVC_BEFORE" | sed 's/^/    /'
  REDIS_TMPL_LABELS="$(kca get pvc data-redis-0 -n "$NS" -o jsonpath='{.metadata.labels}')"
  # The correction to #1107: the claim template says app=redis-data, the
  # selector says app=redis, and the controller merges the selector OVER
  # the template. If a future Kubernetes stops doing that, this is the line
  # that says so.
  [ "$(kca get pvc data-redis-0 -n "$NS" -o jsonpath='{.metadata.labels.app}')" = "redis" ] \
    || fail "data-redis-0's app label reads $(kca get pvc data-redis-0 -n "$NS" -o jsonpath='{.metadata.labels.app}'), want redis (the StatefulSet selector's value, merged over the claim template's app=redis-data)"
  [ "$(kca get pvc data-redis-0 -n "$NS" -o jsonpath='{.metadata.labels.role}')" = "cache-volume" ] \
    || fail "data-redis-0 does not carry role=cache-volume, the claim template's own label that the selector does not override"
  kca get statefulset redis -n "$NS" -o jsonpath='{.spec.volumeClaimTemplates[0].metadata.labels.app}' | grep -qx "redis-data" \
    || fail "the redis StatefulSet's claim template does not declare app=redis-data; the merge measurement above compares nothing"

  write_config "$ADOPTED" live team 2 '    reviewed = "yes"' drop_redis_sts
  write_config "$ORACLE" stock team 2 '    reviewed = "yes"' drop_redis_sts
  grep -q 'kubernetes_stateful_set_v1" "redis"' "$ADOPTED/main.tf" && fail "the redis StatefulSet block is still in the adopted root"
  grep -q 'kubernetes_stateful_set_v1" "postgres"' "$ADOPTED/main.tf" || fail "dropping the redis StatefulSet also dropped the postgres one"

  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan failed on B"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan on B is not exactly one destroy"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's remove apply failed on B"
  O_PVC_LEFT="$(kcb get pvc -n "$NS" -o name | wc -l | tr -d ' ')"

  D_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$D_PLAN" | tail -30; fail "the remove plan failed"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$D_PLAN" || { printf '%s\n' "$D_PLAN" | tail -30; fail "the remove plan is not exactly one destroy"; }
  D_LINE="$(grep -E '^[[:space:]]*# .* will be destroyed' <<< "$D_PLAN" | head -1)"
  D_ADDR="$(sed -E 's/^[[:space:]#]*//; s/ will be destroyed.*$//' <<< "$D_LINE")"
  [ "$D_ADDR" = "kubernetes_stateful_set_v1.orphan_${NS}_redis" ] || { printf '%s\n' "$D_PLAN" | grep -E 'destroyed|^Plan:'; fail "the one destroy is ${D_ADDR:-unnamed} (line: ${D_LINE:-none}), not the orphan address kubernetes_stateful_set_v1.orphan_${NS}_redis the sweep plans a label-found object at"; }
  grep -q "Owned and undeclared: 1 live resource will be destroyed" <<< "$D_PLAN" || fail "the plan does not say the destroy is an owned, undeclared object"
  D_APPLY="$(cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$D_APPLY" | tail -30; fail "the remove apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 0 changed, 1 destroyed" <<< "$D_APPLY" || fail "the remove apply did not destroy exactly one object"
  exists_a statefulset redis && fail "the redis StatefulSet still exists after the remove apply"
  D_REPLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || fail "the replan after the remove failed"
  grep -q "No changes." <<< "$D_REPLAN" || { printf '%s\n' "$D_REPLAN" | tail -30; fail "the replan after the remove is not empty"; }
  [ "$(count_a)" = "13" ] || fail "$(count_a) labelled objects after the remove, want 13"

  # What the removal left behind.
  PVC_AFTER="$(pvc_facts "$KCA")"
  log "  PVC facts after the removal:"; printf '%s\n' "$PVC_AFTER" | sed 's/^/    /'
  [ "$PVC_BEFORE" = "$PVC_AFTER" ] || { diff <(printf '%s\n' "$PVC_BEFORE") <(printf '%s\n' "$PVC_AFTER"); fail "the three PVCs are not what they were before the StatefulSet's block was removed (diff above)"; }
  PVC_LEFT="$(kca get pvc -n "$NS" -o name | wc -l | tr -d ' ')"
  [ "$PVC_LEFT" = "3" ] || fail "$PVC_LEFT PVC(s) left in $NS after the removal, want 3 - both redis PVCs and postgres's"
  [ "$PVC_LEFT" = "$O_PVC_LEFT" ] || fail "choudoufu left $PVC_LEFT PVC(s) where stock's own removal on the oracle cluster left $O_PVC_LEFT"
  BOUND_LEFT="$(kca get pvc -n "$NS" -o jsonpath='{range .items[*]}{.status.phase}{"\n"}{end}' | grep -c '^Bound$')"
  [ "$BOUND_LEFT" = "3" ] || fail "$BOUND_LEFT of the 3 surviving PVCs are Bound"
  [ "$(pvc_count_a)" = "0" ] || fail "a PVC carries tofu-estate=$ESTATE; nothing in the configuration puts it there"

  # The exclusion probe. kubesweep's package documentation names "a
  # StatefulSet's volumeClaimTemplate labels reach its PVCs" as the case
  # ControllerMade exists to exclude. Measured above, an orphaned
  # volume_claim_template PVC on kind carries no ownerReferences at all, so
  # the signal that comment said keeps them out does not fire; what keeps
  # them out is managedFields, and kube-controller-manager is the only
  # manager that wrote the PVC's spec.
  #
  # This puts the estate's own label on one of them with kubectl, since
  # cold_deploy forbids writing a marker into the configuration. Note what
  # that manufacture is and is not (#1179): `kubectl label` adds a
  # kubectl-label manager to managedFields, which a claim template carrying
  # the marker would not - the controller would have written the label
  # itself. So this probe reads a slightly HARSHER state than the
  # documented case: a controller-made PVC that some other client has since
  # touched. Both must be excluded, and the harsher one is what the old
  # "every manager is a control-plane manager" rule failed. The plan is
  # read, never applied: the finding is what choudoufu is willing to
  # destroy.
  PROBE_PVC="data-redis-0"
  PRE_PROBE="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || fail "the pre-probe plan failed"
  grep -q "persistent_volume_claim" <<< "$PRE_PROBE" && fail "the plan already names a PVC before the probe labelled one; the probe would prove nothing"
  if [ "${BREAK_PVC:-}" != "1" ]; then
    kca label pvc "$PROBE_PVC" -n "$NS" "tofu-estate=$ESTATE" >/dev/null || fail "could not label $PROBE_PVC for the exclusion probe"
  else
    log "  BREAK_PVC=1: the PVC is left unlabelled and the sweep is still expected to propose it"
  fi
  # What the probe's own manufacture did to the signal the sweep reads.
  # Recorded rather than asserted: if a future kubectl stops registering a
  # field manager for a label edit, this line is what says so.
  PROBE_FACTS="$(pvc_facts "$KCA" | grep "^$PROBE_PVC ")"
  log "  probe PVC as the sweep now sees it: $PROBE_FACTS"
  PROBE_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$PROBE_PLAN" | tail -30; fail "the probe plan failed"; }
  PROBE_ADDR="kubernetes_persistent_volume_claim_v1.orphan_${NS}_${PROBE_PVC}"
  PROBE_HIT=0
  grep -qF "$PROBE_ADDR will be destroyed" <<< "$PROBE_PLAN" && PROBE_HIT=1
  kca label pvc "$PROBE_PVC" -n "$NS" tofu-estate- >/dev/null 2>&1
  AFTER_PROBE="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || fail "the plan after the probe's label was removed failed"
  grep -q "No changes." <<< "$AFTER_PROBE" || { printf '%s\n' "$AFTER_PROBE" | tail -30; fail "the plan is not empty again once the probe's label is removed; the cluster is not back where the probe found it"; }

  # The other side of the probe, and the reason it is here (#1179). The
  # probe above now expects the sweep to propose NOTHING, and so does
  # BREAK_PVC=1: since the exclusion was fixed, both arms read "No
  # changes", so between them they no longer distinguish "the exclusion
  # held" from "the sweep is blind to PVCs". This arm supplies the
  # difference. It labels the controller-made PVC again AND declares a PVC
  # the way a person does - kubectl, which owns the new object's spec and
  # is nobody's control plane - and requires ONE plan to tell them apart:
  # the declared one proposed, the controller-made one not. A sweep that
  # excluded every PVC would fail the first assertion; a sweep that
  # excluded none would fail the second.
  TWO_PVC="declared-orphan"
  kca label pvc "$PROBE_PVC" -n "$NS" "tofu-estate=$ESTATE" >/dev/null || fail "could not relabel $PROBE_PVC for the two-sided arm"
  kca create -n "$NS" -f - >/dev/null <<YAML || fail "could not create $TWO_PVC for the two-sided arm"
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: $TWO_PVC
  labels:
    tofu-estate: $ESTATE
spec:
  accessModes: ["ReadWriteOnce"]
  resources:
    requests:
      storage: 64Mi
YAML
  log "  two-sided arm, both PVCs labelled:"; pvc_facts "$KCA" | grep -E "^($PROBE_PVC|$TWO_PVC) " | sed 's/^/    /'
  TWO_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$TWO_PLAN" | tail -30; fail "the two-sided arm's plan failed"; }
  TWO_ADDR="kubernetes_persistent_volume_claim_v1.orphan_${NS}_${TWO_PVC}"
  TWO_HIT=0; grep -qF "$TWO_ADDR will be destroyed" <<< "$TWO_PLAN" && TWO_HIT=1
  TWO_EXCLUDED=1; grep -qF "$PROBE_ADDR will be destroyed" <<< "$TWO_PLAN" && TWO_EXCLUDED=0
  TWO_SUMMARY="$(plan_line "$TWO_PLAN")"
  # Waited for, not fired and forgotten: the next plan below asserts the
  # cluster is back where it started, and a PVC still Terminating with the
  # label on it would still be swept.
  kca delete pvc "$TWO_PVC" -n "$NS" --timeout=60s >/dev/null 2>&1
  kca label pvc "$PROBE_PVC" -n "$NS" tofu-estate- >/dev/null 2>&1
  [ "$TWO_HIT" -eq 1 ] || { printf '%s\n' "$TWO_PLAN" | tail -30; fail "the two-sided arm: the sweep did not propose $TWO_ADDR, a PVC kubectl declared and labelled ($TWO_SUMMARY). Either the sweep cannot see PVCs at all, in which case the exclusion probe above proves nothing, or the exclusion is now wider than controller-made objects"; }
  [ "$TWO_EXCLUDED" -eq 1 ] || { printf '%s\n' "$TWO_PLAN" | tail -30; fail "the two-sided arm: the sweep proposed $PROBE_ADDR, the controller-made PVC, in the same plan it correctly proposed $TWO_ADDR - #1179's exclusion has regressed"; }
  log "  two-sided arm: $TWO_SUMMARY, naming $TWO_ADDR and not $PROBE_ADDR"
  TWO_AFTER="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || fail "the plan after the two-sided arm failed"
  grep -q "No changes." <<< "$TWO_AFTER" || { printf '%s\n' "$TWO_AFTER" | tail -30; fail "the plan is not empty again after the two-sided arm cleaned up; the cluster is not back where it started"; }
  [ "$(pvc_count_a)" = "0" ] || fail "a PVC still carries tofu-estate=$ESTATE after the two-sided arm cleaned up"

  if [ "${BREAK_PVC:-}" = "1" ]; then
    [ "$PROBE_HIT" -eq 1 ] && fail "BREAK_PVC=1: the sweep proposed $PROBE_ADDR with no estate label on it - the probe keys on the kind, not on the label, and proves nothing"
    log "  BREAK_PVC=1: caught - with no label on the PVC the sweep proposes nothing ($(plan_line "$PROBE_PLAN")), so 'expect the sweep to propose it' correctly fails"
    gauntlet_stage day2_remove pass "BREAK_PVC=1 control: with no estate label on the orphaned PVC the sweep proposes nothing ($(plan_line "$PROBE_PLAN")), so the probe's own expectation correctly fails; the probe keys on the label"
  elif [ "$PROBE_HIT" -eq 1 ]; then
    PROBE_LINE="$(grep -E "^[[:space:]]*# ${PROBE_ADDR} will be destroyed" <<< "$PROBE_PLAN" | head -1 | sed 's/^[[:space:]]*//')"
    PROBE_SUMMARY="$(grep -E "^Owned and undeclared" <<< "$PROBE_PLAN" | head -1)"
    log "  PVC exclusion probe: $PROBE_SUMMARY"
    log "  PVC exclusion probe: $PROBE_LINE"
    gauntlet_stage day2_remove fail "#1179 regressed. The block-removal half passes: dropping kubernetes_stateful_set_v1.redis proposed exactly one destroy (0 add, 0 change, 1 destroy) at the sweep's synthetic orphan address $D_ADDR (\"Owned and undeclared: 1 live resource will be destroyed\"), applied cleanly, the object gone (kubectl get statefulset redis: NotFound), the next plan empty, 13 objects still labelled, and stock's plan for the same removal on the oracle cluster is also exactly one destroy. What it leaves behind is the finding. The two redis PVCs and postgres's survive the removal unchanged and Bound (stock's own removal on the oracle cluster leaves the same $O_PVC_LEFT), carrying labels the StatefulSet controller wrote: app=redis (the SELECTOR's value, merged over the claim template's app=redis-data - #1107's second research comment records \"the claim template's labels verbatim\", which its probe could not distinguish because its template and selector agreed), plus the template's own tier=storage and role=cache-volume; the kubernetes.io/pvc-protection finalizer; and NO metadata.ownerReferences, because the default persistentVolumeClaimRetentionPolicy is Retain. That last is the signal internal/live/kubesweep's package comment named as \"the test that keeps them out\", and it is absent on the very object that comment names. What must keep them out instead is the managedFields signal: kube-controller-manager is the only manager that wrote the PVC's spec ($PROBE_FACTS). With the estate's label put on data-redis-0 with kubectl - which also adds a kubectl-label manager, so this is a harsher state than a claim template carrying the marker would produce - the sweep proposes destroying it: $PROBE_SUMMARY / $PROBE_LINE. The label was removed again and the plan is empty, so nothing was destroyed by this run; the finding is what the plan is willing to do to a persistent volume nobody declared. BREAK_PVC=1 runs the probe with no label and the expectation correctly fails"
  else
    gauntlet_stage day2_remove pass "deleting kubernetes_stateful_set_v1.redis's block proposed exactly one destroy (0 add, 0 change, 1 destroy) at the sweep's synthetic orphan address $D_ADDR (\"Owned and undeclared: 1 live resource will be destroyed\"), applied cleanly, the object gone from the cluster (kubectl get statefulset redis: NotFound) and the next plan empty; stock's plan for the same removal on the oracle cluster is also exactly one destroy. The three controller-created PVCs survive the removal unchanged and Bound, as they do under stock ($O_PVC_LEFT left there too), carrying labels the controller wrote - app=redis from the SELECTOR merged over the claim template's app=redis-data, plus the template's tier=storage and role=cache-volume - the pvc-protection finalizer, and no ownerReferences, since the default persistentVolumeClaimRetentionPolicy is Retain and they are built to outlive the StatefulSet. Their managedFields name kube-scheduler and kube-controller-manager, and kube-controller-manager alone wrote their spec, which is the signal kubesweep.ControllerMade judges after #1179 (the owner-reference signal its package comment credited is absent here, and kubectl get hides managedFields without --show-managed-fields, which is how #1179 came to record them as absent too). With the estate's label put on one of them by kubectl - a harsher state than a claim template carrying the marker, since kubectl also registers itself as a field manager ($PROBE_FACTS) - the sweep still proposed nothing, so the controller-copy exclusion held. The two-sided arm then labelled that PVC again and declared one with kubectl (spec written by kubectl-create, nobody's control plane) in the same namespace, and one plan told them apart: $TWO_SUMMARY, naming $TWO_ADDR and not $PROBE_ADDR, so the exclusion is not the sweep being blind to the kind. Both were cleaned up and the plan is empty again. BREAK_REMOVE=1 keeps the block and no destroy is proposed; BREAK_PVC=1 runs the probe unlabelled and correctly fails"
  fi
fi

# ── 9. day2_count: shards 2 -> 1 -> 2 ────────────────────────────────────
gauntlet_begin_stage day2_count
log "=== 9. day2_count: kubernetes_config_map_v1.shard scales 2 -> 1 -> 2 ==="
scale_to() { # $1 count, for both roots (the redis StatefulSet block is gone,
             # and so is the moved block, whose move both states already hold)
  write_config "$ADOPTED" live team "$1" '    reviewed = "yes"' drop_redis_sts
  write_config "$ORACLE" stock team "$1" '    reviewed = "yes"' drop_redis_sts
}
scale_to 1
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-down plan failed on B"
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's scale-down plan on B is not exactly one destroy"; }
grep -q 'kubernetes_config_map_v1.shard\[1\]' <<< "$O_PLAN" || fail "stock's scale-down on B does not destroy shard[1]"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-down apply failed on B"
C_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$C_PLAN" | tail -30; fail "the scale-down plan failed"; }
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$C_PLAN" || { printf '%s\n' "$C_PLAN" | tail -30; fail "the scale-down plan is not exactly one destroy"; }
# The instance leaving the count is found by its label, which carries no
# index, so the sweep plans it at its orphan address; kubectl below confirms
# it is shard-1, the object stock destroys.
C_LINE="$(grep -E '^[[:space:]]*# .* will be destroyed' <<< "$C_PLAN" | head -1)"
C_ADDR="$(sed -E 's/^[[:space:]#]*//; s/ will be destroyed.*$//' <<< "$C_LINE")"
grep -qE "^kubernetes_config_map(_v1)?\.orphan_${NS}_shard-1$" <<< "$C_ADDR" || { printf '%s\n' "$C_PLAN" | grep -E 'destroyed|^Plan:'; fail "the scale-down destroys ${C_ADDR:-nothing named}, not shard-1 at its orphan address"; }
( cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1 | grep -qF "0 added, 0 changed, 1 destroyed" ) || fail "the scale-down apply did not destroy exactly one object"
if [ "${BREAK_COUNT:-}" = "1" ]; then
  if ! exists_a configmap shard-0; then
    fail "BREAK_COUNT=1: shard-0 was destroyed - the 'wrong instance' assertion would hold, so the check is not load-bearing"
  fi
  log "  BREAK_COUNT=1: caught - shard-0 still exists, so asserting it was the one destroyed correctly fails"
  gauntlet_stage day2_count pass "BREAK_COUNT=1 control: asserting the lower index (shard-0) was destroyed correctly fails to hold; the real check is skipped"
else
  exists_a configmap shard-0 || fail "shard-0 was destroyed on the scale-down"
  exists_a configmap shard-1 && fail "shard-1 still exists after the scale-down"
  scale_to 2
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-up plan failed on B"
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's scale-up plan on B is not exactly one add"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-up apply failed on B"
  U_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$U_PLAN" | tail -30; fail "the scale-up plan failed"; }
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$U_PLAN" || { printf '%s\n' "$U_PLAN" | tail -30; fail "the scale-up plan is not exactly one add"; }
  grep -q 'kubernetes_config_map_v1.shard\[1\]' <<< "$U_PLAN" || fail "the scale-up does not create shard[1]"
  ( cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1 | grep -qF "1 added, 0 changed, 0 destroyed" ) || fail "the scale-up apply did not create exactly one object"
  exists_a configmap shard-0 && exists_a configmap shard-1 || fail "both shards do not exist after the scale-up"
  U_REPLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || fail "the replan after the scale-up failed"
  grep -q "No changes." <<< "$U_REPLAN" || { printf '%s\n' "$U_REPLAN" | tail -30; fail "the replan after the scale-up is not empty"; }
  [ "$(count_a)" = "13" ] || fail "$(count_a) labelled objects after the count cycle, want 13"
  gauntlet_stage day2_count pass "scaling kubernetes_config_map_v1.shard from 2 to 1 destroyed exactly shard-1, planned at the sweep's orphan address $C_ADDR since the label carries no index (shard-0 untouched, both read with kubectl); back to 2 created exactly kubernetes_config_map_v1.shard[1] under the same name; the next plan is empty; stock's plans for the same two changes on the oracle cluster have the identical shape. BREAK_COUNT=1 asserts the lower index was destroyed and correctly fails"
fi

# ── 10. day2_crash: an apply of several objects, killed after the first ──
#
# day2_crash on the kind substrate (#1110, part 4). The stage's own window
# on AWS - after a create_before_destroy create, before the paired destroy -
# cannot exist here: a name is unique in its namespace, so nothing is
# created before the object it replaces is gone (day2_replace's n/a). The
# Kubernetes window with the same question in it is an apply that creates
# several objects. Kill it after one object exists and before the next
# does, and the next plan has to propose exactly the remainder, with the
# object already created bound by its label rather than created a second
# time (which the API server would refuse) or swept away as an orphan.
#
# crash_second reads crash_first's own name, so the two are an edge in the
# graph rather than two independent nodes the walker may reach in either
# order, and the kill is the engine's own self-signal inside the
# -parallelism=1 walker (internal/command/apply_e2etesting_crash.go): the
# window is closed by the graph, not by timing. Two ConfigMaps rather than
# anything stateful on purpose - what is under test is the recovery of a
# half-applied graph, and a StatefulSet's own rollout would put a wait,
# not a second write, between the two creates.
#
# crash_first is a kubernetes_secret_v1 and not a ConfigMap, which is
# #1235: the stage is measuring what the record store contributes to
# recovering a half-applied graph, and a kubernetes_config_map(_v1) is the
# one type in this lane's surface with neither a ratified identity row nor
# a config-only argument, so it records nothing at all and this stage's own
# evidence line could only ever report a count that did not move - the
# 12 -> 12 #1188 measured here. A Secret records
# residue.wait_for_service_account_token, the applied value of a
# config-only argument the API server never returns, so the record the
# interrupted apply writes has something in it to read; the stage reads it
# below by taking it away again.
gauntlet_begin_stage day2_crash
log "=== 10. day2_crash: SIGTERM between the create of one object and the create of the next ==="
crash_pair() { # $1: first, second or both (default)
  local which="${1:-both}"
  if [ "$which" != "second" ]; then
    cat <<EOF

resource "kubernetes_secret_v1" "crash_first" {
  metadata {
    name      = "crash-first"
    namespace = "$NS"
  }
  data = {
    step = "one"
  }
  depends_on = [kubernetes_namespace_v1.app]
}
EOF
  fi
  if [ "$which" != "first" ]; then
    cat <<EOF

resource "kubernetes_config_map_v1" "crash_second" {
  metadata {
    name      = "crash-second"
    namespace = "$NS"
  }
  data = {
    after = kubernetes_secret_v1.crash_first.metadata[0].name
  }
}
EOF
  fi
}

crash_pair first >> "$ORACLE/main.tf" || fail "could not append crash-first to the oracle root"
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's crash-first plan failed on B"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's crash-first plan on B is not exactly one add"; }
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's crash-first apply failed on B"
crash_pair second >> "$ORACLE/main.tf" || fail "could not append crash-second to the oracle root"
O_REMAINDER="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_REMAINDER" | tail -10; fail "stock's remainder plan failed on B"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_REMAINDER" \
  || { printf '%s\n' "$O_REMAINDER" | tail -10; fail "stock's remainder plan on B is not exactly one add - the oracle for this stage is not what it should be"; }
grep -q 'kubernetes_config_map_v1.crash_second' <<< "$O_REMAINDER" || fail "stock's remainder plan on B does not name crash_second"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's remainder apply failed on B"
log "  oracle: stock at crash-first alone plans exactly one add (crash_second) for the remainder, and applies it"

crash_pair >> "$ADOPTED/main.tf" || fail "could not append the crash pair to the adopted root"
X_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$X_PLAN" | tail -30; fail "the pre-crash plan failed"; }
grep -qF "Plan: 2 to add, 0 to change, 0 to destroy." <<< "$X_PLAN" \
  || { printf '%s\n' "$X_PLAN" | tail -30; fail "the pre-crash plan is not exactly two adds - there is no two-object apply to interrupt"; }
X_RECORDS_BEFORE="$(gauntlet_record_count "$ADOPTED/.tofu-records")"

# The interrupt is delivered by the engine itself, so this runs in the
# plain foreground: no background process, no output tailing, no poll loop.
# A non-zero exit is the NORMAL outcome - the process was killed.
X_OUT="$(cd "$ADOPTED" && TOFU_E2E_APPLY_RESOURCE_INTERRUPT="kubernetes_secret_v1.crash_first" "$TOFU_CRASH" apply -input=false -auto-approve -no-color -parallelism=1 2>&1)"; X_RC=$?
printf '%s\n' "$X_OUT" > "$WORK/day2_crash.log"
log "  interrupted apply exited $X_RC (a genuine crash is not expected to exit 0)"
[ "$X_RC" -ne 0 ] || { printf '%s\n' "$X_OUT" | tail -20; fail "the interrupted apply exited 0 - the engine's self-signal never landed, so nothing was interrupted and this stage would measure a clean apply"; }
exists_a secret crash-first || { printf '%s\n' "$X_OUT" | tail -20; fail "crash-first does not exist after the interrupted apply - the kill landed before the create committed, so there is no crash window to recover from"; }
exists_a configmap crash-second && { printf '%s\n' "$X_OUT" | tail -20; fail "crash-second exists after the interrupted apply - the kill landed after both creates, so there is no remainder to propose"; }
# The selector alone, never a selector next to a resource name: kubectl
# refuses that combination outright, which would read as an unlabelled
# object.
kca get secret -n "$NS" -l "tofu-estate=$ESTATE" -o name 2>/dev/null | grep -qx "secret/crash-first" \
  || fail "crash-first was created by the interrupted apply but does not come back under tofu-estate=$ESTATE - the marker the rerun is supposed to find is not there"
X_RECORDS_AFTER="$(gauntlet_record_count "$ADOPTED/.tofu-records")"
log "  crash-first exists and is labelled; crash-second does not exist; record files $X_RECORDS_BEFORE -> $X_RECORDS_AFTER"

# What the interrupted apply actually wrote, read off the store by the
# envelope's own address rather than counted (#1235). A count that does
# not move is not a reading: it is what this stage reported while its
# crash pair was the one type that records nothing.
X_REC="$(gauntlet_record_file "$ADOPTED/.tofu-records" "kubernetes_secret_v1.crash_first")"
[ -n "$X_REC" ] || fail "the interrupted apply created crash-first but wrote no record for kubernetes_secret_v1.crash_first (record files $X_RECORDS_BEFORE -> $X_RECORDS_AFTER); there is nothing for the recovery to read back and this stage cannot measure what the record contributes"
[ "$X_RECORDS_AFTER" = "$((X_RECORDS_BEFORE + 1))" ] || fail "record files went $X_RECORDS_BEFORE -> $X_RECORDS_AFTER across the interrupted apply, want exactly one more - the apply is supposed to have written the record for the one object it did create, and nothing else"
X_RESIDUE="$(gauntlet_record_residue "$X_REC" | tr '\n' ' ' | sed 's/ $//')"
[ "$X_RESIDUE" = "wait_for_service_account_token" ] || fail "the record the interrupted apply wrote for kubernetes_secret_v1.crash_first carries residue [${X_RESIDUE:-none}], want wait_for_service_account_token - the crash pair's first object has to be a type that records something irrecoverable, or this stage measures the record's contribution with an object that has none (#1188, #1235)"
log "  the interrupted apply wrote one record for kubernetes_secret_v1.crash_first, carrying residue $X_RESIDUE"

if [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  kca label configmap crash-first -n "$NS" tofu-estate- >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: could not strip the label off crash-first"
  log "  BREAK_CRASH_UNBOUND=1: stripped tofu-estate off crash-first with kubectl"
fi

R_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; R_RC=$?
R_LINE="$(grep -E '^Plan:|^No changes' <<< "$R_PLAN" | head -1 | sed 's/\.$//')"
# What the real check asserts, as one predicate, so the Break controls can
# require the SAME predicate to fail rather than approximating it.
recovered() {
  [ "$R_RC" -eq 0 ] || return 1
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$R_PLAN" || return 1
  grep -qE '^[[:space:]]*# kubernetes_config_map(_v1)?\.crash_second will be created' <<< "$R_PLAN" || return 1
  grep -E '^[[:space:]]*# .* will be' <<< "$R_PLAN" | grep -q 'crash_first\|crash-first' && return 1
  return 0
}

if [ "${BREAK_CRASH:-}" = "1" ]; then
  log "=== 10b (BREAK_CRASH=1). assert nothing is proposed after the interrupt - this must fail ==="
  [ "$R_RC" -eq 0 ] || { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK_CRASH=1: the plan after the interrupt exited $R_RC"; }
  grep -qF "No changes." <<< "$R_PLAN" \
    && { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK_CRASH=1: the plan after a real interrupted two-object apply came back empty, so this stage's own check is not load-bearing"; }
  log "  BREAK_CRASH=1: caught - the plan proposes work ($R_LINE), so 'nothing is proposed' correctly fails to hold"
  gauntlet_stage day2_crash pass "BREAK_CRASH=1 control: after the same real interrupt the plan proposes work ($R_LINE), so the stage's own Break line - interrupt and then assert nothing is proposed - correctly fails to hold; the real check is skipped"
  ( cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_CRASH: the recovery apply failed afterwards"
elif [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  log "=== 10b (BREAK_CRASH_UNBOUND=1). the same check against an unbound object - this must fail ==="
  if recovered; then
    printf '%s\n' "$R_PLAN" | grep -E '^Plan:|will be'
    fail "BREAK_CRASH_UNBOUND=1: the recovery check still holds with crash-first carrying no tofu-estate label - it is not measuring whether the crashed-out object was bound at all"
  fi
  log "  BREAK_CRASH_UNBOUND=1: caught - with the label stripped the recovery check fails ($R_LINE)"
  gauntlet_stage day2_crash pass "BREAK_CRASH_UNBOUND=1 control: with the tofu-estate label stripped off the object the interrupted apply created - the unrecovered run this stage exists to catch - the recovery check correctly fails to hold ($R_LINE); the real check is skipped"
  kca delete secret crash-first -n "$NS" >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: could not delete the unlabelled crash-first afterwards"
  ( cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_CRASH_UNBOUND: the apply after the cleanup failed"
else
  if ! recovered; then
    printf '%s\n' "$R_PLAN" | grep -E '^Plan:|^No changes|will be' | head -20
    gauntlet_stage day2_crash fail "the plan after a real interrupt between the create of kubernetes_config_map_v1.crash_first and the create of kubernetes_config_map_v1.crash_second is not exactly the remainder: ${R_LINE:-no plan line} (exit $R_RC). crash-first exists on the cluster carrying tofu-estate=$ESTATE and crash-second does not, both read with kubectl; stock, walked into the same position on the oracle cluster, plans exactly one add (crash_second). The interrupted apply wrote one record for kubernetes_secret_v1.crash_first carrying residue ${X_RESIDUE:-none} (record files $X_RECORDS_BEFORE -> $X_RECORDS_AFTER)"
  else
    # ── the record's contribution, measured rather than counted (#1235) ──
    #
    # Take the one record the interrupted apply wrote out of the store,
    # replan from the identical position, and the difference between the
    # two plans IS what the record carried. Put it back and the difference
    # has to go away. Neither plan can pass vacuously: what is asserted is
    # that they differ, and in which direction.
    mv "$X_REC" "$WORK/crash_first.record" || fail "could not move the crash record aside"
    N_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; N_RC=$?
    N_LINE="$(grep -E '^Plan:|^No changes' <<< "$N_PLAN" | head -1 | sed 's/\.$//')"
    [ "$N_RC" -eq 0 ] || { printf '%s\n' "$N_PLAN" | tail -20; fail "the replan with the crash record taken out of the store exited $N_RC"; }
    grep -qF "Plan: 1 to add, 1 to change, 0 to destroy." <<< "$N_PLAN" \
      || { printf '%s\n' "$N_PLAN" | grep -E '^Plan:|will be|^ +[+~-] ' | head -20
           fail "with the one record the interrupted apply wrote taken out of the store, the recovery plan is $N_LINE, not the remainder plus one in-place update - so the record contributed nothing this stage can read, and its file count is not evidence of anything"; }
    grep -qE '^[[:space:]]+\+ wait_for_service_account_token +=' <<< "$N_PLAN" \
      || { printf '%s\n' "$N_PLAN" | grep -E 'will be|^ +[+~-] ' | head -20
           fail "the extra in-place update the missing record produces does not propose wait_for_service_account_token back - that config-only argument's applied value is the residue the record is supposed to be carrying"; }
    mv "$WORK/crash_first.record" "$X_REC" || fail "could not put the crash record back"
    B_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"
    grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$B_PLAN" \
      || { printf '%s\n' "$B_PLAN" | grep -E '^Plan:|will be' | head -10
           fail "putting the record back does not restore the exact-remainder plan, so the difference measured above is not the record's"; }
    log "  the record's contribution: with it, $R_LINE; without it, $N_LINE (+ wait_for_service_account_token on crash_first); with it again, the remainder"

    R_APPLY="$(cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1)"; R_APPLY_RC=$?
    [ "$R_APPLY_RC" -eq 0 ] || { printf '%s\n' "$R_APPLY" | tail -20; fail "the recovery apply exited $R_APPLY_RC"; }
    grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed" <<< "$R_APPLY" \
      || { printf '%s\n' "$R_APPLY" | tail -5; fail "the recovery apply did not add exactly the one remaining object"; }
    exists_a configmap crash-second || fail "crash-second does not exist after the recovery apply"
    exists_a secret crash-first || fail "crash-first is gone after the recovery apply - the recovery replaced the object the crash created instead of binding it"
    R_REPLAN="$(cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1)"; R_REPLAN_RC=$?
    [ "$R_REPLAN_RC" -eq 0 ] || { printf '%s\n' "$R_REPLAN" | tail -30
      R_ERR="$(grep -E '^Error' <<< "$R_REPLAN" | head -1)"
      fail "the replan after the recovery exited $R_REPLAN_RC: ${R_ERR:-no Error: line; the last 30 lines of the plan are above this verdict in the log}"; }
    grep -q "No changes." <<< "$R_REPLAN" || { printf '%s\n' "$R_REPLAN" | tail -30; fail "the replan after the recovery is not empty"; }
    [ "$(pvc_count_a)" = "0" ] || fail "$(pvc_count_a) PVC(s) carry the estate label after the crash recovery"
    gauntlet_stage day2_crash pass "an apply creating two objects was interrupted by a real SIGTERM (exit $X_RC), delivered by the engine itself inside the -parallelism=1 graph walker the instant kubernetes_config_map_v1.crash_first's create committed (internal/command/apply_e2etesting_crash.go); crash_second reads crash_first's name, so the walker cannot have reached it - kubectl confirms crash-first exists carrying tofu-estate=$ESTATE and crash-second does not. The next plan proposed exactly the remainder ($R_LINE, kubernetes_config_map_v1.crash_second created) and proposed nothing at all for crash-first, which it bound by its label and its namespace and name - not a second create the API server would refuse, not an orphan sweep - matching stock's own plan from the same position on the oracle cluster; the recovery apply added exactly one object, both read back with kubectl, the plan after it is empty and no PVC carries the estate's label. The record store's contribution is read, not counted: the interrupted apply wrote exactly one record (files $X_RECORDS_BEFORE -> $X_RECORDS_AFTER) for kubernetes_secret_v1.crash_first carrying residue $X_RESIDUE, and taking that one file out of the store and replanning from the identical position turns the recovery plan from $R_LINE into $N_LINE, proposing wait_for_service_account_token back on the object the crash left behind; putting it back restores the exact-remainder plan. The crash pair's first object is a Secret and not a ConfigMap for that reason (#1235): a kubernetes_config_map(_v1) has neither a ratified identity row nor a config-only argument, records nothing, and made this line the 12 -> 12 count #1188 could not read anything out of. BREAK_CRASH=1 asserts nothing is proposed and correctly fails; BREAK_CRASH_UNBOUND=1 strips the label off crash-first and the same recovery check correctly fails"
  fi
fi
gauntlet_end_stage

# ── 11. day2_teardown: destroy the adopted estate ────────────────────────
#
# The stage that explains why the PVC behaviour above has never been
# caught: destroying the whole root takes the PVCs with it, because the
# Namespace is in the root and its deletion cascades. An ordinary teardown
# reads clean.
gauntlet_begin_stage day2_teardown
log "=== 11. day2_teardown: apply -destroy on the adopted estate, and stock's own destroy on B ==="
T_PVC_BEFORE="$(kca get pvc -n "$NS" -o name | wc -l | tr -d ' ')"
[ "$T_PVC_BEFORE" -ge 1 ] || fail "no PVC is left to watch the teardown cascade take"
T_EXPECT="$(count_a)"
T_OUT="$(cd "$ADOPTED" && "$TOFU" apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$T_OUT" | tail -30; fail "apply -destroy failed"; }
grep -qF "Resources: 0 added, 0 changed, $T_EXPECT destroyed" <<< "$T_OUT" || { printf '%s\n' "$T_OUT" | tail -5; fail "apply -destroy did not remove exactly the $T_EXPECT remaining objects"; }
# No PVC may be DESTROYED by the apply - the cascade has to be the
# namespace's doing. The type name itself appears in choudoufu's own
# "Not swept" classification prose, which is a listing and not a change,
# so the guard is on the destroy lines rather than on the string.
T_PVC_LINES="$(grep -E 'kubernetes_persistent_volume_claim[a-z0-9_]*\.[^ ]+: (Destroying|Destruction complete)|^[[:space:]]*# kubernetes_persistent_volume_claim[a-z0-9_]*\.[^ ]+ will be destroyed' <<< "$T_OUT")"
[ -z "$T_PVC_LINES" ] || { printf '%s\n' "$T_PVC_LINES"; fail "apply -destroy destroyed a PVC (lines above); no block declares one and none carries the label, so the cascade must be the namespace's doing"; }
for _ in $(seq 1 45); do kca get namespace "$NS" >/dev/null 2>&1 || break; sleep 2; done
kca get namespace "$NS" >/dev/null 2>&1 && fail "the $NS namespace still exists after the destroy"
[ "$(count_a)" = "0" ] || fail "$(count_a) object(s) still carry tofu-estate=$ESTATE after the destroy"
T_PVC_AFTER="$(kca get pvc -A -o name 2>/dev/null | wc -l | tr -d ' ')"
[ "$T_PVC_AFTER" = "0" ] || { kca get pvc -A; fail "$T_PVC_AFTER PVC(s) survive the destroy; the namespace's deletion should have cascaded to all $T_PVC_BEFORE"; }
O_EXPECT="$(stock_b state list 2>/dev/null | wc -l | tr -d ' ')"
O_T="$(stock_b apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$O_T" | tail -10; fail "stock's destroy failed on B"; }
grep -qF "Resources: 0 added, 0 changed, $O_EXPECT destroyed" <<< "$O_T" || fail "stock's destroy on B did not remove exactly the $O_EXPECT objects its state held"
gauntlet_stage day2_teardown pass "apply -destroy removed exactly the $T_EXPECT remaining objects in one apply, naming no PVC; the namespace is gone and no object of any of the estate's eight kinds carries tofu-estate=$ESTATE (kubectl, every namespace). The $T_PVC_BEFORE PVCs nobody declared went with it, to zero cluster-wide, because the Namespace is in the root and its deletion cascades - which is exactly why the day2_remove residue above has never shown up in a teardown; stock's destroy of the same estate on the oracle cluster also removed exactly the $O_EXPECT its state held"

# ── 12. greenfield: the same shape, fresh, with a live block ─────────────
gauntlet_begin_stage greenfield
log "=== 12. greenfield: choudoufu applies the shape fresh on the now-empty cluster A ==="
mkdir -p "$GREEN"
write_config "$GREEN" live
( cd "$GREEN" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "greenfield init failed"
G_OUT="$(cd "$GREEN" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$G_OUT" | tail -30; fail "greenfield apply failed"; }
grep -qF "Apply complete! Resources: 14 added, 0 changed, 0 destroyed" <<< "$G_OUT" || fail "greenfield apply did not add exactly 14 objects"
[ ! -f "$GREEN/terraform.tfstate" ] || fail "a terraform.tfstate appeared after a live-block apply"
[ "$(count_a)" = "14" ] || fail "$(count_a) object(s) carry tofu-estate=$ESTATE after the greenfield apply, want 14"
[ "$(pvc_count_a)" = "0" ] || fail "a PVC carries tofu-estate=$ESTATE after the greenfield apply; a live-block apply must not label what it did not declare"
G_RECORDS="$(gauntlet_record_count "$GREEN/.tofu-records")"
G_PLAN="$(cd "$GREEN" && "$TOFU" plan -input=false -no-color 2>&1)" || fail "the greenfield replan failed"
grep -q "No changes." <<< "$G_PLAN" || { printf '%s\n' "$G_PLAN" | tail -30; fail "the greenfield replan is not empty"; }
rm -f "$GREEN/.terraform/choudoufu-cache.tfstate"
G_PLAN2="$(cd "$GREEN" && "$TOFU" plan -input=false -no-color 2>&1)" || fail "the greenfield replan without the cache failed"
grep -q "No changes." <<< "$G_PLAN2" || { printf '%s\n' "$G_PLAN2" | tail -30; fail "the greenfield replan without the cache is not empty"; }

# ── the lost-store control (#1235) ───────────────────────────────────────
#
# Six AWS estates delete the local record store here and require the next
# plan to come back empty (reference-ec2-vpc/run.sh's A4); no Kubernetes
# estate took the reading at all. It is not the same answer on this
# substrate, and that is the point of taking it: identity re-derives from
# the tofu-estate label plus the configuration's own namespace and name,
# so nothing is created and nothing is swept - but the residue is gone,
# and the plan proposes it back as in-place updates. A lost record store
# costs one converging apply here, not an object (#1188). The "nothing is
# created" half is the same predicate BREAK_CRASH_UNBOUND proves red in
# section 10 of this script: strip the label and the plan proposes a
# create.
rm -rf "$GREEN/.tofu-records" "$GREEN/.terraform/choudoufu-cache.tfstate"
L_PLAN="$(cd "$GREEN" && "$TOFU" plan -input=false -no-color 2>&1)"; L_RC=$?
L_LINE="$(grep -E '^Plan:|^No changes' <<< "$L_PLAN" | head -1 | sed 's/\.$//')"
[ "$L_RC" -eq 0 ] || { printf '%s\n' "$L_PLAN" | tail -20; fail "the plan with no record store at all exited $L_RC"; }
L_GONE="$(grep -cE '^[[:space:]]*# .* will be (created|destroyed|replaced)' <<< "$L_PLAN")"
[ "$L_GONE" = "0" ] || { printf '%s\n' "$L_PLAN" | grep -E 'will be' | head -20
  fail "with the whole record store deleted the plan proposes $L_GONE create/destroy/replace(s) ($L_LINE) - the objects are not being found by their label and their namespace and name alone, so a lost store costs objects and not just an apply"; }
L_CHANGES="$(grep -cE '^[[:space:]]*# .* will be updated in-place' <<< "$L_PLAN")"
L_RESIDUE="$(grep -oE '^[[:space:]]+\+ [a-z_]+ +=' <<< "$L_PLAN" | sed -E 's/^[[:space:]]*\+ //; s/ *=$//' | sort -u | tr '\n' ' ' | sed 's/ $//')"
L_APPLY="$(cd "$GREEN" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$L_APPLY" | tail -20; fail "the apply that should reconverge after a lost record store failed"; }
grep -qF "Apply complete! Resources: 0 added, $L_CHANGES changed, 0 destroyed" <<< "$L_APPLY" \
  || { printf '%s\n' "$L_APPLY" | tail -5; fail "the reconverging apply after a lost record store is not exactly the $L_CHANGES in-place update(s) the plan proposed"; }
L_REPLAN="$(cd "$GREEN" && "$TOFU" plan -input=false -no-color 2>&1)" || fail "the replan after the reconverging apply failed"
grep -q "No changes." <<< "$L_REPLAN" || { printf '%s\n' "$L_REPLAN" | tail -20; fail "the plan after the reconverging apply is not empty - a lost record store costs more than the one apply #1188 measured"; }
[ "$(count_a)" = "14" ] || fail "$(count_a) object(s) carry tofu-estate=$ESTATE after the lost-store reconvergence, want 14"
log "  lost store: $L_LINE, every object still bound; one apply reconverged and the plan after it is empty"
DROP=""; [ "${BREAK:-}" = "1" ] && DROP="statefulset/redis"
inventory "$KCA" "$DROP" > "$WORK/inventory.green.json" || fail "could not read the greenfield inventory"
if [ "${BREAK:-}" = "1" ]; then
  if diff -q "$WORK/inventory.stock.json" "$WORK/inventory.green.json" >/dev/null; then
    fail "BREAK=1: with the redis StatefulSet dropped from the greenfield inventory the two inventories still match - the comparison is not load-bearing"
  fi
  log "  BREAK=1: caught - the inventories differ once the redis StatefulSet is dropped; the real comparison is skipped"
  gauntlet_stage greenfield pass "BREAK=1 control: dropping the redis StatefulSet from the greenfield inventory makes the object-by-object comparison correctly fail; the estate applied (14 added, no terraform.tfstate) and replanned empty with and without the cache"
else
  if ! diff -u "$WORK/inventory.stock.json" "$WORK/inventory.green.json"; then
    fail "the greenfield inventory differs from stock's cold-deploy inventory (diff above)"
  fi
  gauntlet_stage greenfield pass "14 objects applied fresh with a live block and no terraform.tfstate, every one labelled tofu-estate=$ESTATE (kubectl, eight kinds) and none of the three PVCs; the record store held $G_RECORDS file(s); replanned empty with and without the cache. Deleting the whole record store and the cache and replanning - the reading six AWS estates take here and no Kubernetes estate took - proposed $L_LINE: nothing created, nothing destroyed, nothing swept as an orphan, every object still bound by its label and its namespace and name, and $L_CHANGES in-place update(s) putting back the residue the store held (${L_RESIDUE:-none}); one apply reconverged and the plan after it is empty, so a lost store costs an apply here and not an object (#1188, #1235). The cluster's inventory matches stock's cold deploy on the same cluster object by object, labels never compared - ConfigMap and Secret data, each Service's headless-ness, ports and selector, both StatefulSets' replicas, serviceName, containers and volume_claim_templates (claim-template labels included), the Deployment, the PodDisruptionBudget, and the three controller-created PVCs with their merged labels, absent ownerReferences and pvc-protection finalizer. BREAK=1 drops the redis StatefulSet from the expected inventory and the match correctly fails"
fi
( cd "$GREEN" && "$TOFU" apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "greenfield teardown failed"

# ── 13. strict: every toggle on, one refusal ─────────────────────────────
gauntlet_begin_stage strict
STRICT="$WORK/strict"
mkdir -p "$STRICT"
strict_block() { # $1 = the secrets setting under test ("refuse" or "store")
  cat <<EOF
terraform {
  required_providers {
    random = {
      source  = "hashicorp/random"
      version = ">= 3.0"
    }
  }
  live {
    estate = "$ESTATE-strict"
    record_store "local" {
      path = ".tofu-records"
    }
    strict {
      secrets          = "$1"
      no_source_create = "refuse"
      marker_repair    = "never"
      markers "record" {
        types = ["kubernetes_config_map_v1"]
      }
    }
  }
}

resource "random_password" "db" {
  length = 16
}
EOF
}
log "=== 13. strict: every strict toggle on ==="
strict_block "refuse" > "$STRICT/main.tf"
( cd "$STRICT" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "choudoufu init for the strict-stage scratch estate failed"
STRICT_ON="$(cd "$STRICT" && "$TOFU" plan -input=false -no-color 2>&1)"; STRICT_ON_RC=$?
if [ "${BREAK_STRICT:-}" = "1" ]; then
  strict_block "store" > "$STRICT/main.tf"
  STRICT_OFF="$(cd "$STRICT" && "$TOFU" plan -input=false -no-color 2>&1)"; STRICT_OFF_RC=$?
  [ "$STRICT_OFF_RC" -eq 0 ] || { printf '%s\n' "$STRICT_OFF" | tail -30; fail "BREAK_STRICT=1: the plan with secrets = \"store\" exited $STRICT_OFF_RC - a refusal appeared where none should"; }
  grep -q "^Error:" <<< "$STRICT_OFF" && fail "BREAK_STRICT=1: turning secrets off did not clear every refusal"
  grep -qF 'random_password.db will be created' <<< "$STRICT_OFF" || fail "BREAK_STRICT=1: the plan with secrets = \"store\" does not propose creating random_password.db"
  gauntlet_stage strict pass "BREAK_STRICT=1 control: with secrets back to \"store\" the refusal is gone and the plan is an ordinary create; the real check is skipped"
else
  [ "$STRICT_ON_RC" -eq 1 ] || { printf '%s\n' "$STRICT_ON" | tail -30; fail "the every-toggle-on plan exited $STRICT_ON_RC, not the refusal's usual 1"; }
  [ "$(grep -c '^Error:' <<< "$STRICT_ON")" -eq 1 ] || { printf '%s\n' "$STRICT_ON"; fail "every strict toggle on refused more than one thing"; }
  grep -qF 'Error: Logical resource is not admitted' <<< "$STRICT_ON" || { printf '%s\n' "$STRICT_ON"; fail "the one refusal is not \"Logical resource is not admitted\""; }
  grep -qF 'strict { secrets = "refuse" }' <<< "$STRICT_ON" || { printf '%s\n' "$STRICT_ON"; fail "the refusal does not cite strict { secrets = \"refuse\" }"; }
  gauntlet_stage strict pass "every strict toggle on (secrets = refuse, no_source_create = refuse, marker_repair = never with a markers \"record\" selection naming kubernetes_config_map_v1) against a scratch estate carrying random_password.db: exactly one refusal, Logical resource is not admitted under strict { secrets = \"refuse\" }; the other two toggles are on and silent. BREAK_STRICT=1 turns secrets back to \"store\" and the refusal disappears"
fi
gauntlet_end_stage

gauntlet_end
log "reference-k8s-stateful: done"
