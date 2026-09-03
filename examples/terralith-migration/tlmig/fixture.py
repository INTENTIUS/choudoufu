"""The terralith the example stands up, generated from the run's prefix.

Deliberately small but shaped like the real problem: a monolith estate that
owns every team's resources, and a per-team config for each team that
declares that team's own subset. Decomposition is choudoufu adopting each
team's resources out of the monolith by retagging them — no state surgery —
and the carve is one role moving from one team's estate to another's.

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


def monolith_hcl(cfg: config.Config) -> str:
    """The whole terralith under one estate: every team's resources in a
    single config, the way an org's monolith actually looks before anyone
    splits it."""
    parts = [_HEADER.format(estate=cfg.monolith_estate, region=cfg.region)]
    for team in FIXTURE_TEAMS:
        parts.append(f"# ---- {team} ----\n" + _team_resources(cfg, team))
    return "\n\n".join(parts) + "\n"


def team_hcl(cfg: config.Config, team: str) -> str:
    """One team's slice, under its own estate. Declares exactly the resources
    the monolith already holds for this team, so applying it adopts them by
    retag rather than creating anything new."""
    return _HEADER.format(estate=cfg.estate(team), region=cfg.region) + "\n" + _team_resources(cfg, team) + "\n"


def role_name(cfg: config.Config, team: str) -> str:
    """The IAM role name for a team — the handle the carve and the governance
    guard use to name the resource that moves."""
    return f"{cfg.prefix}-{team}-role"
