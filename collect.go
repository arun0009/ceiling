package main

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

const gapCap = 100

type snapshot struct {
	Evidence    []metricEvidence `json:"evidence"`
	Assessment  assessment       `json:"assessment"`
	Identity    *identity        `json:"identity,omitempty"`
	Target      target           `json:"target"`
	ECS         *ecsInfo         `json:"ecs,omitempty"`
	Lambda      *fnInfo          `json:"lambda,omitempty"`
	Metrics     metricSet        `json:"metrics"`
	SinceDeploy *splits          `json:"since_deploy,omitempty"`
	Database    *database        `json:"database,omitempty"`
	Budget      budget           `json:"connection_budget"`
	Stopped     []string         `json:"stopped_reasons,omitempty"`
	Gaps        []string         `json:"gaps"`
}

type target struct {
	Kind     string `json:"kind"`
	Profile  string `json:"profile,omitempty"`
	Region   string `json:"region"`
	Cluster  string `json:"cluster,omitempty"`
	Service  string `json:"service,omitempty"`
	Function string `json:"function,omitempty"`
	Hours    int    `json:"hours"`
	PeriodS  int    `json:"period_seconds"`
	Start    string `json:"start"`
	End      string `json:"end"`
}

type ecsInfo struct {
	TaskDefinition       string         `json:"task_definition,omitempty"`
	DeploymentMaxPercent int            `json:"deployment_max_percent,omitempty"`
	Status               string         `json:"status"`
	Desired              int            `json:"desired"`
	Running              int            `json:"running"`
	Launch               string         `json:"launch"`
	CPUUnits             int            `json:"cpu_units,omitempty"`
	MemoryMB             int            `json:"memory_mb,omitempty"`
	Min                  *int           `json:"min,omitempty"`
	Max                  *int           `json:"max,omitempty"`
	Policies             []scalePolicy  `json:"policies,omitempty"`
	DeployedAt           string         `json:"deployed_at,omitempty"`
	Rollout              string         `json:"rollout,omitempty"`
	DeployRollback       deployRollback `json:"deploy_rollback"`
	ALBIdleS             *int           `json:"alb_idle_timeout_s,omitempty"`
	groups               []string       `json:"-"`
	stopped              []string       `json:"-"`
}

type fnInfo struct {
	Version        string `json:"version,omitempty"`
	EphemeralMB    int    `json:"ephemeral_storage_mb,omitempty"`
	MemoryMB       int    `json:"memory_mb,omitempty"`
	TimeoutS       int    `json:"timeout_s,omitempty"`
	Runtime        string `json:"runtime,omitempty"`
	Reserved       *int   `json:"reserved_concurrency,omitempty"`
	Provisioned    *int   `json:"provisioned_concurrency,omitempty"`
	AccountLimit   *int   `json:"account_concurrency_limit,omitempty"`
	ConcurrencyCap *int   `json:"concurrency_cap,omitempty"`
	CapSource      string `json:"concurrency_cap_source,omitempty"`
}

type scalePolicy struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Metric    string   `json:"metric,omitempty"`
	Target    *float64 `json:"target,omitempty"`
	ScaleOutS *int     `json:"scale_out_cooldown_s,omitempty"`
	ScaleInS  *int     `json:"scale_in_cooldown_s,omitempty"`
}

type deployRollback struct {
	Enabled  bool `json:"enabled"`
	Rollback bool `json:"rollback"`
}

type metricSet struct {
	CPUAvg               *float64  `json:"cpu_pct_avg,omitempty"`
	CPUMax               *float64  `json:"cpu_pct_max,omitempty"`
	MemoryAvg            *float64  `json:"memory_pct_avg,omitempty"`
	MemoryMax            *float64  `json:"memory_pct_max,omitempty"`
	RunningTasksAvg      *float64  `json:"running_tasks_avg,omitempty"`
	ALBRPS               *float64  `json:"alb_rps,omitempty"`
	ALBRPSPeak           *float64  `json:"alb_rps_peak,omitempty"`
	ActiveConnsMax       *float64  `json:"active_connections_max,omitempty"`
	TargetConnErrors     *float64  `json:"target_connection_errors,omitempty"`
	NetRxMBps            *float64  `json:"net_rx_mbps,omitempty"`
	NetTxMBps            *float64  `json:"net_tx_mbps,omitempty"`
	DiskReadMBps         *float64  `json:"disk_read_mbps,omitempty"`
	DiskWriteMBps        *float64  `json:"disk_write_mbps,omitempty"`
	RPSPerTask           *float64  `json:"rps_per_task,omitempty"`
	RPSBasis             string    `json:"rps_per_task_basis,omitempty"`
	TargetAvgMs          *float64  `json:"target_avg_ms,omitempty"`
	TargetP99Ms          *float64  `json:"target_p99_ms,omitempty"`
	Target5xx            *float64  `json:"target_5xx,omitempty"`
	ELB5xx               *float64  `json:"elb_5xx,omitempty"`
	HealthyMin           *float64  `json:"healthy_hosts_min,omitempty"`
	InflightPerTask      *float64  `json:"inflight_per_task,omitempty"`
	StallInflightPerTask *float64  `json:"stall_inflight_per_task,omitempty"`
	Invocations          *float64  `json:"invocations,omitempty"`
	RPS                  *float64  `json:"rps,omitempty"`
	Errors               *float64  `json:"errors,omitempty"`
	ErrorPct             *float64  `json:"error_pct,omitempty"`
	Throttles            *float64  `json:"throttles,omitempty"`
	DurationAvgMs        *float64  `json:"duration_avg_ms,omitempty"`
	DurationP99Ms        *float64  `json:"duration_p99_ms,omitempty"`
	DurationMaxMs        *float64  `json:"duration_max_ms,omitempty"`
	ConcurrentMax        *float64  `json:"concurrent_max,omitempty"`
	InitAvgMs            *float64  `json:"init_avg_ms,omitempty"`
	InitMaxMs            *float64  `json:"init_max_ms,omitempty"`
	ProvisionedSpillover *float64  `json:"provisioned_spillover,omitempty"`
	LambdaMemoryPctMax   *float64  `json:"lambda_memory_pct_max,omitempty"`
	latencyPoints        []cwPoint `json:"-"`
}

