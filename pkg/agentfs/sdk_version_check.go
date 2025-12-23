package agentfs

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/Masterminds/semver/v3"
)

// PackageInfo represents information about a package found in a project
type PackageInfo struct {
	Name        string
	Version     string
	FoundInFile string
	ProjectType ProjectType
	Ecosystem   string // "pypi" or "npm"
}

// VersionCheckResult represents the result of a version check
type VersionCheckResult struct {
	PackageInfo
	MinVersion string
	Satisfied  bool
	Error      error
}

// CheckSDKVersion performs a comprehensive check for livekit-agents packages
func CheckSDKVersion(dir string, projectType ProjectType, settingsMap map[string]string) error {
	pythonMinSDKVersion := settingsMap["python-min-sdk-version"]
	nodeMinSDKVersion := settingsMap["node-min-sdk-version"]

	if pythonMinSDKVersion == "" || nodeMinSDKVersion == "" {
		return fmt.Errorf("unable to fetch client settings from server, please try again later")
	}

	// Detect all possible project files
	projectFiles := detectProjectFiles(dir, projectType)
	if len(projectFiles) == 0 {
		return fmt.Errorf("unable to locate project files, please use a supported Python or Node.js project structure")
	}

	// Check for packages in all detected files
	var results []VersionCheckResult
	for _, file := range projectFiles {
		result := checkPackageInFile(file, projectType, pythonMinSDKVersion, nodeMinSDKVersion)
		if result.Error == nil && result.Name != "" {
			results = append(results, result)
		}
	}

	// Find the best result (prefer lock files over source files)
	bestResult := findBestResult(results)
	if bestResult == nil {
		return fmt.Errorf("package %s not found in any project files. Are you sure this is an agent?", getTargetPackageName(projectType))
	}

	if !bestResult.Satisfied {
		return fmt.Errorf("package %s version %s is too old, please upgrade to %s",
			bestResult.Name, bestResult.Version, bestResult.MinVersion)
	}

	return nil
}

// detectProjectFiles finds all relevant project files for the given project type
func detectProjectFiles(dir string, projectType ProjectType) []string {
	var files []string

	switch projectType {
	case ProjectTypePythonPip, ProjectTypePythonUV:
		pythonFiles := []string{
			"requirements.txt",
			"requirements.lock",
			"pyproject.toml",
			"Pipfile",
			"Pipfile.lock",
			"setup.py",
			"setup.cfg",
			"poetry.lock",
			"uv.lock",
		}
		for _, filename := range pythonFiles {
			if path := filepath.Join(dir, filename); fileExists(path) {
				files = append(files, path)
			}
		}
	case ProjectTypeNode:
		nodeFiles := []string{
			"package.json",
			"package-lock.json",
			"yarn.lock",
			"pnpm-lock.yaml",
			"bun.lockb",
		}
		for _, filename := range nodeFiles {
			if path := filepath.Join(dir, filename); fileExists(path) {
				files = append(files, path)
			}
		}
	}

	return files
}

// checkPackageInFile checks for the target package in a specific file
func checkPackageInFile(filePath string, projectType ProjectType, pythonMinVersion, nodeMinVersion string) VersionCheckResult {
	fileName := filepath.Base(filePath)

	switch {
	case strings.Contains(fileName, "requirements"):
		return checkRequirementsFile(filePath, pythonMinVersion)
	case fileName == "pyproject.toml":
		return checkPyprojectToml(filePath, pythonMinVersion)
	case strings.Contains(fileName, "Pipfile"):
		return checkPipfile(filePath, pythonMinVersion)
	case fileName == "setup.py":
		return checkSetupPy(filePath, pythonMinVersion)
	case fileName == "setup.cfg":
		return checkSetupCfg(filePath, pythonMinVersion)
	case fileName == "package.json":
		return checkPackageJSON(filePath, nodeMinVersion)
	case strings.Contains(fileName, "lock"):
		return checkLockFile(filePath, projectType, pythonMinVersion, nodeMinVersion)
	}

	return VersionCheckResult{Error: fmt.Errorf("unsupported file type: %s", fileName)}
}

