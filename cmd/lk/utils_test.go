// Copyright 2024 LiveKit, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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