type splits struct {
	CPUPct      *split `json:"cpu_pct,omitempty"`
	TargetP99Ms *split `json:"target_p99_ms,omitempty"`
}

type split struct {
	At     string  `json:"at"`
	Before float64 `json:"before"`
	After  float64 `json:"after"`
}

type database struct {
	Kind                string   `json:"kind"`
	Name                string   `json:"name"`
	MetricScope         string   `json:"metric_scope"`
	Engine              string   `json:"engine,omitempty"`
	MaxConnections      *int     `json:"max_connections,omitempty"`
	MaxConnectionsRaw   string   `json:"max_connections_raw,omitempty"`
	CPUAvg              *float64 `json:"cpu_pct_avg,omitempty"`
	CPUMax              *float64 `json:"cpu_pct_max,omitempty"`
	ConnectionsAvg      *float64 `json:"connections_avg,omitempty"`
	ConnectionsMax      *float64 `json:"connections_max,omitempty"`
	CommitLatencyAvgMs  *float64 `json:"commit_latency_avg_ms,omitempty"`
	CommitLatencyMaxMs  *float64 `json:"commit_latency_max_ms,omitempty"`
	ReadIOPSAvg         *float64 `json:"read_iops_avg,omitempty"`
	ReadIOPSMax         *float64 `json:"read_iops_max,omitempty"`
	WriteIOPSAvg        *float64 `json:"write_iops_avg,omitempty"`
	WriteIOPSMax        *float64 `json:"write_iops_max,omitempty"`
	ReadLatencyAvgMs    *float64 `json:"read_latency_avg_ms,omitempty"`
	WriteLatencyAvgMs   *float64 `json:"write_latency_avg_ms,omitempty"`
	DiskQueueMax        *float64 `json:"disk_queue_max,omitempty"`
	FreeableMemoryMinMB *float64 `json:"freeable_memory_min_mb,omitempty"`
	Deadlocks           *float64 `json:"deadlocks,omitempty"`
	MonitoringIntervalS *int     `json:"monitoring_interval_s,omitempty"`
	EnhancedMonitoring  *bool    `json:"enhanced_monitoring,omitempty"`
	ACUMin              *float64 `json:"acu_min,omitempty"`
	ACUMax              *float64 `json:"acu_max,omitempty"`
}

type budget struct {
	Basis               string   `json:"basis"`
	ConnectionTaskLimit *int     `json:"connection_task_limit,omitempty"`
	PoolMax             *int     `json:"pool_max,omitempty"`
	Multiplier          *int     `json:"multiplier,omitempty"`
	MultiplierSource    string   `json:"multiplier_source,omitempty"`
	Product             *int     `json:"product,omitempty"`
	DBMax               *int     `json:"db_max_connections,omitempty"`
	Peak                *float64 `json:"peak_connections,omitempty"`
	Fit                 string   `json:"fit"`
}

type options struct {
	region      string
	profile     string
	hours       int
	poolMax     int
	dbCluster   string
	dbInstance  string
	noDB        bool
	cluster     string
	service     string
	function    string
	series      bool
	dbReserve   int
	dbOther     int
	budgetKnown bool
}

type collector struct {
	call     caller
	gaps     []string
	evidence []metricEvidence
	series   bool
}

func collectECS(call caller, opt options) (snapshot, error) {
	end := time.Now().UTC()
	start := end.Add(-time.Duration(opt.hours) * time.Hour)
	period := periodFor(opt.hours)
	c := &collector{call: call, series: opt.series}
	described, err := c.call.Call(
		"ecs", "describe-services",
		"--cluster", opt.cluster,
		"--services", opt.service,
	)
	if err != nil {
		return snapshot{}, fmt.Errorf("describe service: %w", err)
	}
	var listed struct {
		Services []ecsService `json:"services"`
		Failures []struct {
			Reason string `json:"reason"`
		} `json:"failures"`
	}
	if err := json.Unmarshal(described, &listed); err != nil {
		return snapshot{}, fmt.Errorf("describe service: %w", err)
	}
	if len(listed.Services) == 0 {
		reason := "not found"
		if len(listed.Failures) > 0 && listed.Failures[0].Reason != "" {
			reason = listed.Failures[0].Reason
		}
		return snapshot{}, fmt.Errorf("%s: %s", opt.service, reason)
	}
	raw := listed.Services[0]
	info := parseECS(raw)
	if info.Launch == "EC2" {
		c.gap("tasks run on EC2; host CPU and memory are not in this report")
	}
	if raw.TaskDefinition != "" {
		buf := c.optional("task definition", "ecs", "describe-task-definition", "--task-definition", raw.TaskDefinition)
		var td struct {
			TaskDefinition struct {
				CPU    string `json:"cpu"`
				Memory string `json:"memory"`
			} `json:"taskDefinition"`
		}
		if json.Unmarshal(buf, &td) == nil {
			info.CPUUnits = atoi(td.TaskDefinition.CPU)
			info.MemoryMB = atoi(td.TaskDefinition.Memory)
		}
	}
	resource := "service/" + opt.cluster + "/" + opt.service
	targets := c.optional("scaling target", "application-autoscaling", "describe-scalable-targets", "--service-namespace", "ecs", "--resource-ids", resource)
	policies := c.optional("scaling policy", "application-autoscaling", "describe-scaling-policies", "--service-namespace", "ecs", "--resource-id", resource)
	info.Min, info.Max, info.Policies = parseScaling(targets, policies)

	dims := []string{"Name=ClusterName,Value=" + opt.cluster, "Name=ServiceName,Value=" + opt.service}
	cpu := c.metric("AWS/ECS", "CPUUtilization", dims, period, start, end, []string{"Average", "Maximum"}, nil)
	mem := c.metric("AWS/ECS", "MemoryUtilization", dims, period, start, end, []string{"Average", "Maximum"}, nil)
	running := c.metric("ECS/ContainerInsights", "RunningTaskCount", dims, period, start, end, []string{"Average"}, nil)
	if len(cpu) == 0 {
		c.gap("no CloudWatch datapoints for CPUUtilization")
	}
	metrics := metricSet{
		CPUAvg:          stat(cpu, "Average", false),
		CPUMax:          stat(cpu, "Maximum", false),
		MemoryAvg:       stat(mem, "Average", false),
		MemoryMax:       stat(mem, "Maximum", false),
		RunningTasksAvg: stat(running, "Average", false),
	}
	c.attachTaskIO(&metrics, opt, period, start, end)
	c.attachALB(&info, &metrics, period, start, end)
	c.attachStopped(opt, &info)
	db := c.collectDB(opt, period, start, end)
	var peak *float64
	var dbMax *int
	if db != nil {
		peak = db.ConnectionsMax
		dbMax = db.MaxConnections
	}
	mult, src := multiplier(info.Max, info.Desired)
	b := connectionBudget(opt.poolMax, mult, src, dbMax, peak)
	applyConnectionLimits(&b, opt, info.DeploymentMaxPercent)
	fillInflight(&metrics, info.ALBIdleS)
	var since *splits
	if info.DeployedAt != "" {
		since = &splits{
			CPUPct:      sinceDeploy(cpu, info.DeployedAt, "Average", false),
			TargetP99Ms: sinceDeploy(metrics.latencyPoints, info.DeployedAt, "p99", true),
		}
		if since.CPUPct == nil && since.TargetP99Ms == nil {
			since = nil
		}
	}
	metrics.latencyPoints = nil
	if c.gaps == nil {
		c.gaps = []string{}
	}
	id := c.readIdentity()
	return snapshot{
		Evidence: c.evidence, Assessment: assessResources(c.evidence, end, db, &info, nil), Identity: id,
		Target: target{
			Kind: "ecs", Profile: opt.profile, Region: opt.region, Cluster: opt.cluster, Service: opt.service,
			Hours: opt.hours, PeriodS: period, Start: iso(start), End: iso(end),
		},
		ECS: &info, Metrics: metrics, SinceDeploy: since, Database: db, Budget: b,
		Stopped: info.stopped, Gaps: c.gaps,
	}, nil
}

