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
	Cloud   *LiveKitTOMLCloudConfig   `toml:"-"`
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

// LiveKitTOMLCloudConfig holds the Cloud Agents id(s) for the agent. ID is the
// id of a single-region agent; Regions maps region code to id when the agent
// is deployed per region. Exactly one of the two is populated.
type LiveKitTOMLCloudConfig struct {
	ID      string
	Regions map[string]string
}

// tomlFile is the on-disk shape: [cloud] is "id" and/or one sub-table per
// region, which a struct with fixed fields cannot express.
type tomlFile struct {
	Project *LiveKitTOMLProjectConfig `toml:"project"`
	Agent   *LiveKitTOMLAgentConfig   `toml:"agent"`
	Cloud   map[string]any            `toml:"cloud,omitempty"`
}

func (c *LiveKitTOML) toFile() *tomlFile {
	f := &tomlFile{Project: c.Project, Agent: c.Agent}
	if c.Cloud == nil {
		return f
	}
	f.Cloud = map[string]any{}
	if c.Cloud.ID != "" {
		f.Cloud["id"] = c.Cloud.ID
	}
	for region, id := range c.Cloud.Regions {
		f.Cloud[region] = map[string]string{"id": id}
	}
	return f
}

func (f *tomlFile) toConfig() (*LiveKitTOML, error) {
	c := &LiveKitTOML{Project: f.Project, Agent: f.Agent}
	if c.Agent != nil && c.Agent.ID != "" {
		c.Cloud = &LiveKitTOMLCloudConfig{ID: c.Agent.ID}
		c.Agent.ID = ""
	}
	if len(f.Cloud) == 0 {
		return c, nil
	}
	if c.Cloud == nil {
		c.Cloud = &LiveKitTOMLCloudConfig{}
	}
	for key, value := range f.Cloud {
		switch v := value.(type) {
		case string:
			if key != "id" {
				return nil, fmt.Errorf("[cloud] %s: unknown key: %w", key, ErrInvalidConfig)
			}
			c.Cloud.ID = v
		case map[string]any:
			id, _ := v["id"].(string)
			if id == "" {
				return nil, fmt.Errorf("[cloud.%s] id is required: %w", key, ErrInvalidConfig)
			}
			if c.Cloud.Regions == nil {
				c.Cloud.Regions = map[string]string{}
			}
			c.Cloud.Regions[key] = id
		default:
			return nil, fmt.Errorf("[cloud] %s: unexpected value: %w", key, ErrInvalidConfig)
		}
	}
	if c.Cloud.ID != "" && len(c.Cloud.Regions) > 0 {
		return nil, fmt.Errorf("[cloud] id and [cloud.<region>] tables are mutually exclusive: %w", ErrInvalidConfig)
	}
	return c, nil
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

// AgentIDs returns the Cloud Agents ids keyed by region; a single-region id
// is keyed by "".
func (c *LiveKitTOML) AgentIDs() map[string]string {
	if c.Cloud == nil {
		return nil
	}
	if c.Cloud.ID != "" {
		return map[string]string{"": c.Cloud.ID}
	}
	return c.Cloud.Regions
}

// AgentID returns the id deployed to region, or the only id when region is "".
func (c *LiveKitTOML) AgentID(region string) (string, error) {
	ids := c.AgentIDs()
	if len(ids) == 0 {
		return "", fmt.Errorf("no agent id in [cloud]: %w", ErrInvalidConfig)
	}
	if region == "" {
		if len(ids) > 1 {
			return "", fmt.Errorf("%s lists %d regions; pass --region: %w", LiveKitTOMLFile, len(ids), ErrInvalidConfig)
		}
		for _, id := range ids {
			return id, nil
		}
	}
	if id, ok := ids[region]; ok {
		return id, nil
	}
	if id, ok := ids[""]; ok {
		return id, nil
	}
	return "", fmt.Errorf("no agent id for region %q in %s: %w", region, LiveKitTOMLFile, ErrInvalidConfig)
}

// SetAgentID records id for region. The layout stays flat until a second
// region is added, at which point the existing id is keyed by existingRegion.
func (c *LiveKitTOML) SetAgentID(region, id, existingRegion string) {
	if c.Cloud == nil {
		c.Cloud = &LiveKitTOMLCloudConfig{}
	}
	if len(c.Cloud.Regions) == 0 && (c.Cloud.ID == "" || region == "" || region == existingRegion) {
		c.Cloud.ID = id
		return
	}
	if c.Cloud.Regions == nil {
		c.Cloud.Regions = map[string]string{}
	}
	if c.Cloud.ID != "" {
		c.Cloud.Regions[existingRegion] = c.Cloud.ID
		c.Cloud.ID = ""
	}
	c.Cloud.Regions[region] = id
}

func (c *LiveKitTOML) SaveTOMLFile(dir string, tomlFileName string) error {
	f, err := os.Create(filepath.Join(dir, tomlFileName))
	if err != nil {
		return err
	}
	defer f.Close()
	encoder := toml.NewEncoder(f)
	if err := encoder.Encode(c.toFile()); err != nil {
		return fmt.Errorf("error encoding TOML: %w", err)
	}
	fmt.Printf("Saving config file [%s]\n", util.Accented(tomlFileName))
	return nil
}

func LoadTOMLFile(dir string, tomlFileName string) (*LiveKitTOML, bool, error) {
	logger.Debugw(fmt.Sprintf("loading %s file", tomlFileName))
	path := filepath.Join(dir, tomlFileName)

	if _, err := os.Stat(path); err != nil {
		return nil, !errors.Is(err, fs.ErrNotExist), err
	}

	var file tomlFile
	if _, err := toml.DecodeFile(path, &file); err != nil {
		return nil, true, err
	}
	if file.Project == nil {
		// Attempt to decode old agent config
		var oldConfig AgentTOML
		if _, err := toml.DecodeFile(path, &oldConfig); err != nil {
			return nil, true, err
		}
		file.Project = &LiveKitTOMLProjectConfig{
			Subdomain: oldConfig.ProjectSubdomain,
		}
		file.Agent = &LiveKitTOMLAgentConfig{}
	}
	config, err := file.toConfig()
	return config, true, err
}