// parsePythonPackageVersion parses a Python package line and extracts the version
func parsePythonPackageVersion(line string) (string, bool) {
	// Git URLs don't have traditional versions, so we treat them as "latest"
	gitPattern := regexp.MustCompile(`(?i)^livekit-agents(?:\[[^\]]+\])?\s*@\s*git\+`)
	if gitPattern.MatchString(line) {
		return "latest", true
	}

	// match with optional extras and version specifiers
	pattern := regexp.MustCompile(`(?i)^livekit-agents(?:\[[^\]]+\])?\s*([=~><!]+)?\s*([^#]+)?`)
	matches := pattern.FindStringSubmatch(line)
	if matches == nil {
		return "", false
	}

	// get the operator (==, >=, etc.) and the version
	operator := matches[1]
	version := strings.TrimSpace(matches[2])

	if version == "" {
		// no version specified means latest
		return "latest", true
	}

	// clean up the version string if it contains multiple constraints
	// handle comma-separated version constraints like ">=1.2.5,<2"
	if strings.Contains(version, ",") {
		parts := strings.Split(version, ",")
		for _, part := range parts {
			trimmed := strings.TrimSpace(part)
			if regexp.MustCompile(`\d`).MatchString(trimmed) {
				if strings.ContainsAny(trimmed, "=~><") {
					version = trimmed
				} else if operator != "" {
					version = operator + trimmed
				} else {
					version = trimmed
				}
				break
			}
		}
	} else {
		// handle space-separated constraints
		firstPart := strings.Split(version, " ")[0]
		// output expects operator, so we'll preserve or add it
		if strings.ContainsAny(firstPart, "=~><") {
			version = firstPart
		} else if operator != "" {
			version = operator + firstPart
		} else {
			version = firstPart
		}
	}

	return version, true
}

// checkRequirementsFile checks for livekit-agents in requirements.txt
func checkRequirementsFile(filePath, minVersion string) VersionCheckResult {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return VersionCheckResult{Error: err}
	}

	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		version, found := parsePythonPackageVersion(line)
		if found {
			satisfied, err := isVersionSatisfied(version, minVersion)
			return VersionCheckResult{
				PackageInfo: PackageInfo{
					Name:        "livekit-agents",
					Version:     version,
					FoundInFile: filePath,
					ProjectType: ProjectTypePythonPip,
					Ecosystem:   "pypi",
				},
				MinVersion: minVersion,
				Satisfied:  satisfied,
				Error:      err,
			}
		}
	}

	return VersionCheckResult{}
}

// checkPyprojectToml checks for livekit-agents in pyproject.toml
func checkPyprojectToml(filePath, minVersion string) VersionCheckResult {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return VersionCheckResult{Error: err}
	}

	var doc map[string]any
	if err := toml.Unmarshal(content, &doc); err != nil {
		return VersionCheckResult{Error: err}
	}

	// Check project.dependencies
	if project, ok := doc["project"].(map[string]any); ok {
		if deps, ok := project["dependencies"].([]any); ok {
			for _, dep := range deps {
				if line, ok := dep.(string); ok {
					version, found := parsePythonPackageVersion(line)
					if found {
						satisfied, err := isVersionSatisfied(version, minVersion)
						return VersionCheckResult{
							PackageInfo: PackageInfo{
								Name:        "livekit-agents",
								Version:     version,
								FoundInFile: filePath,
								ProjectType: ProjectTypePythonPip,
								Ecosystem:   "pypi",
							},
							MinVersion: minVersion,
							Satisfied:  satisfied,
							Error:      err,
						}
					}
				}
			}
		}
	}

	return VersionCheckResult{}
}

