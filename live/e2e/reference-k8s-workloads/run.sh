#!/usr/bin/env bash
# reference-k8s-workloads: the kubernetes lane's workload-breadth estate
# (#1884, under epic #1885), crossed on the kind substrate.
#
# Hand-written and named so, under #1107's fallback rule: the published
# roots that declare these kinds either reach them through Helm or a cloud
# provider (#1107 pass 2) or are two-to-seven-object modules. The
# "reference-" prefix says which it is, as for reference-k8s and
# reference-k8s-stateful.
#
# ── why this estate ──────────────────────────────────────────────────────
#
# Breadth. The coverage map found no estate declaring any of these typed
# resources, and this one declares all of them:
#
#   kubernetes_job_v1, kubernetes_cron_job_v1, kubernetes_daemon_set_v1,
#   kubernetes_ingress_v1, kubernetes_network_policy_v1,
#   kubernetes_horizontal_pod_autoscaler_v2, kubernetes_persistent_volume_
#   claim_v1 (declared directly, not by a claim template), kubernetes_role_v1,
#   kubernetes_role_binding_v1, kubernetes_limit_range_v1 and
#   kubernetes_resource_quota_v1
#
# plus three of the deprecated non-_v1 aliases #1107 flagged (19 of them
# still exist in hashicorp/kubernetes 3.2.1): kubernetes_daemonset,
# kubernetes_role and kubernetes_network_policy. The aliases chosen are the
# ones whose API is still served on the pinned kind node image. The others
# (kubernetes_cron_job, kubernetes_ingress, kubernetes_pod_disruption_budget
# and the autoscaling/v2beta2 HPA) target beta APIs Kubernetes removed in
# 1.22-1.26, so an apply of them is a provider error on any current cluster
# and measures nothing about choudoufu. reference-k8s already declares
# kubernetes_namespace, kubernetes_config_map, kubernetes_service_account,
# kubernetes_service and kubernetes_deployment by their plain names.
#
# kubernetes_daemonset is the alias that found something while this estate
# was built: internal/live/kubesweep's KindOfType joined its name to
# "Daemonset", where the API server lists DaemonSet, so a declared object of
# that type could not meet its own listing. #1884 fixed the join in the same
# unit (the registry's spelling wins when only case differs); this estate is
# what measures the fix end to end.
#
# ── the shape ────────────────────────────────────────────────────────────
#
# 22 objects in two namespaces, 17 kinds' worth of types, small multi-arch
# images (registry.k8s.io/pause and busybox), no StorageClass:
#
#   refk8swl (the apps namespace)
#     ServiceAccount web, Role + RoleBinding config-reader, ConfigMap
#     web-config, a two-instance count ConfigMap (shard-N) for day2_count,
#     PersistentVolumeClaim web-cache (declared, bound by its first
#     consumer on kind's own standard class), Deployment web (2 replicas,
#     mounts the claim), Service web, Ingress web (no controller: the object
#     is what is measured, not traffic), NetworkPolicy web,
#     HorizontalPodAutoscaler web (min 2, max 4, over the Deployment), and
#     DaemonSet agent (one pod per kind node).
#   refk8swl-batch (the batch namespace)
#     LimitRange defaults, ResourceQuota counts (object counts only, so no
#     pod has to declare requests), Job migrate (completes), CronJob nightly
#     (suspended, so it never fires during the run), and the three
#     deprecated aliases: kubernetes_daemonset legacy-agent, kubernetes_role
#     legacy-reader and kubernetes_network_policy legacy-deny.
#
# ── what this estate measures ────────────────────────────────────────────
#
#   * identity and the sweep for each kind. The controller-made objects are
#     the point: the Job's Pod, the Deployment's ReplicaSet and Pods and the
#     DaemonSets' Pods all carry ownerReferences and must never be proposed;
#     day2_remove labels the Job's Pod with the estate's own label and
#     requires the plan to stay empty, then declares a bare Pod the same way
#     and requires ONE plan to tell the two apart. The CronJob's Jobs are
#     none during the run, asserted with kubectl at cold_deploy and again
#     when its block is removed.
#   * day2_replace on a Job, which is immutable once created: a change to
#     its pod template is a replace on either tool, measured after the
#     shared create_before_destroy rename leg every kind estate runs.
#   * drift_reconverge where a CONTROLLER rewrites a declared field: the
#     HPA's minReplicas is raised out of band, the HPA controller then
#     rewrites the Deployment's spec.replicas, and the plan must propose
#     exactly those two changes, as stock's does on the oracle cluster.
#   * the deprecated aliases plan and apply with their warnings and
#     nothing else: the deprecation warnings choudoufu prints are compared
#     with the ones stock prints for the same configuration, by warning and
#     by address, in cold_deploy and test_plan.
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
#      for the same change" is stock's own plan on its own cluster.
#
# A stage this script cannot pass records fail with the reason and exits,
# as reference-k8s-stateful does; the runner, not the script, decides what
# "clear" means. no_local_state is not reported (not_run), as on every
# kind estate; greenfield takes the lost-store reading instead.
#
#   go run ./tools/gauntlet run reference-k8s-workloads   # one estate, local kind
#   bash live/e2e/reference-k8s-workloads/run.sh
#
# Needs kind, kubectl, terraform (the stock binary), python3 and Docker on
# PATH. No emulator and no FLOCI_PORT: two kind clusters, named after this
# process, so parallel runs never collide.
#
# Env overrides:
#   TOFU_BIN       path to a prebuilt choudoufu binary; skips the `go build`.
#   BREAK          set to 1 to run drift_reconverge's, day2_rename's and
#                  greenfield's negative controls: a third object is
#                  tampered and the exactly-two-changes assertion must fail;
#                  the ServiceAccount's own metadata.name is changed and the
#                  marker-rewritten-in-place assertion must fail; the legacy
#                  DaemonSet is dropped from greenfield's expected inventory
#                  and the object-by-object match must fail.
#   BREAK_REMOVE   set to 1 to keep the CronJob block and assert no destroy
#                  is proposed (day2_remove's own Break line).
#   BREAK_POD      set to 1 to run day2_remove's Job-pod probe WITHOUT the
#                  estate label on the pod and still expect the sweep to
#                  propose it; must fail.
#   BREAK_COUNT    set to 1 to assert the wrong instance was destroyed on the
#                  scale-down (day2_count's Break line); must fail.
#   BREAK_APPROVAL set to 1 to apply the saved plan after the world moved and
#                  expect success (plan_approval's Break line); must fail.
#   BREAK_REPLACE  set to 1 for the shared day2_replace Break line (see
#                  live/e2e/lib/gauntlet.sh's gauntlet_kind_day2_replace);
#                  the Job leg is skipped under it.
#   BREAK_CRASH    set to 1 to assert, after the real interrupt, that nothing
#                  is proposed (day2_crash's Break line); must fail.
#   BREAK_CRASH_UNBOUND
#                  set to 1 to strip the tofu-estate label off the object the
#                  interrupted apply created before replanning; the real
#                  recovery check must then fail to hold.
#   BREAK_STRICT   set to 1 to turn secrets back to "store" and require the
#                  refusal to vanish (strict's Break line).
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# shellcheck source=live/e2e/lib/gauntlet.sh
source "$ROOT/live/e2e/lib/gauntlet.sh"

# The shared provider plugin cache and the cross-process lock real terraform
# needs to use it safely (#1300); see live/e2e/lib/gauntlet.sh.
gauntlet_plugin_cache
ESTATE="reference-k8s-workloads"
NS="refk8swl"
NSB="refk8swl-batch"
# Every kind the estate declares, by resource and group, so a CRD spelled
# like one of them can never be counted. Secrets are here for day2_crash's
# crash-first, the one Secret any stage declares. Pods are NOT here: nothing declares
# one, and pod_count_a below is zero at every stage of a correct run.
KINDS="namespaces serviceaccounts secrets roles.rbac.authorization.k8s.io rolebindings.rbac.authorization.k8s.io configmaps persistentvolumeclaims services deployments.apps daemonsets.apps ingresses.networking.k8s.io networkpolicies.networking.k8s.io horizontalpodautoscalers.autoscaling limitranges resourcequotas jobs.batch cronjobs.batch"
N_KINDS="$(wc -w <<< "$KINDS" | tr -d ' ')"
N_OBJECTS=22
WORK="$(mktemp -d)"
STOCK="$WORK/stock"; ADOPTED="$WORK/adopted"; ORACLE="$WORK/oracle"; GREEN="$WORK/green"
KCA="$WORK/a.kubeconfig"; KCB="$WORK/b.kubeconfig"
CLUSTER_A="chdf-refk8swl-a-$$"; CLUSTER_B="chdf-refk8swl-b-$$"
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

# day2_crash needs the e2eTestingFeatures build, for the engine's own
# TOFU_E2E_APPLY_RESOURCE_INTERRUPT hook; a no-op unless that variable names
# an applied address, so it is identical to $TOFU everywhere else.
mkdir -p "$WORK/bin"
TOFU_CRASH="$WORK/bin/choudoufu-e2e"
( cd "$ROOT" && env -u PWD go build -ldflags="-X 'main.e2eTestingFeatures=yes'" -o "$TOFU_CRASH" ./cmd/choudoufu ) \
  || fail "go build -ldflags e2eTestingFeatures ./cmd/choudoufu failed"
log "  built $TOFU_CRASH (e2eTestingFeatures=yes, for day2_crash's interrupt)"

# ── the shape ────────────────────────────────────────────────────────────
# The hashicorp/kubernetes requirement is live/oracle-versions.json's
# kubernetes_provider_version, read once behind one fail (#1252).
K8S_REQUIRED_PROVIDER="$(gauntlet_kubernetes_required_provider)" \
  || fail "could not read the hashicorp/kubernetes pin from live/oracle-versions.json"

