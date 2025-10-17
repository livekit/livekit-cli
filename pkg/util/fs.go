// Copyright 2024 LiveKit, Inc.
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

package util

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/livekit/protocol/utils/guid"
)

func FileExists(dir fs.FS, filename string) bool {
	_, err := fs.Stat(dir, filename)
	return err == nil
}

// Safely copy a file across filesystems, preserving permissions
func CopyFile(src, dest string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer srcFile.Close()

	destFile, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, srcFile); err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}

	// Preserve the file permissions
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("failed to stat source file: %w", err)
	}
	if err := os.Chmod(dest, srcInfo.Mode()); err != nil {
		return fmt.Errorf("failed to chmod destination file: %w", err)
	}

	return nil
}

// Safely move a directory across filesystems, preserving permissions
func MoveDir(src, dest string) error {
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("destination directory already exists: %s", dest)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to stat destination directory: %w", err)
	}

	if err := os.MkdirAll(dest, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("failed to compute relative path: %w", err)
		}
		targetPath := filepath.Join(dest, relPath)

		if info.IsDir() {
			if err := os.MkdirAll(targetPath, info.Mode()); err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}
		} else {
			if err := CopyFile(path, targetPath); err != nil {
				return fmt.Errorf("failed to copy file: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	if err := os.RemoveAll(src); err != nil {
		return fmt.Errorf("failed to remove source directory: %w", err)
	}

	return nil
}

// Provides a temporary path, a function to relocate it to a permanent path,
// and a function to clean up the temporary path that should always be deferred
// in the case of a failure to relocate.
func UseTempPath(permanentPath string) (string, func() error, func() error) {
	tempPath := path.Join(os.TempDir(), guid.New("LK_"))
	relocate := func() error {
		return MoveDir(tempPath, permanentPath)
	}
	cleanup := func() error {
		if err := os.RemoveAll(tempPath); err != nil {
			return fmt.Errorf("failed to remove temporary directory: %w", err)
		}
		return nil
	}
	return tempPath, relocate, cleanup
}

// Converts a path (possibly Windows-style) to a Unix-style path.
func ToUnixPath(p string) string {
	clean := filepath.Clean(p)
	return strings.ReplaceAll(clean, `\`, `/`)
}