func collectLambda(call caller, opt options) (snapshot, error) {
	end := time.Now().UTC()
	start := end.Add(-time.Duration(opt.hours) * time.Hour)
	period := periodFor(opt.hours)
	c := &collector{call: call, series: opt.series}
	buf, err := c.call.Call("lambda", "get-function-configuration", "--function-name", opt.function)
	if err != nil {
		return snapshot{}, fmt.Errorf("describe function: %w", err)
	}
	var cfg struct {
		FunctionName     string `json:"FunctionName"`
		MemorySize       int    `json:"MemorySize"`
		Timeout          int    `json:"Timeout"`
		Runtime          string `json:"Runtime"`
		Version          string `json:"Version"`
		EphemeralStorage struct {
			Size int `json:"Size"`
		} `json:"EphemeralStorage"`
	}
	if err := json.Unmarshal(buf, &cfg); err != nil {
		return snapshot{}, fmt.Errorf("describe function: %w", err)
	}
	info := fnInfo{MemoryMB: cfg.MemorySize, TimeoutS: cfg.Timeout, Runtime: cfg.Runtime, Version: cfg.Version, EphemeralMB: cfg.EphemeralStorage.Size}
	conc := c.optional("reserved concurrency", "lambda", "get-function-concurrency", "--function-name", opt.function)
	var reserved struct {
		Reserved *int `json:"ReservedConcurrentExecutions"`
	}
	_ = json.Unmarshal(conc, &reserved)
	info.Reserved = reserved.Reserved
	acct := c.optional("account concurrency", "lambda", "get-account-settings")
	var account struct {
		AccountLimit struct {
			ConcurrentExecutions *int `json:"ConcurrentExecutions"`
		} `json:"AccountLimit"`
	}
	_ = json.Unmarshal(acct, &account)
	info.AccountLimit = account.AccountLimit.ConcurrentExecutions
	if info.Reserved != nil {
		info.ConcurrencyCap = info.Reserved
		info.CapSource = "reserved"
	} else if info.AccountLimit != nil {
		info.ConcurrencyCap = info.AccountLimit
		info.CapSource = "account limit, shared"
		c.gap("reserved concurrency is unset; the account limit is shared")
	}
	name := cfg.FunctionName
	if name == "" {
		name = opt.function
	}
	dims := []string{"Name=FunctionName,Value=" + name}
	inv := c.metric("AWS/Lambda", "Invocations", dims, period, start, end, []string{"Sum"}, nil)
	errs := c.metric("AWS/Lambda", "Errors", dims, period, start, end, []string{"Sum"}, nil)
	throt := c.metric("AWS/Lambda", "Throttles", dims, period, start, end, []string{"Sum"}, nil)
	dur := c.metric("AWS/Lambda", "Duration", dims, period, start, end, []string{"Average", "Maximum"}, []string{"p99"})
	cc := c.metric("AWS/Lambda", "ConcurrentExecutions", dims, period, start, end, []string{"Average", "Maximum"}, nil)
	spill := c.metric("AWS/Lambda", "ProvisionedConcurrencySpilloverInvocations", dims, period, start, end, []string{"Sum"}, nil)
	insightDims := []string{"Name=function_name,Value=" + name}
	initDur := c.metric("LambdaInsights", "init_duration", insightDims, period, start, end, []string{"Average", "Maximum"}, nil)
	c.metric("LambdaInsights", "cpu_total_time", insightDims, period, start, end, []string{"Average", "Maximum"}, nil)
	c.metric("LambdaInsights", "used_memory_max", insightDims, period, start, end, []string{"Maximum"}, nil)
	c.metric("LambdaInsights", "tmp_used", insightDims, period, start, end, []string{"Maximum"}, nil)
	c.metric("LambdaInsights", "tmp_free", insightDims, period, start, end, []string{"Minimum"}, nil)
	c.metric("LambdaInsights", "rx_bytes", insightDims, period, start, end, []string{"Sum"}, nil)
	c.metric("LambdaInsights", "tx_bytes", insightDims, period, start, end, []string{"Sum"}, nil)
	lambdaMem := c.metric("LambdaInsights", "memory_utilization", insightDims, period, start, end, []string{"Maximum"}, nil)
	if len(initDur) == 0 {
		c.gap("no init_duration datapoints")
	}
	invokeSum := stat(inv, "Sum", false)
	errSum := stat(errs, "Sum", false)
	window := end.Sub(start).Seconds()
	metrics := metricSet{
		Invocations:          invokeSum,
		Errors:               errSum,
		Throttles:            stat(throt, "Sum", false),
		DurationAvgMs:        stat(dur, "Average", false),
		DurationP99Ms:        stat(dur, "p99", false),
		DurationMaxMs:        stat(dur, "Maximum", false),
		ConcurrentMax:        stat(cc, "Maximum", false),
		InitAvgMs:            stat(initDur, "Average", false),
		InitMaxMs:            stat(initDur, "Maximum", false),
		ProvisionedSpillover: stat(spill, "Sum", false),
		LambdaMemoryPctMax:   stat(lambdaMem, "Maximum", false),
	}
	if invokeSum != nil && window > 0 {
		metrics.RPS = new(round2(*invokeSum / window))
	}
	if invokeSum != nil && errSum != nil && *invokeSum > 0 {
		metrics.ErrorPct = new(round2(*errSum / *invokeSum * 100))
	}
	db := c.collectDB(opt, period, start, end)
	var peak *float64
	var dbMax *int
	mult, src := 0, ""
	if info.ConcurrencyCap != nil {
		mult, src = *info.ConcurrencyCap, info.CapSource
	}
	if db != nil {
		peak = db.ConnectionsMax
		dbMax = db.MaxConnections
	}
	b := connectionBudget(opt.poolMax, mult, src, dbMax, peak)
	b.Fit = "unknown"
	b.Basis = "Lambda connection demand is unverified: warm environments can retain pools; concurrency is not a backend connection limit"
	if c.gaps == nil {
		c.gaps = []string{}
	}
	id := c.readIdentity()
	return snapshot{
		Evidence: c.evidence, Assessment: assessResources(c.evidence, end, db, nil, &info), Identity: id,
		Target: target{
			Kind: "lambda", Profile: opt.profile, Region: opt.region, Function: opt.function,
			Hours: opt.hours, PeriodS: period, Start: iso(start), End: iso(end),
		},
		Lambda: &info, Metrics: metrics, Database: db, Budget: b, Gaps: c.gaps,
	}, nil
}

