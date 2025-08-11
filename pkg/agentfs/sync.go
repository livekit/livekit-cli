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
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/livekit/protocol/logger"
)

// SyncCode syncs the local directory to a development agent via the sync server
func SyncCode(ctx context.Context, workingDir string, syncURL string, token string) error {
	// Create a tarball of the working directory
	logger.Debugw("Creating tarball for sync", "dir", workingDir)
	
	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	tarWriter := tar.NewWriter(gzipWriter)
	
	// Walk the directory and add files to the tarball
	err := filepath.Walk(workingDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		
		// Get relative path
		relPath, err := filepath.Rel(workingDir, path)
		if err != nil {
			return err
		}
		
		// Skip certain directories and files
		if shouldSkipFile(relPath) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		
		// Skip directories themselves (we'll create them via file paths)
		if info.IsDir() {
			return nil
		}
		
		// Create tar header
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		
		// Use forward slashes for tar compatibility
		header.Name = filepath.ToSlash(relPath)
		
		// Write header
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		
		// Write file content
		if !info.IsDir() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()
			
			if _, err := io.Copy(tarWriter, file); err != nil {
				return err
			}
		}
		
		return nil
	})
	
	if err != nil {
		return fmt.Errorf("failed to create tarball: %w", err)
	}
	
	// Close the tar writer
	if err := tarWriter.Close(); err != nil {
		return fmt.Errorf("failed to close tar writer: %w", err)
	}
	
	// Close the gzip writer
	if err := gzipWriter.Close(); err != nil {
		return fmt.Errorf("failed to close gzip writer: %w", err)
	}
	
	// Send the tarball to the sync server
	logger.Debugw("Sending sync request", "url", syncURL, "size", buf.Len())
	
	req, err := http.NewRequestWithContext(ctx, "PUT", syncURL, &buf)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	
	// Set headers
	req.Header.Set("Content-Type", "application/x-gzip")
	req.Header.Set("X-LIVEKIT-AGENT-DEV-SYNC-TOKEN", token)
	
	// Send the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send sync request: %w", err)
	}
	defer resp.Body.Close()
	
	// Check response
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sync failed with status %d: %s", resp.StatusCode, string(body))
	}
	
	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}
	
	logger.Debugw("Sync completed", "response", string(body))
	
	return nil
}

// shouldSkipFile determines if a file should be skipped during sync
func shouldSkipFile(path string) bool {
	// Skip hidden directories and files
	parts := strings.Split(filepath.ToSlash(path), "/")
	for _, part := range parts {
		if strings.HasPrefix(part, ".") && part != "." {
			return true
		}
	}
	
	// Skip specific directories
	skipDirs := []string{
		"node_modules",
		"__pycache__",
		"venv",
		"env",
		".venv",
		"dist",
		"build",
		"target",
		".livekit-dev-tools",  // Skip the dev-tools directory we added
	}
	
	for _, skipDir := range skipDirs {
		if strings.HasPrefix(path, skipDir+string(os.PathSeparator)) || path == skipDir {
			return true
		}
	}
	
	// Skip specific files
	skipFiles := []string{
		"livekit.toml",
		"develop.livekit.toml",
		"livekit.develop.Dockerfile",
		"Dockerfile.dev",
		".dockerignore",
	}
	
	for _, skipFile := range skipFiles {
		if path == skipFile {
			return true
		}
	}
	
	// Skip binary and large files by extension
	skipExtensions := []string{
		".pyc", ".pyo", ".pyd",
		".so", ".dylib", ".dll",
		".exe", ".bin",
		".tar", ".gz", ".zip", ".rar",
		".jpg", ".jpeg", ".png", ".gif", ".bmp",
		".mp3", ".mp4", ".avi", ".mov",
		".db", ".sqlite", ".sqlite3",
	}
	
	for _, ext := range skipExtensions {
		if strings.HasSuffix(strings.ToLower(path), ext) {
			return true
		}
	}
	
	return false
}