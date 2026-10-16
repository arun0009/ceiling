# Capacity

- Inflight per task ≈ requests/second × mean latency in seconds. Use the mean. The tail is a separate line.
- Tasks ≈ ceil(peak requests/second / measured sustainable rate per task at the SLO).
- Usable connections = database limit − reserve − other consumers. Peak tasks, including deployment overlap, × connections per task must fit. `connection_task_limit` is that bound. It is not a license to scale.
- The max is the tightest measured limit. When a limit is missing, say which input is missing and what to keep until it is known.

Fargate: request count per target, in requests per target per minute, when the limit is not CPU. CPU target tracking when CPU is the limit. Memory is a size change before it is a scaling signal. Min comes from availability and startup, not from a fixed task count. Cooldown comes from startup and drain.

Lambda: memory size sets the CPU share. Reserved concurrency is the cap. Unset means the function can take the account limit. Required concurrency ≈ arrival rate × mean duration. Warm environments keep connections; do not multiply the pool by reserved concurrency and call it the database load.

A breaker is for a named remote call whose failure would exhaust a pool, with a defined fast-fail result. Count retries across the SDK, the app, and the event source. The ECS deployment breaker rolls back a deploy. It is not a client breaker.