// checkPipfile checks for livekit-agents in Pipfile
func checkPipfile(filePath, minVersion string) VersionCheckResult {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return VersionCheckResult{Error: err}
	}

	// Look for livekit-agents in [packages] section
	pattern := regexp.MustCompile(`(?m)^\s*livekit-agents\s*=\s*["']?([^"'\s]+)["']?`)
	matches := pattern.FindStringSubmatch(string(content))
	if matches != nil {
		version := strings.TrimSpace(matches[1])
		if version == "*" {
			version = "latest"
		}

		satisfied, err := isVersionSatisfied(version, minVersion)
		return VersionCheckResult{
			PackageInfo: PackageInfo{
				Name:        "livekit-agents",
				Version:     version,
				FoundInFile: filePath,
				ProjectType: ProjectTypePythonPip,
				Ecosystem:   "pypi",
			},
			MinVersion: minVersion,
			Satisfied:  satisfied,
			Error:      err,
		}
	}

	return VersionCheckResult{}
}

// checkSetupPy checks for livekit-agents in setup.py
func checkSetupPy(filePath, minVersion string) VersionCheckResult {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return VersionCheckResult{Error: err}
	}

	// Look for install_requires or dependencies
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)install_requires\s*=\s*\[([\s\S]*?)\]`),
		regexp.MustCompile(`(?i)dependencies\s*=\s*\[([\s\S]*?)\]`),
	}

	for _, pattern := range patterns {
		matches := pattern.FindStringSubmatch(string(content))
		if matches != nil {
			depsSection := matches[1]
			// extract the livekit-agents dependency line
			depPattern := regexp.MustCompile(`(?i)["']livekit-agents(?:\[[^\]]+\])?([^"']+)?["']`)
			depMatches := depPattern.FindStringSubmatch(depsSection)
			if depMatches != nil {
				packageLine := "livekit-agents"
				if depMatches[1] != "" {
					packageLine += depMatches[1]
				}
				version, found := parsePythonPackageVersion(packageLine)
				if found {
					satisfied, err := isVersionSatisfied(version, minVersion)
					return VersionCheckResult{
						PackageInfo: PackageInfo{
							Name:        "livekit-agents",
							Version:     version,
							FoundInFile: filePath,
							ProjectType: ProjectTypePythonPip,
							Ecosystem:   "pypi",
						},
						MinVersion: minVersion,
						Satisfied:  satisfied,
						Error:      err,
					}
				}
			}
		}
	}

	return VersionCheckResult{}
}

// checkSetupCfg checks for livekit-agents in setup.cfg
func checkSetupCfg(filePath, minVersion string) VersionCheckResult {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return VersionCheckResult{Error: err}
	}

	// Look for install_requires in [options] section
	pattern := regexp.MustCompile(`(?m)^\s*livekit-agents\s*([=~><!]+)\s*([^\s]+)`)
	matches := pattern.FindStringSubmatch(string(content))
	if matches != nil {
		version := strings.TrimSpace(matches[2])

		satisfied, err := isVersionSatisfied(version, minVersion)
		return VersionCheckResult{
			PackageInfo: PackageInfo{
				Name:        "livekit-agents",
				Version:     version,
				FoundInFile: filePath,
				ProjectType: ProjectTypePythonPip,
				Ecosystem:   "pypi",
			},
			MinVersion: minVersion,
			Satisfied:  satisfied,
			Error:      err,
		}
	}

	return VersionCheckResult{}
}

// checkPackageJSON checks for @livekit/agents in package.json
func checkPackageJSON(filePath, minVersion string) VersionCheckResult {
	file, err := os.Open(filePath)
	if err != nil {
		return VersionCheckResult{Error: err}
	}
	defer file.Close()

	var pkgJSON struct {
		Dependencies         map[string]string `json:"dependencies"`
		DevDependencies      map[string]string `json:"devDependencies"`
		PeerDependencies     map[string]string `json:"peerDependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
	}

	if err := json.NewDecoder(file).Decode(&pkgJSON); err != nil {
		return VersionCheckResult{Error: err}
	}

	// Check all dependency types
	dependencyMaps := []map[string]string{
		pkgJSON.Dependencies,
		pkgJSON.DevDependencies,
		pkgJSON.PeerDependencies,
		pkgJSON.OptionalDependencies,
	}

	for _, deps := range dependencyMaps {
		if version, ok := deps["@livekit/agents"]; ok {
			satisfied, err := isVersionSatisfied(version, minVersion)
			return VersionCheckResult{
				PackageInfo: PackageInfo{
					Name:        "@livekit/agents",
					Version:     version,
					FoundInFile: filePath,
					ProjectType: ProjectTypeNode,
					Ecosystem:   "npm",
				},
				MinVersion: minVersion,
				Satisfied:  satisfied,
				Error:      err,
			}
		}
	}

	return VersionCheckResult{}
}

