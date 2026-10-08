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
	Cloud   *LiveKitTOMLCloudConfig   `toml:"cloud,omitempty"`
}

type LiveKitTOMLProjectConfig struct {
	Subdomain string `toml:"subdomain"`
}

type LiveKitTOMLAgentConfig struct {
	// Deprecated: the id lives in [cloud]; a legacy value is moved there on load.
	ID string `toml:"id,omitempty"`
	// Identity of the agent under test in simulation runs; self-hosted agents
	// set it by hand.
	Name string `toml:"name"`
}

// LiveKitTOMLCloudConfig identifies the agent on LiveKit Cloud.
type LiveKitTOMLCloudConfig struct {
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
	return c.Agent != nil || c.Cloud != nil
}

// AgentID returns the Cloud Agents id, or "" for an agent not on Cloud.
func (c *LiveKitTOML) AgentID() string {
	if c.Cloud == nil {
		return ""
	}
	return c.Cloud.ID
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
	util.Statusf("Saving config file [%s]", util.Accented(tomlFileName))
	return nil
}

func LoadTOMLFile(dir string, tomlFileName string) (*LiveKitTOML, bool, error) {
	logger.Debugw(fmt.Sprintf("loading %s file", tomlFileName))
	path := filepath.Join(dir, tomlFileName)

	if _, err := os.Stat(path); err != nil {
		return nil, !errors.Is(err, fs.ErrNotExist), err
	}

	var config LiveKitTOML
	if _, err := toml.DecodeFile(path, &config); err != nil {
		return nil, true, err
	}
	if config.Project == nil {
		// Attempt to decode old agent config
		var oldConfig AgentTOML
		if _, err := toml.DecodeFile(path, &oldConfig); err != nil {
			return nil, true, err
		}
		config.Project = &LiveKitTOMLProjectConfig{
			Subdomain: oldConfig.ProjectSubdomain,
		}
		config.Agent = &LiveKitTOMLAgentConfig{}
	}
	if config.Agent != nil && config.Agent.ID != "" {
		if config.Cloud == nil {
			config.Cloud = &LiveKitTOMLCloudConfig{ID: config.Agent.ID}
		}
		config.Agent.ID = ""
	}
	return &config, true, nil
}