versions_block() { # $1 = "live" to include the live block, anything else for stock
  cat <<EOF
terraform {
  required_providers {
$K8S_REQUIRED_PROVIDER
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

# The configuration's day-2 knobs. Every write_config call reads them, so
# the adopted root and the oracle root are always written from one setting.
#   SA_BLOCK   the ServiceAccount's block name (web, then frontend)
#   SHARDS     the shard ConfigMap's count
#   EXTRA      an extra data line for web-config (plan_approval)
#   DROP_CRON  1 to leave the CronJob block out (day2_remove)
#   JOB_REV    the Job's revision, baked into its pod template (day2_replace)
SA_BLOCK="web"; SHARDS=2; EXTRA=""; DROP_CRON=0; JOB_REV=1

# resource_block writes the 22 objects.
#
# NOTHING here carries a tofu-estate label. cold_deploy asserts that a plain
# stock apply leaves zero labelled objects, which is the whole source of
# genuinely unmarked infrastructure for migrate.
resource_block() {
  cat <<EOF
resource "kubernetes_namespace_v1" "apps" {
  metadata {
    name = "$NS"
  }
}

resource "kubernetes_namespace_v1" "batch" {
  metadata {
    name = "$NSB"
  }
}

# ── the apps namespace ──

resource "kubernetes_service_account_v1" "$SA_BLOCK" {
  metadata {
    name      = "web"
    namespace = "$NS"
  }
  depends_on = [kubernetes_namespace_v1.apps]
}

resource "kubernetes_role_v1" "config_reader" {
  metadata {
    name      = "config-reader"
    namespace = "$NS"
  }
  rule {
    api_groups = [""]
    resources  = ["configmaps"]
    verbs      = ["get", "list", "watch"]
  }
  depends_on = [kubernetes_namespace_v1.apps]
}

# The subject names the ServiceAccount by its literal name, not by the
# block's attribute, so day2_rename's moved block touches nothing here.
resource "kubernetes_role_binding_v1" "config_reader" {
  metadata {
    name      = "config-reader"
    namespace = "$NS"
  }
  role_ref {
    api_group = "rbac.authorization.k8s.io"
    kind      = "Role"
    name      = "config-reader"
  }
  subject {
    kind      = "ServiceAccount"
    name      = "web"
    namespace = "$NS"
  }
  depends_on = [kubernetes_role_v1.config_reader]
}

resource "kubernetes_config_map_v1" "web" {
  metadata {
    name      = "web-config"
    namespace = "$NS"
  }
  data = {
    listen = ":8080"
$EXTRA
  }
  depends_on = [kubernetes_namespace_v1.apps]
}

resource "kubernetes_config_map_v1" "shard" {
  count = $SHARDS
  metadata {
    name      = "shard-\${count.index}"
    namespace = "$NS"
  }
  data = {
    shard = tostring(count.index)
  }
  depends_on = [kubernetes_namespace_v1.apps]
}

# Declared directly, unlike reference-k8s-stateful's claim-template PVCs.
# kind's standard class binds on first consumer, so the provider must not
# wait for a binding the Deployment below is what triggers.
resource "kubernetes_persistent_volume_claim_v1" "cache" {
  metadata {
    name      = "web-cache"
    namespace = "$NS"
  }
  spec {
    access_modes = ["ReadWriteOnce"]
    resources {
      requests = {
        storage = "64Mi"
      }
    }
  }
  wait_until_bound = false
  depends_on       = [kubernetes_namespace_v1.apps]
}

# min_replicas equals the Deployment's replicas, so the HPA has nothing to
# do until drift_reconverge raises its floor out of band. kind runs no
# metrics-server: within [min, max] the HPA cannot compute a recommendation
# and leaves the Deployment alone, and below min it scales up regardless.
resource "kubernetes_horizontal_pod_autoscaler_v2" "web" {
  metadata {
    name      = "web"
    namespace = "$NS"
  }
  spec {
    min_replicas = 2
    max_replicas = 4
    scale_target_ref {
      api_version = "apps/v1"
      kind        = "Deployment"
      name        = "web"
    }
    metric {
      type = "Resource"
      resource {
        name = "cpu"
        target {
          type                = "Utilization"
          average_utilization = 80
        }
      }
    }
  }
  depends_on = [kubernetes_namespace_v1.apps]
}

# depends_on the HPA so an apply that lowers both writes the HPA's floor
# first: lowering the Deployment while the HPA's floor is still the raised
# one would have the controller scale it straight back up.
resource "kubernetes_deployment_v1" "web" {
  metadata {
    name      = "web"
    namespace = "$NS"
    labels = {
      app = "web"
    }
  }
  spec {
    replicas = 2
    selector {
      match_labels = {
        app = "web"
      }
    }
    template {
      metadata {
        labels = {
          app = "web"
        }
      }
      spec {
        service_account_name = "web"
        container {
          name  = "web"
          image = "registry.k8s.io/pause:3.10"
          resources {
            requests = {
              cpu    = "10m"
              memory = "16Mi"
            }
          }
          volume_mount {
            name       = "cache"
            mount_path = "/cache"
          }
        }
        volume {
          name = "cache"
          persistent_volume_claim {
            claim_name = "web-cache"
          }
        }
      }
    }
  }
  depends_on = [kubernetes_horizontal_pod_autoscaler_v2.web, kubernetes_persistent_volume_claim_v1.cache, kubernetes_service_account_v1.$SA_BLOCK]
}

resource "kubernetes_service_v1" "web" {
  metadata {
    name      = "web"
    namespace = "$NS"
  }
  spec {
    selector = {
      app = "web"
    }
    port {
      name        = "http"
      port        = 80
      target_port = 8080
    }
  }
  depends_on = [kubernetes_namespace_v1.apps]
}

# No ingress controller runs on kind here: the object is what is measured.
resource "kubernetes_ingress_v1" "web" {
  metadata {
    name      = "web"
    namespace = "$NS"
  }
  spec {
    rule {
      host = "web.$NS.example"
      http {
        path {
          path      = "/"
          path_type = "Prefix"
          backend {
            service {
              name = "web"
              port {
                number = 80
              }
            }
          }
        }
      }
    }
  }
  depends_on = [kubernetes_service_v1.web]
}

resource "kubernetes_network_policy_v1" "web" {
  metadata {
    name      = "web"
    namespace = "$NS"
  }
  spec {
    pod_selector {
      match_labels = {
        app = "web"
      }
    }
    ingress {
      from {
        pod_selector {}
      }
      ports {
        port     = "8080"
        protocol = "TCP"
      }
    }
    policy_types = ["Ingress"]
  }
  depends_on = [kubernetes_namespace_v1.apps]
}

resource "kubernetes_daemon_set_v1" "agent" {
  metadata {
    name      = "agent"
    namespace = "$NS"
  }
  spec {
    selector {
      match_labels = {
        app = "agent"
      }
    }
    template {
      metadata {
        labels = {
          app = "agent"
        }
      }
      spec {
        container {
          name  = "agent"
          image = "registry.k8s.io/pause:3.10"
        }
      }
    }
  }
  depends_on = [kubernetes_namespace_v1.apps]
}

# ── the batch namespace ──

resource "kubernetes_limit_range_v1" "defaults" {
  metadata {
    name      = "defaults"
    namespace = "$NSB"
  }
  spec {
    limit {
      type = "Container"
      default = {
        cpu    = "100m"
        memory = "64Mi"
      }
      default_request = {
        cpu    = "10m"
        memory = "16Mi"
      }
    }
  }
  depends_on = [kubernetes_namespace_v1.batch]
}

# Object counts only: a compute quota would make every pod in the namespace
# declare requests, which is a property of the pods and not of this estate.
resource "kubernetes_resource_quota_v1" "counts" {
  metadata {
    name      = "counts"
    namespace = "$NSB"
  }
  spec {
    hard = {
      pods                    = "10"
      configmaps              = "10"
      "count/jobs.batch"      = "5"
      "count/cronjobs.batch"  = "2"
    }
  }
  depends_on = [kubernetes_namespace_v1.batch]
}

# Completes. Its pod template is immutable once created, which is what
# day2_replace's Job leg changes (JOB_REV). No ttl_seconds_after_finished:
# a Job that deletes itself is drift nobody made.
resource "kubernetes_job_v1" "migrate" {
  metadata {
    name      = "migrate"
    namespace = "$NSB"
  }
  spec {
    backoff_limit = 2
    template {
      metadata {
        labels = {
          app = "migrate"
        }
      }
      spec {
        restart_policy = "Never"
        container {
          name    = "migrate"
          image   = "busybox:1.37"
          command = ["sh", "-c", "echo migrated rev-$JOB_REV"]
        }
      }
    }
  }
  wait_for_completion = true
  timeouts {
    create = "3m"
    update = "3m"
  }
  depends_on = [kubernetes_limit_range_v1.defaults, kubernetes_resource_quota_v1.counts]
}
EOF
  if [ "$DROP_CRON" != "1" ]; then cat <<EOF

# Suspended, so it never fires during a run whatever the clock says; the
# schedule is still a real one the API server validates.
resource "kubernetes_cron_job_v1" "nightly" {
  metadata {
    name      = "nightly"
    namespace = "$NSB"
  }
  spec {
    schedule = "0 0 1 1 *"
    suspend  = true
    job_template {
      metadata {}
      spec {
        backoff_limit = 1
        template {
          metadata {}
          spec {
            restart_policy = "Never"
            container {
              name    = "nightly"
              image   = "busybox:1.37"
              command = ["sh", "-c", "echo nightly"]
            }
          }
        }
      }
    }
  }
  depends_on = [kubernetes_limit_range_v1.defaults, kubernetes_resource_quota_v1.counts]
}
EOF
  fi
  cat <<EOF

# ── the deprecated aliases (#1107) ──
#
# The same kinds as three _v1 blocks above, under the provider's plain,
# deprecated names, on APIs the cluster still serves.

resource "kubernetes_daemonset" "legacy_agent" {
  metadata {
    name      = "legacy-agent"
    namespace = "$NSB"
  }
  spec {
    selector {
      match_labels = {
        app = "legacy-agent"
      }
    }
    template {
      metadata {
        labels = {
          app = "legacy-agent"
        }
      }
      spec {
        container {
          name  = "agent"
          image = "registry.k8s.io/pause:3.10"
        }
      }
    }
  }
  depends_on = [kubernetes_limit_range_v1.defaults, kubernetes_resource_quota_v1.counts]
}

resource "kubernetes_role" "legacy_reader" {
  metadata {
    name      = "legacy-reader"
    namespace = "$NSB"
  }
  rule {
    api_groups = ["batch"]
    resources  = ["jobs", "cronjobs"]
    verbs      = ["get", "list"]
  }
  depends_on = [kubernetes_namespace_v1.batch]
}

resource "kubernetes_network_policy" "legacy_deny" {
  metadata {
    name      = "legacy-deny"
    namespace = "$NSB"
  }
  spec {
    pod_selector {}
    policy_types = ["Ingress"]
  }
  depends_on = [kubernetes_namespace_v1.batch]
}
EOF
}
write_config() { # $1 dir, $2 live|stock
  { versions_block "$2"; echo; resource_block; } > "$1/main.tf"
}
# write_both writes the adopted root and the oracle root from the knobs.
write_both() { write_config "$ADOPTED" live; write_config "$ORACLE" stock; }

# The 22 objects, KIND NAMESPACE NAME ("-" for cluster-scoped), in the
# order test_plan confirms them and greenfield inventories them.
OBJECTS="namespace - $NS
namespace - $NSB
serviceaccount $NS web
role $NS config-reader
rolebinding $NS config-reader
configmap $NS web-config
configmap $NS shard-0
configmap $NS shard-1
persistentvolumeclaim $NS web-cache
horizontalpodautoscaler $NS web
deployment $NS web
service $NS web
ingress $NS web
networkpolicy $NS web
daemonset $NS agent
limitrange $NSB defaults
resourcequota $NSB counts
job $NSB migrate
cronjob $NSB nightly
daemonset $NSB legacy-agent
role $NSB legacy-reader
networkpolicy $NSB legacy-deny"

# ── cluster helpers ──────────────────────────────────────────────────────
# Every kubectl call here carries a request timeout, so an unreachable
# cluster is a failed call and never a hung stage.
kca() { kubectl --kubeconfig "$KCA" --request-timeout=60s "$@"; }
kcb() { kubectl --kubeconfig "$KCB" --request-timeout=60s "$@"; }
# stock_b runs the stock binary in the oracle root against cluster B.
stock_b() { ( cd "$ORACLE" && KUBECONFIG="$KCB" KUBE_CONFIG_PATH="$KCB" terraform "$@" ); }
# chdf runs choudoufu in the adopted root against cluster A.
chdf() { ( cd "$ADOPTED" && "$TOFU" "$@" ); }
# count_a is the marker count on cluster A, the tagging index's equivalent.
# shellcheck disable=SC2086 # KINDS is a word list on purpose
count_a() { KUBECONFIG="$KCA" gauntlet_kind_count "$ESTATE" $KINDS; }
# pod_count_a is how many pods carry the estate label on cluster A - zero at
# every stage of a correct run: no block declares a pod, and a controller
# copies template labels, never the object's own metadata labels.
pod_count_a() { KUBECONFIG="$KCA" gauntlet_kind_count "$ESTATE" pods; }
# obj_a <kind> <namespace|-> <name>: does the object exist on cluster A.
obj_a() {
  if [ "$2" = "-" ]; then kca get "$1" "$3" >/dev/null 2>&1
  else kca get "$1" "$3" -n "$2" >/dev/null 2>&1; fi
}
plan_line() { grep -E '^Plan:|^No changes' <<< "$1" | head -1 | sed 's/\.$//'; }

# cron_jobs_on <kubeconfig>: how many Jobs in the batch namespace a CronJob
# owns. The CronJob is suspended, so this is zero at every stage.
cron_jobs_on() {
  KUBECONFIG="$1" NSB="$NSB" python3 - <<'PY'
import json, os, subprocess
out = subprocess.run(["kubectl", "--request-timeout=60s", "get", "jobs", "-n", os.environ["NSB"], "-o", "json"], capture_output=True, text=True)
if out.returncode != 0:
    print("ERROR"); raise SystemExit(1)
n = 0
for o in json.loads(out.stdout).get("items", []):
    if any(r.get("kind") == "CronJob" for r in (o["metadata"].get("ownerReferences") or [])):
        n += 1
print(n)
PY
}

# job_pod <kubeconfig>: the name of the Pod the Job controller made for
# migrate, or nothing. Read by the Job's own controller-uid, never by name.
job_pod() {
  KUBECONFIG="$1" NSB="$NSB" python3 - <<'PY'
import json, os, subprocess
ns = os.environ["NSB"]
job = subprocess.run(["kubectl", "--request-timeout=60s", "get", "job", "migrate", "-n", ns, "-o", "json"], capture_output=True, text=True)
if job.returncode != 0:
    raise SystemExit(1)
uid = json.loads(job.stdout)["metadata"]["uid"]
pods = subprocess.run(["kubectl", "--request-timeout=60s", "get", "pods", "-n", ns, "-o", "json"], capture_output=True, text=True)
if pods.returncode != 0:
    raise SystemExit(1)
for o in json.loads(pods.stdout).get("items", []):
    if any(r.get("uid") == uid for r in (o["metadata"].get("ownerReferences") or [])):
        print(o["metadata"]["name"]); break
PY
}

# job_facts <kubeconfig>: the Job's uid, its succeeded count and the command
# its pod template runs, one line - what day2_replace's Job leg compares
# across the replace.
job_facts() {
  KUBECONFIG="$1" NSB="$NSB" python3 - <<'PY'
import json, os, subprocess
out = subprocess.run(["kubectl", "--request-timeout=60s", "get", "job", "migrate", "-n", os.environ["NSB"], "-o", "json"], capture_output=True, text=True)
if out.returncode != 0:
    print("absent"); raise SystemExit(0)
o = json.loads(out.stdout)
cmd = " ".join(o["spec"]["template"]["spec"]["containers"][0].get("command") or [])
print("uid=%s succeeded=%s command=[%s]" % (o["metadata"]["uid"], o.get("status", {}).get("succeeded", 0), cmd))
PY
}

# deprecation_warnings reads a plan or apply's output on stdin and prints
# one normalised line per warning occurrence it can attribute: the warning's
# summary and the address its "with" line names, sorted. Terraform and
# OpenTofu fold repeats into "(and N more similar warnings elsewhere)"; the
# fold count is kept as its own line so a difference there is a difference.
deprecation_warnings() {
  python3 -c '
import re, sys
lines = sys.stdin.read().splitlines()
out = []
summary = None
for ln in lines:
    m = re.match(r"^(Warning|Error): (.*)$", ln)
    if m:
        summary = m.group(1) + ": " + m.group(2).strip() if m.group(1) == "Warning" else None
        continue
    if summary is None:
        continue
    w = re.match(r"^\s+with ([^,]+),", ln)
    if w:
        out.append("%s @ %s" % (summary, w.group(1)))
        continue
    f = re.match(r"^\(and ([0-9]+) more similar warnings? elsewhere\)", ln.strip())
    if f:
        out.append("%s (+%s similar elsewhere)" % (summary, f.group(1)))
    if re.match(r"^(Plan:|No changes|Apply complete|Terraform|OpenTofu)", ln):
        summary = None
for o in sorted(out):
    if re.search(r"deprecat", o, re.I):
        print(o)
'
}

# inventory prints the estate's objects on the cluster $1 names, normalised:
# each object's body without metadata and status (labels and annotations are
# where the two tools legitimately differ), and with the fields the server
# assigns per object scrubbed - a Service's cluster IPs, a claim's bound
# volume, and a Job's controller-uid selector. Then what controllers made,
# counted: the Job's succeeded pods and the CronJob's Jobs. $2 is an object
# key to drop (BREAK's control).
inventory() {
  local cfg="$1" drop="${2:-}"
  KUBECONFIG="$cfg" DROP="$drop" OBJECTS="$OBJECTS" NSB="$NSB" python3 - <<'PY'
import json, os, subprocess
drop = os.environ.get("DROP", "")
SCRUB = {"clusterIP", "clusterIPs", "volumeName"}
def scrub(v):
    if isinstance(v, dict):
        return {k: scrub(x) for k, x in v.items() if k not in SCRUB and "controller-uid" not in k}
    if isinstance(v, list):
        return [scrub(x) for x in v]
    return v
def get(kind, ns, name):
    args = ["kubectl", "--request-timeout=60s", "get", kind, name, "-o", "json"]
    if ns != "-":
        args += ["-n", ns]
    out = subprocess.run(args, capture_output=True, text=True)
    return json.loads(out.stdout) if out.returncode == 0 else None
inv = {}
for line in os.environ["OBJECTS"].splitlines():
    kind, ns, name = line.split()
    key = "%s/%s/%s" % (kind, ns, name)
    o = get(kind, ns, name)
    if o is None:
        inv[key] = None
        continue
    body = {k: v for k, v in o.items() if k not in ("metadata", "status", "apiVersion")}
    inv[key] = scrub(body)
job = get("job", os.environ["NSB"], "migrate")
inv["controller-made/job-migrate-succeeded"] = None if job is None else job.get("status", {}).get("succeeded", 0)
if drop:
    inv.pop(drop, None)
print(json.dumps(inv, sort_keys=True, indent=1))
PY
}

# ── 1. cold_deploy: stock stands the estate up on A (and B, the oracle) ──
gauntlet_begin_stage cold_deploy
log "=== 1. cold_deploy: two kind clusters, stock terraform applies the shape on each ==="
gauntlet_kind_up "$CLUSTER_A" "$KCA" || fail "kind cluster A ($CLUSTER_A) did not come up"
gauntlet_kind_up "$CLUSTER_B" "$KCB" || fail "kind cluster B ($CLUSTER_B) did not come up"
export KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA"
K8S_VERSION="$(kca version 2>/dev/null | gauntlet_k8s_server_version)"
log "  cluster A: ${K8S_VERSION:-unknown server version}; cluster B: $CLUSTER_B"
mkdir -p "$STOCK" "$ORACLE" "$ADOPTED"
write_config "$STOCK" stock
write_config "$ORACLE" stock
( cd "$STOCK" && gauntlet_locked_init terraform init -input=false -no-color >/dev/null 2>&1 ) || fail "the stock init failed on A"
COLD_OUT="$(cd "$STOCK" && terraform apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$COLD_OUT" | tail -40; fail "stock cold deploy failed on A"; }
grep -qF "Apply complete! Resources: $N_OBJECTS added, 0 changed, 0 destroyed" <<< "$COLD_OUT" || { printf '%s\n' "$COLD_OUT" | tail -5; fail "stock cold deploy did not add exactly $N_OBJECTS objects on A"; }
[ -f "$STOCK/terraform.tfstate" ] || fail "stock left no terraform.tfstate on A"
STOCK_N="$(cd "$STOCK" && terraform state list | wc -l | tr -d ' ')"
[ "$STOCK_N" = "$N_OBJECTS" ] || fail "stock's state holds $STOCK_N instances, want $N_OBJECTS"
STOCK_DEPRECATIONS="$(deprecation_warnings <<< "$COLD_OUT")"
log "  stock's deprecation warnings on the cold deploy:"; printf '%s\n' "${STOCK_DEPRECATIONS:-  (none)}" | sed 's/^/    /'
# Stock has to converge on its own estate before anything is compared with
# it: a field the provider and a controller fight over (the HPA and the
# Deployment's replicas above all) would otherwise read as choudoufu's.
STOCK_REPLAN="$(cd "$STOCK" && terraform plan -detailed-exitcode -input=false -no-color 2>&1)"; STOCK_REPLAN_RC=$?
[ "$STOCK_REPLAN_RC" -eq 0 ] || { printf '%s\n' "$STOCK_REPLAN" | grep -E '^Plan:|will be|^ +[~+-] ' | head -30; fail "stock's own replan on A right after its cold deploy exited $STOCK_REPLAN_RC, not 0 - the shape does not converge under stock, so nothing below would be choudoufu's doing: $(plan_line "$STOCK_REPLAN")"; }
UNMARKED="$(count_a)"
[ "$UNMARKED" = "0" ] || fail "$UNMARKED object(s) already carry tofu-estate=$ESTATE after a plain stock apply - this proves nothing"
JOB_DONE="$(kca get job migrate -n "$NSB" -o jsonpath='{.status.succeeded}')"
[ "$JOB_DONE" = "1" ] || fail "the Job migrate reads succeeded=${JOB_DONE:-none} after stock's apply, want 1 (wait_for_completion is on)"
JOB_POD="$(job_pod "$KCA")"
[ -n "$JOB_POD" ] || fail "no Pod owned by the Job migrate exists after it completed; day2_remove's exclusion probe needs one"
CRON_JOBS="$(cron_jobs_on "$KCA")"
[ "$CRON_JOBS" = "0" ] || fail "the suspended CronJob owns $CRON_JOBS Job(s) after the cold deploy, want 0"
PVC_PHASE="$(kca get pvc web-cache -n "$NS" -o jsonpath='{.status.phase}')"
gauntlet_wait_until 120 "the declared PVC web-cache Bound by its first consumer" -- \
  sh -c "kubectl --kubeconfig '$KCA' --request-timeout=30s get pvc web-cache -n '$NS' -o jsonpath='{.status.phase}' | grep -qx Bound" \
  || fail "the declared PVC web-cache is ${PVC_PHASE:-in no phase}, not Bound, after the Deployment mounting it rolled out"
DS_READY="$(kca get daemonset agent -n "$NS" -o jsonpath='{.status.numberReady}')/$(kca get daemonset legacy-agent -n "$NSB" -o jsonpath='{.status.numberReady}')"
NODES="$(kca get nodes -o name | wc -l | tr -d ' ')"
inventory "$KCA" > "$WORK/inventory.stock.json" || fail "could not read the cold-deployed inventory on A"
( cd "$ORACLE" && KUBECONFIG="$KCB" KUBE_CONFIG_PATH="$KCB" gauntlet_locked_init terraform init -input=false -no-color >/dev/null 2>&1 ) || fail "the stock init failed on B"
O_COLD="$(stock_b apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$O_COLD" | tail -20; fail "stock cold deploy failed on B"; }
grep -qF "Apply complete! Resources: $N_OBJECTS added" <<< "$O_COLD" || fail "stock cold deploy did not add exactly $N_OBJECTS objects on B"
gauntlet_stage cold_deploy pass "$N_OBJECTS objects in two namespaces from plain terraform against kind $K8S_VERSION: Job, CronJob (suspended), two DaemonSets, Ingress, two NetworkPolicies, HPA v2 over a two-replica Deployment, a directly declared PVC, two Roles and a RoleBinding, a LimitRange and a count ResourceQuota, plus a Namespace pair, a ServiceAccount, a Service and three ConfigMaps - 19 on the _v1/_v2 types and 3 on the deprecated aliases kubernetes_daemonset, kubernetes_role and kubernetes_network_policy. A real terraform.tfstate with $N_OBJECTS instances, stock's own replan empty, zero tofu-estate labels read back with kubectl over $N_KINDS kinds. The Job completed (succeeded=1, its Pod $JOB_POD), the suspended CronJob owns 0 Jobs, the declared PVC is Bound by its first consumer on kind's standard class, the DaemonSets read $DS_READY ready on $NODES node(s). Stock's deprecation warnings: $(printf '%s' "${STOCK_DEPRECATIONS:-none}" | tr '\n' ';'). The identical shape cold-deployed by stock on a second cluster as every later stage's oracle"

# ── 2. migrate: choudoufu live-import against stock's state ──────────────
gauntlet_begin_stage migrate
log "=== 2. migrate: live-import against the stock state file, read-only then -approve ==="
write_config "$ADOPTED" live
( cd "$ADOPTED" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "the adopted root's choudoufu init failed"
IMPORT_OUT="$(chdf live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -no-color 2>&1)" || { printf '%s\n' "$IMPORT_OUT" | tail -30; fail "live-import (dry run) failed"; }
ELIGIBLE_LINE="$(grep -E 'resource instance\(s\) are eligible for stamping' <<< "$IMPORT_OUT" | head -1)"
log "  dry run: ${ELIGIBLE_LINE:-no eligibility line}"
APPROVE_OUT="$(chdf live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)" || { printf '%s\n' "$APPROVE_OUT" | tail -30; fail "live-import -approve failed"; }
SUMMARY_LINE="$(grep -E 'resource\(s\) newly stamped' <<< "$APPROVE_OUT" | head -1 | sed 's/\.$//')"
log "  approve: ${SUMMARY_LINE:-no summary line}"
LABELLED="$(count_a)"
if grep -qF "$N_OBJECTS resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 0 skipped" <<< "$APPROVE_OUT" && [ "$LABELLED" = "$N_OBJECTS" ]; then
  [ "$(pod_count_a)" = "0" ] || fail "live-import labelled a Pod; nothing in the configuration declares one"
  gauntlet_stage migrate pass "$N_OBJECTS of $N_OBJECTS stamped, 0 failed, 0 skipped ($SUMMARY_LINE); every object carries tofu-estate=$ESTATE, read back with kubectl over $N_KINDS kinds, the three deprecated-alias objects included, and no Pod does - not the Job's, not the Deployment's, not either DaemonSet's"
else
  gauntlet_stage migrate fail "live-import -approve did not stamp all $N_OBJECTS: ${SUMMARY_LINE:-no summary line}; kubectl counts $LABELLED labelled object(s) over the estate's $N_KINDS kinds"
fi

# ── 3. test_plan: replan from nothing ────────────────────────────────────
gauntlet_begin_stage test_plan
log "=== 3. test_plan: choudoufu plan with no state file, identities read with kubectl ==="
PLAN_OUT="$(chdf plan -input=false -no-color 2>&1)" || { printf '%s\n' "$PLAN_OUT" | tail -30; fail "the post-migration plan failed"; }
IDS_OK=1; MISSING=""
while read -r kind ns name; do
  obj_a "$kind" "$ns" "$name" || { IDS_OK=0; MISSING="$MISSING $kind/$ns/$name"; }
done <<< "$OBJECTS"
# The deprecated aliases plan with their warnings and nothing else: the
# deprecation warnings choudoufu prints for this configuration are stock's,
# warning by warning and address by address, read off stock's own plan of
# the identical configuration on the oracle cluster.
O_PLAN_DEP="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN_DEP" | tail -10; fail "stock's plan on B for the deprecation comparison failed"; }
O_DEP="$(deprecation_warnings <<< "$O_PLAN_DEP")"
A_DEP="$(deprecation_warnings <<< "$PLAN_OUT")"
DEP_SAME=1; [ "$O_DEP" = "$A_DEP" ] || DEP_SAME=0
if grep -q "No changes." <<< "$PLAN_OUT" && [ "$IDS_OK" = "1" ] && [ "$DEP_SAME" = "1" ] && ! grep -q '^Error:' <<< "$PLAN_OUT"; then
  gauntlet_stage test_plan pass "the plan with no state file is empty; all $N_OBJECTS identities (KIND NAMESPACE/NAME) confirmed present with kubectl across $N_KINDS kinds and two namespaces, the deprecated-alias objects bound by the same label-and-name join as their _v1 kinds (kubernetes_daemonset's included, which joined to a kind the server does not list until #1884). The deprecation warnings choudoufu printed are exactly stock's for the same configuration on the oracle cluster, by warning and by address: $(printf '%s' "${A_DEP:-none on either side}" | tr '\n' ';'), and no error"
else
  [ "$DEP_SAME" = "1" ] || { log "  deprecation warnings, stock on B vs choudoufu on A:"; diff <(printf '%s\n' "$O_DEP") <(printf '%s\n' "$A_DEP") | sed 's/^/    /'; }
  gauntlet_stage test_plan fail "the plan with no state file: $(plan_line "$PLAN_OUT"); identities confirmed with kubectl: $IDS_OK (missing:${MISSING:- none}); deprecation warnings match stock's: $DEP_SAME (stock: $(printf '%s' "${O_DEP:-none}" | tr '\n' ';') choudoufu: $(printf '%s' "${A_DEP:-none}" | tr '\n' ';')); first error: $(grep -m1 '^Error:' <<< "$PLAN_OUT" || echo none)"
  log "  adopting through choudoufu's own apply so the day-2 stages below run on a labelled estate"
  ADOPT_OUT="$(chdf apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$ADOPT_OUT" | tail -30; fail "the adopting apply failed"; }
  REPLAN="$(chdf plan -input=false -no-color 2>&1)" || fail "the replan after the adopting apply failed"
  grep -q "No changes." <<< "$REPLAN" || { printf '%s\n' "$REPLAN" | tail -30; fail "the replan after the adopting apply is not empty; nothing below would measure day-2 behaviour"; }
