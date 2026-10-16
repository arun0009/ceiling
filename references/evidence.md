# Evidence

`p99` is the worst bucket, not the window percentile. Averages are weighted by sample count when CloudWatch sends it. `inflight_per_task` is rate times mean latency. `rps_per_task` divides by average running tasks. Network bytes from Container Insights are already bytes per second. Storage bytes are a sum, divided by the period.

`connection_task_limit` needs the reserve, the other consumers, the per-task pool, and the deployment maximum percent. Otherwise it is omitted and `fit` is `unknown`. Zero means one task does not fit. `fit: yes` is a connection fit only. Lambda does not emit this field.

`assessment.status` covers the last 15 minutes of CPU, memory, errors, throttles, and healthy targets. `pressure_detected` means at least one of these: three buckets at or above 90% average CPU, memory max at or above 90%, any errors or throttles, zero healthy targets, database connections at or above 90% of a known limit, running below desired, or Lambda reserved concurrency of 0. `no_recent_pressure` does not describe the rest of the day. `unknown` means none of those metrics had a fresh window. A missing optional metric stays in `gaps`.

`historical_signals` is pressure in the collection window that is not in the last 15 minutes. The line includes the number. A quiet recent window with an unreadable recent sample says recovery is unverified.

Aurora metrics are the current writer. Lambda `cpu_total_time` is not a CPU percent. Function metrics sum versions. Hours above 48 use 60-minute buckets and cannot show the last 15 minutes.

The collector does not read per-task CPU, queue age, Performance Insights, JVM or Node profiles, DynamoDB, or CloudWatch Logs. Say so when the decision needs them.