func (c *collector) attachALB(info *ecsInfo, metrics *metricSet, period int, start, end time.Time) {
	if len(info.groups) == 0 {
		c.gap("no ALB target group on the service")
		return
	}
	if len(info.groups) > 1 {
		c.gap("%d target groups; metrics use the first", len(info.groups))
	}
	buf := c.optional("target group", "elbv2", "describe-target-groups", "--target-group-arns", info.groups[0])
	var groups struct {
		TargetGroups []struct {
			LoadBalancerArns []string `json:"LoadBalancerArns"`
		} `json:"TargetGroups"`
	}
	if json.Unmarshal(buf, &groups) != nil || len(groups.TargetGroups) == 0 || len(groups.TargetGroups[0].LoadBalancerArns) == 0 {
		c.gap("target group has no load balancer")
		return
	}
	lbArn := groups.TargetGroups[0].LoadBalancerArns[0]
	attr := c.optional("load balancer attributes", "elbv2", "describe-load-balancer-attributes", "--load-balancer-arn", lbArn)
	if idle, ok := idleTimeout(attr); ok {
		info.ALBIdleS = &idle
	}
	tg := tgDimension(info.groups[0])
	lb := lbDimension(lbArn)
	dims := []string{"Name=TargetGroup,Value=" + tg, "Name=LoadBalancer,Value=" + lb}
	requests := c.metric("AWS/ApplicationELB", "RequestCount", dims, period, start, end, []string{"Sum"}, nil)
	latency := c.metric("AWS/ApplicationELB", "TargetResponseTime", dims, period, start, end, []string{"Average"}, []string{"p99"})
	t5 := c.metric("AWS/ApplicationELB", "HTTPCode_Target_5XX_Count", dims, period, start, end, []string{"Sum"}, nil)
	e5 := c.metric("AWS/ApplicationELB", "HTTPCode_ELB_5XX_Count", []string{"Name=LoadBalancer,Value=" + lb}, period, start, end, []string{"Sum"}, nil)
	healthy := c.metric("AWS/ApplicationELB", "HealthyHostCount", dims, period, start, end, []string{"Minimum"}, nil)
	metrics.ALBRPS = rate(requests, period)
	metrics.ALBRPSPeak = peakRate(requests, period)
	active := c.metric("AWS/ApplicationELB", "ActiveConnectionCount", []string{"Name=LoadBalancer,Value=" + lb}, period, start, end, []string{"Maximum"}, nil)
	connErr := c.metric("AWS/ApplicationELB", "TargetConnectionErrorCount", dims, period, start, end, []string{"Sum"}, nil)
	metrics.ActiveConnsMax = stat(active, "Maximum", false)
	metrics.TargetConnErrors = stat(connErr, "Sum", false)
	if metrics.ALBRPS != nil && metrics.RunningTasksAvg != nil && *metrics.RunningTasksAvg > 0 {
		metrics.RPSPerTask = new(round2(*metrics.ALBRPS / *metrics.RunningTasksAvg))
		metrics.RPSBasis = "average running tasks"
	}
	metrics.TargetAvgMs = stat(latency, "Average", true)
	metrics.TargetP99Ms = stat(latency, "p99", true)
	metrics.Target5xx = stat(t5, "Sum", false)
	metrics.ELB5xx = stat(e5, "Sum", false)
	metrics.HealthyMin = stat(healthy, "Minimum", false)
	metrics.latencyPoints = latency
}

