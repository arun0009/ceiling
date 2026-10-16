# Fix / Scale / Break / Now

One **URGENT** sentence when fresh data shows customer impact or an exhausted resource. High utilization alone is **AT RISK**.

## Fix

Three to five items, more only when the extra defect changes the limit. Each line: `[app]` or `[config]`, `file:line`, the change, and whether it is implemented or proposed. Name the test that accepts it. "Optimize queries" needs the query or the measurement that will find it.

Honor "audit only". Local test success is not a production result.

## Scale

Current → proposed CPU, memory, min, max, signal, target, and units. Name the first limit and whether more tasks help it or make it worse. One line for the connection arithmetic. End on the min, max, signal, and units, or "keep current" when that is the decision.

Add a growth table only when the user names a target rate. Columns: target, required capacity, first limit, action. An unknown capacity names the load test and the interim cap.

## Break

Only the clients that need a change. For each: deadline, retries, inflight bound, trip rule, and fallback. A candidate value names the fault test that accepts it. Skip a client that already has them.

## Now

Account, region, target, and the windows. Recent rate, CPU, memory, latency, errors, and the database, plus what is missing. Put the last 15 minutes next to the 24-hour peak. Worst-bucket p99 is labeled as a bucket. A historical signal is included when the recent window is quiet. End with the desired count today, one alarm on the first limit, and the next check.