fi
[ "$(count_a)" = "$N_OBJECTS" ] || fail "$(count_a) object(s) carry tofu-estate=$ESTATE after adoption, want $N_OBJECTS"

# ── 4. test_apply: no-op apply, marker count unchanged ───────────────────
gauntlet_begin_stage test_apply
log "=== 4. test_apply: apply the empty plan; the labelled-object count must not move ==="
BEFORE_N="$(count_a)"
NOOP_OUT="$(chdf apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$NOOP_OUT" | tail -30; fail "the no-op apply failed"; }
grep -qF "Apply complete! Resources: 0 added, 0 changed, 0 destroyed" <<< "$NOOP_OUT" || { printf '%s\n' "$NOOP_OUT" | tail -5; fail "the no-op apply changed something"; }
AFTER_N="$(count_a)"
[ "$BEFORE_N" = "$AFTER_N" ] || fail "labelled-object count moved across a no-op apply: $BEFORE_N -> $AFTER_N"
[ "$(pod_count_a)" = "0" ] || fail "a Pod gained tofu-estate=$ESTATE across a no-op apply"
A_APPLY_DEP="$(deprecation_warnings <<< "$NOOP_OUT")"
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); objects carrying tofu-estate=$ESTATE unchanged at $BEFORE_N across the estate's $N_KINDS kinds, and still zero Pods, counted with kubectl. The apply's deprecation warnings: $(printf '%s' "${A_APPLY_DEP:-none}" | tr '\n' ';')"

