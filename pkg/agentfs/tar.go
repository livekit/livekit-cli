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
	"fmt"
	"io"
	"net/http"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"

	"github.com/schollz/progressbar/v3"

	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/livekit/protocol/logger"
)

var (
	defaultExcludePatterns = []string{
		"Dockerfile",
		".dockerignore",
		".gitignore",
		".git",
		"node_modules",
		".env",
		".env.*",
	}

	ignoreFilePatterns = []string{
		".dockerignore",
	}
)

func UploadTarball(directory string, presignedUrl string, excludeFiles []string) error {
	excludeFiles = append(defaultExcludePatterns, excludeFiles...)

	for _, exclude := range ignoreFilePatterns {
		ignore := filepath.Join(directory, exclude)
		if _, err := os.Stat(ignore); err == nil {
			content, err := os.ReadFile(ignore)
			if err != nil {
				return fmt.Errorf("failed to read %s: %w", ignore, err)
			}
			excludeFiles = append(excludeFiles, strings.Split(string(content), "\n")...)
		}
	}

	// Normalize and filter ignore patterns
	{
		filtered := make([]string, 0, len(excludeFiles))
		for _, exclude := range excludeFiles {
			exclude = strings.TrimSpace(exclude)
			if exclude == "" || strings.HasPrefix(exclude, "#") {
				continue
			}
			filtered = append(filtered, filepath.ToSlash(exclude))
		}
		excludeFiles = filtered
	}

	var totalSize int64
	err := filepath.Walk(directory, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(directory, path)
		if err != nil {
			return nil
		}
		// Use forward slashes inside tar archives regardless of OS
		relPath = filepath.ToSlash(relPath)

		for _, exclude := range excludeFiles {
			if exclude == "" || strings.Contains(exclude, "Dockerfile") {
				continue
			}
			if info.IsDir() {
				if strings.HasPrefix(relPath, exclude+"/") || relPath == exclude {
					return filepath.SkipDir
				}
			}
			matched, err := pathpkg.Match(exclude, relPath)
			if err != nil {
				return nil
			}
			if matched {
				return nil
			}
		}

		if !info.IsDir() && info.Mode().IsRegular() {
			totalSize += info.Size()
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to calculate total size: %w", err)
	}

	tarProgress := progressbar.NewOptions64(
		totalSize,
		progressbar.OptionSetDescription("Compressing files"),
		progressbar.OptionSetWidth(30),
		progressbar.OptionShowBytes(true),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "=",
			SaucerHead:    ">",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
	)

	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	defer gzipWriter.Close()
	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	err = filepath.Walk(directory, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(directory, path)
		if err != nil {
			return fmt.Errorf("failed to calculate relative path for %s: %w", path, err)
		}
		// Normalize to forward slashes for tar header names and matching
		relPath = filepath.ToSlash(relPath)

		for _, exclude := range excludeFiles {
			if exclude == "" || strings.Contains(exclude, "Dockerfile") {
				continue
			}

			if info.IsDir() {
				if strings.HasPrefix(relPath, exclude+"/") || relPath == exclude {
					logger.Debugw("excluding directory from tarball", "path", path)
					return filepath.SkipDir
				}
			}

			matched, err := pathpkg.Match(exclude, relPath)
			if err != nil {
				return nil
			}
			if matched {
				logger.Debugw("excluding file from tarball", "path", path)
				return nil
			}
		}

		// Follow symlinks and include the actual file contents
		if info.Mode()&os.ModeSymlink != 0 {
			realPath, err := filepath.EvalSymlinks(path)
			if err != nil {
				return fmt.Errorf("failed to evaluate symlink %s: %w", path, err)
			}
			info, err = os.Stat(realPath)
			if err != nil {
				return fmt.Errorf("failed to stat %s: %w", realPath, err)
			}
			// Open the real file instead of the symlink
			file, err := os.Open(realPath)
			if err != nil {
				return fmt.Errorf("failed to open file %s: %w", realPath, err)
			}
			defer file.Close()

			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return fmt.Errorf("failed to create tar header for file %s: %w", path, err)
			}
			header.Name = relPath
			if err := tarWriter.WriteHeader(header); err != nil {
				return fmt.Errorf("failed to write tar header for file %s: %w", path, err)
			}

			// Copy file contents directly without progress bar
			_, err = io.Copy(tarWriter, file)
			if err != nil {
				return fmt.Errorf("failed to copy file content for %s: %w", path, err)
			}
			return nil
		}

		// Handle directories
		if info.IsDir() {
			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return fmt.Errorf("failed to create tar header for directory %s: %w", path, err)
			}
			header.Name = relPath + "/"
			if err := tarWriter.WriteHeader(header); err != nil {
				return fmt.Errorf("failed to write tar header for directory %s: %w", path, err)
			}
			return nil
		}

		// Handle regular files
		if !info.Mode().IsRegular() {
			// Skip non-regular files (devices, pipes, etc.)
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("failed to open file %s: %w", path, err)
		}
		defer file.Close()

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return fmt.Errorf("failed to create tar header for file %s: %w", path, err)
		}
		header.Name = util.ToUnixPath(relPath)
		if err := tarWriter.WriteHeader(header); err != nil {
			return fmt.Errorf("failed to write tar header for file %s: %w", path, err)
		}

		reader := io.TeeReader(file, tarProgress)
		_, err = io.Copy(tarWriter, reader)
		if err != nil {
			return fmt.Errorf("failed to copy file content for %s: %w", path, err)
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to walk directory: %w", err)
	}

	if err := tarWriter.Close(); err != nil {
		return fmt.Errorf("failed to close tar writer: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return fmt.Errorf("failed to close gzip writer: %w", err)
	}

	uploadProgress := progressbar.NewOptions64(
		int64(buffer.Len()),
		progressbar.OptionSetDescription("Uploading"),
		progressbar.OptionSetWidth(30),
		progressbar.OptionShowBytes(true),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "=",
			SaucerHead:    ">",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
	)

	req, err := http.NewRequest("PUT", presignedUrl, io.TeeReader(&buffer, uploadProgress))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/gzip")
	req.ContentLength = int64(buffer.Len())

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to upload tarball: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to upload tarball: %d: %s", resp.StatusCode, body)
	}

	fmt.Println()
	return nil
}
