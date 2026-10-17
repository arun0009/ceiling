// Command ceiling prints read-only capacity facts for one service.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

const usageText = `ceiling scan [--app-profile PROFILE] [dir]
ceiling collect ecs --cluster C --service S --region R [--hours 24] [--pool-max N]
ceiling collect lambda --function NAME --region R [--hours 24] [--pool-max N]

Collection options:
  --profile NAME                    AWS CLI profile, for example orders-prod
  --hours N                         1..336; use 3 for current pressure, 24 for trends
  --series                          include timestamped metric datapoints
  --db-instance NAME | --db-cluster NAME | --no-db
  --pool-max N                      verified total per-task pool capacity for this DB
  --db-reserve-connections N         requires --db-other-connections and --pool-max
  --db-other-connections N           other consumers' budget (zero must be explicit)
Region is --region, else the region on --profile, else AWS_REGION or AWS_DEFAULT_REGION.
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usageText)
		return 2
	}
	switch args[0] {
	case "scan":
		return runScan(args[1:])
	case "collect":
		return runCollect(args[1:])
	default:
		fmt.Fprint(os.Stderr, usageText)
		return 2
	}
}

func runScan(args []string) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	profile := fs.String("app-profile", "", "explicit application profile; otherwise conflicting pool settings remain unknown")
	if err := fs.Parse(args); err != nil || fs.NArg() > 1 {
		fmt.Fprint(os.Stderr, usageText)
		return 2
	}
	dir := "."
	if fs.NArg() == 1 {
		dir = fs.Arg(0)
	}
	result, err := scanProfile(dir, *profile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	return writeJSON(result)
}

func runCollect(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usageText)
		return 2
	}
	kind := args[0]
	fs := flag.NewFlagSet("collect", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	region := fs.String("region", "", "")
	profile := fs.String("profile", "", "")
	hours := fs.Int("hours", 24, "")
	series := fs.Bool("series", false, "include raw timestamped datapoints")
	reserve := fs.Int("db-reserve-connections", -1, "operational connection reserve")
	other := fs.Int("db-other-connections", -1, "connection budget for other consumers")
	poolMax := fs.Int("pool-max", 0, "")
	dbCluster := fs.String("db-cluster", "", "")
	dbInstance := fs.String("db-instance", "", "")
	noDB := fs.Bool("no-db", false, "")
	cluster := fs.String("cluster", "", "")
	service := fs.String("service", "", "")
	function := fs.String("function", "", "")
	if err := fs.Parse(args[1:]); err != nil {
		fmt.Fprint(os.Stderr, usageText)
		return 2
	}
	if fs.NArg() != 0 || (*noDB && (*dbCluster != "" || *dbInstance != "")) {
		fmt.Fprintln(os.Stderr, "unexpected arguments or conflicting database flags")
		return 2
	}
	budgetKnown := flagPassed(fs, "db-reserve-connections") || flagPassed(fs, "db-other-connections")
	if budgetKnown && (*reserve < 0 || *other < 0 || *poolMax <= 0) {
		fmt.Fprintln(os.Stderr, "connection budget requires nonnegative --db-reserve-connections and --db-other-connections plus --pool-max")
		return 2
	}
	if budgetKnown && kind != "ecs" {
		fmt.Fprintln(os.Stderr, "deployment connection-budget flags apply only to ECS; validate Lambda backend connections separately")
		return 2
	}
	prof := *profile
	if prof == "" {
		prof = os.Getenv("AWS_PROFILE")
	}
	reg, err := resolveRegion(*region, prof, profileRegion(prof), os.Getenv("AWS_REGION"), os.Getenv("AWS_DEFAULT_REGION"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if *hours < 1 || *hours > 336 {
		fmt.Fprintln(os.Stderr, "--hours must be from 1 to 336")
		return 2
	}
	if *poolMax < 0 || (*poolMax == 0 && flagPassed(fs, "pool-max")) {
		fmt.Fprintln(os.Stderr, "--pool-max must be >= 1")
		return 2
	}
	if *dbCluster != "" && *dbInstance != "" {
		fmt.Fprintln(os.Stderr, "pass one of --db-cluster or --db-instance")
		return 2
	}
	opt := options{
		series: *series, dbReserve: *reserve, dbOther: *other, budgetKnown: budgetKnown,
		region:     reg,
		profile:    prof,
		hours:      *hours,
		poolMax:    *poolMax,
		dbCluster:  *dbCluster,
		dbInstance: *dbInstance,
		noDB:       *noDB,
		cluster:    *cluster,
		service:    *service,
		function:   *function,
	}
	var snap snapshot
	switch kind {
	case "ecs":
		if opt.cluster == "" || opt.service == "" {
			fmt.Fprintln(os.Stderr, "ecs requires --cluster and --service")
			return 2
		}
		snap, err = collectECS(awsCLI{region: reg, profile: opt.profile}, opt)
	case "lambda":
		if opt.function == "" {
			fmt.Fprintln(os.Stderr, "lambda requires --function")
			return 2
		}
		snap, err = collectLambda(awsCLI{region: reg, profile: opt.profile}, opt)
	default:
		fmt.Fprint(os.Stderr, usageText)
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return writeJSON(snap)
}

func flagPassed(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func writeJSON(v any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
