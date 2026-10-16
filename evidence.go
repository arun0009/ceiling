package main

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"
)

type identity struct {
	Account string `json:"account"`
	ARN     string `json:"arn"`
}

func (c *collector) readIdentity() *identity {
	buf := c.optional("AWS identity", "sts", "get-caller-identity")
	var id identity
	if json.Unmarshal(buf, &id) != nil || id.Account == "" {
		c.gap("AWS account identity unavailable")
		return nil
	}
	return &id
}

type metricEvidence struct {
	Window     metricSummary   `json:"window_summary"`
	Namespace  string          `json:"namespace"`
	Name       string          `json:"metric"`
	Dimensions []string        `json:"dimensions"`
	Unit       string          `json:"unit,omitempty"`
	Period     int             `json:"period_seconds"`
	Points     int             `json:"points"`
	Expected   int             `json:"expected_buckets"`
	Last       string          `json:"last_timestamp,omitempty"`
	Recent     metricSummary   `json:"recent_15m"`
	Hourly     []trafficBucket `json:"traffic_by_hour,omitempty"`
	Series     []cwPoint       `json:"series,omitempty"`
}

type metricSummary struct {
	Points             int      `json:"points"`
	Average            *float64 `json:"average,omitempty"`
	Max                *float64 `json:"max,omitempty"`
	Min                *float64 `json:"min,omitempty"`
	Sum                *float64 `json:"sum,omitempty"`
	WorstP99           *float64 `json:"worst_bucket_p99,omitempty"`
	HighAverageBuckets int      `json:"buckets_average_at_least_90,omitempty"`
}

type trafficBucket struct {
	Start           string  `json:"start"`
	Count           float64 `json:"count"`
	ObservedSeconds int     `json:"observed_seconds"`
	ObservedRPS     float64 `json:"rps_in_observed_buckets"`
	PeakRPS         float64 `json:"peak_bucket_rps"`
}

func (c *collector) recordMetric(ns, name string, dims []string, period int, start, end time.Time, points []cwPoint) {
	e := metricEvidence{Namespace: ns, Name: name, Dimensions: dims, Period: period, Points: len(points), Expected: int(math.Ceil(end.Sub(start).Seconds() / float64(period)))}
	var recent []cwPoint
	for _, p := range points {
		if p.Timestamp > e.Last {
			e.Last = p.Timestamp
		}
		if e.Unit == "" {
			e.Unit = p.Unit
		}
		at, ok := parseTime(p.Timestamp)
		// Only fully completed buckets inside the recent window qualify.
		if ok && !at.Before(end.Add(-15*time.Minute)) && !at.Add(time.Duration(period)*time.Second).After(end) {
			recent = append(recent, p)
		}
	}
	e.Recent = summarize(recent)
	e.Window = summarize(points)
	if name == "RequestCount" || name == "Invocations" {
		e.Hourly = trafficHistory(points, period)
	}
	if c.series {
		e.Series = points
	}
	if len(points) == 0 {
		c.gap("no datapoints for %s/%s", ns, name)
	}
	c.evidence = append(c.evidence, e)
}

func summarize(points []cwPoint) metricSummary {
	s := metricSummary{Points: len(points), Average: stat(points, "Average", false), Max: stat(points, "Maximum", false), Min: stat(points, "Minimum", false), Sum: stat(points, "Sum", false), WorstP99: stat(points, "p99", false)}
	for _, p := range points {
		if p.Average != nil && *p.Average >= 90 {
			s.HighAverageBuckets++
		}
	}
	return s
}