// checkLockFile checks for packages in lock files
func checkLockFile(filePath string, projectType ProjectType, pythonMinVersion, nodeMinVersion string) VersionCheckResult {
	fileName := filepath.Base(filePath)

	switch {
	case strings.Contains(fileName, "package-lock.json"):
		return checkPackageLockJSON(filePath, nodeMinVersion)
	case strings.Contains(fileName, "yarn.lock"):
		return checkYarnLock(filePath, nodeMinVersion)
	case strings.Contains(fileName, "pnpm-lock.yaml"):
		return checkPnpmLock(filePath, nodeMinVersion)
	case strings.Contains(fileName, "poetry.lock"):
		return checkPoetryLock(filePath, pythonMinVersion)
	case strings.Contains(fileName, "uv.lock"):
		return checkUvLock(filePath, pythonMinVersion)
	case strings.Contains(fileName, "Pipfile.lock"):
		return checkPipfileLock(filePath, pythonMinVersion)
	}

	return VersionCheckResult{}
}

// checkPackageLockJSON checks for @livekit/agents in package-lock.json
func checkPackageLockJSON(filePath, minVersion string) VersionCheckResult {
	file, err := os.Open(filePath)
	if err != nil {
		return VersionCheckResult{Error: err}
	}
	defer file.Close()

	var lockJSON struct {
		Dependencies map[string]struct {
			Version string `json:"version"`
		} `json:"dependencies"`
	}

	if err := json.NewDecoder(file).Decode(&lockJSON); err != nil {
		return VersionCheckResult{Error: err}
	}

	if dep, ok := lockJSON.Dependencies["@livekit/agents"]; ok {
		satisfied, err := isVersionSatisfied(dep.Version, minVersion)
		return VersionCheckResult{
			PackageInfo: PackageInfo{
				Name:        "@livekit/agents",
				Version:     dep.Version,
				FoundInFile: filePath,
				ProjectType: ProjectTypeNode,
				Ecosystem:   "npm",
			},
			MinVersion: minVersion,
			Satisfied:  satisfied,
			Error:      err,
		}
	}

	return VersionCheckResult{}
}

// checkYarnLock checks for @livekit/agents in yarn.lock
func checkYarnLock(filePath, minVersion string) VersionCheckResult {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return VersionCheckResult{Error: err}
	}

	// Yarn lock format: "@livekit/agents@^1.0.0":
	pattern := regexp.MustCompile(`(?m)^"@livekit/agents@[^"]*":\s*\n\s*version\s+"([^"]+)"`)
	matches := pattern.FindStringSubmatch(string(content))
	if matches != nil {
		version := matches[1]
		satisfied, err := isVersionSatisfied(version, minVersion)
		return VersionCheckResult{
			PackageInfo: PackageInfo{
				Name:        "@livekit/agents",
				Version:     version,
				FoundInFile: filePath,
				ProjectType: ProjectTypeNode,
				Ecosystem:   "npm",
			},
			MinVersion: minVersion,
			Satisfied:  satisfied,
			Error:      err,
		}
	}

	return VersionCheckResult{}
}

