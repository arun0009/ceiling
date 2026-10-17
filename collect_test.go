package main

import (
	"strings"
	"testing"
)

func TestRegionFollowsNamedProfile(t *testing.T) {
	got, err := resolveRegion("", "cof-820283-prod", "us-west-2", "us-east-1", "")
	if err != nil || got != "us-west-2" {
		t.Fatalf("profile region lost: %s %v", got, err)
	}
	got, err = resolveRegion("eu-west-1", "cof-820283-prod", "us-west-2", "us-east-1", "")
	if err != nil || got != "eu-west-1" {
		t.Fatalf("explicit region lost: %s %v", got, err)
	}
	got, err = resolveRegion("", "cof-820283-prod", "", "us-east-1", "")
	if err != nil || got != "us-east-1" {
		t.Fatalf("env region lost: %s %v", got, err)
	}
	if _, err = resolveRegion("", "cof-820283-prod", "", "", ""); err == nil {
		t.Fatal("missing region was accepted")
	}
}

func TestBudgetWithoutExplicitReservesRemainsUnknown(t *testing.T) {
	for _, pool := range []int{10, 1000} {
		got := connectionBudget(pool, 4, "scaling max", ip(800), fp(740))
		if got.Fit != "unknown" || got.ConnectionTaskLimit != nil {
			t.Fatalf("invented budget: %+v", got)
		}
	}
}

func TestPeakRateAndDiskThroughput(t *testing.T) {
	points := []cwPoint{{Sum: fp(600)}, {Sum: fp(1200)}}
	if got := rate(points, 60); got == nil || *got != 15 {
		t.Fatalf("avg rps %v", got)
	}
	if got := peakRate(points, 60); got == nil || *got != 20 {
		t.Fatalf("peak rps %v", got)
	}
	bytes := []cwPoint{{Sum: fp(120_000_000)}}
	if got := byteRateMBps(bytes, 60); got == nil || *got != 2 {
		t.Fatalf("mbps %v", got)
	}
}

func TestInflightUsesMeanLatency(t *testing.T) {
	idle := 60
	m := metricSet{RPSPerTask: fp(5), TargetAvgMs: fp(200)}
	fillInflight(&m, &idle)
	if m.InflightPerTask == nil || *m.InflightPerTask != 1 {
		t.Fatalf("inflight %+v", m.InflightPerTask)
	}
	if m.StallInflightPerTask == nil || *m.StallInflightPerTask != 300 {
		t.Fatalf("stall %+v", m.StallInflightPerTask)
	}
}

func TestCommitLatencyStaysMilliseconds(t *testing.T) {
	points := []cwPoint{{
		Unit:    "Milliseconds",
		Average: fp(4),
		Maximum: fp(20),
	}}
	if got := stat(points, "Average", false); got == nil || *got != 4 {
		t.Fatalf("commit latency %v", got)
	}
	seconds := []cwPoint{{Unit: "Seconds", Average: fp(0.18), ExtendedStatistics: map[string]float64{"p99": 0.4}}}
	if got := stat(seconds, "Average", true); got == nil || *got != 180 {
		t.Fatalf("target avg ms %v", got)
	}
	if got := stat(seconds, "p99", true); got == nil || *got != 400 {
		t.Fatalf("target p99 ms %v", got)
	}
}

func TestAllowlistIsReadOnly(t *testing.T) {
	for key := range allowed {
		verb := strings.SplitN(key, " ", 2)[1]
		switch {
		case strings.HasPrefix(verb, "describe"), strings.HasPrefix(verb, "get"), strings.HasPrefix(verb, "list"):
		default:
			t.Fatalf("%s is not read-only", key)
		}
	}
}

func TestCallRejectsWrites(t *testing.T) {
	_, err := (awsCLI{region: "us-east-1"}).Call("ecs", "update-service", "--service", "orders")
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatal(err)
	}
}

func TestMaxConnectionsFormulaIsNotANumber(t *testing.T) {
	got := parseMaxConnections("LEAST({DBInstanceClassMemory/12582880},5000)")
	if got.value != nil || got.raw == "" {
		t.Fatalf("%+v", got)
	}
	num := parseMaxConnections("1000")
	if num.value == nil || *num.value != 1000 {
		t.Fatalf("%+v", num)
	}
}

func ip(n int) *int         { return new(n) }
func fp(n float64) *float64 { return new(n) }
