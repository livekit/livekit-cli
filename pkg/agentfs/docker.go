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
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
	"github.com/pkg/errors"

	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/livekit/protocol/logger"
)

//go:embed examples/*
var fs embed.FS

func HasDockerfile(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}

	for _, entry := range entries {
		if entry.Name() == "Dockerfile" {
			return true, nil
		}
	}
	return false, nil
}

func CreateDockerfile(dir string, settingsMap map[string]string) error {
	if len(settingsMap) == 0 {
		return fmt.Errorf("unable to fetch client settings from server, please try again later")
	}

	projectType, err := DetectProjectType(dir)
	if err != nil {
		return errors.Wrap(err, "unable to determine project type, please create a Dockerfile in the current directory")
	}

	var dockerfileContent []byte
	var dockerIgnoreContent []byte

	dockerfileContent, err = fs.ReadFile("examples/" + string(projectType) + ".Dockerfile")
	if err != nil {
		return err
	}
	dockerIgnoreContent, err = fs.ReadFile("examples/" + string(projectType) + ".dockerignore")
	if err != nil {
		return err
	}

	// TODO: (@rektdeckard) support Node entrypoint validation
	if projectType.IsPython() {
		dockerfileContent, err = validateEntrypoint(dir, dockerfileContent, dockerIgnoreContent, projectType, settingsMap)
		if err != nil {
			return err
		}
	}

	err = os.WriteFile(filepath.Join(dir, "Dockerfile"), dockerfileContent, 0644)
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(dir, ".dockerignore"), dockerIgnoreContent, 0644)
	if err != nil {
		return err
	}

	return nil
}

func validateEntrypoint(dir string, dockerfileContent []byte, dockerignoreContent []byte, projectType ProjectType, settingsMap map[string]string) ([]byte, error) {
	valFile := func(fileName string) (string, error) {
		// NOTE: we need to recurse to find entrypoints which may exist in src/ or some other directory.
		// This could be a lot of files, so we omit any files in .dockerignore, since they cannot be
		// used as entrypoints.

		reader := bytes.NewReader(dockerignoreContent)
		patterns, err := ignorefile.ReadAll(reader)
		if err != nil {
			return "", err
		}
		matcher, err := patternmatcher.New(patterns)
		if err != nil {
			return "", err
		}

		var fileList []string
		if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if ignored, err := matcher.MatchesOrParentMatches(path); ignored {
				return nil
			} else if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(d.Name(), projectType.FileExt()) {
				fileList = append(fileList, path)
			}
			return nil
		}); err != nil {
			return "", fmt.Errorf("error walking directory %s: %w", dir, err)
		}

		if slices.Contains(fileList, fileName) {
			return fileName, nil
		}

		// If no matching files found, return early
		if len(fileList) == 0 {
			return "", nil
		}

		var selected string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title(fmt.Sprintf("Select %s file to use as entrypoint", projectType.Lang())).
					Options(huh.NewOptions(fileList...)...).
					Value(&selected).
					WithTheme(util.Theme),
			),
		)

		if err := form.Run(); err != nil {
			return "", err
		}

		return selected, nil
	}

	if err := validateSettingsMap(settingsMap, []string{"python_entrypoint"}); err != nil {
		return nil, err
	}

	pythonEntrypoint := settingsMap["python_entrypoint"]
	newEntrypoint, err := valFile(pythonEntrypoint)
	if err != nil {
		return nil, err
	}

	lines := bytes.Split(dockerfileContent, []byte("\n"))
	var result bytes.Buffer
	for i := range lines {
		line := lines[i]
		trimmedLine := bytes.TrimSpace(line)

		if bytes.HasPrefix(trimmedLine, []byte("ARG PROGRAM_MAIN")) {
			result.WriteString(fmt.Sprintf("ARG PROGRAM_MAIN=\"%s\"", newEntrypoint))
		} else if bytes.HasPrefix(trimmedLine, []byte("ENTRYPOINT")) {
			// Extract the current entrypoint file
			parts := bytes.Fields(trimmedLine)
			if len(parts) < 2 {
				return nil, fmt.Errorf("invalid ENTRYPOINT format")
			}

			// Handle both JSON array and shell format
			var currentEntrypoint string
			if bytes.HasPrefix(parts[1], []byte("[")) {
				// JSON array format: ENTRYPOINT ["python", "app.py"]
				// Get the last element before the closing bracket
				jsonStr := bytes.Join(parts[1:], []byte(" "))
				var entrypointArray []string
				if err := json.Unmarshal(jsonStr, &entrypointArray); err != nil {
					return nil, fmt.Errorf("invalid ENTRYPOINT JSON format: %v", err)
				}
				if len(entrypointArray) > 0 {
					currentEntrypoint = entrypointArray[len(entrypointArray)-1]
				}
			} else {
				// Shell format: ENTRYPOINT python app.py
				currentEntrypoint = string(parts[len(parts)-1])
			}

			logger.Debugw("found entrypoint", "entrypoint", currentEntrypoint)

			// Preserve the original format
			if bytes.HasPrefix(parts[1], []byte("[")) {
				// Replace the last element in the JSON array
				var entrypointArray []string
				jsonStr := bytes.Join(parts[1:], []byte(" "))
				if err := json.Unmarshal(jsonStr, &entrypointArray); err != nil {
					return nil, err
				}
				entrypointArray[len(entrypointArray)-1] = newEntrypoint
				newJSON, err := json.Marshal(entrypointArray)
				if err != nil {
					return nil, err
				}
				fmt.Fprintf(&result, "ENTRYPOINT %s\n", newJSON)
			} else {
				// Preserve the original command but replace the last part
				parts[len(parts)-1] = []byte(newEntrypoint)
				result.Write(bytes.Join(parts, []byte(" ")))
				result.WriteByte('\n')
			}
		} else if bytes.HasPrefix(trimmedLine, []byte("CMD")) {
			// Handle CMD JSON array format: CMD ["python", "main.py", "start"]
			parts := bytes.Fields(trimmedLine)
			if len(parts) >= 2 && bytes.HasPrefix(parts[1], []byte("[")) {
				jsonStr := bytes.Join(parts[1:], []byte(" "))
				var cmdArray []string
				if err := json.Unmarshal(jsonStr, &cmdArray); err != nil {
					return nil, err
				}
				for i, arg := range cmdArray {
					if strings.HasSuffix(arg, projectType.FileExt()) {
						cmdArray[i] = newEntrypoint
						break
					}
				}
				newJSON, err := json.Marshal(cmdArray)
				if err != nil {
					return nil, err
				}
				fmt.Fprintf(&result, "CMD %s\n", newJSON)
			}
		} else if bytes.HasPrefix(trimmedLine, fmt.Appendf(nil, "RUN python %s", pythonEntrypoint)) {
			line = bytes.ReplaceAll(line, []byte(pythonEntrypoint), []byte(newEntrypoint))
			result.Write(line)
			if i < len(lines)-1 {
				result.WriteByte('\n')
			}
		} else {
			result.Write(line)
			if i < len(lines)-1 {
				result.WriteByte('\n')
			}
		}
	}

	return result.Bytes(), nil
}
