# AWS access

The collector uses the current AWS CLI session. It stores nothing. Account id comes from `sts get-caller-identity`. Region is the flag or `AWS_REGION` / `AWS_DEFAULT_REGION`.

The executable allowlist is `aws.go`. Grant the rows you will actually query.

| Area | Actions |
|---|---|
| CloudWatch | `cloudwatch:GetMetricStatistics` |
| ECS | `ecs:DescribeServices`, `ecs:DescribeTaskDefinition`, `ecs:ListTasks`, `ecs:DescribeTasks` |
| Scaling | `application-autoscaling:DescribeScalableTargets`, `application-autoscaling:DescribeScalingPolicies` |
| ALB | `elasticloadbalancing:DescribeLoadBalancerAttributes`, `elasticloadbalancing:DescribeTargetGroups` |
| Lambda | `lambda:GetFunctionConfiguration`, `lambda:GetFunctionConcurrency`, `lambda:GetAccountSettings` |
| RDS | `rds:DescribeDBClusters`, `rds:DescribeDBInstances`, `rds:DescribeDBClusterParameters`, `rds:DescribeDBParameters` |

A missing permission is a gap. It is not a zero.
