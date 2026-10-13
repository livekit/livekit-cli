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

	"github.com/livekit/livekit-cli/v2/pkg/public"
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
	// APIURL is the Public API base URL the session was issued by (the
	// --experimental-api-url in effect at sign-in). A session is only valid
	// against that API, so the same person signed in to two environments (e.g.
	// staging and production) is stored as two entries. Empty means the default
	// Public API (entries written before this field existed).
	APIURL string `yaml:"api_url,omitempty"`
	// Projects caches the projects this user can access, as last fetched from
	// the Public API (listProjects). Caching lets project resolution avoid a
	// network round-trip on every command; ProjectsFetchedAt records the Unix
	// time it was populated so callers can refresh a stale cache.
	Projects          []UserProjectConfig `yaml:"projects,omitempty"`
	ProjectsFetchedAt int64               `yaml:"projects_fetched_at,omitempty"`
	// Workspaces caches the workspaces this user can access, mirroring Projects:
	// it lets workspace references resolve by name/alias offline, and
	// WorkspacesFetchedAt records when it was last populated.
	Workspaces          []UserWorkspaceConfig `yaml:"workspaces,omitempty"`
	WorkspacesFetchedAt int64                 `yaml:"workspaces_fetched_at,omitempty"`
}

type UserWorkspaceConfig struct {
	WorkspaceId string `yaml:"workspace_id" json:"id"`
	Name        string `yaml:"name,omitempty" json:"name,omitempty"`
	// Alias is a URL-safe handle derived from Name (deduplicated with a numeric
	// suffix), so a workspace can be referenced by a short, typeable name.
	Alias          string `yaml:"alias,omitempty" json:"alias,omitempty"`
	OrganizationId string `yaml:"organization_id,omitempty" json:"organizationId,omitempty"`
}

// UserProjectConfig is a project accessible under user-based auth. Unlike
// ProjectConfig it carries no API key/secret: requests are authorized with the
// user's session token and scoped to a project by id.
type UserProjectConfig struct {
	ProjectId string `yaml:"project_id" json:"id"`
	Name      string `yaml:"name,omitempty" json:"name,omitempty"`
	// Alias is a URL-safe handle derived from Name (deduplicated with a numeric
	// suffix), so a project can be referenced by a short, typeable name.
	Alias     string `yaml:"alias,omitempty" json:"alias,omitempty"`
	Subdomain string `yaml:"subdomain,omitempty" json:"subdomain,omitempty"`
	URL       string `yaml:"url,omitempty" json:"url,omitempty"`
}

// FindProject returns the cached project matching ref by ProjectId or
// (case-insensitively) by Name/alias, or nil if none matches. Used to resolve
// the --project flag against the user's project cache in user-auth mode.
func (u *UserConfig) FindProject(ref string) *UserProjectConfig {
	if u == nil || ref == "" {
		return nil
	}
	for i := range u.Projects {
		p := &u.Projects[i]
		if p.ProjectId == ref ||
			(p.Name != "" && strings.EqualFold(p.Name, ref)) ||
			(p.Alias != "" && strings.EqualFold(p.Alias, ref)) {
			return p
		}
	}
	return nil
}

