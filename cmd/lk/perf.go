package main

import "github.com/urfave/cli/v3"

var (
	PerfCommands = []*cli.Command{
		{
			Name:        "perf",
			Usage:       "Performance testing commands",
			Description: "Commands for running various performance tests",
			Commands:    append(LoadTestCommands, AgentLoadTestCommands...),
		},
	}
)
