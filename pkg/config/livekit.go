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
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/livekit/protocol/logger"
)

const (
	LiveKitTOMLFile            = "livekit.toml"
	clientDefaults_CPU         = "1"
	clientDefaults_Replicas    = 1
	clientDefaults_MaxReplicas = 10
)

// Deprecated: use LiveKitTOML instead
type AgentTOML struct {
	ProjectSubdomain string    `toml:"project_subdomain"`
	Name             string    `toml:"name"`
	CPU              CPUString `toml:"cpu"`
	Replicas         int       `toml:"replicas"`
	MaxReplicas      int       `toml:"max_replicas"`

	Regions []string `toml:"regions"`
}

type LiveKitTOML struct {
	Project *LiveKitTOMLProjectConfig `toml:"project"` // Required
	Agent   *LiveKitTOMLAgentConfig   `toml:"agent"`
}

type LiveKitTOMLProjectConfig struct {
	Subdomain string `toml:"subdomain"`
}

type LiveKitTOMLAgentConfig struct {
	Name        string    `toml:"name"`
	CPU         CPUString `toml:"cpu"`
	Replicas    int       `toml:"replicas"`
	MaxReplicas int       `toml:"max_replicas"`
}

func NewLiveKitTOML(forSubdomain string) *LiveKitTOML {
	return &LiveKitTOML{
		Project: &LiveKitTOMLProjectConfig{
			Subdomain: forSubdomain,
		},
	}
}

func (c *LiveKitTOML) WithDefaultAgent(agentName string) *LiveKitTOML {
	c.Agent = &LiveKitTOMLAgentConfig{
		Name:        agentName,
		CPU:         CPUString(clientDefaults_CPU),
		Replicas:    clientDefaults_Replicas,
		MaxReplicas: clientDefaults_MaxReplicas,
	}
	return c
}

func (c *LiveKitTOML) HasAgent() bool {
	return c.Agent != nil
}

type CPUString string

func (c *CPUString) UnmarshalTOML(v interface{}) error {
	switch value := v.(type) {
	case int64:
		*c = CPUString(fmt.Sprintf("%d", value))
	case float64:
		*c = CPUString(fmt.Sprintf("%g", value))
	case string:
		*c = CPUString(value)
	default:
		return fmt.Errorf("invalid type for cpu: %T", v)
	}
	return nil
}

func LoadTomlFile(dir string, tomlFileName string) (*LiveKitTOML, bool, error) {
	logger.Debugw(fmt.Sprintf("loading %s file", tomlFileName))
	var config LiveKitTOML
	var err error
	var configExists bool = true

	tomlFile := filepath.Join(dir, tomlFileName)

	if _, err = os.Stat(tomlFile); err == nil {
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
			config.Agent = &LiveKitTOMLAgentConfig{
				Name:        oldConfig.Name,
				CPU:         oldConfig.CPU,
				Replicas:    oldConfig.Replicas,
				MaxReplicas: oldConfig.MaxReplicas,
			}
		}
	} else {
		if errors.Is(err, os.ErrNotExist) {
			configExists = false
		}
	}

	if configExists {
		// validate agent config
		if config.HasAgent() && config.Agent.Replicas > config.Agent.MaxReplicas {
			return nil, configExists, fmt.Errorf("replicas cannot be greater than max_replicas")
		}
	}

	return &config, configExists, err
}
