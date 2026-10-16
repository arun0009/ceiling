# Runtime checks

Scanner output is a lead. Read the file and the config that is actually deployed.

## Java

Heap after GC, allocation rate, direct buffers, and thread stacks are separate from the task RSS. A transaction that calls HTTP keeps the connection. Open-in-view left unset is true on Spring Boot. A timeout belongs on the client that blocks the request. `.block()` on the event loop is not the same as blocking a worker thread. More threads do not add database connections.

## Node

One process is one event loop. A hot loop can pin one core while the task average looks idle. Bound `Promise.all`, the cache, and the body. A TTL does not cap the number of keys.

## Both

Password hashing stays correct. Bound how many run at once. A cache without `maximumSize`, `maximumWeight`, `max`, or `maxKeys` grows with cardinality.

## Starting points

Use the service's own value when it has one. These apply when it does not, and only when the condition holds. Say they are candidates.

| Setting | Candidate | Condition |
|---|---|---|
| Connect / read | 1s / 2s, plus a total deadline | The caller budget covers connect, read, and every attempt. A 300ms SLO cannot use 2s. |
| Retries | 1, with jitter. 0 when the call is not idempotent | Transient failures only. Count the SDK and the proxy. None during overload. |
| Breaker | 50% of failures over 30s, at least 20 calls; open 10s; one probe | The dependency is busy and fail-fast has a defined result. Low traffic needs a wider window. |
| Scale-in | 180s | Startup and drain fit in 180s. Lengthen it when they do not. |
| Request-count target | sustainable requests/second per target × 60 × headroom | Requests per target per minute. Do not copy a per-second number into the policy. |

There is no default task max. Name the missing budget and the cap to keep until it is measured.