func (c *collector) attachTaskIO(metrics *metricSet, opt options, period int, start, end time.Time) {
	dims := []string{
		"Name=ClusterName,Value=" + opt.cluster,
		"Name=ServiceName,Value=" + opt.service,
	}
	rx := c.metric("ECS/ContainerInsights", "NetworkRxBytes", dims, period, start, end, []string{"Average"}, nil)
	tx := c.metric("ECS/ContainerInsights", "NetworkTxBytes", dims, period, start, end, []string{"Average"}, nil)
	read := c.metric("ECS/ContainerInsights", "StorageReadBytes", dims, period, start, end, []string{"Sum"}, nil)
	write := c.metric("ECS/ContainerInsights", "StorageWriteBytes", dims, period, start, end, []string{"Sum"}, nil)
	metrics.NetRxMBps = networkMBps(rx)
	metrics.NetTxMBps = networkMBps(tx)
	metrics.DiskReadMBps = byteRateMBps(read, period)
	metrics.DiskWriteMBps = byteRateMBps(write, period)
	if metrics.NetRxMBps == nil && metrics.NetTxMBps == nil && metrics.DiskReadMBps == nil && metrics.DiskWriteMBps == nil {
		c.gap("no task I/O datapoints")
	}
}

func (c *collector) attachStopped(opt options, info *ecsInfo) {
	buf := c.optional("stopped tasks", "ecs", "list-tasks",
		"--cluster", opt.cluster, "--service-name", opt.service,
		"--desired-status", "STOPPED", "--max-items", "5")
	var listed struct {
		TaskArns []string `json:"taskArns"`
	}
	if json.Unmarshal(buf, &listed) != nil || len(listed.TaskArns) == 0 {
		return
	}
	args := []string{"ecs", "describe-tasks", "--cluster", opt.cluster, "--tasks"}
	args = append(args, listed.TaskArns...)
	described := c.optional("stopped tasks", args...)
	var tasks struct {
		Tasks []struct {
			StoppedReason string `json:"stoppedReason"`
		} `json:"tasks"`
	}
	_ = json.Unmarshal(described, &tasks)
	for _, task := range tasks.Tasks {
		if task.StoppedReason == "" {
			continue
		}
		if len(info.stopped) == 3 {
			break
		}
		info.stopped = append(info.stopped, task.StoppedReason)
	}
}

func (c *collector) collectDB(opt options, period int, start, end time.Time) *database {
	if opt.noDB {
		return nil
	}
	kind, name := c.pickDB(opt.dbCluster, opt.dbInstance)
	if name == "" {
		return nil
	}
	db := &database{Kind: kind, Name: name, MetricScope: name}
	var dims []string
	var raw string
	if kind == "cluster" {
		buf := c.optional("db cluster", "rds", "describe-db-clusters", "--db-cluster-identifier", name)
		var listed struct {
			DBClusters []struct {
				Engine                           string `json:"Engine"`
				DBClusterParameterGroup          string `json:"DBClusterParameterGroup"`
				ServerlessV2ScalingConfiguration *struct {
					MinCapacity float64 `json:"MinCapacity"`
					MaxCapacity float64 `json:"MaxCapacity"`
				} `json:"ServerlessV2ScalingConfiguration"`
				DBClusterMembers []struct {
					DBInstanceIdentifier string `json:"DBInstanceIdentifier"`
					IsClusterWriter      bool   `json:"IsClusterWriter"`
				} `json:"DBClusterMembers"`
			} `json:"DBClusters"`
		}
		if json.Unmarshal(buf, &listed) == nil && len(listed.DBClusters) > 0 {
			cluster := listed.DBClusters[0]
			db.Engine = cluster.Engine
			if acu := cluster.ServerlessV2ScalingConfiguration; acu != nil {
				min, max := acu.MinCapacity, acu.MaxCapacity
				db.ACUMin, db.ACUMax = &min, &max
			}
			raw = c.param("rds", "describe-db-cluster-parameters", "--db-cluster-parameter-group-name", cluster.DBClusterParameterGroup)
			writer := writerID(cluster.DBClusterMembers)
			if writer != "" {
				writerOpt := opt
				writerOpt.dbCluster = ""
				writerOpt.dbInstance = writer
				w := c.collectDB(writerOpt, period, start, end)
				if w != nil {
					w.Kind = kind
					w.Name = name
					w.ACUMin = db.ACUMin
					w.ACUMax = db.ACUMax
					if w.MaxConnectionsRaw == "" {
						parsed := parseMaxConnections(raw)
						w.MaxConnections = parsed.value
						w.MaxConnectionsRaw = parsed.raw
					}
					c.gap("cluster metrics are writer %s; readers are separate", writer)
					return w
				}
			}
		}
		c.gap("database writer unknown")
		return db
	} else {
		buf := c.optional("db instance", "rds", "describe-db-instances", "--db-instance-identifier", name)
		var listed struct {
			DBInstances []struct {
				Engine             string `json:"Engine"`
				MonitoringInterval int    `json:"MonitoringInterval"`
				DBParameterGroups  []struct {
					DBParameterGroupName string `json:"DBParameterGroupName"`
				} `json:"DBParameterGroups"`
			} `json:"DBInstances"`
		}
		if json.Unmarshal(buf, &listed) == nil && len(listed.DBInstances) > 0 {
			db.Engine = listed.DBInstances[0].Engine
			setMonitoring(db, listed.DBInstances[0].MonitoringInterval)
			if len(listed.DBInstances[0].DBParameterGroups) > 0 {
				raw = c.param("rds", "describe-db-parameters", "--db-parameter-group-name", listed.DBInstances[0].DBParameterGroups[0].DBParameterGroupName)
			}
		}
		dims = []string{"Name=DBInstanceIdentifier,Value=" + name}
	}
	parsed := parseMaxConnections(raw)
	db.MaxConnections = parsed.value
	db.MaxConnectionsRaw = parsed.raw
	if parsed.raw != "" && parsed.value == nil {
		c.gap("max_connections is a formula, not a number")
	}
	cpu := c.metric("AWS/RDS", "CPUUtilization", dims, period, start, end, []string{"Average", "Maximum"}, nil)
	conns := c.metric("AWS/RDS", "DatabaseConnections", dims, period, start, end, []string{"Average", "Maximum"}, nil)
	commit := c.metric("AWS/RDS", "CommitLatency", dims, period, start, end, []string{"Average", "Maximum"}, nil)
	db.CPUAvg, db.CPUMax = stat(cpu, "Average", false), stat(cpu, "Maximum", false)
	db.ConnectionsAvg, db.ConnectionsMax = stat(conns, "Average", false), stat(conns, "Maximum", false)
	db.CommitLatencyAvgMs, db.CommitLatencyMaxMs = stat(commit, "Average", false), stat(commit, "Maximum", false)
	c.fillDBLoad(db, dims, period, start, end)
	return db
}

