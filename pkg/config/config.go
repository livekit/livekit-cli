// Copyright 2022-2026 LiveKit, Inc.
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
	"time"

	"github.com/livekit/livekit-cli/v2/pkg/util"
	"gopkg.in/yaml.v3"
)

type CLIConfig struct {
	DefaultProject string          `yaml:"default_project"`
	DefaultUser    string          `yaml:"default_user"`
	Projects       []ProjectConfig `yaml:"projects"`
	Users          []UserConfig    `yaml:"users"`
	DeviceName     string          `yaml:"device_name"`
	Theme          string          `yaml:"theme"`
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

type UserConfig struct {
	Id            string `yaml:"id"`
	Name          string `yaml:"name"`
	Email         string `yaml:"email"`
	SessionToken  string `yaml:"session_token"`
	SessionExpiry int64  `yaml:"session_expiry"`
	// Projects caches the projects this user can access, as last fetched from
	// the Public API (listProjects). Caching lets project resolution avoid a
	// network round-trip on every command; ProjectsFetchedAt records the Unix
	// time it was populated so callers can refresh a stale cache.
	Projects          []UserProjectConfig `yaml:"projects,omitempty"`
	ProjectsFetchedAt int64               `yaml:"projects_fetched_at,omitempty"`
}

// UserProjectConfig is a project accessible under user-based auth. Unlike
// ProjectConfig it carries no API key/secret: requests are authorized with the
// user's session token and scoped to a project by id.
type UserProjectConfig struct {
	ProjectId string `yaml:"project_id"`
	Name      string `yaml:"name,omitempty"`
	Subdomain string `yaml:"subdomain,omitempty"`
	URL       string `yaml:"url,omitempty"`
}

// SessionValid reports whether the user has a session token that has not
// expired. A zero SessionExpiry means "no known expiry" and is treated as
// valid, so a manually-injected token without an expiry remains usable.
func (u *UserConfig) SessionValid() bool {
	if u == nil || u.SessionToken == "" {
		return false
	}
	return u.SessionExpiry == 0 || time.Now().Unix() < u.SessionExpiry
}

// GetUser returns the configured user matching idOrEmail (by id, or
// case-insensitively by email), or nil if none is configured. The returned
// pointer aliases the slice element, so mutations persist through a subsequent
// PersistIfNeeded on the same CLIConfig.
func (c *CLIConfig) GetUser(idOrEmail string) *UserConfig {
	for i := range c.Users {
		u := &c.Users[i]
		if u.Id == idOrEmail || (u.Email != "" && strings.EqualFold(u.Email, idOrEmail)) {
			return u
		}
	}
	return nil
}

// LoadDefaultUser returns the configured default user. It mirrors
// LoadDefaultProject and is used by user-based (experimental) auth.
func LoadDefaultUser() (*UserConfig, error) {
	conf, err := LoadOrCreate()
	if err != nil {
		return nil, err
	}
	if conf.DefaultUser == "" {
		return nil, errors.New("no default user set. Run `lk cloud auth` to sign in")
	}
	if u := conf.GetUser(conf.DefaultUser); u != nil {
		return u, nil
	}
	return nil, fmt.Errorf("default user %q not found in config", conf.DefaultUser)
}

// SetUserProjects replaces the cached project list for the user identified by
// idOrEmail and persists the config. fetchedAt is the Unix time the list was
// retrieved.
func (c *CLIConfig) SetUserProjects(idOrEmail string, projects []UserProjectConfig, fetchedAt int64) error {
	u := c.GetUser(idOrEmail)
	if u == nil {
		return fmt.Errorf("user %q not found in config", idOrEmail)
	}
	u.Projects = projects
	u.ProjectsFetchedAt = fetchedAt
	return c.PersistIfNeeded()
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
			return &p, nil
		}
	}

	return nil, ProjectNotFoundError(conf.Projects)
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

	return nil, ProjectNotFoundError(conf.Projects)
}

func ProjectNotFoundError(projects []ProjectConfig) error {
	if len(projects) == 0 {
		return errors.New("project not found. No projects configured, use `lk cloud auth` or `lk project add` to add a new project")
	}
	names := make([]string, len(projects))
	for i, p := range projects {
		names[i] = p.Name
	}
	return fmt.Errorf("project not found. Available projects: %s", strings.Join(names, ", "))
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
	if len(c.Projects) == 0 && c.Theme == "" && !c.hasPersisted {
		// nothing worth persisting yet
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
