package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

type fakeAWS struct {
	t     *testing.T
	calls [][]string
	fail  string
}

func arg(args []string, flag string) string {
	for i, v := range args {
		if v == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
func (f *fakeAWS) Call(args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	if err := allow(args); err != nil {
		return nil, err
	}
	key := strings.Join(args[:2], " ")
	if strings.Contains(strings.Join(args, " "), f.fail) && f.fail != "" {
		return nil, fmt.Errorf("AccessDenied")
	}
	var out any
	switch key {
	case "sts get-caller-identity":
		return []byte(`{"Account":"123456789012","Arn":"arn:aws:iam::123456789012:role/reader"}`), nil
	case "ecs describe-services":
		return []byte(`{"services":[{"status":"ACTIVE","desiredCount":4,"runningCount":4,"launchType":"FARGATE","taskDefinition":"orders:3","deploymentConfiguration":{"maximumPercent":200},"loadBalancers":[{"targetGroupArn":"arn:aws:elasticloadbalancing:us-west-2:123:targetgroup/orders/abc"}]}]}`), nil
	case "ecs describe-task-definition":
		return []byte(`{"taskDefinition":{"cpu":"1024","memory":"2048"}}`), nil
	case "ecs list-tasks":
		return []byte(`{"taskArns":[]}`), nil
	case "application-autoscaling describe-scalable-targets":
		return []byte(`{"ScalableTargets":[{"MinCapacity":2,"MaxCapacity":10}]}`), nil
	case "application-autoscaling describe-scaling-policies":
		return []byte(`{"ScalingPolicies":[]}`), nil
	case "elbv2 describe-target-groups":
		return []byte(`{"TargetGroups":[{"LoadBalancerArns":["arn:aws:elasticloadbalancing:us-west-2:123:loadbalancer/app/orders/abc"]}]}`), nil
	case "elbv2 describe-load-balancer-attributes":
		return []byte(`{"Attributes":[{"Key":"idle_timeout.timeout_seconds","Value":"60"}]}`), nil
	case "lambda get-function-configuration":
		return []byte(`{"FunctionName":"orders","MemorySize":1024,"Timeout":15,"Runtime":"nodejs22.x"}`), nil
	case "lambda get-function-concurrency":
		return []byte(`{"ReservedConcurrentExecutions":0}`), nil
	case "lambda get-account-settings":
		return []byte(`{"AccountLimit":{"ConcurrentExecutions":1000}}`), nil
	case "rds describe-db-instances":
		return []byte(`{"DBInstances":[{"Engine":"postgres","MonitoringInterval":0,"DBParameterGroups":[{"DBParameterGroupName":"orders"}]}]}`), nil
	case "rds describe-db-parameters":
		return []byte(`"800"`), nil
	case "rds describe-db-clusters":
		return []byte(`{"DBClusters":[{"Engine":"aurora-postgresql","DBClusterMembers":[{"DBInstanceIdentifier":"writer","IsClusterWriter":true},{"DBInstanceIdentifier":"reader","IsClusterWriter":false}]}]}`), nil
	case "cloudwatch get-metric-statistics":
		if slices.Contains(args, "--statistics") && slices.Contains(args, "--extended-statistics") {
			return nil, fmt.Errorf("InvalidParameterCombination")
		}
		if arg(args, "--metric-name") == "RunningTaskCount" && arg(args, "--namespace") != "ECS/ContainerInsights" {
			return []byte(`{"Datapoints":[]}`), nil
		}
		end, _ := parseTime(arg(args, "--end-time"))
		period := atoi(arg(args, "--period"))
		points := []cwPoint{}
		for i := 4; i >= 1; i-- {
			p := cwPoint{Timestamp: iso(end.Add(-time.Duration(i*period) * time.Second)), Unit: "Count"}
			if slices.Contains(args, "--extended-statistics") {
				p.Unit = "Seconds"
				p.ExtendedStatistics = map[string]float64{"p99": 0.4}
			} else {
				avg, max, sum := 20.0, 30.0, 0.0
				switch arg(args, "--metric-name") {
				case "CPUUtilization":
					avg = 95
					max = 99
					p.Unit = "Percent"
				case "MemoryUtilization":
					avg = 92
					max = 95
					p.Unit = "Percent"
				case "RunningTaskCount":
					avg = 4
					max = 4
				case "RequestCount", "Invocations":
					sum = 600
				case "NetworkRxBytes", "NetworkTxBytes":
					avg = 2_000_000
					p.Unit = "Bytes/Second"
				case "TargetResponseTime":
					avg = 0.2
					p.Unit = "Seconds"
				}
				if slices.Contains(args, "Average") {
					p.Average = fp(avg)
					p.SampleCount = fp(10)
				}
				if slices.Contains(args, "Maximum") {
					p.Maximum = fp(max)
				}
				if slices.Contains(args, "Minimum") {
					p.Minimum = fp(1)
				}
				if slices.Contains(args, "Sum") {
					p.Sum = fp(sum)
				}
			}
			points = append(points, p)
		}
		out = map[string]any{"Datapoints": points}
	default:
		f.t.Fatalf("unhandled fake operation %s", key)
	}
	return json.Marshal(out)
}

func TestECSCollectionContract(t *testing.T) {
	f := &fakeAWS{t: t}
	s, err := collectECS(f, options{region: "us-west-2", cluster: "prod", service: "orders", hours: 3, noDB: true, series: true})
	if err != nil {
		t.Fatal(err)
	}
	if s.Metrics.TargetP99Ms == nil || *s.Metrics.TargetP99Ms != 400 || s.Metrics.TargetAvgMs == nil || *s.Metrics.TargetAvgMs != 200 {
		t.Fatalf("latency lost: %+v", s.Metrics)
	}
	if s.Metrics.RunningTasksAvg == nil || *s.Metrics.RunningTasksAvg != 4 || s.Metrics.RPSPerTask == nil || *s.Metrics.RPSPerTask != 2.5 {
		t.Fatalf("task normalization: %+v", s.Metrics)
	}
	if s.Metrics.NetRxMBps == nil || *s.Metrics.NetRxMBps != 2 {
		t.Fatalf("network units: %+v", s.Metrics.NetRxMBps)
	}
	if s.Identity == nil || s.Identity.Account != "123456789012" || s.Assessment.Status != "pressure_detected" {
		t.Fatalf("missing triage/identity: %+v", s)
	}
	if s.ECS.DeploymentMaxPercent != 200 || len(s.Evidence) == 0 {
		t.Fatal("missing deployment or metric evidence")
	}
	for _, e := range s.Evidence {
		if e.Points > 0 && len(e.Series) == 0 {
			t.Fatal("raw evidence omitted")
		}
	}
}

func TestLambdaCollectionAndZeroReservation(t *testing.T) {
	s, err := collectLambda(&fakeAWS{t: t}, options{region: "us-west-2", function: "orders", hours: 3, noDB: true})
	if err != nil {
		t.Fatal(err)
	}
	if s.Metrics.DurationP99Ms == nil || s.Metrics.DurationAvgMs == nil {
		t.Fatal("duration request failed")
	}
	if s.Lambda.Reserved == nil || *s.Lambda.Reserved != 0 || s.Lambda.ConcurrencyCap == nil || *s.Lambda.ConcurrencyCap != 0 {
		t.Fatal("zero reservation must not fall back to account limit")
	}
}

func TestPartialAccessIsUnknownNotHealthy(t *testing.T) {
	s, err := collectECS(&fakeAWS{t: t, fail: "cloudwatch"}, options{region: "us-west-2", cluster: "prod", service: "orders", hours: 3, noDB: true})
	if err != nil {
		t.Fatal(err)
	}
	if s.Assessment.Status != "unknown" || len(s.Gaps) == 0 || s.Metrics.CPUAvg != nil {
		t.Fatalf("misleading partial evidence: %+v", s)
	}
}

func TestWriterMetricsDoNotBlendReaders(t *testing.T) {
	f := &fakeAWS{t: t}
	c := collector{call: f}
	now := time.Now().UTC()
	db := c.collectDB(options{dbCluster: "orders"}, 60, now.Add(-time.Hour), now)
	if db == nil || db.MetricScope != "writer" || db.MaxConnections == nil || *db.MaxConnections != 800 {
		t.Fatalf("bad DB mapping %+v", db)
	}
	for _, e := range c.evidence {
		if !slices.Contains(e.Dimensions, "Name=DBInstanceIdentifier,Value=writer") {
			t.Fatalf("wrong DB metric scope %+v", e)
		}
	}
}

func TestConnectionBudgetAllowsZeroAndIncludesSurge(t *testing.T) {
	b := connectionBudget(20, 10, "max", ip(800), nil)
	applyConnectionLimits(&b, options{poolMax: 20, dbReserve: 100, dbOther: 300, budgetKnown: true}, 200)
	if b.ConnectionTaskLimit == nil || *b.ConnectionTaskLimit != 10 {
		t.Fatalf("wrong surge bound %+v", b)
	}
	applyConnectionLimits(&b, options{poolMax: 20, dbReserve: 500, dbOther: 400, budgetKnown: true}, 200)
	if *b.ConnectionTaskLimit != 0 || b.Fit != "no" {
		t.Fatal("negative budget must not permit tasks")
	}
	unknown := connectionBudget(20, 10, "max", ip(800), nil)
	applyConnectionLimits(&unknown, options{poolMax: 20}, 200)
	if unknown.ConnectionTaskLimit != nil {
		t.Fatal("missing budgets are not safe")
	}
}

func TestOldSpikeAndCoarseHistoryAreNotCurrentPressure(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	c := collector{}
	c.recordMetric("AWS/ECS", "CPUUtilization", nil, 60, now.Add(-time.Hour), now, []cwPoint{{Timestamp: iso(now.Add(-time.Hour)), Average: fp(99)}})
	if got := assess(c.evidence, now); got.Status != "unknown" || len(got.Alerts) > 0 {
		t.Fatalf("old spike: %+v", got)
	}
	c = collector{}
	c.recordMetric("AWS/ECS", "CPUUtilization", nil, 3600, now.Add(-24*time.Hour), now, []cwPoint{{Timestamp: iso(now.Add(-time.Hour)), Average: fp(99)}})
	if got := assess(c.evidence, now); got.Status != "unknown" {
		t.Fatal("coarse data claimed current health")
	}
}

func TestWeightedMeanAndTrafficCoverage(t *testing.T) {
	points := []cwPoint{{Average: fp(1), SampleCount: fp(99)}, {Average: fp(100), SampleCount: fp(1)}}
	if got := stat(points, "Average", false); got == nil || *got != 1.99 {
		t.Fatalf("mean %v", got)
	}
	history := trafficHistory([]cwPoint{{Timestamp: "2026-01-01T01:00:00Z", Sum: fp(600)}, {Timestamp: "2026-01-01T01:10:00Z", Sum: fp(1200)}}, 60)
	if len(history) != 1 || history[0].ObservedSeconds != 120 || history[0].ObservedRPS != 15 || history[0].PeakRPS != 20 {
		t.Fatalf("missing buckets counted as zero: %+v", history)
	}
}

func TestDatabaseRequiresMapping(t *testing.T) {
	f := &fakeAWS{t: t}
	c := collector{call: f}
	kind, name := c.pickDB("", "")
	if kind != "" || name != "" || len(f.calls) != 0 || len(c.gaps) == 0 {
		t.Fatal("must not attach arbitrary regional database")
	}
}

func TestDatabasePressureAndMissingMapping(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	e := []metricEvidence{{Namespace: "AWS/RDS", Name: "DatabaseConnections", Period: 60, Last: iso(now.Add(-time.Minute)), Recent: metricSummary{Points: 3, Max: fp(740)}}}
	a := assessResources(e, now, &database{MaxConnections: ip(800)}, nil, nil)
	if a.Status != "pressure_detected" || len(a.Alerts) != 1 {
		t.Fatalf("missed connection exhaustion: %+v", a)
	}
	a = assessResources(nil, now, nil, nil, nil)
	if a.Status != "unknown" {
		t.Fatal("unknown mapping cannot imply health")
	}
}

func TestInvalidMetricJSONRemainsGap(t *testing.T) {
	c := collector{call: invalidJSONCaller{}}
	now := time.Now().UTC()
	p := c.metric("AWS/ECS", "CPUUtilization", nil, 60, now.Add(-time.Hour), now, []string{"Average"}, nil)
	if len(p) != 0 || len(c.gaps) == 0 {
		t.Fatal("invalid response silently accepted")
	}
}

type invalidJSONCaller struct{}

func (invalidJSONCaller) Call(...string) ([]byte, error) { return []byte("not json"), nil }

func TestQuietRecentWindowPreservesHistoricalPressure(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	points := []cwPoint{}
	for i := 0; i < 3; i++ {
		points = append(points, cwPoint{Timestamp: iso(now.Add(-time.Hour + time.Duration(i)*time.Minute)), Average: fp(95)})
	}
	for i := 1; i <= 5; i++ {
		points = append(points, cwPoint{Timestamp: iso(now.Add(-time.Duration(i) * time.Minute)), Average: fp(20)})
	}
	c := collector{}
	c.recordMetric("AWS/ECS", "CPUUtilization", nil, 60, now.Add(-24*time.Hour), now, points)
	a := assess(c.evidence, now)
	if a.Status != "no_recent_pressure" || len(a.Alerts) != 0 || len(a.HistoricalSignals) != 1 || !strings.Contains(a.HistoricalSignals[0], "3 buckets") {
		t.Fatalf("quiet interval hid historical pressure: %+v", a)
	}
}

func TestMissingMetricDoesNotHideAQuietWindow(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	c := collector{}
	var points []cwPoint
	for i := 1; i <= 5; i++ {
		points = append(points, cwPoint{Timestamp: iso(now.Add(-time.Duration(i) * time.Minute)), Average: fp(20), Maximum: fp(30)})
	}
	c.recordMetric("AWS/ECS", "CPUUtilization", nil, 60, now.Add(-time.Hour), now, points)
	c.recordMetric("ECS/ContainerInsights", "NetworkRxBytes", nil, 60, now.Add(-time.Hour), now, nil)
	c.gap("no task I/O datapoints")
	a := assessResources(c.evidence, now, nil, nil, nil)
	if a.Status != "no_recent_pressure" || len(a.Alerts) != 0 {
		t.Fatalf("optional gap hid a quiet window: %+v", a)
	}
}

func TestDatabaseSpikeOutsideRecentWindow(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cpu := metricEvidence{
		Namespace: "AWS/ECS", Name: "CPUUtilization", Period: 60, Last: iso(now.Add(-time.Minute)),
		Recent: metricSummary{Points: 5, Average: fp(20)},
		Window: metricSummary{Points: 20, Average: fp(25)},
	}
	db := metricEvidence{
		Namespace: "AWS/RDS", Name: "DatabaseConnections", Period: 60, Last: iso(now.Add(-time.Minute)),
		Recent: metricSummary{Points: 3, Max: fp(40)},
		Window: metricSummary{Points: 10, Max: fp(760)},
	}
	a := assessResources([]metricEvidence{cpu, db}, now, &database{MaxConnections: ip(800)}, nil, nil)
	if a.Status != "no_recent_pressure" || len(a.Alerts) != 0 || len(a.HistoricalSignals) != 1 || !strings.Contains(a.HistoricalSignals[0], "760") {
		t.Fatalf("earlier connection spike dropped: %+v", a)
	}
}

func TestOnlyOneConnectionLimitIsSerialized(t *testing.T) {
	b := connectionBudget(20, 10, "max", ip(800), nil)
	applyConnectionLimits(&b, options{poolMax: 20, dbReserve: 100, dbOther: 300, budgetKnown: true}, 200)
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["connection_task_limit"] != float64(10) {
		t.Fatalf("missing authoritative limit: %s", raw)
	}
	for _, legacy := range []string{"task_ceiling", "safe_max_tasks"} {
		if _, ok := fields[legacy]; ok {
			t.Fatalf("competing ceiling %s", legacy)
		}
	}
	unknown := connectionBudget(20, 10, "max", ip(800), nil)
	raw, _ = json.Marshal(unknown)
	if strings.Contains(string(raw), "connection_task_limit") || unknown.Fit != "unknown" {
		t.Fatal("guessed limit without explicit budgets")
	}
}

func TestDatabaseHistoryDistinguishesRecoveryFromUnavailableData(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		last   string
		period int
		recent metricSummary
		known  bool
	}{
		{"measured recovery", iso(now.Add(-time.Minute)), 60, metricSummary{Points: 3, Max: fp(40)}, true},
		{"missing window", "", 60, metricSummary{}, false},
		{"stale window", iso(now.Add(-time.Hour)), 60, metricSummary{Points: 3, Max: fp(40)}, false},
		{"missing maximum", iso(now.Add(-time.Minute)), 60, metricSummary{Points: 3}, false},
		{"coarse window", iso(now.Add(-time.Minute)), 3600, metricSummary{Points: 1, Max: fp(40)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := metricEvidence{Namespace: "AWS/RDS", Name: "DatabaseConnections", Period: tc.period, Last: tc.last, Recent: tc.recent, Window: metricSummary{Points: 10, Max: fp(760)}}
			a := assessResources([]metricEvidence{e}, now, &database{MaxConnections: ip(800)}, nil, nil)
			if len(a.Alerts) != 0 || len(a.HistoricalSignals) != 1 {
				t.Fatalf("unexpected assessment: %+v", a)
			}
			signal := a.HistoricalSignals[0]
			if !strings.Contains(signal, "760 versus limit 800") {
				t.Fatalf("lost historical measurement: %s", signal)
			}
			if tc.known {
				if !strings.Contains(signal, "observed maximum in the last 15 minutes 40") || strings.Contains(signal, "unavailable") {
					t.Fatalf("lost measured recovery: %s", signal)
				}
			} else if !strings.Contains(signal, "recent data unavailable; recovery unverified") || strings.Contains(signal, "under 90%") {
				t.Fatalf("claimed recovery without evidence: %s", signal)
			}
		})
	}
}
