#!/usr/bin/env bash
#
# Stand up a throwaway GitLab CE + gitlab-runner + floci, seed a project with
# the generated GitLab pipeline, and leave it ready to run. Modelled on
# gitlab-warden's e2e/bootstrap.sh (#1026 item 3).
#
#   bash examples/ci-pipelines/e2e/gitlab/bootstrap.sh
#   ...                                       # drive it, see README.md
#   docker compose -f examples/ci-pipelines/e2e/gitlab/docker-compose.yml down -v
#
# What it does NOT do: open the merge request, push the staging branch or
# create the schedule. Those are the four separate triggers the pipeline has,
# and README.md's "Driving it" spells each one out as a curl, because which
# one you want depends on which Op you are looking at.
#
# GitLab CE's first boot runs `gitlab-ctl reconfigure`. This waited ~55s on an
# M-series Mac under Docker Desktop and takes several minutes on a CI runner;
# the wait below allows ~25 min.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EXAMPLE="$(cd "$HERE/../.." && pwd)"
COMPOSE="docker compose -f $HERE/docker-compose.yml"

URL="http://localhost:8929"        # the host's way in
NET="choudoufu-gitlab-e2e"         # the network job containers are put on
TOKEN="glpat-choudoufue2etoken12345"
PROJECT="ci-pipelines-e2e"

log() { echo "[bootstrap] $*" >&2; }
api() { curl -fsS -H "PRIVATE-TOKEN: $TOKEN" "$@"; }

log "starting GitLab CE and floci…"
$COMPOSE up -d gitlab floci >&2

# Poll /users/sign_in, not /-/health: monitoring endpoints are IP-restricted
# and a host-side curl arrives via the Docker bridge gateway, so health 403s.
log "waiting for the web UI (GitLab CE cold boot, be patient)…"
for i in $(seq 1 300); do
  if curl -fsS -o /dev/null "${URL}/users/sign_in" 2>/dev/null; then log "serving after ~$((i * 5))s"; break; fi
  sleep 5
  if [ "$i" = "300" ]; then log "GitLab did not start serving in ~25 min"; $COMPOSE logs --tail=80 >&2 || true; exit 1; fi
done

# Health can pass before the rails app accepts runner commands - retry.
log "minting a root access token via gitlab-rails…"
for i in $(seq 1 30); do
  if $COMPOSE exec -T gitlab gitlab-rails runner "
    u = User.find_by_username('root')
    u.personal_access_tokens.where(name: 'choudoufu-e2e').delete_all
    t = u.personal_access_tokens.create!(scopes: ['api'], name: 'choudoufu-e2e', expires_at: 1.day.from_now)
    t.set_token('${TOKEN}'); t.save!
  " >/dev/null 2>&1; then log "token minted"; break; fi
  sleep 10
  if [ "$i" = "30" ]; then log "failed to mint a token via gitlab-rails"; exit 1; fi
done

VERSION="$(api "${URL}/api/v4/version")"
log "GitLab ${VERSION}"

log "creating project root/${PROJECT}…"
PROJECT_ID="$(api -X POST -H 'content-type: application/json' \
  -d "{\"name\":\"${PROJECT}\",\"path\":\"${PROJECT}\",\"visibility\":\"private\",\"default_branch\":\"main\"}" \
  "${URL}/api/v4/projects" | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')"
log "project id ${PROJECT_ID}"

# GitLab 17.x has no registration tokens: a runner is created first (as an
# object, over the API or in the UI) and the authentication token it returns
# is what `gitlab-runner register --token` consumes. The old
# `--registration-token` path is gone.
log "creating a project runner (authentication token, not a registration token)…"
RUNNER_TOKEN="$(api -X POST -H 'content-type: application/json' \
  -d "{\"runner_type\":\"project_type\",\"project_id\":${PROJECT_ID},\"description\":\"choudoufu-e2e-docker\",\"run_untagged\":true,\"locked\":false}" \
  "${URL}/api/v4/user/runners" | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')"

$COMPOSE up -d runner >&2
sleep 5
# network-mode puts the job containers on the same network as GitLab and
# floci, so `gitlab:8929` and `floci:4566` resolve inside a job.
$COMPOSE exec -T runner gitlab-runner register --non-interactive \
  --url "http://gitlab:8929" --token "$RUNNER_TOKEN" \
  --executor docker --docker-image node:22 \
  --docker-network-mode "$NET" --docker-pull-policy if-not-present \
  --docker-volumes /cache >&2
$COMPOSE exec -T runner sed -i 's/^concurrent = .*/concurrent = 4/' /etc/gitlab-runner/config.toml
$COMPOSE restart runner >&2
log "runner registered: $($COMPOSE exec -T runner gitlab-runner --version | head -1)"

# The five the generated file reads, plus the three that point the AWS SDK at
# floci. Every one UNPROTECTED on purpose: a protected variable is not
# exposed to a merge-request pipeline from an unprotected branch, which is
# the trap README.md's "Protected variables" section reproduces.
log "setting CI/CD variables (all unprotected - see README.md)…"
setvar() {
  api -X POST -H 'content-type: application/json' \
    -d "{\"key\":\"$1\",\"value\":\"$2\",\"protected\":false,\"masked\":false,\"raw\":true}" \
    "${URL}/api/v4/projects/${PROJECT_ID}/variables" >/dev/null
  log "  $1"
}
setvar AWS_REGION us-east-1
setvar CHOUDOUFU_PLAN_ROLE_ARN  "arn:aws:iam::000000000000:role/choudoufu-ci-plan"
setvar CHOUDOUFU_ADOPT_ROLE_ARN "arn:aws:iam::000000000000:role/choudoufu-ci-adopt"
setvar CHOUDOUFU_APPLY_ROLE_ARN "arn:aws:iam::000000000000:role/choudoufu-ci-apply"
setvar GITLAB_TOKEN "$TOKEN"
setvar AWS_ENDPOINT_URL "http://floci:4566"
setvar AWS_ACCESS_KEY_ID test
setvar AWS_SECRET_ACCESS_KEY test

# The project root a consumer would lay out: the chant project at the top,
# the generated file beside it, and the .gitlab-ci.yml that includes it.
log "seeding the project…"
SEED="$(mktemp -d)"
trap 'rm -rf "$SEED"' EXIT
cp "$EXAMPLE"/package.json "$EXAMPLE"/package-lock.json "$EXAMPLE"/chant.config.ts "$SEED/"
cp -R "$EXAMPLE"/src "$EXAMPLE"/terraform "$SEED/"
cp "$EXAMPLE"/gitlab/scheduled-ops.gitlab-ci.yml "$SEED/"   # byte-identical to the generated file
cp "$HERE"/overlay/.gitlab-ci.yml "$SEED/"
(
  cd "$SEED"
  git init -q -b main
  git add -A
  git -c user.email=e2e@example.com -c user.name=e2e commit -qm "the generated GitLab pipeline, as a consumer lays it out"
  git remote add origin "http://root:${TOKEN}@localhost:8929/root/${PROJECT}.git"
  git push -q origin main
)

log "ready."
echo "export GITLAB_E2E_URL=${URL}"
echo "export GITLAB_E2E_TOKEN=${TOKEN}"
echo "export GITLAB_E2E_PROJECT_ID=${PROJECT_ID}"