// checkPnpmLock checks for @livekit/agents in pnpm-lock.yaml
func checkPnpmLock(filePath, minVersion string) VersionCheckResult {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return VersionCheckResult{Error: err}
	}

	// Look for @livekit/agents in the lock file
	pattern := regexp.MustCompile(`(?m)^\s*"@livekit/agents@[^"]*":\s*\n\s*version:\s*([^\n]+)`)
	matches := pattern.FindStringSubmatch(string(content))
	if matches != nil {
		version := strings.TrimSpace(matches[1])
		satisfied, err := isVersionSatisfied(version, minVersion)
		return VersionCheckResult{
			PackageInfo: PackageInfo{
				Name:        "@livekit/agents",
				Version:     version,
				FoundInFile: filePath,
				ProjectType: ProjectTypeNode,
				Ecosystem:   "npm",
			},
			MinVersion: minVersion,
			Satisfied:  satisfied,
			Error:      err,
		}
	}

	return VersionCheckResult{}
}

// checkPoetryLock checks for livekit-agents in poetry.lock
func checkPoetryLock(filePath, minVersion string) VersionCheckResult {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return VersionCheckResult{Error: err}
	}

	// Look for [[package]] section with livekit-agents
	pattern := regexp.MustCompile(`(?s)\[\[package\]\]\s*\nname\s*=\s*"livekit-agents"\s*\nversion\s*=\s*"([^"]+)"`)
	matches := pattern.FindStringSubmatch(string(content))
	if matches != nil {
		version := matches[1]
		satisfied, err := isVersionSatisfied(version, minVersion)
		return VersionCheckResult{
			PackageInfo: PackageInfo{
				Name:        "livekit-agents",
				Version:     version,
				FoundInFile: filePath,
				ProjectType: ProjectTypePythonPip,
				Ecosystem:   "pypi",
			},
			MinVersion: minVersion,
			Satisfied:  satisfied,
			Error:      err,
		}
	}

	return VersionCheckResult{}
}

// checkUvLock checks for livekit-agents in uv.lock
func checkUvLock(filePath, minVersion string) VersionCheckResult {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return VersionCheckResult{Error: err}
	}

	// Look for livekit-agents in the lock file
	pattern := regexp.MustCompile(`(?m)^\s*livekit-agents\s*=\s*"([^"]+)"`)
	matches := pattern.FindStringSubmatch(string(content))
	if matches != nil {
		version := matches[1]
		satisfied, err := isVersionSatisfied(version, minVersion)
		return VersionCheckResult{
			PackageInfo: PackageInfo{
				Name:        "livekit-agents",
				Version:     version,
				FoundInFile: filePath,
				ProjectType: ProjectTypePythonUV,
				Ecosystem:   "pypi",
			},
			MinVersion: minVersion,
			Satisfied:  satisfied,
			Error:      err,
		}
	}

	return VersionCheckResult{}
}

// checkPipfileLock checks for livekit-agents in Pipfile.lock
func checkPipfileLock(filePath, minVersion string) VersionCheckResult {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return VersionCheckResult{Error: err}
	}

	// Look for livekit-agents in the default section
	pattern := regexp.MustCompile(`(?s)"default":\s*\{[^}]*"livekit-agents":\s*\{[^}]*"version":\s*"([^"]+)"`)
	matches := pattern.FindStringSubmatch(string(content))
	if matches != nil {
		version := matches[1]
		satisfied, err := isVersionSatisfied(version, minVersion)
		return VersionCheckResult{
			PackageInfo: PackageInfo{
				Name:        "livekit-agents",
				Version:     version,
				FoundInFile: filePath,
				ProjectType: ProjectTypePythonPip,
				Ecosystem:   "pypi",
			},
			MinVersion: minVersion,
			Satisfied:  satisfied,
			Error:      err,
		}
	}

	return VersionCheckResult{}
}

