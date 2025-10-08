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
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/schollz/progressbar/v3"
	"golang.org/x/sync/errgroup"

	"github.com/livekit/livekit-cli/v2/pkg/util"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"

	"github.com/moby/patternmatcher"
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
		".gitignore",
		".dockerignore",
	}
)

func UploadTarball(
	directory string,
	presignedUrl string,
	presignedPostRequest *livekit.PresignedPostRequest,
	excludeFiles []string,
	projectType ProjectType,
) error {
	excludeFiles = append(excludeFiles, defaultExcludePatterns...)

	loadExcludeFiles := func(filename string) (bool, string, error) {
		if _, err := os.Stat(filename); err == nil {
			content, err := os.ReadFile(filename)
			if err != nil {
				return false, "", err
			}
			return true, string(content), nil
		}
		return false, "", nil
	}

	foundDockerIgnore := false
	for _, exclude := range ignoreFilePatterns {
		found, content, err := loadExcludeFiles(path.Join(directory, exclude))
		if err != nil {
			logger.Debugw("failed to load exclude file", "filename", exclude, "error", err)
			continue
		}
		if exclude == ".dockerignore" && found {
			foundDockerIgnore = true
		}
		excludeFiles = append(excludeFiles, strings.Split(content, "\n")...)
	}

	// need to ensure we use a dockerignore file
	// if we fail to load a dockerignore file, we have to exit
	if !foundDockerIgnore {
		dockerIgnoreContent, err := fs.ReadFile(path.Join("examples", string(projectType)+".dockerignore"))
		if err != nil {
			return fmt.Errorf("failed to load exclude file %s: %w", string(projectType), err)
		}
		excludeFiles = append(excludeFiles, strings.Split(string(dockerIgnoreContent), "\n")...)
	}

	matcher, err := patternmatcher.New(excludeFiles)
	if err != nil {
		return fmt.Errorf("failed to create pattern matcher: %w", err)
	}

	for i, exclude := range excludeFiles {
		excludeFiles[i] = strings.TrimSpace(exclude)
	}

	checkFilesToInclude := func(path string, info os.FileInfo) bool {
		fileName := filepath.Base(path)
		// we have to include the Dockerfile in the upload, as it is required for the build
		if strings.Contains(fileName, "Dockerfile") {
			return true
		}

		if ignored, err := matcher.MatchesOrParentMatches(path); ignored {
			return false
		} else if err != nil {
			return false
		}
		return true
	}

	// we walk the directory first to calculate the total size of the tarball
	// this lets the progress bar show the correct progress
	var totalSize int64
	err = filepath.Walk(directory, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(directory, path)
		if err != nil {
			return nil
		}

		if !checkFilesToInclude(relPath, info) {
			return nil
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

		if !checkFilesToInclude(relPath, info) {
			logger.Debugw("excluding file from tarball", "path", path)
			return nil
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

	if presignedPostRequest != nil {
		if err := multipartUpload(presignedPostRequest.Url, presignedPostRequest.Values, &buffer, uploadProgress); err != nil {
			return fmt.Errorf("multipart upload failed: %w", err)
		}
	} else {
		if err := upload(presignedUrl, &buffer, uploadProgress); err != nil {
			return fmt.Errorf("upload failed: %w", err)
		}
	}

	fmt.Println()
	return nil
}

func upload(presignedUrl string, buffer *bytes.Buffer, uploadProgress *progressbar.ProgressBar) error {
	req, err := http.NewRequest("PUT", presignedUrl, io.TeeReader(buffer, uploadProgress))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/gzip")
	req.ContentLength = int64(buffer.Len())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to upload tarball: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to upload tarball: %d: %s", resp.StatusCode, body)
	}
	return nil
}

func multipartUpload(presignedURL string, fields map[string]string, buf *bytes.Buffer, uploadProgress *progressbar.ProgressBar) error {
	pr, pw := io.Pipe()
	w := multipart.NewWriter(pw)
	var eg errgroup.Group
	eg.Go(func() error {
		defer pw.Close()
		defer w.Close()
		for k, v := range fields {
			if err := w.WriteField(k, v); err != nil {
				pw.CloseWithError(err)
				return err
			}
		}
		part, err := w.CreateFormFile("file", "upload.tar.gz")
		if err != nil {
			pw.CloseWithError(err)
			return err
		}
		if _, err := io.Copy(part, io.TeeReader(buf, uploadProgress)); err != nil {
			pw.CloseWithError(err)
			return err
		}
		return nil
	})
	eg.Go(func() error {
		req, err := http.NewRequest("POST", presignedURL, pr)
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", w.FormDataContentType())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			respBody, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("failed to upload tarball: %d: %s", resp.StatusCode, respBody)
		}
		return nil
	})
	return eg.Wait()
}
