# Example

The numbers below are a shape. Do not copy them.

**URGENT: 740 of 800 database connections are in use and the error rate is above the SLO. Do not add tasks.**

## Fix

1. [app] `OrderService.java:44` — move the inventory call to after commit. Proposed. The call sits inside `@Transactional`. Accept when a slow inventory response holds no connection. Roll back if an order is saved without the inventory write the contract requires.
2. [config] `OrderClient.java:18` — read timeout 2s does not fit a 300ms caller budget. Proposed. Set the deadline from inventory's measured tail. Accept when a timeout releases the socket and the order result stays honest.

## Scale

Keep 1 vCPU / 2 GiB. CPU averages 30%. Memory peaks at 65%. Four tasks serve 400 requests/second.

Connection limit unknown: reserve and other consumers were not supplied. Keep max where it is.

Signal candidate, after the hold is fixed and a task has a measured rate: `ALBRequestCountPerTarget`, requests per target per minute.

## Break

`OrderClient.java:18`: no retries while connections are this high. Candidate: open at 50% over 30s with at least 20 calls, half-open one probe after 10s. The whole deadline stays inside 300ms. Accept when an open circuit returns the defined unavailable result.

## Now

orders / Fargate / us-west-2. Last 15 minutes: 400 requests/second, 4 tasks, CPU 30%, memory 65%, errors 4% against a 1% SLO, worst bucket p99 2.8s. Connections 740/800. Task disk throughput was not reported. No 24-hour window in this example.

Leave desired at 4. Alarm on target 5xx above 1% for 3 minutes. Next check is the connection budgets, then a ramp from 400 requests/second.