// isVersionSatisfied checks if a version satisfies the minimum requirement
func isVersionSatisfied(version, minVersion string) (bool, error) {
	// Handle special cases
	if version == "latest" || version == "*" || version == "" || version == "next" {
		return true, nil // Latest version always satisfies
	}

	// Normalize version strings
	normalizedVersion := normalizeVersion(version)
	normalizedMin := normalizeVersion(minVersion)

	// Parse versions
	v, err := semver.NewVersion(normalizedVersion)
	if err != nil {
		return false, fmt.Errorf("invalid version format: %s", version)
	}

	min, err := semver.NewVersion(normalizedMin)
	if err != nil {
		return false, fmt.Errorf("invalid minimum version format: %s", minVersion)
	}

	// Check if version satisfies minimum using semver comparison
	if !v.LessThan(min) {
		return true, nil
	}

	// Special handling for prerelease versions: if the base version matches,
	// consider prerelease versions as satisfying the requirement
	// (e.g., 1.3.0-rc1 should satisfy >=1.3.0)
	vBase := v.String()
	minBase := min.String()

	// Remove prerelease suffix for base version comparison
	if strings.Contains(vBase, "-") {
		vBase = strings.Split(vBase, "-")[0]
	}
	if strings.Contains(minBase, "-") {
		minBase = strings.Split(minBase, "-")[0]
	}

	if vBase == minBase && v.LessThan(min) {
		// Same base version, prerelease should satisfy
		return true, nil
	}

	return false, nil
}

// normalizeVersion normalizes version strings for semver parsing
func normalizeVersion(version string) string {
	// Remove common prefixes and suffixes
	version = strings.TrimSpace(version)
	version = strings.Trim(version, " \"'")

	// Remove version specifiers that aren't part of the version itself
	version = regexp.MustCompile(`^[=~><!]+`).ReplaceAllString(version, "")

	// Handle npm version ranges
	if strings.HasPrefix(version, "^") || strings.HasPrefix(version, "~") {
		version = version[1:]
	}

	// First check if we have a version with dot separator (1.0.0.rc2)
	// If so, replace the dot with a hyphen to make it semver compliant
	if dotIndex := strings.LastIndex(version, "."); dotIndex > 0 {
		// Check if what follows the last dot is a prerelease identifier (starts with a letter)
		if dotIndex < len(version)-1 && regexp.MustCompile(`^[a-zA-Z]`).MatchString(version[dotIndex+1:]) {
			// Convert 1.0.0.rc2 -> 1.0.0-rc2
			version = version[:dotIndex] + "-" + version[dotIndex+1:]
		}
	}

	// Handle prerelease versions without separator: 1.3.0rc1 -> 1.3.0-rc1, 1.3rc -> 1.3.0-rc, etc.
	prereleasePattern := regexp.MustCompile(`^(\d+(?:\.\d+)*)([a-zA-Z][a-zA-Z0-9]*.*)$`)
	if matches := prereleasePattern.FindStringSubmatch(version); matches != nil {
		baseVersion := matches[1]
		prerelease := matches[2]

		// Ensure we have at least MAJOR.MINOR.PATCH
		parts := strings.Split(baseVersion, ".")
		for len(parts) < 3 {
			parts = append(parts, "0")
		}
		version = strings.Join(parts, ".") + "-" + prerelease
	}

	return version
}

// findBestResult finds the best result from multiple package checks
func findBestResult(results []VersionCheckResult) *VersionCheckResult {
	if len(results) == 0 {
		return nil
	}

	// Prefer lock files over source files
	lockFilePriority := map[string]int{
		"package-lock.json": 10,
		"yarn.lock":         10,
		"pnpm-lock.yaml":    10,
		"poetry.lock":       10,
		"uv.lock":           10,
		"Pipfile.lock":      10,
		"requirements.lock": 8,
		"package.json":      5,
		"pyproject.toml":    5,
		"requirements.txt":  3,
		"Pipfile":           3,
		"setup.py":          2,
		"setup.cfg":         2,
	}

	var bestResult *VersionCheckResult
	bestPriority := -1

	for i := range results {
		result := &results[i]
		if result.Error != nil {
			continue
		}

		fileName := filepath.Base(result.FoundInFile)
		priority := lockFilePriority[fileName]

		if priority > bestPriority {
			bestPriority = priority
			bestResult = result
		}
	}

	return bestResult
}

// getTargetPackageName returns the target package name for the project type
func getTargetPackageName(projectType ProjectType) string {
	switch projectType {
	case ProjectTypePythonPip, ProjectTypePythonUV:
		return "livekit-agents"
	case ProjectTypeNode:
		return "@livekit/agents"
	default:
		return ""
	}
}

// fileExists checks if a file exists
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
