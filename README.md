# ceiling

Capacity facts for one Java or Node service on ECS Fargate or Lambda. The `ceiling` skill writes Fix, Scale, Break, and Now. The command prints JSON and does not change AWS.

Module `github.com/arun0009/ceiling`. Go 1.27. Apache-2.0.

## Claude Code

```bash
ln -sfn /path/to/ceiling ~/.claude/skills/ceiling
```

```text
/ceiling /absolute/path/to/service. Cluster prod, service orders, region us-west-2.
```

Add "audit only" to skip edits.

## Commands

The AWS CLI must already be signed in. Region is `--region`, else `AWS_REGION` or `AWS_DEFAULT_REGION`.

```bash
go run . scan [--app-profile prod] <service-dir>
go run . collect ecs --cluster C --service S --region R [--hours 24]
go run . collect lambda --function NAME --region R [--hours 24]
```

`--profile` is the AWS profile. `--app-profile` is the application profile. Use 3 hours for an incident, 24 for the day, and up to 336 for a weekly peak. The reads are the allowlist in `aws.go`.