func (c *collector) fillDBLoad(db *database, dims []string, period int, start, end time.Time) {
	readIOPS := c.metric("AWS/RDS", "ReadIOPS", dims, period, start, end, []string{"Average", "Maximum"}, nil)
	writeIOPS := c.metric("AWS/RDS", "WriteIOPS", dims, period, start, end, []string{"Average", "Maximum"}, nil)
	readLat := c.metric("AWS/RDS", "ReadLatency", dims, period, start, end, []string{"Average"}, nil)
	writeLat := c.metric("AWS/RDS", "WriteLatency", dims, period, start, end, []string{"Average"}, nil)
	queue := c.metric("AWS/RDS", "DiskQueueDepth", dims, period, start, end, []string{"Maximum"}, nil)
	free := c.metric("AWS/RDS", "FreeableMemory", dims, period, start, end, []string{"Minimum"}, nil)
	dead := c.metric("AWS/RDS", "Deadlocks", dims, period, start, end, []string{"Sum"}, nil)
	db.ReadIOPSAvg, db.ReadIOPSMax = stat(readIOPS, "Average", false), stat(readIOPS, "Maximum", false)
	db.WriteIOPSAvg, db.WriteIOPSMax = stat(writeIOPS, "Average", false), stat(writeIOPS, "Maximum", false)
	db.ReadLatencyAvgMs = stat(readLat, "Average", true)
	db.WriteLatencyAvgMs = stat(writeLat, "Average", true)
	db.DiskQueueMax = stat(queue, "Maximum", false)
	db.Deadlocks = stat(dead, "Sum", false)
	if min := stat(free, "Minimum", false); min != nil {
		mb := round2(*min / (1024 * 1024))
		db.FreeableMemoryMinMB = &mb
	}
}

func (c *collector) noteMonitoring(db *database, instanceID string) {
	if instanceID == "" {
		return
	}
	buf := c.optional("enhanced monitoring", "rds", "describe-db-instances", "--db-instance-identifier", instanceID)
	var listed struct {
		DBInstances []struct {
			MonitoringInterval int `json:"MonitoringInterval"`
		} `json:"DBInstances"`
	}
	if json.Unmarshal(buf, &listed) != nil || len(listed.DBInstances) == 0 {
		return
	}
	setMonitoring(db, listed.DBInstances[0].MonitoringInterval)
}

func setMonitoring(db *database, interval int) {
	on := interval > 0
	db.MonitoringIntervalS = &interval
	db.EnhancedMonitoring = &on
}

func writerID(members []struct {
	DBInstanceIdentifier string `json:"DBInstanceIdentifier"`
	IsClusterWriter      bool   `json:"IsClusterWriter"`
}) string {
	for _, member := range members {
		if member.IsClusterWriter && member.DBInstanceIdentifier != "" {
			return member.DBInstanceIdentifier
		}
	}

	return ""
}

func (c *collector) pickDB(cluster, instance string) (string, string) {
	if cluster != "" {
		return "cluster", cluster
	}
	if instance != "" {
		return "instance", instance
	}
	c.gap("database not named; pass --db-cluster, --db-instance, or --no-db")
	return "", ""
}

func (c *collector) param(service, action, flag, group string) string {
	if group == "" {
		return ""
	}
	buf := c.optional("max_connections", service, action, flag, group, "--query", "Parameters[?ParameterName=='max_connections'].ParameterValue | [0]")
	var raw string
	if json.Unmarshal(buf, &raw) != nil {
		return ""
	}
	return raw
}

func (c *collector) metric(namespace, name string, dims []string, period int, start, end time.Time, stats, extended []string) []cwPoint {
	if len(stats) > 0 && len(extended) > 0 {
		a := c.fetchMetric(namespace, name, dims, period, start, end, stats, nil)
		b := c.fetchMetric(namespace, name, dims, period, start, end, nil, extended)
		points := mergePoints(a, b)
		c.recordMetric(namespace, name, dims, period, start, end, points)
		return points
	}
	points := c.fetchMetric(namespace, name, dims, period, start, end, stats, extended)
	c.recordMetric(namespace, name, dims, period, start, end, points)
	return points
}

func (c *collector) fetchMetric(namespace, name string, dims []string, period int, start, end time.Time, stats, extended []string) []cwPoint {
	args := []string{"cloudwatch", "get-metric-statistics", "--namespace", namespace, "--metric-name", name,
		"--start-time", iso(start), "--end-time", iso(end), "--period", fmt.Sprint(period)}
	if len(stats) > 0 {
		args = append(args, "--statistics")
		args = append(args, stats...)
		if slices.Contains(stats, "Average") && !slices.Contains(stats, "SampleCount") {
			args = append(args, "SampleCount")
		}
	}
	if len(extended) > 0 {
		args = append(args, "--extended-statistics")
		args = append(args, extended...)
	}
	if len(dims) > 0 {
		args = append(args, "--dimensions")
		args = append(args, dims...)
	}
	buf := c.optional(namespace+"/"+name, args...)
	if buf == nil {
		return nil
	}
	var out struct {
		Datapoints []cwPoint `json:"Datapoints"`
	}
	if err := json.Unmarshal(buf, &out); err != nil {
		c.gap("%s/%s: invalid metric response", namespace, name)
		return nil
	}
	sort.Slice(out.Datapoints, func(i, j int) bool { return out.Datapoints[i].Timestamp < out.Datapoints[j].Timestamp })
	return out.Datapoints
}

func mergePoints(a, b []cwPoint) []cwPoint {
	byTime := map[string]cwPoint{}
	for _, p := range a {
		byTime[p.Timestamp] = p
	}
	for _, p := range b {
		if old, ok := byTime[p.Timestamp]; ok {
			old.ExtendedStatistics = p.ExtendedStatistics
			byTime[p.Timestamp] = old
		} else {
			byTime[p.Timestamp] = p
		}
	}
	out := make([]cwPoint, 0, len(byTime))
	for _, p := range byTime {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp < out[j].Timestamp })
	return out
}