# ── 5. drift_reconverge: the HPA rewrites the Deployment ─────────────────
#
# The out-of-band change is the HPA's floor, raised with kubectl patch. The
# second change is the HPA CONTROLLER's: below its new floor it rewrites the
# Deployment's spec.replicas. So the plan has two objects to put back, and
# only one of them was touched by a person - which is the case a
# declared-field drift test with one patched object cannot reach.
gauntlet_begin_stage drift_reconverge
log "=== 5. drift_reconverge: kubectl raises the HPA's floor on A and on B; the HPA controller rewrites the Deployment ==="
replicas_are() { # $1 kubeconfig, $2 want
  [ "$(kubectl --kubeconfig "$1" --request-timeout=30s get deployment web -n "$NS" -o jsonpath='{.spec.replicas}' 2>/dev/null)" = "$2" ]
}
kca patch hpa web -n "$NS" --type merge -p '{"spec":{"minReplicas":3}}' >/dev/null || fail "could not raise the HPA's floor on A"
kcb patch hpa web -n "$NS" --type merge -p '{"spec":{"minReplicas":3}}' >/dev/null || fail "could not raise the HPA's floor on B"
gauntlet_wait_until 120 "the HPA controller rewrote web's replicas to 3 on A" -- replicas_are "$KCA" 3 \
  || fail "the HPA controller did not rewrite the Deployment's replicas to 3 on A within 120s of its floor being raised; there is no controller-made drift to measure"
gauntlet_wait_until 120 "the HPA controller rewrote web's replicas to 3 on B" -- replicas_are "$KCB" 3 \
  || fail "the HPA controller did not rewrite the Deployment's replicas to 3 on B within 120s"
if [ "${BREAK:-}" = "1" ]; then
  kca patch configmap shard-0 -n "$NS" --type merge -p '{"data":{"shard":"tampered"}}' >/dev/null || fail "BREAK: could not tamper shard-0 on A"
fi
ORACLE_PLAN="$(stock_b plan -detailed-exitcode -input=false -no-color 2>&1)"; ORACLE_RC=$?
[ "$ORACLE_RC" -eq 2 ] || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B after the drift exited $ORACLE_RC, want 2 (changes)"; }
grep -qF "Plan: 0 to add, 2 to change, 0 to destroy." <<< "$ORACLE_PLAN" || { printf '%s\n' "$ORACLE_PLAN" | grep -E '^Plan:|will be'; fail "stock's plan on B does not propose exactly two changes (the HPA's floor and the Deployment's replicas)"; }
{ grep -q "kubernetes_horizontal_pod_autoscaler_v2.web will be updated" <<< "$ORACLE_PLAN" && grep -q "kubernetes_deployment_v1.web will be updated" <<< "$ORACLE_PLAN"; } \
  || fail "stock's plan on B does not name both kubernetes_horizontal_pod_autoscaler_v2.web and kubernetes_deployment_v1.web"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not reconverge B"
