//go:build !console

package main

import "github.com/urfave/cli/v3"

// ConsoleCommands is nil when built without the console tag.
// This ensures the default build (CGO_ENABLED=0) is unaffected.
var ConsoleCommands []*cli.Command