// FindWorkspace resolves a workspace reference (id, name, or alias) against the
// cached workspaces, mirroring FindProject.
func (u *UserConfig) FindWorkspace(ref string) *UserWorkspaceConfig {
	if u == nil || ref == "" {
		return nil
	}
	for i := range u.Workspaces {
		w := &u.Workspaces[i]
		if w.WorkspaceId == ref ||
			(w.Name != "" && strings.EqualFold(w.Name, ref)) ||
			(w.Alias != "" && strings.EqualFold(w.Alias, ref)) {
			return w
		}
	}
	return nil
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

// GetUser returns the configured user signed in to the Public API at apiURL
// matching idOrEmail (by id, or case-insensitively by email), or nil if none is
// configured. The returned pointer aliases the slice element, so mutations
// persist through a subsequent PersistIfNeeded on the same CLIConfig.
func (c *CLIConfig) GetUser(idOrEmail, apiURL string) *UserConfig {
	for i := range c.Users {
		u := &c.Users[i]
		if !SameAPIURL(u.APIURL, apiURL) {
			continue
		}
		if u.Id == idOrEmail || (u.Email != "" && strings.EqualFold(u.Email, idOrEmail)) {
			return u
		}
	}
	return nil
}

// FindUsers returns the configured users matching idOrEmail (by id, or
// case-insensitively by email) on any Public API. The same person signed in to
// several APIs yields one entry per API. Use GetUser to resolve a user for a
// specific API. The returned pointers alias the slice elements.
func (c *CLIConfig) FindUsers(idOrEmail string) []*UserConfig {
	var users []*UserConfig
	for i := range c.Users {
		u := &c.Users[i]
		if u.Id == idOrEmail || (u.Email != "" && strings.EqualFold(u.Email, idOrEmail)) {
			users = append(users, u)
		}
	}
	return users
}

// UsersForAPIURL returns the configured users signed in to the Public API at
// apiURL. The returned pointers alias the slice elements.
func (c *CLIConfig) UsersForAPIURL(apiURL string) []*UserConfig {
	var users []*UserConfig
	for i := range c.Users {
		if SameAPIURL(c.Users[i].APIURL, apiURL) {
			users = append(users, &c.Users[i])
		}
	}
	return users
}

// UpsertUser stores u in the config. If a user matching the same person on the
// same Public API — by non-empty Id or (case-insensitively) email, plus APIURL —
// already exists, that entry is replaced outright rather than a duplicate being
// inserted; otherwise u is appended. It returns the stored user and whether an
// existing entry was replaced.
func (c *CLIConfig) UpsertUser(u UserConfig) (stored *UserConfig, replaced bool) {
	for i := range c.Users {
		if sameUser(c.Users[i], u) {
			c.Users[i] = u
			return &c.Users[i], true
		}
	}
	c.Users = append(c.Users, u)
	return &c.Users[len(c.Users)-1], false
}

// sameUser reports whether two entries refer to the same person on the same
// Public API, matching by non-empty Id or case-insensitive email.
func sameUser(a, b UserConfig) bool {
	if !SameAPIURL(a.APIURL, b.APIURL) {
		return false
	}
	if a.Id != "" && a.Id == b.Id {
		return true
	}
	return a.Email != "" && strings.EqualFold(a.Email, b.Email)
}

// SameAPIURL reports whether two Public API base URLs refer to the same API.
// Empty means the default Public API, and trailing slashes and case are
// ignored.
func SameAPIURL(a, b string) bool {
	return strings.EqualFold(normalizeAPIURL(a), normalizeAPIURL(b))
}

func normalizeAPIURL(u string) string {
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	if u == "" {
		return public.DefaultBaseURL
	}
	return u
}

// LoadDefaultUser returns the configured default user signed in to the Public
// API at apiURL. It mirrors LoadDefaultProject and is used by user-based
// (experimental) auth.
func LoadDefaultUser(apiURL string) (*UserConfig, error) {
	conf, err := LoadOrCreate()
	if err != nil {
		return nil, err
	}
	if conf.DefaultUser == "" {
		return nil, errors.New("no default user set. Run `lk cloud auth` to sign in")
	}
	if u := conf.GetUser(conf.DefaultUser, apiURL); u != nil {
		return u, nil
	}
	return nil, fmt.Errorf("default user %q not found in config", conf.DefaultUser)
}

// SetUserProjects replaces the cached project list for the user identified by
// idOrEmail on the Public API at apiURL and persists the config. fetchedAt is
// the Unix time the list was retrieved.
func (c *CLIConfig) SetUserProjects(idOrEmail, apiURL string, projects []UserProjectConfig, fetchedAt int64) error {
	u := c.GetUser(idOrEmail, apiURL)
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
		util.Warnf("WARNING: config file %s should have permissions %o", configPath, 0600)
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

	util.Status("Removed project", name)
	return nil
}

func (c *CLIConfig) PersistIfNeeded() error {
	return c.persist(true)
}

// PersistQuietly writes the config without printing the "Saved CLI config"
// notice. Use it for background updates (e.g. refreshing the project cache) that
// shouldn't announce themselves — and must not pollute stdout/JSON output.
func (c *CLIConfig) PersistQuietly() error {
	return c.persist(false)
}

func (c *CLIConfig) persist(announce bool) error {
	if len(c.Projects) == 0 && len(c.Users) == 0 && c.Theme == "" && !c.hasPersisted {
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
	if announce {
		util.Statusf("Saved CLI config to [%s]", util.Accented(configPath))
	}
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
