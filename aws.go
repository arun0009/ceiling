package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// allowed is the complete set of AWS CLI operations this program may run.
// A write verb here is a defect: collect must not be able to change a service.
var allowed = map[string]struct{}{
	"sts get-caller-identity":                           {},
	"application-autoscaling describe-scalable-targets": {},
	"application-autoscaling describe-scaling-policies": {},
	"cloudwatch get-metric-statistics":                  {},
	"ecs describe-services":                             {},
	"ecs describe-task-definition":                      {},
	"ecs describe-tasks":                                {},
	"ecs list-tasks":                                    {},
	"elbv2 describe-load-balancer-attributes":           {},
	"elbv2 describe-target-groups":                      {},
	"lambda get-account-settings":                       {},
	"lambda get-function-concurrency":                   {},
	"lambda get-function-configuration":                 {},
	"rds describe-db-cluster-parameters":                {},
	"rds describe-db-clusters":                          {},
	"rds describe-db-instances":                         {},
	"rds describe-db-parameters":                        {},
}

const callTimeout = 30 * time.Second

// caller runs one allowlisted AWS CLI command and returns stdout.
type caller interface {
	Call(args ...string) ([]byte, error)
}

type awsCLI struct {
	region  string
	profile string
}

func (a awsCLI) Call(args ...string) ([]byte, error) {
	if err := allow(args); err != nil {
		return nil, err
	}
	cmdArgs := []string{"--output", "json"}
	if a.profile != "" {
		cmdArgs = append(cmdArgs, "--profile", a.profile)
	}
	if a.region != "" {
		cmdArgs = append(cmdArgs, "--region", a.region)
	}
	cmdArgs = append(cmdArgs, args...)
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "aws", cmdArgs...)
	out, err := cmd.Output()
	if err != nil {
		var execErr *exec.Error
		if errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
			return nil, errors.New("aws cli not found on PATH")
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, errors.New(lastLine(string(exitErr.Stderr)))
		}
		return nil, err
	}
	return out, nil
}

func allow(args []string) error {
	if len(args) < 2 {
		return errors.New("blocked aws call")
	}
	key := args[0] + " " + args[1]
	if _, ok := allowed[key]; !ok {
		return fmt.Errorf("blocked aws call: %s", key)
	}
	return nil
}

func lastLine(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "aws failed"
	}
	lines := strings.Split(text, "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	if len(line) > 180 {
		return line[:180]
	}
	return line
}
