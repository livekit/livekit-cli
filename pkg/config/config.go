// Copyright 2022-2024 LiveKit, Inc.
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
	"path"
	"strings"

	"github.com/livekit/livekit-cli/v2/pkg/util"
	"gopkg.in/yaml.v3"
)

type CLIConfig struct {
	DefaultProject string          `yaml:"default_project"`
	Projects       []ProjectConfig `yaml:"projects"`
	DeviceName     string          `yaml:"device_name"`
	// absent from YAML
	hasPersisted bool
}

type ProjectConfig struct {
	Name      string `yaml:"name"`
	ProjectId string `yaml:"project_id"`
	URL       string `yaml:"url"`
	APIKey    string `yaml:"api_key"`
	APISecret string `yaml:"api_secret"`
}

func LoadDefaultProject() (*ProjectConfig, error) {
	conf, err := LoadOrCreate()
	if err != nil {
		return nil, err
	}

	// prefer default project
	if conf.DefaultProject != "" {
		for _, p := range conf.Projects {
			if p.Name == conf.DefaultProject {
				return &p, nil
			}
		}
	}

	return nil, errors.New("no default project set")
}

func LoadProjectBySubdomain(subdomain string) (*ProjectConfig, error) {
	conf, err := LoadOrCreate()
	if err != nil {
		return nil, err
	}

	if subdomain == "" {
		return nil, errors.New("invalid URL")
	}

	for _, p := range conf.Projects {
		projectSubdomain := util.ExtractSubdomain(p.URL)
		if projectSubdomain == subdomain {
			fmt.Printf("Using project [%s]\n", util.Accented(p.Name))
			return &p, nil
		}
	}

	return nil, errors.New("project not found")
}

func LoadProject(name string) (*ProjectConfig, error) {
	conf, err := LoadOrCreate()
	if err != nil {
		return nil, err
	}

	for _, p := range conf.Projects {
		if p.Name == name {
			return &p, nil
		}
	}

	return nil, errors.New("project not found")
}

// LoadOrCreate loads config file from ~/.livekit/cli-config.yaml
// if it doesn't exist, it'll return an empty config file
func LoadOrCreate() (*CLIConfig, error) {
	configPath, err := getConfigLocation()
	if err != nil {
		return nil, err
	}

	c := &CLIConfig{}
	if s, err := os.Stat(configPath); os.IsNotExist(err) {
		return c, nil
	} else if err != nil {
		return nil, err
	} else if s.Mode().Perm()&0077 != 0 {
		// because this file contains private keys, warn that
		// only the owner should have permission to access it
		fmt.Fprintf(os.Stderr, "WARNING: config file %s should have permissions %o\n", configPath, 0600)
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	err = yaml.Unmarshal(content, c)
	if err != nil {
		return nil, err
	}
	c.hasPersisted = true

	return c, nil
}

func (c *CLIConfig) ProjectExists(name string) bool {
	for _, p := range c.Projects {
		if strings.EqualFold(p.Name, name) {
			return true
		}
	}
	return false
}

func (c *CLIConfig) RemoveProject(name string) error {
	var newProjects []ProjectConfig
	for _, p := range c.Projects {
		if p.Name == name {
			continue
		}
		newProjects = append(newProjects, p)
	}
	c.Projects = newProjects

	if c.DefaultProject == name {
		c.DefaultProject = ""
	}

	if err := c.PersistIfNeeded(); err != nil {
		return err
	}

	fmt.Println("Removed project", name)
	return nil
}

func (c *CLIConfig) PersistIfNeeded() error {
	if len(c.Projects) == 0 && !c.hasPersisted {
		// doesn't need to be persisted
		return nil
	}

	configPath, err := getConfigLocation()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(path.Dir(configPath), 0700); err != nil {
		return err
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}

	if err = os.WriteFile(configPath, data, 0600); err != nil {
		return err
	}
	fmt.Printf("Saved CLI config to [%s]\n", util.Accented(configPath))
	c.hasPersisted = true
	return nil
}

func getConfigLocation() (string, error) {
	dir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return path.Join(dir, ".livekit", "cli-config.yaml"), nil
}
