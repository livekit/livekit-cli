package config

import (
	"errors"
	"fmt"
	"os"
	"path"

	"gopkg.in/yaml.v3"
)

type CLIConfig struct {
	DefaultProject string          `yaml:"default_project"`
	Projects       []ProjectConfig `yaml:"projects"`
	// absent from YAML
	hasPersisted bool
}

type ProjectConfig struct {
	Name      string `yaml:"name"`
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
		return nil, fmt.Errorf("config file %s should be 0600", configPath)
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
	fmt.Println("Saved CLI config to", configPath)
	return nil
}

func getConfigLocation() (string, error) {
	dir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return path.Join(dir, ".livekit", "cli-config.yaml"), nil
}