func trafficHistory(points []cwPoint, period int) []trafficBucket {
	buckets := map[string]*trafficBucket{}
	for _, p := range points {
		at, ok := parseTime(p.Timestamp)
		if !ok || p.Sum == nil || period <= 0 {
			continue
		}
		key := iso(at.Truncate(time.Hour))
		b := buckets[key]
		if b == nil {
			b = &trafficBucket{Start: key}
			buckets[key] = b
		}
		b.Count += *p.Sum
		b.ObservedSeconds += period
		b.PeakRPS = math.Max(b.PeakRPS, *p.Sum/float64(period))
	}
	out := make([]trafficBucket, 0, len(buckets))
	for _, b := range buckets {
		b.ObservedRPS = round2(b.Count / float64(b.ObservedSeconds))
		b.PeakRPS = round2(b.PeakRPS)
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

type pressureAlert struct {
	Signal   string `json:"signal"`
	Evidence string `json:"evidence"`
	Action   string `json:"action"`
}

type assessment struct {
	HistoricalSignals []string        `json:"historical_signals,omitempty"`
	Status            string          `json:"status"`
	Scope             string          `json:"scope"`
	Alerts            []pressureAlert `json:"alerts"`
	Unknown           []string        `json:"unknown"`
}

func assess(evidence []metricEvidence, end time.Time) assessment {
	a := assessment{Status: "no_recent_pressure", Scope: "last 15 minutes", Alerts: []pressureAlert{}, Unknown: []string{}}
	judged := false
	for _, e := range evidence {
		if !triageMetric(e.Name) {
			continue
		}
		key := e.Namespace + "/" + e.Name
		recentOK := recentUsable(e, end)
		recentHot := recentOK && summaryHot(e.Name, e.Recent)
		if summaryHot(e.Name, e.Window) && !recentHot {
			a.HistoricalSignals = append(a.HistoricalSignals, historicalLine(e, recentOK))
		}
		if !recentOK {
			a.Unknown = append(a.Unknown, key+": missing, stale, or too coarse for current triage")
			continue
		}
		judged = true
		if !recentHot {
			continue
		}
		add := func(reason, action string) {
			a.Alerts = append(a.Alerts, pressureAlert{Signal: key, Evidence: reason, Action: action})
		}
		switch e.Name {
		case "CPUUtilization":
			add(fmt.Sprintf("average CPU >=90%% in %d recent buckets", e.Recent.HighAverageBuckets), "Inspect the hot task before adding tasks.")
		case "MemoryUtilization", "memory_utilization":
			add(fmt.Sprintf("recent memory maximum %.2f%%", *e.Recent.Max), "Inspect what the task is retaining before raising memory.")
		case "Throttles":
			add(fmt.Sprintf("%.0f recent throttles", *e.Recent.Sum), "Inspect the concurrency cap and the backlog before raising it.")
		case "Errors", "HTTPCode_Target_5XX_Count", "HTTPCode_ELB_5XX_Count":
			add(fmt.Sprintf("%.0f recent errors", *e.Recent.Sum), "Compare the error rate with the SLO before scaling.")
		case "HealthyHostCount":
			add("zero healthy targets in a recent bucket", "Inspect stopped tasks and target health.")
		}
	}
	if len(a.Alerts) > 0 {
		a.Status = "pressure_detected"
	} else if !judged {
		a.Status = "unknown"
	}
	return a
}

func triageMetric(name string) bool {
	switch name {
	case "CPUUtilization", "MemoryUtilization", "memory_utilization", "Throttles", "Errors", "HTTPCode_Target_5XX_Count", "HTTPCode_ELB_5XX_Count", "HealthyHostCount":
		return true
	default:
		return false
	}
}

func recentUsable(e metricEvidence, end time.Time) bool {
	last, ok := parseTime(e.Last)
	staleAfter := math.Max(300, float64(e.Period*3))
	return ok && end.Sub(last).Seconds() <= staleAfter && e.Recent.Points > 0 && e.Period <= 300
}

func summaryHot(name string, s metricSummary) bool {
	switch name {
	case "CPUUtilization":
		return s.HighAverageBuckets >= 3
	case "MemoryUtilization", "memory_utilization":
		return s.Max != nil && *s.Max >= 90
	case "Throttles", "Errors", "HTTPCode_Target_5XX_Count", "HTTPCode_ELB_5XX_Count":
		return s.Sum != nil && *s.Sum > 0
	case "HealthyHostCount":
		return s.Min != nil && *s.Min == 0
	default:
		return false
	}
}

func historicalLine(e metricEvidence, recentOK bool) string {
	key := e.Namespace + "/" + e.Name
	tail := "the last 15 minutes did not"
	if !recentOK {
		tail = "the last 15 minutes could not be read"
	}
	switch e.Name {
	case "CPUUtilization":
		return fmt.Sprintf("%s: %d buckets averaged at least 90%% in the collection window; %s", key, e.Window.HighAverageBuckets, tail)
	case "MemoryUtilization", "memory_utilization":
		quiet := "the last 15 minutes stayed under 90%"
		if !recentOK {
			quiet = tail
		}
		return fmt.Sprintf("%s: window maximum %.0f%%; %s", key, *e.Window.Max, quiet)
	case "Throttles", "Errors", "HTTPCode_Target_5XX_Count", "HTTPCode_ELB_5XX_Count":
		quiet := "none in the last 15 minutes"
		if !recentOK {
			quiet = tail
		}
		return fmt.Sprintf("%s: %.0f in the collection window; %s", key, *e.Window.Sum, quiet)
	case "HealthyHostCount":
		return fmt.Sprintf("%s: healthy targets hit 0 in the collection window; %s", key, tail)
	default:
		return fmt.Sprintf("%s: pressure in the collection window; %s", key, tail)
	}
}

func networkMBps(points []cwPoint) *float64 {
	// Container Insights network values are already bytes/second.
	n := stat(points, "Average", false)
	if n == nil {
		return nil
	}
	return new(round2(*n / 1_000_000))
}

func applyConnectionLimits(b *budget, opt options, surgePercent int) {
	if !opt.budgetKnown || b.DBMax == nil || opt.poolMax <= 0 || surgePercent < 100 {
		return
	}
	usable := *b.DBMax - opt.dbReserve - opt.dbOther
	max := 0
	if usable > 0 {
		max = int(math.Floor(float64(usable) * 100 / (float64(opt.poolMax) * float64(surgePercent))))
	}
	b.ConnectionTaskLimit = &max
	b.Fit = "unknown"
	if b.Multiplier != nil {
		b.Fit = "no"
		if *b.Multiplier <= max {
			b.Fit = "yes"
		}
	}
	b.Basis = fmt.Sprintf("connections only: (%d - %d - %d) / (%d per task * %d%% overlap)", *b.DBMax, opt.dbReserve, opt.dbOther, opt.poolMax, surgePercent)
}

func assessResources(evidence []metricEvidence, end time.Time, db *database, ecs *ecsInfo, fn *fnInfo) assessment {
	a := assess(evidence, end)
	if db != nil && db.MaxConnections != nil && *db.MaxConnections > 0 {
		limit := float64(*db.MaxConnections)
		for _, e := range evidence {
			if e.Namespace != "AWS/RDS" || e.Name != "DatabaseConnections" {
				continue
			}
			recentKnown := recentUsable(e, end) && e.Recent.Max != nil
			recentHot := recentKnown && *e.Recent.Max >= limit*0.9
			if e.Window.Max != nil && *e.Window.Max >= limit*0.9 && !recentHot {
				recent := "recent data unavailable; recovery unverified"
				if recentKnown {
					recent = fmt.Sprintf("observed maximum in the last 15 minutes %.0f, under 90%%", *e.Recent.Max)
				}
				a.HistoricalSignals = append(a.HistoricalSignals, fmt.Sprintf("AWS/RDS/DatabaseConnections: window maximum %.0f versus limit %d; %s", *e.Window.Max, *db.MaxConnections, recent))
			}
			if !recentHot {
				continue
			}
			a.Alerts = append(a.Alerts, pressureAlert{Signal: "AWS/RDS/DatabaseConnections", Evidence: fmt.Sprintf("recent maximum %.0f connections versus configured limit %d", *e.Recent.Max, *db.MaxConnections), Action: "Do not add tasks until connections drop."})
		}
	}
	if ecs != nil && ecs.Running < ecs.Desired {
		a.Alerts = append(a.Alerts, pressureAlert{Signal: "ECS running tasks", Evidence: fmt.Sprintf("%d running versus %d desired at collection", ecs.Running, ecs.Desired), Action: "Inspect why running is below desired."})
	}
	if fn != nil && fn.Reserved != nil && *fn.Reserved == 0 {
		a.Alerts = append(a.Alerts, pressureAlert{Signal: "Lambda reserved concurrency", Evidence: "reserved concurrency is zero", Action: "Reserved concurrency is 0. Confirm that is intentional."})
	}
	if len(a.Alerts) > 0 {
		a.Status = "pressure_detected"
	}
	return a
}
