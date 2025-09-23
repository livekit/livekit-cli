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

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/livekit/protocol/logger"
)

const (
	LiveKitTOMLFile = "livekit.toml"
)

var (
	ErrInvalidConfig       = errors.New("invalid configuration file")
	ErrInvalidReplicaCount = fmt.Errorf("replicas cannot be greater than max_replicas: %w", ErrInvalidConfig)
)

// Deprecated: use LiveKitTOML instead
type AgentTOML struct {
	ProjectSubdomain string `toml:"project_subdomain"`
}

type LiveKitTOML struct {
	Project *LiveKitTOMLProjectConfig `toml:"project"` // Required
	Agent   *LiveKitTOMLAgentConfig   `toml:"agent"`
}

type LiveKitTOMLProjectConfig struct {
	Subdomain string `toml:"subdomain"`
}

type LiveKitTOMLAgentConfig struct {
	ID string `toml:"id"`
}

func NewLiveKitTOML(forSubdomain string) *LiveKitTOML {
	return &LiveKitTOML{
		Project: &LiveKitTOMLProjectConfig{
			Subdomain: forSubdomain,
		},
	}
}

func (c *LiveKitTOML) WithDefaultAgent() *LiveKitTOML {
	c.Agent = &LiveKitTOMLAgentConfig{}
	return c
}

func (c *LiveKitTOML) HasAgent() bool {
	return c.Agent != nil
}

func (c *LiveKitTOML) SaveTOMLFile(dir string, tomlFileName string) error {
	f, err := os.Create(filepath.Join(dir, tomlFileName))
	if err != nil {
		return err
	}
	defer f.Close()
	encoder := toml.NewEncoder(f)
	if err := encoder.Encode(c); err != nil {
		return fmt.Errorf("error encoding TOML: %w", err)
	}
	fmt.Printf("Saving config file [%s]\n", util.Accented(tomlFileName))
	return nil
}

func LoadTOMLFile(dir string, tomlFileName string) (*LiveKitTOML, bool, error) {
	logger.Debugw(fmt.Sprintf("loading %s file", tomlFileName))
	var config *LiveKitTOML = nil
	var err error
	var configExists bool = false

	tomlFile := filepath.Join(dir, tomlFileName)

	if _, err = os.Stat(tomlFile); err == nil {
		configExists = true

		_, err = toml.DecodeFile(tomlFile, &config)
		if config.Project == nil {
			// Attempt to decode old agent config
			var oldConfig AgentTOML
			_, err = toml.DecodeFile(tomlFile, &oldConfig)
			if err != nil {
				return nil, configExists, err
			}
			config.Project = &LiveKitTOMLProjectConfig{
				Subdomain: oldConfig.ProjectSubdomain,
			}
			config.Agent = &LiveKitTOMLAgentConfig{}
		}
	} else {
		configExists = !errors.Is(err, fs.ErrNotExist)
	}

	return config, configExists, err
}