func (c *collector) optional(label string, args ...string) []byte {
	buf, err := c.call.Call(args...)
	if err != nil {
		c.gap("%s: %s", label, err.Error())
		return nil
	}
	return buf
}

func (c *collector) gap(format string, args ...any) {
	if len(c.gaps) >= gapCap {
		return
	}
	msg := fmt.Sprintf(format, args...)
	for _, have := range c.gaps {
		if have == msg {
			return
		}
	}
	c.gaps = append(c.gaps, msg)
}

type cwPoint struct {
	SampleCount        *float64           `json:"SampleCount,omitempty"`
	Timestamp          string             `json:"Timestamp"`
	Average            *float64           `json:"Average"`
	Maximum            *float64           `json:"Maximum"`
	Minimum            *float64           `json:"Minimum"`
	Sum                *float64           `json:"Sum"`
	Unit               string             `json:"Unit"`
	ExtendedStatistics map[string]float64 `json:"ExtendedStatistics"`
}

func stat(points []cwPoint, field string, scaleSeconds bool) *float64 {
	var vals []float64
	var weighted, samples float64
	completeWeights := true
	for _, p := range points {
		v, ok := pointValue(p, field)
		if !ok {
			continue
		}
		if scaleSeconds && p.Unit == "Seconds" {
			v *= 1000
		}
		vals = append(vals, v)
		if p.SampleCount != nil && *p.SampleCount > 0 {
			weighted += v * *p.SampleCount
			samples += *p.SampleCount
		} else {
			completeWeights = false
		}
	}
	if len(vals) == 0 {
		return nil
	}
	var out float64
	switch field {
	case "Sum":
		for _, v := range vals {
			out += v
		}
	case "Maximum", "p99":
		out = vals[0]
		for _, v := range vals[1:] {
			if v > out {
				out = v
			}
		}
	case "Minimum":
		out = vals[0]
		for _, v := range vals[1:] {
			if v < out {
				out = v
			}
		}
	default:
		for _, v := range vals {
			out += v
		}
		out /= float64(len(vals))
		if field == "Average" && completeWeights && samples > 0 {
			out = weighted / samples
		}
	}
	return new(round2(out))
}

func pointValue(p cwPoint, field string) (float64, bool) {
	switch field {
	case "Average":
		if p.Average == nil {
			return 0, false
		}
		return *p.Average, true
	case "Maximum":
		if p.Maximum == nil {
			return 0, false
		}
		return *p.Maximum, true
	case "Minimum":
		if p.Minimum == nil {
			return 0, false
		}
		return *p.Minimum, true
	case "Sum":
		if p.Sum == nil {
			return 0, false
		}
		return *p.Sum, true
	default:
		v, ok := p.ExtendedStatistics[field]
		return v, ok
	}
}

func rate(points []cwPoint, period int) *float64 {
	total := stat(points, "Sum", false)
	if total == nil || period <= 0 || len(points) == 0 {
		return nil
	}
	r := round2(*total / float64(len(points)*period))
	return &r
}

func peakRate(points []cwPoint, period int) *float64 {
	if period <= 0 {
		return nil
	}
	var max float64
	var seen bool
	for _, point := range points {
		sum, ok := pointValue(point, "Sum")
		if !ok {
			continue
		}
		value := sum / float64(period)
		if !seen || value > max {
			max = value
			seen = true
		}
	}
	if !seen {
		return nil
	}
	out := round2(max)
	return &out
}

func byteRateMBps(points []cwPoint, period int) *float64 {
	perSecond := rate(points, period)
	if perSecond == nil {
		return nil
	}
	out := round2(*perSecond / 1_000_000)
	return &out
}

func fillInflight(m *metricSet, idle *int) {
	if m.RPSPerTask != nil && m.TargetAvgMs != nil {
		v := round2(*m.RPSPerTask * (*m.TargetAvgMs / 1000))
		m.InflightPerTask = &v
	}
	if m.RPSPerTask != nil && idle != nil && *idle > 0 {
		v := round2(*m.RPSPerTask * float64(*idle))
		m.StallInflightPerTask = &v
	}
}

func sinceDeploy(points []cwPoint, at, field string, scaleSeconds bool) *split {
	moment, ok := parseTime(at)
	if !ok {
		return nil
	}
	var before, after []float64
	for _, p := range points {
		ts, ok := parseTime(p.Timestamp)
		v, okV := pointValue(p, field)
		if !ok || !okV {
			continue
		}
		if scaleSeconds && p.Unit == "Seconds" {
			v *= 1000
		}
		if ts.Before(moment) {
			before = append(before, v)
			continue
		}
		after = append(after, v)
	}
	if len(before) < 3 || len(after) < 3 {
		return nil
	}
	return &split{At: at, Before: round2(mean(before)), After: round2(mean(after))}
}

func connectionBudget(poolMax, multiplier int, source string, dbMax *int, peak *float64) budget {
	b := budget{Basis: "limit unknown: explicit reserve, other-consumer budget and deployment overlap required", Fit: "unknown", MultiplierSource: source, Peak: peak, DBMax: dbMax}
	if poolMax > 0 {
		b.PoolMax = &poolMax
	}
	if multiplier > 0 {
		b.Multiplier = &multiplier
	}
	if poolMax > 0 && multiplier > 0 {
		product := poolMax * multiplier
		b.Product = &product
	}
	return b
}

func multiplier(max *int, desired int) (int, string) {
	if max != nil && *max > 0 {
		return *max, "scaling max"
	}
	if desired > 0 {
		return desired, "desired, no scaling max"
	}
	return 0, ""
}

func periodFor(hours int) int {
	if hours <= 6 {
		return 60
	}
	if hours <= 48 {
		return 300
	}
	return 3600
}

type maxConn struct {
	raw   string
	value *int
}

func parseMaxConnections(raw string) maxConn {
	raw = strings.Trim(strings.TrimSpace(raw), `"`)
	if raw == "" || raw == "null" {
		return maxConn{}
	}
	if n := atoi(raw); n > 0 && strconvDigits(raw) {
		return maxConn{raw: raw, value: &n}
	}
	return maxConn{raw: raw}
}

