package agentfs

import (
	"fmt"
	"path/filepath"

	"github.com/Masterminds/semver/v3"
	"github.com/google/osv-scanner/pkg/lockfile"
)

func CheckSDKVersion(dir string, settingsMap map[string]string) error {
	pythonMinSDKVersion := settingsMap["python-min-sdk-version"]
	nodeMinSDKVersion := settingsMap["node-min-sdk-version"]

	if pythonMinSDKVersion == "" || nodeMinSDKVersion == "" {
		return fmt.Errorf("unable to fetch client settings from server, please try again later")
	}

	if isPython, dependencyFile := isPython(dir); isPython {
		return scanDependencyFile(dependencyFile, "livekit-agents", pythonMinSDKVersion)
	} else if isNode, dependencyFile := isNode(dir); isNode {
		return scanDependencyFile(dependencyFile, "livekit-agents", nodeMinSDKVersion)
	}

	return fmt.Errorf("unable to determine project type, please create a Dockerfile in the current directory")
}

func scanDependencyFile(filePath, targetPackage, minVersion string) error {
	extractor, _ := lockfile.FindExtractor(filePath, filepath.Base(filePath))
	if extractor == nil {
		return fmt.Errorf("no extractor found for file type: %s", filepath.Base(filePath))
	}

	depFile, err := lockfile.OpenLocalDepFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", filePath, err)
	}

	packages, err := extractor.Extract(depFile)
	if err != nil {
		return fmt.Errorf("failed to extract deps from %s: %w", filePath, err)
	}

	for _, pkg := range packages {
		if pkg.Name == targetPackage {
			return validateVersion(pkg.Version, minVersion, targetPackage)
		}
	}

	return fmt.Errorf("package %s not found in %s", targetPackage, filePath)
}

func validateVersion(currentVersion, minVersion, packageName string) error {
	// if the version is unset in requirements.txt, the osv scanner will return 0.0.0, which will indicate the latest version
	if currentVersion == "0.0.0" {
		return nil
	}

	current, err := semver.NewVersion(currentVersion)
	if err != nil {
		return fmt.Errorf("invalid current version format for %s: %s", packageName, currentVersion)
	}

	minimum, err := semver.NewVersion(minVersion)
	if err != nil {
		return fmt.Errorf("invalid minimum version format: %s", minVersion)
	}

	if current.LessThan(minimum) {
		return fmt.Errorf("package %s version %s is too old, please upgrade to %s", packageName, currentVersion, minVersion)
	}
	return nil
}
