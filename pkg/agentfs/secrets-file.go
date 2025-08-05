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
	"os"

	"github.com/charmbracelet/huh"
	"github.com/joho/godotenv"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

var knownEnvFiles = []string{
	".env.production",
	".env",
	".env.staging",
	".env.development",
	".env.local",
	".env.test",
}

func ParseEnvFile(file string) (map[string]string, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return godotenv.Parse(f)
}

func DetectEnvFile(maybeFile string) (string, map[string]string, error) {
	if maybeFile != "" {
		env, err := ParseEnvFile(maybeFile)
		return maybeFile, env, err
	}

	extantEnvFiles := []string{}
	for _, file := range knownEnvFiles {
		if _, err := os.Stat(file); err == nil {
			extantEnvFiles = append(extantEnvFiles, file)
		}
	}

	if len(extantEnvFiles) == 0 {
		return "", nil, nil
	}

	var selectedFile string = extantEnvFiles[0]
	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select secrets file").
				OptionsFunc(func() []huh.Option[string] {
					var options []huh.Option[string]
					for _, file := range extantEnvFiles {
						options = append(options, huh.Option[string]{Key: file, Value: file})
					}
					options = append(options, huh.Option[string]{Key: "[none]", Value: ""})
					return options
				}, nil).
				Value(&selectedFile).
				WithHeight(5).
				WithTheme(util.Theme),
		),
	).
		Run(); err != nil {
		return "", nil, err
	}

	if selectedFile == "" {
		return "", nil, nil
	}

	env, err := ParseEnvFile(selectedFile)
	return selectedFile, env, err
}