DRIFT_PLAN="$(chdf plan -input=false -no-color 2>&1)" || { printf '%s\n' "$DRIFT_PLAN" | tail -30; fail "the plan after the drift failed"; }
drift_is_exact() {
  grep -qF "Plan: 0 to add, 2 to change, 0 to destroy." <<< "$DRIFT_PLAN" || return 1
  grep -q "kubernetes_horizontal_pod_autoscaler_v2.web will be updated" <<< "$DRIFT_PLAN" || return 1
  grep -q "kubernetes_deployment_v1.web will be updated" <<< "$DRIFT_PLAN" || return 1
  grep -qE '~ +min_replicas += 3 -> 2' <<< "$DRIFT_PLAN" || return 1
  grep -qE '~ +replicas += "?3"? -> "?2"?' <<< "$DRIFT_PLAN" || return 1
  return 0
}
if [ "${BREAK:-}" = "1" ]; then
  drift_is_exact && fail "BREAK=1: a third object was tampered but the plan still reads as exactly the HPA's floor and the Deployment's replicas - the exact-two assertion is not load-bearing"
  log "  BREAK=1: caught - with shard-0 also tampered the plan is $(plan_line "$DRIFT_PLAN"); the real check is skipped"
  ( chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: could not reconverge A"
  gauntlet_stage drift_reconverge pass "BREAK=1 control: with a third object tampered the exactly-two-changes assertion correctly fails to hold ($(plan_line "$DRIFT_PLAN")); reconverged afterwards"
else
  drift_is_exact || { printf '%s\n' "$DRIFT_PLAN" | grep -E '^Plan:|will be|^ +~ '; fail "the plan after the drift is not exactly the HPA's min_replicas 3 -> 2 and the Deployment's replicas 3 -> 2: $(plan_line "$DRIFT_PLAN")"; }
  grep -q "shard" <<< "$DRIFT_PLAN" && fail "the plan touches a shard ConfigMap nobody tampered"
  grep -qE 'kubernetes_(replica_set|pod)' <<< "$DRIFT_PLAN" && fail "the plan names a ReplicaSet or a Pod - a controller's copy, never declared"
  RECONV="$(chdf apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$RECONV" | tail -30; fail "the reconverging apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 2 changed, 0 destroyed" <<< "$RECONV" || fail "the reconverging apply did not change exactly two objects"
  MIN="$(kca get hpa web -n "$NS" -o jsonpath='{.spec.minReplicas}')"
  [ "$MIN" = "2" ] || fail "the HPA's minReplicas reads $MIN after reconverging, want 2"
  replicas_are "$KCA" 2 || fail "the Deployment's replicas read $(kca get deployment web -n "$NS" -o jsonpath='{.spec.replicas}') after reconverging, want 2"
  SETTLE="$(chdf plan -input=false -no-color 2>&1)" || fail "the plan after reconverging failed"
  grep -q "No changes." <<< "$SETTLE" || { printf '%s\n' "$SETTLE" | tail -20; fail "the plan after reconverging is not empty - the HPA and the Deployment are still fighting: $(plan_line "$SETTLE")"; }
  gauntlet_stage drift_reconverge pass "the HPA's minReplicas raised 2 -> 3 with kubectl patch; the HPA controller then rewrote the Deployment's spec.replicas 2 -> 3 on its own (waited for, bounded). choudoufu proposed exactly kubernetes_horizontal_pod_autoscaler_v2.web (min_replicas 3 -> 2) and kubernetes_deployment_v1.web (replicas 3 -> 2), 0 add, 2 change, 0 destroy, matching stock's own plan on the oracle cluster for the same drift; no ReplicaSet, Pod or shard ConfigMap was named. The apply changed 2, the HPA's floor first (the Deployment depends on it, so the controller is never left below a raised floor), both read back as configured, and the next plan is empty. BREAK=1 tampers a third object and the exact-two assertion correctly fails"
fi

# ── 6. plan_approval: plan -out, the world moves, apply refuses ──────────
gauntlet_begin_stage plan_approval
log "=== 6. plan_approval: a saved plan, an out-of-band label, a refusal; then the same file applies once the world is back ==="
EXTRA='    reviewed = "yes"'
write_both
P_PLAN="$(chdf plan -out=approved.tfplan -input=false -no-color 2>&1)" || { printf '%s\n' "$P_PLAN" | tail -30; fail "plan -out failed"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$P_PLAN" || { printf '%s\n' "$P_PLAN" | tail -10; fail "the saved plan is not exactly one change"; }
kca label configmap shard-0 -n "$NS" stray=yes >/dev/null || fail "could not move the world (label shard-0) on A"
P_APPLY="$(chdf apply -input=false -no-color approved.tfplan 2>&1)"; P_RC=$?
if [ "${BREAK_APPROVAL:-}" = "1" ]; then
  [ "$P_RC" -eq 0 ] && fail "BREAK_APPROVAL=1: applying the saved plan after the world moved succeeded - the refusal is not load-bearing"
  log "  BREAK_APPROVAL=1: caught - the apply after the world moved exited $P_RC, so 'expect success' correctly fails"
  kca label configmap shard-0 -n "$NS" stray- >/dev/null
  ( chdf apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: the saved plan did not apply once the world was put back"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: stock could not apply the reviewed change on B"
  gauntlet_stage plan_approval pass "BREAK_APPROVAL=1 control: applying the saved plan after the world moved exited $P_RC (refused), so the stage's own Break line correctly fails; applied once the world was put back"
else
  [ "$P_RC" -eq 3 ] || { printf '%s\n' "$P_APPLY" | tail -30; fail "apply of the saved plan after the world moved exited $P_RC, want 3 (the refusal)"; }
  grep -qF "The approved plan no longer matches the live system" <<< "$P_APPLY" || { printf '%s\n' "$P_APPLY" | tail -30; fail "the refusal does not carry its documented sentence"; }
  REVIEWED="$(kca get configmap web-config -n "$NS" -o jsonpath='{.data.reviewed}')"
  [ -z "$REVIEWED" ] || fail "web-config gained reviewed=$REVIEWED despite the refusal"
  kca label configmap shard-0 -n "$NS" stray- >/dev/null || fail "could not put the world back"
  P_APPLY2="$(chdf apply -input=false -no-color approved.tfplan 2>&1)" || { printf '%s\n' "$P_APPLY2" | tail -30; fail "the saved plan did not apply once the world was put back"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$P_APPLY2" || fail "the saved plan's apply did not change exactly one object"
  [ "$(kca get configmap web-config -n "$NS" -o jsonpath='{.data.reviewed}')" = "yes" ] || fail "web-config does not read reviewed=yes after the saved plan applied"
  ( stock_b plan -out=approved.tfplan -input=false -no-color >/dev/null 2>&1 && stock_b apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "stock's own planfile did not apply on B"
  gauntlet_stage plan_approval pass "plan -out wrote one update (web-config gains reviewed=yes); the world then moved out of band (a stray label on shard-0, kubectl, never choudoufu) and apply of the saved plan refused with \"The approved plan no longer matches the live system\" at exit 3, nothing applied (kubectl reads no reviewed key); with the label removed the identical file applied, 0 added, 1 changed, 0 destroyed, and reviewed=yes reads back; stock's own planfile applied on the oracle cluster in the unchanged case. BREAK_APPROVAL=1 expects success after the move and correctly fails"
fi

# ── 7. day2_rename: a moved block, zero churn ────────────────────────────
gauntlet_begin_stage day2_rename
log "=== 7. day2_rename: kubernetes_service_account_v1.web becomes .frontend through a moved block ==="
moved_block() { cat <<'EOF'

moved {
  from = kubernetes_service_account_v1.web
  to   = kubernetes_service_account_v1.frontend
}
EOF
}
if [ "${BREAK:-}" = "1" ]; then
  # A bare block rename plans the same one in-place annotation change as
  # the moved block (the block name is not part of the object's identity),
  # so the control that can fire is a rename of the object's own name.
  python3 - "$ADOPTED/main.tf" <<'PYIN' || fail "BREAK: could not rename the ServiceAccount's metadata.name"
import sys
p = sys.argv[1]; s = open(p).read()
old = 'resource "kubernetes_service_account_v1" "web" {\n  metadata {\n    name      = "web"'
assert old in s
open(p, 'w').write(s.replace(old, old.replace('name      = "web"', 'name      = "web-renamed"'), 1))
PYIN
  R_PLAN="$(chdf plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -30; fail "BREAK: the plan after renaming the object failed"; }
  { grep -q "1 to add" <<< "$R_PLAN" && grep -q "1 to destroy" <<< "$R_PLAN"; } \
    || fail "BREAK=1: renaming the object's own name did not plan a destroy and a create - the marker-rewritten-in-place assertion is not load-bearing: $(plan_line "$R_PLAN")"
  log "  BREAK=1: caught - renaming metadata.name plans $(plan_line "$R_PLAN"); the real moved-block check is skipped"
  SA_BLOCK="frontend"; write_both; moved_block >> "$ADOPTED/main.tf"; moved_block >> "$ORACLE/main.tf"
  ( chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: the moved-block apply failed"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: stock's moved-block apply failed on B"
  gauntlet_stage day2_rename pass "BREAK=1 control: renaming the object's own metadata.name plans a replace ($(plan_line "$R_PLAN")), so the marker-rewritten-in-place assertion correctly fails to hold; the moved block then applied"
else
  SA_BLOCK="frontend"; write_both; moved_block >> "$ADOPTED/main.tf"; moved_block >> "$ORACLE/main.tf"
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan failed on B"; }
  grep -qE "^No changes|Plan: 0 to add, 0 to change, 0 to destroy" <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan on B is not zero churn"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's moved-block apply failed on B"
  R_PLAN="$(chdf plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -30; fail "the moved-block plan failed"; }
  grep -qE 'will be (created|destroyed)' <<< "$R_PLAN" \
    && { printf '%s\n' "$R_PLAN" | grep -E '^  # .+ will be'; fail "the moved-block rename proposes a create or a destroy - not the marker rewritten in place"; }
  grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy.' <<< "$R_PLAN" \
    || { printf '%s\n' "$R_PLAN" | tail -30; fail "the moved-block plan is not exactly one in-place change (the address annotation rewrite)"; }
  grep -qE '~ +"choudoufu\.intentius\.io/tofu-address" = ".*" -> ".*"' <<< "$R_PLAN" \
    || { printf '%s\n' "$R_PLAN"; fail "the moved-block plan does not show the tofu-address annotation being rewritten"; }
  R_APPLY_OUT="$(chdf apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$R_APPLY_OUT" | tail -30; fail "the moved-block apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$R_APPLY_OUT" || { printf '%s\n' "$R_APPLY_OUT" | tail -10; fail "the moved-block apply was not exactly one in-place change"; }
  obj_a serviceaccount "$NS" web || fail "the ServiceAccount is gone after the rename"
  [ "$(count_a)" = "$N_OBJECTS" ] || fail "$(count_a) labelled objects after the rename, want $N_OBJECTS"
  gauntlet_stage day2_rename pass "moved block: kubernetes_service_account_v1.web -> .frontend, no add and no destroy, one in-place change confined to the address annotation rewrite (0 add, 1 change, 0 destroy) - the marker rewritten in place; the live object untouched and still labelled, and the RoleBinding naming it by its unchanged name untouched, read with kubectl; stock's plan for the same moved block on the oracle cluster is zero churn, since stock never writes this annotation. The moved-block half only: live-mv's Kubernetes leg (#1639) is not exercised here. BREAK=1 renames the object's own metadata.name instead, which plans a destroy and a create"
fi
# Both states hold the move now; the moved block is not written again.
write_both

# ── 8. day2_remove: the CronJob's block leaves the configuration ─────────
#
# Two things are measured. The removal itself: the sweep plans the CronJob
# at its orphan address, under the versioned type since no block declares
# the kind any more, and nothing it ever made (none: it is suspended) is
# named. And the controller-copy exclusion, on the Job's own Pod: it carries
# an ownerReference to the Job, so with the estate's label put on it by
# kubectl the sweep must still propose nothing - and a bare Pod labelled the
# same way, in the same namespace, must be proposed in that same plan, so
# the exclusion is not the sweep being blind to Pods.
gauntlet_begin_stage day2_remove
log "=== 8. day2_remove: the CronJob block leaves the configuration ==="
if [ "${BREAK_REMOVE:-}" = "1" ]; then
  K_PLAN="$(chdf plan -input=false -no-color 2>&1)" || fail "BREAK_REMOVE: the plan with the block kept failed"
  grep -qE "will be destroyed|[1-9][0-9]* to destroy" <<< "$K_PLAN" && fail "BREAK_REMOVE=1: with the block kept a destroy was still proposed - the destroy below would not be the block removal's doing"
  log "  BREAK_REMOVE=1: caught - with the block kept the plan proposes no destroy ($(plan_line "$K_PLAN"))"
  DROP_CRON=1; write_both
  ( chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: the removal apply failed afterwards"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: stock's removal apply failed on B afterwards"
  gauntlet_stage day2_remove pass "BREAK_REMOVE=1 control: with the CronJob block kept, no destroy is proposed; the real check is skipped"
else
  [ "$(cron_jobs_on "$KCA")" = "0" ] || fail "the suspended CronJob owns Jobs before its removal; nothing below would be about the CronJob alone"
  DROP_CRON=1; write_both
  grep -q 'kubernetes_cron_job_v1" "nightly"' "$ADOPTED/main.tf" && fail "the CronJob block is still in the adopted root"
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan failed on B"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan on B is not exactly one destroy"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's remove apply failed on B"

  D_PLAN="$(chdf plan -input=false -no-color 2>&1)" || { printf '%s\n' "$D_PLAN" | tail -30; fail "the remove plan failed"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$D_PLAN" || { printf '%s\n' "$D_PLAN" | tail -30; fail "the remove plan is not exactly one destroy"; }
  D_LINE="$(grep -E '^[[:space:]]*# .* will be destroyed' <<< "$D_PLAN" | head -1)"
  D_ADDR="$(sed -E 's/^[[:space:]#]*//; s/ will be destroyed.*$//' <<< "$D_LINE")"
  [ "$D_ADDR" = "kubernetes_cron_job_v1.orphan_${NSB}_nightly" ] || { printf '%s\n' "$D_PLAN" | grep -E 'destroyed|^Plan:'; fail "the one destroy is ${D_ADDR:-unnamed} (line: ${D_LINE:-none}), not the orphan address kubernetes_cron_job_v1.orphan_${NSB}_nightly the sweep plans a label-found CronJob at once no block declares the kind"; }
  grep -q "Owned and undeclared: 1 live resource will be destroyed" <<< "$D_PLAN" || fail "the plan does not say the destroy is an owned, undeclared object"
  D_APPLY="$(chdf apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$D_APPLY" | tail -30; fail "the remove apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 0 changed, 1 destroyed" <<< "$D_APPLY" || fail "the remove apply did not destroy exactly one object"
  obj_a cronjob "$NSB" nightly && fail "the CronJob still exists after the remove apply"
  D_REPLAN="$(chdf plan -input=false -no-color 2>&1)" || fail "the replan after the remove failed"
  grep -q "No changes." <<< "$D_REPLAN" || { printf '%s\n' "$D_REPLAN" | tail -30; fail "the replan after the remove is not empty"; }
  [ "$(count_a)" = "$((N_OBJECTS - 1))" ] || fail "$(count_a) labelled objects after the remove, want $((N_OBJECTS - 1))"

  # The exclusion probe, on the Job's own Pod.
  PROBE_POD="$(job_pod "$KCA")"
  [ -n "$PROBE_POD" ] || fail "the Job migrate owns no Pod to probe the controller-copy exclusion with"
  if [ "${BREAK_POD:-}" != "1" ]; then
    kca label pod "$PROBE_POD" -n "$NSB" "tofu-estate=$ESTATE" >/dev/null || fail "could not label $PROBE_POD for the exclusion probe"
  else
    log "  BREAK_POD=1: the Job's Pod is left unlabelled and the sweep is still expected to propose it"
  fi
  PROBE_PLAN="$(chdf plan -input=false -no-color 2>&1)" || { printf '%s\n' "$PROBE_PLAN" | tail -30; fail "the probe plan failed"; }
  PROBE_ADDR="kubernetes_pod_v1.orphan_${NSB}_${PROBE_POD}"
  PROBE_HIT=0; grep -qF "$PROBE_ADDR will be destroyed" <<< "$PROBE_PLAN" && PROBE_HIT=1

  # The other side of the probe: a Pod nobody's controller made, declared
  # by kubectl and labelled the same way, in the same namespace. One plan
  # must tell the two apart.
  BARE_POD="declared-orphan"
  kca label pod "$PROBE_POD" -n "$NSB" "tofu-estate=$ESTATE" --overwrite >/dev/null || fail "could not label $PROBE_POD for the two-sided arm"
  kca create -n "$NSB" -f - >/dev/null <<YAML || fail "could not create $BARE_POD for the two-sided arm"
apiVersion: v1
kind: Pod
metadata:
  name: $BARE_POD
  labels:
    tofu-estate: $ESTATE
spec:
  restartPolicy: Never
  containers:
  - name: pause
    image: registry.k8s.io/pause:3.10
YAML
  TWO_PLAN="$(chdf plan -input=false -no-color 2>&1)" || { printf '%s\n' "$TWO_PLAN" | tail -30; fail "the two-sided arm's plan failed"; }
  TWO_ADDR="kubernetes_pod_v1.orphan_${NSB}_${BARE_POD}"
  TWO_HIT=0; grep -qF "$TWO_ADDR will be destroyed" <<< "$TWO_PLAN" && TWO_HIT=1
  TWO_EXCLUDED=1; grep -qF "$PROBE_ADDR will be destroyed" <<< "$TWO_PLAN" && TWO_EXCLUDED=0
  TWO_SUMMARY="$(plan_line "$TWO_PLAN")"
  kca delete pod "$BARE_POD" -n "$NSB" --timeout=90s >/dev/null 2>&1
  kca label pod "$PROBE_POD" -n "$NSB" tofu-estate- >/dev/null 2>&1
  TWO_AFTER="$(chdf plan -input=false -no-color 2>&1)" || fail "the plan after the two-sided arm failed"
  grep -q "No changes." <<< "$TWO_AFTER" || { printf '%s\n' "$TWO_AFTER" | tail -30; fail "the plan is not empty again after the two-sided arm cleaned up; the cluster is not back where it started"; }
  [ "$(pod_count_a)" = "0" ] || fail "a Pod still carries tofu-estate=$ESTATE after the two-sided arm cleaned up"
  [ "$TWO_HIT" -eq 1 ] || { printf '%s\n' "$TWO_PLAN" | tail -30; fail "the two-sided arm: the sweep did not propose $TWO_ADDR, a Pod kubectl declared and labelled ($TWO_SUMMARY), so the exclusion probe proves nothing"; }

  if [ "${BREAK_POD:-}" = "1" ]; then
    [ "$PROBE_HIT" -eq 1 ] && fail "BREAK_POD=1: the sweep proposed $PROBE_ADDR with no estate label on it - the probe keys on the kind, not on the label"
    log "  BREAK_POD=1: caught - with no label on the Job's Pod the sweep proposes nothing ($(plan_line "$PROBE_PLAN"))"
    gauntlet_stage day2_remove pass "BREAK_POD=1 control: with no estate label on the Job's Pod the sweep proposes nothing ($(plan_line "$PROBE_PLAN")), so the probe's own expectation correctly fails; the probe keys on the label"
  elif [ "$PROBE_HIT" -eq 1 ] || [ "$TWO_EXCLUDED" -eq 0 ]; then
    gauntlet_stage day2_remove fail "the CronJob's removal passes (exactly one destroy at $D_ADDR, applied, gone, next plan empty, as stock's own removal on the oracle cluster), but the sweep proposed destroying the Job's own Pod $PROBE_POD once kubectl put the estate's label on it ($(plan_line "$PROBE_PLAN")); it carries an ownerReference to the Job migrate, so it is a controller's copy kubesweep.ControllerMade must exclude. The label was removed again and the plan is empty, so nothing was destroyed by this run"
    exit 1
  else
    gauntlet_stage day2_remove pass "deleting kubernetes_cron_job_v1.nightly's block proposed exactly one destroy (0 add, 0 change, 1 destroy) at the sweep's orphan address $D_ADDR (\"Owned and undeclared: 1 live resource will be destroyed\"), under the versioned type since no block declares a CronJob any more; applied cleanly, kubectl reads the CronJob gone, it owned no Job at any point (suspended), and the next plan is empty; stock's plan for the same removal on the oracle cluster is also exactly one destroy. The controller-copy exclusion, on the Job's own Pod $PROBE_POD (ownerReference to the Job): with the estate's label put on it by kubectl the sweep proposed nothing. The two-sided arm then declared a bare Pod with kubectl in the same namespace, labelled the same way, and one plan told them apart: $TWO_SUMMARY, naming $TWO_ADDR and not $PROBE_ADDR. Both were cleaned up, the plan is empty again and no Pod carries the label. BREAK_REMOVE=1 keeps the block and no destroy is proposed; BREAK_POD=1 runs the probe unlabelled and correctly fails"
  fi
fi

# ── 9. day2_count: shards 2 -> 1 -> 2 ────────────────────────────────────
gauntlet_begin_stage day2_count
log "=== 9. day2_count: kubernetes_config_map_v1.shard scales 2 -> 1 -> 2 ==="
SHARDS=1; write_both
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-down plan failed on B"
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's scale-down plan on B is not exactly one destroy"; }
grep -q 'kubernetes_config_map_v1.shard\[1\]' <<< "$O_PLAN" || fail "stock's scale-down on B does not destroy shard[1]"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-down apply failed on B"
C_PLAN="$(chdf plan -input=false -no-color 2>&1)" || { printf '%s\n' "$C_PLAN" | tail -30; fail "the scale-down plan failed"; }
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$C_PLAN" || { printf '%s\n' "$C_PLAN" | tail -30; fail "the scale-down plan is not exactly one destroy"; }
C_LINE="$(grep -E '^[[:space:]]*# .* will be destroyed' <<< "$C_PLAN" | head -1)"
C_ADDR="$(sed -E 's/^[[:space:]#]*//; s/ will be destroyed.*$//' <<< "$C_LINE")"
grep -qE "^kubernetes_config_map(_v1)?\.orphan_${NS}_shard-1$" <<< "$C_ADDR" || { printf '%s\n' "$C_PLAN" | grep -E 'destroyed|^Plan:'; fail "the scale-down destroys ${C_ADDR:-nothing named}, not shard-1 at its orphan address"; }
( chdf apply -auto-approve -input=false -no-color 2>&1 | grep -qF "0 added, 0 changed, 1 destroyed" ) || fail "the scale-down apply did not destroy exactly one object"
if [ "${BREAK_COUNT:-}" = "1" ]; then
  obj_a configmap "$NS" shard-0 || fail "BREAK_COUNT=1: shard-0 was destroyed - the 'wrong instance' assertion would hold, so the check is not load-bearing"
  log "  BREAK_COUNT=1: caught - shard-0 still exists, so asserting it was the one destroyed correctly fails"
  gauntlet_stage day2_count pass "BREAK_COUNT=1 control: asserting the lower index (shard-0) was destroyed correctly fails to hold; the real check is skipped"
  SHARDS=2; write_both
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_COUNT: stock's scale-up apply failed on B"
  ( chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_COUNT: the scale-up apply failed"
else
  obj_a configmap "$NS" shard-0 || fail "shard-0 was destroyed on the scale-down"
  obj_a configmap "$NS" shard-1 && fail "shard-1 still exists after the scale-down"
  SHARDS=2; write_both
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-up plan failed on B"
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's scale-up plan on B is not exactly one add"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-up apply failed on B"
  U_PLAN="$(chdf plan -input=false -no-color 2>&1)" || { printf '%s\n' "$U_PLAN" | tail -30; fail "the scale-up plan failed"; }
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$U_PLAN" || { printf '%s\n' "$U_PLAN" | tail -30; fail "the scale-up plan is not exactly one add"; }
  grep -q 'kubernetes_config_map_v1.shard\[1\]' <<< "$U_PLAN" || fail "the scale-up does not create shard[1]"
  ( chdf apply -auto-approve -input=false -no-color 2>&1 | grep -qF "1 added, 0 changed, 0 destroyed" ) || fail "the scale-up apply did not create exactly one object"
  { obj_a configmap "$NS" shard-0 && obj_a configmap "$NS" shard-1; } || fail "both shards do not exist after the scale-up"
  U_REPLAN="$(chdf plan -input=false -no-color 2>&1)" || fail "the replan after the scale-up failed"
  grep -q "No changes." <<< "$U_REPLAN" || { printf '%s\n' "$U_REPLAN" | tail -30; fail "the replan after the scale-up is not empty"; }
  [ "$(count_a)" = "$((N_OBJECTS - 1))" ] || fail "$(count_a) labelled objects after the count cycle, want $((N_OBJECTS - 1))"
  gauntlet_stage day2_count pass "scaling kubernetes_config_map_v1.shard from 2 to 1 destroyed exactly shard-1, planned at the sweep's orphan address $C_ADDR since the label carries no index (shard-0 untouched, both read with kubectl); back to 2 created exactly kubernetes_config_map_v1.shard[1] under the same name; the next plan is empty; stock's plans for the same two changes on the oracle cluster have the identical shape. BREAK_COUNT=1 asserts the lower index was destroyed and correctly fails"
fi

# ── 9b. day2_replace: the shared rename leg, then a Job ──────────────────
#
# The shared body first (live/e2e/lib/gauntlet.sh's
# gauntlet_kind_day2_replace, every kind estate's): a create_before_destroy
# ConfigMap whose content-hashed name changes. It reports the stage's
# verdict itself, so the Job leg after it re-opens the stage and reports
# the verdict again with both legs in it; the runner keeps the last line.
#
# The Job leg (#1884). A Job's pod template is immutable once created, so a
# change to it is a replace on either tool, and since the name is kept it is
# destroy-then-create (the substrate note's "a replacement that keeps its
# name"). What is asserted is that choudoufu plans the replace stock plans,
# applies it to a new object (a new uid) that completes, leaves no second
# Job behind and replans empty.
gauntlet_begin_stage day2_replace
log "=== 9b. day2_replace: a content-hashed ConfigMap renamed under create_before_destroy, then a Job's immutable template ==="
gauntlet_kind_day2_replace "$ADOPTED" "$ORACLE" "$NS"
if [ "${BREAK_REPLACE:-}" != "1" ]; then
  gauntlet_begin_stage day2_replace
  J_BEFORE="$(job_facts "$KCA")"
  JOB_REV=2; write_both
  O_JPLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_JPLAN" | tail -10; fail "stock's Job replace plan failed on B"; }
  job_replace_shape() { # $1 plan, $2 where
    grep -qF "Plan: 1 to add, 0 to change, 1 to destroy." <<< "$1" || { printf '%s\n' "$1" | grep -E '^Plan:|will be|must be'; fail "$2: the Job's template change is not one add and one destroy: $(plan_line "$1")"; }
    grep -q "# kubernetes_job_v1.migrate must be replaced" <<< "$1" || { printf '%s\n' "$1" | grep -E '# '; fail "$2: kubernetes_job_v1.migrate is not planned as a replace"; }
    grep -q "orphan_" <<< "$1" && { printf '%s\n' "$1" | grep -E '# '; fail "$2: an orphan destroy is planned beside the Job's replace"; }
    return 0
  }
  job_replace_shape "$O_JPLAN" "stock on B (the oracle)"
  O_JAPPLY="$(stock_b apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$O_JAPPLY" | tail -10; fail "stock's Job replace apply failed on B"; }
  J_PLAN="$(chdf plan -input=false -no-color 2>&1)" || { printf '%s\n' "$J_PLAN" | tail -30; fail "the Job replace plan failed"; }
  job_replace_shape "$J_PLAN" "choudoufu"
  J_APPLY="$(chdf apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$J_APPLY" | tail -30; fail "the Job replace apply failed"; }
  grep -qF "Apply complete! Resources: 1 added, 0 changed, 1 destroyed" <<< "$J_APPLY" || { printf '%s\n' "$J_APPLY" | tail -5; fail "the Job replace apply did not add one object and destroy one"; }
  J_AFTER="$(job_facts "$KCA")"
  grep -q 'command=\[sh -c echo migrated rev-2\]' <<< "$J_AFTER" || fail "the Job after the replace reads $J_AFTER, not the rev-2 template"
  grep -q 'succeeded=1 ' <<< "$J_AFTER" || fail "the replacement Job did not complete: $J_AFTER"
  [ "${J_BEFORE%% *}" != "${J_AFTER%% *}" ] || fail "the Job's uid did not change across the replace ($J_BEFORE -> $J_AFTER); nothing was replaced"
  [ "$(kca get jobs -n "$NSB" -o name | wc -l | tr -d ' ')" = "1" ] || fail "more than one Job exists in $NSB after the replace"
  kca get job migrate -n "$NSB" -o jsonpath='{.metadata.labels.tofu-estate}' | grep -qx "$ESTATE" || fail "the replacement Job does not carry tofu-estate=$ESTATE"
  J_REPLAN="$(chdf plan -input=false -no-color 2>&1)" || fail "the replan after the Job replace failed"
  grep -q "No changes." <<< "$J_REPLAN" || { printf '%s\n' "$J_REPLAN" | tail -20; fail "the replan after the Job replace is not empty"; }
  [ "$(pod_count_a)" = "0" ] || fail "a Pod carries tofu-estate=$ESTATE after the Job replace"
  gauntlet_stage day2_replace pass "two legs. The shared create_before_destroy rename (gauntlet_kind_day2_replace) passed first: cfg-a -> cfg-b planned as stock's replace, create first, the new object's create complete before the old one's deposed destroy at -parallelism=1, kubectl reading cfg-b alone with the block's annotation, next plan empty, the block removed again. Then a Job, immutable once created: changing kubernetes_job_v1.migrate's pod template planned '# kubernetes_job_v1.migrate must be replaced', 1 add and 1 destroy, no orphan beside it, the same shape as stock's plan on the oracle cluster; the apply replaced it (uid ${J_BEFORE%% *} -> ${J_AFTER%% *}), the new Job completed with the rev-2 template and carries the estate's label, it is the only Job in $NSB, no Pod carries the label, and the next plan is empty. BREAK_REPLACE=1 runs the shared leg's Break line and skips the Job leg"
fi

# ── 10. day2_crash: an apply of several objects, killed after the first ──
#
# The create_before_destroy rename window first (#1768), shared by every
# kind estate: live/e2e/lib/gauntlet.sh's gauntlet_kind_day2_crash_rename,
# which leaves CRASH_RENAME_DETAIL for every day2_crash verdict below. Then
# reference-k8s-stateful's multi-object window, on the same pair: a Secret
# (which records residue.wait_for_service_account_token, so the record the
# interrupted apply writes has something in it to read, #1235) and a
# ConfigMap that reads the Secret's name, so the walker cannot reach the
# second before the first's create commits.
gauntlet_begin_stage day2_crash
log "=== 10. day2_crash: SIGTERM between the create of one object and the create of the next ==="
gauntlet_kind_day2_crash_rename "$ADOPTED" "$NS"

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
  depends_on = [kubernetes_namespace_v1.apps]
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
X_PLAN="$(chdf plan -input=false -no-color 2>&1)" || { printf '%s\n' "$X_PLAN" | tail -30; fail "the pre-crash plan failed"; }
grep -qF "Plan: 2 to add, 0 to change, 0 to destroy." <<< "$X_PLAN" \
  || { printf '%s\n' "$X_PLAN" | tail -30; fail "the pre-crash plan is not exactly two adds - there is no two-object apply to interrupt"; }
X_RECORDS_BEFORE="$(gauntlet_record_envelope_count "$ADOPTED/.tofu-records")"

# The interrupt is the engine's own, so this runs in the plain foreground.
# A non-zero exit is the NORMAL outcome - the process was killed.
X_OUT="$(cd "$ADOPTED" && TOFU_E2E_APPLY_RESOURCE_INTERRUPT="kubernetes_secret_v1.crash_first" "$TOFU_CRASH" apply -input=false -auto-approve -no-color -parallelism=1 2>&1)"; X_RC=$?
log "  interrupted apply exited $X_RC (a genuine crash is not expected to exit 0)"
[ "$X_RC" -ne 0 ] || { printf '%s\n' "$X_OUT" | tail -20; fail "the interrupted apply exited 0 - the engine's self-signal never landed, so nothing was interrupted"; }
obj_a secret "$NS" crash-first || { printf '%s\n' "$X_OUT" | tail -20; fail "crash-first does not exist after the interrupted apply - the kill landed before the create committed"; }
obj_a configmap "$NS" crash-second && { printf '%s\n' "$X_OUT" | tail -20; fail "crash-second exists after the interrupted apply - the kill landed after both creates"; }
kca get secret -n "$NS" -l "tofu-estate=$ESTATE" -o name 2>/dev/null | grep -qx "secret/crash-first" \
  || fail "crash-first was created by the interrupted apply but does not come back under tofu-estate=$ESTATE"
X_RECORDS_AFTER="$(gauntlet_record_envelope_count "$ADOPTED/.tofu-records")"
X_REC="$(gauntlet_record_file "$ADOPTED/.tofu-records" "kubernetes_secret_v1.crash_first")"
[ -n "$X_REC" ] || fail "the interrupted apply created crash-first but wrote no record for kubernetes_secret_v1.crash_first (records $X_RECORDS_BEFORE -> $X_RECORDS_AFTER)"
[ "$X_RECORDS_AFTER" = "$((X_RECORDS_BEFORE + 1))" ] || fail "records went $X_RECORDS_BEFORE -> $X_RECORDS_AFTER across the interrupted apply, want exactly one more"
X_RESIDUE="$(gauntlet_record_residue "$X_REC" | tr '\n' ' ' | sed 's/ $//')"
[ "$X_RESIDUE" = "wait_for_service_account_token" ] || fail "the record the interrupted apply wrote for kubernetes_secret_v1.crash_first carries residue [${X_RESIDUE:-none}], want wait_for_service_account_token (#1235)"
log "  crash-first exists and is labelled, crash-second does not; one record for crash_first, residue $X_RESIDUE"

if [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  kca label secret crash-first -n "$NS" tofu-estate- >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: could not strip the label off crash-first"
  log "  BREAK_CRASH_UNBOUND=1: stripped tofu-estate off crash-first with kubectl"
fi

R_PLAN="$(chdf plan -input=false -no-color 2>&1)"; R_RC=$?
R_LINE="$(plan_line "$R_PLAN")"
recovered() {
  [ "$R_RC" -eq 0 ] || return 1
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$R_PLAN" || return 1
  grep -qE '^[[:space:]]*# kubernetes_config_map(_v1)?\.crash_second will be created' <<< "$R_PLAN" || return 1
  grep -E '^[[:space:]]*# .* will be' <<< "$R_PLAN" | grep -q 'crash_first\|crash-first' && return 1
  return 0
}

if [ "${BREAK_CRASH:-}" = "1" ]; then
  [ "$R_RC" -eq 0 ] || { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK_CRASH=1: the plan after the interrupt exited $R_RC"; }
  grep -qF "No changes." <<< "$R_PLAN" \
    && { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK_CRASH=1: the plan after a real interrupted two-object apply came back empty, so this stage's own check is not load-bearing"; }
  gauntlet_stage day2_crash pass "BREAK_CRASH=1 control: after the same real interrupt the plan proposes work ($R_LINE), so the stage's own Break line correctly fails to hold; the real check is skipped $CRASH_RENAME_DETAIL"
  ( chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_CRASH: the recovery apply failed afterwards"
elif [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  if recovered; then
    printf '%s\n' "$R_PLAN" | grep -E '^Plan:|will be'
    fail "BREAK_CRASH_UNBOUND=1: the recovery check still holds with crash-first carrying no tofu-estate label"
  fi
  gauntlet_stage day2_crash pass "BREAK_CRASH_UNBOUND=1 control: with the tofu-estate label stripped off the object the interrupted apply created, the recovery check correctly fails to hold ($R_LINE); the real check is skipped $CRASH_RENAME_DETAIL"
  kca delete secret crash-first -n "$NS" >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: could not delete the unlabelled crash-first afterwards"
  ( chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_CRASH_UNBOUND: the apply after the cleanup failed"
else
  if ! recovered; then
    printf '%s\n' "$R_PLAN" | grep -E '^Plan:|^No changes|will be' | head -20
    gauntlet_stage day2_crash fail "the plan after a real interrupt between the create of kubernetes_secret_v1.crash_first and the create of kubernetes_config_map_v1.crash_second is not exactly the remainder: ${R_LINE:-no plan line} (exit $R_RC). crash-first exists carrying tofu-estate=$ESTATE and crash-second does not, both read with kubectl; stock, walked into the same position on the oracle cluster, plans exactly one add (crash_second). The interrupted apply wrote one record for crash_first carrying residue ${X_RESIDUE:-none} $CRASH_RENAME_DETAIL"
    exit 1
  fi
  # The record's contribution, read rather than counted (#1235): take the
  # one record out, replan, put it back.
  mv "$X_REC" "$WORK/crash_first.record" || fail "could not move the crash record aside"
  N_PLAN="$(chdf plan -input=false -no-color 2>&1)"; N_RC=$?
  N_LINE="$(plan_line "$N_PLAN")"
  [ "$N_RC" -eq 0 ] || { printf '%s\n' "$N_PLAN" | tail -20; fail "the replan with the crash record taken out of the store exited $N_RC"; }
  grep -qF "Plan: 1 to add, 1 to change, 0 to destroy." <<< "$N_PLAN" \
    || { printf '%s\n' "$N_PLAN" | grep -E '^Plan:|will be|^ +[+~-] ' | head -20; fail "with the crash record taken out of the store the recovery plan is $N_LINE, not the remainder plus one in-place update"; }
  grep -qE '^[[:space:]]+\+ wait_for_service_account_token +=' <<< "$N_PLAN" \
    || fail "the extra in-place update the missing record produces does not propose wait_for_service_account_token back"
  mv "$WORK/crash_first.record" "$X_REC" || fail "could not put the crash record back"
  B_PLAN="$(chdf plan -input=false -no-color 2>&1)"
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$B_PLAN" || fail "putting the record back does not restore the exact-remainder plan"
  R_APPLY="$(chdf apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$R_APPLY" | tail -20; fail "the recovery apply failed"; }
  grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed" <<< "$R_APPLY" || fail "the recovery apply did not add exactly the one remaining object"
  obj_a configmap "$NS" crash-second || fail "crash-second does not exist after the recovery apply"
  obj_a secret "$NS" crash-first || fail "crash-first is gone after the recovery apply - the recovery replaced the object the crash created instead of binding it"
  R_REPLAN="$(chdf plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_REPLAN" | tail -30; fail "the replan after the recovery failed: $(grep -m1 '^Error' <<< "$R_REPLAN" || echo 'no Error: line')"; }
  grep -q "No changes." <<< "$R_REPLAN" || { printf '%s\n' "$R_REPLAN" | tail -30; fail "the replan after the recovery is not empty"; }
  [ "$(pod_count_a)" = "0" ] || fail "a Pod carries tofu-estate=$ESTATE after the crash recovery"
  gauntlet_stage day2_crash pass "an apply creating two objects was interrupted by a real SIGTERM (exit $X_RC), delivered by the engine itself inside the -parallelism=1 walker the instant kubernetes_secret_v1.crash_first's create committed; crash_second reads crash_first's name, so the walker cannot have reached it - kubectl confirms crash-first exists carrying tofu-estate=$ESTATE and crash-second does not. The next plan proposed exactly the remainder ($R_LINE) and nothing for crash-first, bound by its label and its namespace and name, matching stock's own plan from the same position on the oracle cluster; the recovery apply added exactly one object and the plan after it is empty. The record's contribution is read, not counted: the interrupted apply wrote exactly one record (envelopes $X_RECORDS_BEFORE -> $X_RECORDS_AFTER) carrying residue $X_RESIDUE, and taking it out turns the recovery plan into $N_LINE, proposing wait_for_service_account_token back; putting it back restores the remainder. BREAK_CRASH=1 and BREAK_CRASH_UNBOUND=1 correctly fail $CRASH_RENAME_DETAIL"
fi
gauntlet_end_stage

# ── 11. day2_teardown: destroy the adopted estate ────────────────────────
gauntlet_begin_stage day2_teardown
log "=== 11. day2_teardown: apply -destroy on the adopted estate, and stock's own destroy on B ==="
T_EXPECT="$(count_a)"
T_OUT="$(chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$T_OUT" | tail -30; fail "apply -destroy failed"; }
grep -qF "Resources: 0 added, 0 changed, $T_EXPECT destroyed" <<< "$T_OUT" || { printf '%s\n' "$T_OUT" | tail -5; fail "apply -destroy did not remove exactly the $T_EXPECT remaining labelled objects"; }
T_CTRL="$(grep -E 'kubernetes_(pod|replica_set)[a-z0-9_]*\.[^ ]+: (Destroying|Destruction complete)' <<< "$T_OUT")"
[ -z "$T_CTRL" ] || { printf '%s\n' "$T_CTRL"; fail "apply -destroy destroyed a Pod or a ReplicaSet (lines above); those are controllers' copies, never declared"; }
ns_gone() { ! kubectl --kubeconfig "$KCA" --request-timeout=30s get namespace "$1" >/dev/null 2>&1; }
gauntlet_wait_until 180 "namespace $NS gone" -- ns_gone "$NS" || fail "the $NS namespace still exists 180s after the destroy"
gauntlet_wait_until 180 "namespace $NSB gone" -- ns_gone "$NSB" || fail "the $NSB namespace still exists 180s after the destroy"
[ "$(count_a)" = "0" ] || fail "$(count_a) object(s) still carry tofu-estate=$ESTATE after the destroy"
O_EXPECT="$(stock_b state list 2>/dev/null | wc -l | tr -d ' ')"
O_T="$(stock_b apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$O_T" | tail -10; fail "stock's destroy failed on B"; }
grep -qF "Resources: 0 added, 0 changed, $O_EXPECT destroyed" <<< "$O_T" || fail "stock's destroy on B did not remove exactly the $O_EXPECT objects its state held"
gauntlet_stage day2_teardown pass "apply -destroy removed exactly the $T_EXPECT remaining labelled objects in one apply, naming no Pod or ReplicaSet; both namespaces are gone (waited for, bounded) and no object of any of the estate's $N_KINDS kinds carries tofu-estate=$ESTATE (kubectl, every namespace); the Job's Pod, the Deployment's ReplicaSet and Pods and both DaemonSets' Pods went with their owners and namespaces; stock's destroy of the same estate on the oracle cluster also removed exactly the $O_EXPECT its state held"

# ── 12. greenfield: the same shape, fresh, with a live block ─────────────
gauntlet_begin_stage greenfield
log "=== 12. greenfield: choudoufu applies the shape fresh on the now-empty cluster A ==="
mkdir -p "$GREEN"
SA_BLOCK="web"; SHARDS=2; EXTRA=""; DROP_CRON=0; JOB_REV=1
write_config "$GREEN" live
( cd "$GREEN" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "the greenfield root's choudoufu init failed"
G_OUT="$(cd "$GREEN" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$G_OUT" | tail -30; fail "greenfield apply failed"; }
grep -qF "Apply complete! Resources: $N_OBJECTS added, 0 changed, 0 destroyed" <<< "$G_OUT" || fail "greenfield apply did not add exactly $N_OBJECTS objects"
[ ! -f "$GREEN/terraform.tfstate" ] || fail "a terraform.tfstate appeared after a live-block apply"
[ "$(count_a)" = "$N_OBJECTS" ] || fail "$(count_a) object(s) carry tofu-estate=$ESTATE after the greenfield apply, want $N_OBJECTS"
[ "$(pod_count_a)" = "0" ] || fail "a Pod carries tofu-estate=$ESTATE after the greenfield apply"
G_DEP="$(deprecation_warnings <<< "$G_OUT")"
[ "$G_DEP" = "$STOCK_DEPRECATIONS" ] || { diff <(printf '%s\n' "$STOCK_DEPRECATIONS") <(printf '%s\n' "$G_DEP"); fail "the greenfield apply's deprecation warnings differ from stock's cold-deploy apply of the same configuration (diff above)"; }
gauntlet_wait_until 120 "the declared PVC web-cache Bound on the greenfield apply" -- \
  sh -c "kubectl --kubeconfig '$KCA' --request-timeout=30s get pvc web-cache -n '$NS' -o jsonpath='{.status.phase}' | grep -qx Bound" \
  || fail "the declared PVC web-cache did not reach Bound after the greenfield apply"
G_RECORDS="$(gauntlet_record_envelope_count "$GREEN/.tofu-records")"
G_PLAN="$(cd "$GREEN" && "$TOFU" plan -input=false -no-color 2>&1)" || fail "the greenfield replan failed"
grep -q "No changes." <<< "$G_PLAN" || { printf '%s\n' "$G_PLAN" | tail -30; fail "the greenfield replan is not empty"; }
rm -f "$GREEN/.terraform/choudoufu-cache.tfstate"
G_PLAN2="$(cd "$GREEN" && "$TOFU" plan -input=false -no-color 2>&1)" || fail "the greenfield replan without the cache failed"
grep -q "No changes." <<< "$G_PLAN2" || { printf '%s\n' "$G_PLAN2" | tail -30; fail "the greenfield replan without the cache is not empty"; }

# The lost-store control (#1235), as reference-k8s-stateful takes it:
# identity re-derives from the label plus the configuration's namespace and
# name, so nothing is created or swept; the residue is proposed back as
# in-place updates, and one apply reconverges.
rm -rf "$GREEN/.tofu-records" "$GREEN/.terraform/choudoufu-cache.tfstate"
L_PLAN="$(cd "$GREEN" && "$TOFU" plan -input=false -no-color 2>&1)"; L_RC=$?
L_LINE="$(plan_line "$L_PLAN")"
[ "$L_RC" -eq 0 ] || { printf '%s\n' "$L_PLAN" | tail -20; fail "the plan with no record store at all exited $L_RC"; }
L_GONE="$(grep -cE '^[[:space:]]*# .* will be (created|destroyed|replaced)' <<< "$L_PLAN")"
[ "$L_GONE" = "0" ] || { printf '%s\n' "$L_PLAN" | grep -E 'will be' | head -20; fail "with the whole record store deleted the plan proposes $L_GONE create/destroy/replace(s) ($L_LINE) - a lost store costs objects, not just an apply"; }
L_CHANGES="$(grep -cE '^[[:space:]]*# .* will be updated in-place' <<< "$L_PLAN")"
L_RESIDUE="$(grep -oE '^[[:space:]]+\+ [a-z_]+ +=' <<< "$L_PLAN" | sed -E 's/^[[:space:]]*\+ //; s/ *=$//' | sort -u | tr '\n' ' ' | sed 's/ $//')"
L_APPLY="$(cd "$GREEN" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$L_APPLY" | tail -20; fail "the apply that should reconverge after a lost record store failed"; }
grep -qF "Apply complete! Resources: 0 added, $L_CHANGES changed, 0 destroyed" <<< "$L_APPLY" \
  || { printf '%s\n' "$L_APPLY" | tail -5; fail "the reconverging apply after a lost record store is not exactly the $L_CHANGES in-place update(s) the plan proposed"; }
L_REPLAN="$(cd "$GREEN" && "$TOFU" plan -input=false -no-color 2>&1)" || fail "the replan after the reconverging apply failed"
grep -q "No changes." <<< "$L_REPLAN" || { printf '%s\n' "$L_REPLAN" | tail -20; fail "the plan after the reconverging apply is not empty"; }
[ "$(count_a)" = "$N_OBJECTS" ] || fail "$(count_a) object(s) carry tofu-estate=$ESTATE after the lost-store reconvergence, want $N_OBJECTS"
log "  lost store: $L_LINE, every object still bound; one apply reconverged and the plan after it is empty"
DROP=""; [ "${BREAK:-}" = "1" ] && DROP="daemonset/$NSB/legacy-agent"
inventory "$KCA" "$DROP" > "$WORK/inventory.green.json" || fail "could not read the greenfield inventory"
if [ "${BREAK:-}" = "1" ]; then
  diff -q "$WORK/inventory.stock.json" "$WORK/inventory.green.json" >/dev/null \
    && fail "BREAK=1: with the legacy DaemonSet dropped from the greenfield inventory the two inventories still match - the comparison is not load-bearing"
  gauntlet_stage greenfield pass "BREAK=1 control: dropping the legacy DaemonSet from the greenfield inventory makes the object-by-object comparison correctly fail; the estate applied ($N_OBJECTS added, no terraform.tfstate) and replanned empty with and without the cache"
else
  diff -u "$WORK/inventory.stock.json" "$WORK/inventory.green.json" || fail "the greenfield inventory differs from stock's cold-deploy inventory (diff above)"
  gauntlet_stage greenfield pass "$N_OBJECTS objects applied fresh with a live block and no terraform.tfstate, every one labelled tofu-estate=$ESTATE (kubectl, $N_KINDS kinds) and no Pod; the apply's deprecation warnings are exactly stock's cold-deploy apply's; the record store held $G_RECORDS record envelope(s); replanned empty with and without the cache. Deleting the whole record store and the cache and replanning proposed $L_LINE: nothing created, destroyed or swept, every object still bound by its label and its namespace and name, and $L_CHANGES in-place update(s) putting back the residue the store held (${L_RESIDUE:-none}); one apply reconverged and the plan after it is empty. The cluster's inventory matches stock's cold deploy on the same cluster object by object - every object's spec, data, rules, role reference and subjects with metadata and status left out and server-assigned cluster IPs, bound volume and controller-uid selectors scrubbed - and the Job completed again. BREAK=1 drops the legacy DaemonSet from the expected inventory and the match correctly fails"
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
  [ "$STRICT_OFF_RC" -eq 0 ] || { printf '%s\n' "$STRICT_OFF" | tail -30; fail "BREAK_STRICT=1: the plan with secrets = \"store\" exited $STRICT_OFF_RC"; }
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
log "reference-k8s-workloads: done"
