"""The terralith the example stands up, generated from the run's prefix.

Deliberately small but shaped like the real problem: a monolith estate that
owns every team's resources, and a per-team config for each team that
declares that team's own subset. Decomposition retags each of a team's
taggable resources out of the monolith into that team's estate with
`live-mv -from-estate`, then a recording apply binds them - no state surgery,
and the untaggable children follow their parent role. The carve is the same
move applied to one dissolving team's resources into another's estate.

The resources are chosen to be free and fast (IAM and CloudWatch log groups,
no EC2, no NAT, nothing hourly) so a rehearsal costs nothing and tears down
in seconds, while still carrying the IAM glue the demo is about: a role, an
inline policy on it (the untaggable child whose orphan recovery the finale
checks), a managed policy and its attachment. The log groups are the cheap
needs-discovery bulk that makes a whole-monolith plan visibly slower than one
team's.
"""

from __future__ import annotations

from . import config

# Three teams keeps the monolith visibly bigger than one estate while staying
# quick to stand up. team-a is the source of the live carve, team-b its
# destination; the third is just population so the contrast is not two-way.
FIXTURE_TEAMS = config.TEAMS  # single source of truth: config.TEAMS

# Log groups per team — the cheap, taggable bulk. Enough that refreshing all
# of them across three teams is the slow whole-monolith plan the demo opens
# with.
LOG_GROUPS_PER_TEAM = 3

_HEADER = """terraform {{
  required_providers {{
    aws = {{
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }}
  }}
  live {{
    estate = "{estate}"
  }}
}}

provider "aws" {{
  region = "{region}"
}}
"""


def _team_resources(cfg: config.Config, team: str) -> str:
    """The HCL for one team's resources. Identical wherever it appears, so the
    monolith and the per-team config declare the same objects and choudoufu
    can adopt them from one estate into the other by their markers alone."""
    role = f"{cfg.prefix}-{team}-role"
    managed = f"{cfg.prefix}-{team}-policy"
    assume = (
        '{ Version = "2012-10-17", Statement = [{ Effect = "Allow", '
        'Principal = { Service = "ec2.amazonaws.com" }, Action = "sts:AssumeRole" }] }'
    )
    inline = (
        '{ Version = "2012-10-17", Statement = [{ Effect = "Allow", '
        'Action = ["logs:CreateLogStream"], Resource = "*" }] }'
    )
    managed_doc = (
        '{ Version = "2012-10-17", Statement = [{ Effect = "Allow", '
        'Action = ["logs:PutLogEvents"], Resource = "*" }] }'
    )
    t = team.replace("-", "_")
    blocks = [
        f'''resource "aws_iam_role" "{t}" {{
  name               = "{role}"
  assume_role_policy = jsonencode({assume})
}}

resource "aws_iam_role_policy" "{t}_inline" {{
  name   = "{cfg.prefix}-{team}-inline"
  role   = aws_iam_role.{t}.id
  policy = jsonencode({inline})
}}

resource "aws_iam_policy" "{t}" {{
  name   = "{managed}"
  policy = jsonencode({managed_doc})
}}

resource "aws_iam_role_policy_attachment" "{t}" {{
  role       = aws_iam_role.{t}.name
  policy_arn = aws_iam_policy.{t}.arn
}}''',
    ]
    for i in range(LOG_GROUPS_PER_TEAM):
        blocks.append(
            f'''resource "aws_cloudwatch_log_group" "{t}_{i}" {{
  name              = "/{cfg.prefix}/{team}/svc-{i}"
  retention_in_days = 1
}}'''
        )
    return "\n\n".join(blocks)


def monolith_hcl(cfg: config.Config, teams: tuple[str, ...] | None = None) -> str:
    """The terralith under one estate. With no `teams` it declares every team,
    the state before anyone splits it. After a decompose the caller passes the
    teams the monolith still owns (often none), so the monolith config stops
    declaring the moved blocks and its own plan stays clean - the live tag
    already decides ownership, but a config that no longer declares a moved
    resource is what keeps a stray monolith plan honest."""
    if teams is None:
        teams = FIXTURE_TEAMS
    parts = [_HEADER.format(estate=cfg.monolith_estate, region=cfg.region)]
    for team in teams:
        parts.append(f"# ---- {team} ----\n" + _team_resources(cfg, team))
    if not teams:
        parts.append("# decomposed - every team split into its own estate")
    return "\n\n".join(parts) + "\n"


def team_hcl(cfg: config.Config, team: str, also: tuple[str, ...] = ()) -> str:
    """One team's slice, under its own estate. Declares exactly the resources
    the monolith already holds for this team, so applying it adopts them by
    retag rather than creating anything new. `also` names other teams whose
    resources this estate ALSO declares - the carve uses it when a dissolving
    team's resources move into this one's estate and configuration."""
    body = _team_resources(cfg, team)
    for other in also:
        body += f"\n\n# ---- adopted from {other} ----\n" + _team_resources(cfg, other)
    return _HEADER.format(estate=cfg.estate(team), region=cfg.region) + "\n" + body + "\n"


def empty_hcl(cfg: config.Config, estate: str) -> str:
    """A config with the live block and no resources: a dissolved estate after
    its resources have been carved into another. Its next plan should propose
    nothing, because it owns nothing any more."""
    return _HEADER.format(estate=estate, region=cfg.region) + "\n# dissolved - every resource moved to another estate\n"


def taggable_addresses(team: str) -> list[str]:
    """The resource addresses that carry a marker and are moved by live-mv when
    this team is decomposed or dissolved. The untaggable children (the inline
    policy and the attachment) are not listed: they follow their parent role."""
    t = team.replace("-", "_")
    addrs = [f"aws_iam_role.{t}", f"aws_iam_policy.{t}"]
    addrs += [f"aws_cloudwatch_log_group.{t}_{i}" for i in range(LOG_GROUPS_PER_TEAM)]
    return addrs


def role_name(cfg: config.Config, team: str) -> str:
    """The IAM role name for a team — the handle the carve and the governance
    guard use to name the resource that moves."""
    return f"{cfg.prefix}-{team}-role"
