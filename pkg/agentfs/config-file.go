// Copyright 2025 LiveKit, Inc.
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

package agentfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/livekit/protocol/logger"
)

type AgentTOML struct {
	ProjectSubdomain string `toml:"project_subdomain"`
	Name             string `toml:"name"`
	CPU              string `toml:"cpu"`
	Replicas         int    `toml:"replicas"`
	MaxReplicas      int    `toml:"max_replicas"`

	Regions []string `toml:"regions"`
}

const (
	AgentTOMLFile = "livekit.toml"
)

func LoadTomlFile(dir string, tomlFileName string) (*AgentTOML, bool, error) {
	logger.Debugw(fmt.Sprintf("loading %s file", tomlFileName))
	var agentConfig AgentTOML
	var err error
	var configExists bool = true

	tomlFile := filepath.Join(dir, tomlFileName)

	if _, err = os.Stat(tomlFile); err == nil {
		_, err = toml.DecodeFile(tomlFile, &agentConfig)
		if err != nil {
			return nil, configExists, err
		}
	} else {
		if errors.Is(err, os.ErrNotExist) {
			configExists = false
		}
	}

	return &agentConfig, configExists, err
}
