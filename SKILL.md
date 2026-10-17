---
name: ceiling
license: Apache-2.0
description: >-
  Diagnose one Java or Node service on ECS Fargate or Lambda. Read the repo
  and CloudWatch, then prescribe the fix, the task max, the scaling signal,
  and the downstream breakers. Implement a local fix when the evidence
  supports it. Use when asked how much traffic a service can handle, whether
  it is CPU-bound or I/O-bound, or how it should autoscale or break.
---

# Ceiling

Fix the waste, then set a max. Scan findings are leads. A missing metric stays missing.

## Run

Resolve the directory of this file and run the collector from there.

```bash
go run -C <skill-directory> . scan [--app-profile <profile>] <service-directory>
go run -C <skill-directory> . collect ecs --cluster <c> --service <s> --region <r> --profile <aws-profile> --hours <n>
go run -C <skill-directory> . collect lambda --function <name> --region <r> --profile <aws-profile> --hours <n>
```

Collect 3 hours, then 24. Use 336 only for a weekly peak. When the user names an AWS profile, pass it as `--profile`. Omit `--profile` only when they did not name one. Omit `--region` only when that profile has a region configured. If collect fails with an expired token or SSO error, stop and tell them to run `aws sso login --profile <name>`. Do not write a report from gaps.

`--app-profile` is the application profile, such as a Spring profile. It is never the AWS profile. Pass it only when one profile is the one deployed. Otherwise the pool stays unknown. `--pool-max` is the total connections from one task to that database, after runtime overrides. Pass `--db-reserve-connections` and `--db-other-connections` together, and only when both are known. `connection_task_limit` is that connection bound including deployment overlap. It is not a throughput max. Name the database with `--db-cluster` or `--db-instance`. Use `--no-db` only after confirming there is no RDS database. Use `--series` only to align timestamps, and write that output to a temp file.

Scan the deployable module. The scanner covers Java and Node. For anything else, read the code and say so. Do not deploy, change AWS, enable paid telemetry, or load-test production unless the user asks. "audit only" means do not edit the repo.

## Decide

Read [references/evidence.md](references/evidence.md) before naming a cause. Read [runtimes.md](runtimes.md) for the code checks and the starting values. Read [references/capacity.md](references/capacity.md) for the arithmetic. Open every suspect you report.

## Deliver

Follow [references/report.md](references/report.md). The first line names `target.profile` and `identity.account`. One urgent sentence when customers are affected now. Then Fix, Scale, Break, Now. Stay under 700 words unless a finding needs the room. Each item is a change, a keep-current, or the one measurement that blocks the decision. Use a starting value from runtimes.md only when its condition holds, and mark it as a candidate.