func idleTimeout(buf []byte) (int, bool) {
	var listed struct {
		Attributes []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Attributes"`
	}
	if json.Unmarshal(buf, &listed) != nil {
		return 0, false
	}
	for _, attr := range listed.Attributes {
		if attr.Key == "idle_timeout.timeout_seconds" {
			n := atoi(attr.Value)
			if n > 0 {
				return n, true
			}
		}
	}
	return 0, false
}

func parseECS(raw ecsService) ecsInfo {
	primary := ecsDeployment{}
	for _, dep := range raw.Deployments {
		if dep.Status == "PRIMARY" {
			primary = dep
			break
		}
	}
	if primary.Status == "" && len(raw.Deployments) > 0 {
		primary = raw.Deployments[0]
	}
	var groups []string
	for _, lb := range raw.LoadBalancers {
		if lb.TargetGroupArn != "" {
			groups = append(groups, lb.TargetGroupArn)
		}
	}
	launch := raw.LaunchType
	if launch == "" {
		var names []string
		for _, p := range raw.CapacityProviderStrategy {
			if p.CapacityProvider != "" {
				names = append(names, p.CapacityProvider)
			}
		}
		launch = strings.Join(names, ",")
	}
	if launch == "" {
		launch = "unknown"
	}
	return ecsInfo{
		TaskDefinition:       raw.TaskDefinition,
		DeploymentMaxPercent: raw.DeploymentConfiguration.MaximumPercent,
		Status:               raw.Status,
		Desired:              raw.DesiredCount,
		Running:              raw.RunningCount,
		Launch:               launch,
		groups:               groups,
		DeployedAt:           primary.CreatedAt,
		Rollout:              primary.RolloutState,
		DeployRollback: deployRollback{
			Enabled:  raw.DeploymentConfiguration.DeploymentCircuitBreaker.Enable,
			Rollback: raw.DeploymentConfiguration.DeploymentCircuitBreaker.Rollback,
		},
	}
}

type ecsService struct {
	Status         string `json:"status"`
	DesiredCount   int    `json:"desiredCount"`
	RunningCount   int    `json:"runningCount"`
	LaunchType     string `json:"launchType"`
	TaskDefinition string `json:"taskDefinition"`
	LoadBalancers  []struct {
		TargetGroupArn string `json:"targetGroupArn"`
	} `json:"loadBalancers"`
	CapacityProviderStrategy []struct {
		CapacityProvider string `json:"capacityProvider"`
	} `json:"capacityProviderStrategy"`
	Deployments             []ecsDeployment `json:"deployments"`
	DeploymentConfiguration struct {
		MaximumPercent           int `json:"maximumPercent"`
		DeploymentCircuitBreaker struct {
			Enable   bool `json:"enable"`
			Rollback bool `json:"rollback"`
		} `json:"deploymentCircuitBreaker"`
	} `json:"deploymentConfiguration"`
}

type ecsDeployment struct {
	Status       string `json:"status"`
	CreatedAt    string `json:"createdAt"`
	RolloutState string `json:"rolloutState"`
}

func parseScaling(targetBuf, policyBuf []byte) (*int, *int, []scalePolicy) {
	var targets struct {
		ScalableTargets []struct {
			MinCapacity int `json:"MinCapacity"`
			MaxCapacity int `json:"MaxCapacity"`
		} `json:"ScalableTargets"`
	}
	_ = json.Unmarshal(targetBuf, &targets)
	var minPtr, maxPtr *int
	if len(targets.ScalableTargets) > 0 {
		minPtr = &targets.ScalableTargets[0].MinCapacity
		maxPtr = &targets.ScalableTargets[0].MaxCapacity
	}
	var policies struct {
		ScalingPolicies []struct {
			PolicyName                               string `json:"PolicyName"`
			PolicyType                               string `json:"PolicyType"`
			TargetTrackingScalingPolicyConfiguration struct {
				TargetValue                   *float64 `json:"TargetValue"`
				ScaleOutCooldown              *int     `json:"ScaleOutCooldown"`
				ScaleInCooldown               *int     `json:"ScaleInCooldown"`
				PredefinedMetricSpecification struct {
					PredefinedMetricType string `json:"PredefinedMetricType"`
				} `json:"PredefinedMetricSpecification"`
				CustomizedMetricSpecification struct {
					MetricName string `json:"MetricName"`
				} `json:"CustomizedMetricSpecification"`
			} `json:"TargetTrackingScalingPolicyConfiguration"`
		} `json:"ScalingPolicies"`
	}
	_ = json.Unmarshal(policyBuf, &policies)
	var out []scalePolicy
	for _, p := range policies.ScalingPolicies {
		cfg := p.TargetTrackingScalingPolicyConfiguration
		metric := cfg.PredefinedMetricSpecification.PredefinedMetricType
		if metric == "" {
			metric = cfg.CustomizedMetricSpecification.MetricName
		}
		out = append(out, scalePolicy{
			Name: p.PolicyName, Type: p.PolicyType, Metric: metric, Target: cfg.TargetValue,
			ScaleOutS: cfg.ScaleOutCooldown, ScaleInS: cfg.ScaleInCooldown,
		})
	}
	return minPtr, maxPtr, out
}

func lbDimension(arn string) string {
	if i := strings.Index(arn, "loadbalancer/"); i >= 0 {
		return arn[i+len("loadbalancer/"):]
	}
	return arn
}

func tgDimension(arn string) string {
	if i := strings.LastIndex(arn, ":"); i >= 0 {
		return arn[i+1:]
	}
	return arn
}

func joinNames(names []string) string {
	if len(names) > 8 {
		return strings.Join(names[:8], ", ") + fmt.Sprintf(" (+%d)", len(names)-8)
	}
	return strings.Join(names, ", ")
}

func iso(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

func parseTime(value string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, value); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func mean(vals []float64) float64 {
	var s float64
	for _, v := range vals {
		s += v
	}
	return s / float64(len(vals))
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func atoi(s string) int {
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func strconvDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
