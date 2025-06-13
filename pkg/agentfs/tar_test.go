package agentfs

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUploadTarball(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tarball-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	subDir := filepath.Join(tmpDir, "subdir")
	err = os.MkdirAll(subDir, 0755)
	require.NoError(t, err)

	normalFiles := []string{
		filepath.Join(subDir, "normal1.txt"),
		filepath.Join(subDir, "normal2.txt"),
		filepath.Join(tmpDir, "root.txt"),
	}
	for _, path := range normalFiles {
		err = os.WriteFile(path, []byte("normal content"), 0644)
		require.NoError(t, err)
	}

	largeFile := filepath.Join(tmpDir, "large.bin")
	f, err := os.Create(largeFile)
	require.NoError(t, err)

	fileSize := int64(1024 * 1024 * 1024) // 1GB
	err = f.Truncate(fileSize)
	require.NoError(t, err)
	f.Close()

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mockServer.Close()

	err = UploadTarball(tmpDir, mockServer.URL, []string{})
	require.NoError(t, err)
}

func TestUploadTarballFilePermissions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tarball-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	subDir := filepath.Join(tmpDir, "subdir")
	err = os.MkdirAll(subDir, 0755)
	require.NoError(t, err)

	normalFiles := []string{
		filepath.Join(subDir, "normal1.txt"),
		filepath.Join(subDir, "normal2.txt"),
	}
	for _, path := range normalFiles {
		err = os.WriteFile(path, []byte("normal content"), 0644)
		require.NoError(t, err)
	}

	restrictedFile := filepath.Join(subDir, "restricted.txt")
	err = os.WriteFile(restrictedFile, []byte("restricted content"), 0000)
	require.NoError(t, err)

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mockServer.Close()

	err = UploadTarball(tmpDir, mockServer.URL, []string{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "permission denied")

	err = os.Remove(restrictedFile)
	require.NoError(t, err)

	err = UploadTarball(tmpDir, mockServer.URL, []string{})
	require.NoError(t, err)
}

type tarContents struct {
	Name       string
	Size       int64
	IsDir      bool
	IsSymlink  bool
	LinkTarget string
}

func readTarContents(t *testing.T, tarData []byte) []tarContents {
	t.Helper()
	var contents []tarContents

	gzipReader, err := gzip.NewReader(bytes.NewReader(tarData))
	require.NoError(t, err)
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)

		content := tarContents{
			Name:       header.Name,
			Size:       header.Size,
			IsDir:      header.Typeflag == tar.TypeDir,
			IsSymlink:  header.Typeflag == tar.TypeSymlink,
			LinkTarget: header.Linkname,
		}
		contents = append(contents, content)
	}
	return contents
}

func TestUploadTarballDotfiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tarball-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	subDir := filepath.Join(tmpDir, "src")
	err = os.MkdirAll(subDir, 0755)
	require.NoError(t, err)

	files := []struct {
		path    string
		content string
	}{
		{filepath.Join(tmpDir, "regular.txt"), "regular file"},
		{filepath.Join(subDir, "code.go"), "package main"},
	}
	for _, f := range files {
		err = os.WriteFile(f.path, []byte(f.content), 0644)
		require.NoError(t, err)
	}

	dotfiles := []struct {
		path    string
		content string
	}{
		{filepath.Join(tmpDir, ".gitignore"), "*.env\nnode_modules/"},
		{filepath.Join(tmpDir, ".env"), "SECRET=123"},
		{filepath.Join(tmpDir, ".config"), "config data"},
		{filepath.Join(subDir, ".DS_Store"), "mac file"},
	}
	for _, f := range dotfiles {
		err = os.WriteFile(f.path, []byte(f.content), 0644)
		require.NoError(t, err)
	}

	// symlinks
	err = os.Symlink(files[0].path, filepath.Join(tmpDir, "link_to_regular.txt"))
	require.NoError(t, err)
	err = os.Symlink(dotfiles[2].path, filepath.Join(tmpDir, ".link_to_config"))
	require.NoError(t, err)

	nodeModules := filepath.Join(tmpDir, "node_modules")
	err = os.MkdirAll(nodeModules, 0755)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(nodeModules, "package.json"), []byte("{}"), 0644)
	require.NoError(t, err)

	var tarBuffer bytes.Buffer
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(&tarBuffer, r.Body)
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
	}))
	defer mockServer.Close()

	err = UploadTarball(tmpDir, mockServer.URL, []string{})
	require.NoError(t, err)

	contents := readTarContents(t, tarBuffer.Bytes())

	expectedFiles := map[string]struct{}{
		"regular.txt":         {},
		"src/code.go":         {},
		".config":             {},
		"link_to_regular.txt": {},
		".link_to_config":     {},
	}

	excludedFiles := map[string]struct{}{
		".env":          {},
		".gitignore":    {},
		"node_modules/": {},
		".DS_Store":     {},
	}

	for _, content := range contents {
		if _, ok := expectedFiles[content.Name]; ok {
			delete(expectedFiles, content.Name)

			switch content.Name {
			case "link_to_regular.txt":
				require.Equal(t, int64(len("regular file")), content.Size)
				require.False(t, content.IsSymlink)
			case ".link_to_config":
				require.Equal(t, int64(len("config data")), content.Size)
				require.False(t, content.IsSymlink)
			}
		}
		_, shouldBeExcluded := excludedFiles[content.Name]
		require.False(t, shouldBeExcluded, "excluded file was found in tar: %s", content.Name)
	}

	require.Empty(t, expectedFiles, "some expected files were not found in tar: %v", expectedFiles)
}

func TestUploadTarballDeepDirectories(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tarball-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	dirs := []string{
		"level1",
		"level1/level2",
		"level1/level2/level3",
		"level1/level2/level3/level4",
	}

	for _, dir := range dirs {
		err = os.MkdirAll(filepath.Join(tmpDir, dir), 0755)
		require.NoError(t, err)
		initPath := filepath.Join(tmpDir, dir, "__init__.py")
		err = os.WriteFile(initPath, []byte(""), 0644)
		require.NoError(t, err)
	}

	files := []struct {
		path    string
		content string
	}{
		{filepath.Join(tmpDir, "root.txt"), "root file"},
		{filepath.Join(tmpDir, "level1", "level1.txt"), "level 1 file"},
		{filepath.Join(tmpDir, "level1", "level2", "level2.txt"), "level 2 file"},
		{filepath.Join(tmpDir, "level1", "level2", "level3", "level3.txt"), "level 3 file"},
		{filepath.Join(tmpDir, "level1", "level2", "level3", "level4", "level4.txt"), "level 4 file"},
	}

	for _, f := range files {
		err = os.WriteFile(f.path, []byte(f.content), 0644)
		require.NoError(t, err)
	}

	var tarBuffer bytes.Buffer
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(&tarBuffer, r.Body)
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
	}))
	defer mockServer.Close()

	err = UploadTarball(tmpDir, mockServer.URL, []string{})
	require.NoError(t, err)

	contents := readTarContents(t, tarBuffer.Bytes())

	for _, dir := range dirs {
		found := false
		for _, content := range contents {
			if content.Name == dir+"/" && content.IsDir {
				found = true
				break
			}
		}
		require.True(t, found, "directory not found in tar: %s", dir)
	}

	for _, f := range files {
		found := false
		relPath, err := filepath.Rel(tmpDir, f.path)
		require.NoError(t, err)
		for _, content := range contents {
			if content.Name == relPath {
				found = true
				require.Equal(t, int64(len(f.content)), content.Size, "incorrect file size for %s", relPath)
				require.False(t, content.IsDir, "file marked as directory: %s", relPath)
				break
			}
		}
		require.True(t, found, "file not found in tar: %s", relPath)
	}

	for _, dir := range dirs {
		initPath := filepath.Join(dir, "__init__.py")
		found := false
		for _, content := range contents {
			if content.Name == initPath {
				found = true
				require.Equal(t, int64(0), content.Size, "incorrect file size for %s", initPath)
				require.False(t, content.IsDir, "file marked as directory: %s", initPath)
				break
			}
		}
		require.True(t, found, "__init__.py not found in tar: %s", initPath)
	}
}
