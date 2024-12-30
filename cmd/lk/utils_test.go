package main

import (
	"testing"

	"github.com/urfave/cli/v3"
)

func TestOptionalFlag(t *testing.T) {
	requiredFlag := &cli.StringFlag{
		Name:     "test",
		Required: true,
	}
	optionalFlag := optional(requiredFlag)

	if requiredFlag == optionalFlag {
		t.Error("optional should return a new flag")
	}
	if !requiredFlag.Required {
		t.Error("optional should not mutate the original flag")
	}
	if optionalFlag.Required {
		t.Error("optional should return a new flag with Required set to false")
	}
}

func TestHiddenFlag(t *testing.T) {
	visibleFlag := &cli.StringFlag{
		Name:   "test",
		Hidden: false,
	}
	hiddenFlag := hidden(visibleFlag)

	if visibleFlag == hiddenFlag {
		t.Error("hidden should return a new flag")
	}
	if visibleFlag.Hidden {
		t.Error("hidden should not mutate the original flag")
	}
	if !hiddenFlag.Hidden {
		t.Error("hidden should return a new flag with Hidden set to true")
	}
}
