package main

import (
	"slices"
	"strings"
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

func TestMapStrings(t *testing.T) {
	initial := []string{"a1", "b2", "c3"}
	mapped := mapStrings(initial, func(s string) string {
		return strings.ToUpper(s)
	})
	if len(mapped) != len(initial) {
		t.Error("mapStrings should return a slice of the same length")
	}
	if !slices.Equal([]string{"A1", "B2", "C3"}, mapped) {
		t.Error("mapStrings should apply the function to all elements")
	}
}
