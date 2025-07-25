package util

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFileExists tests the FileExists utility function
func TestFileExists(t *testing.T) {
	tempDir := t.TempDir()

	// Test 1: File exists
	testFile := "test-file.txt"
	os.WriteFile(filepath.Join(tempDir, testFile), []byte("test content"), 0644)

	if !FileExists(tempDir, testFile) {
		t.Errorf("Expected FileExists to return true for existing file")
	}

	// Test 2: File does not exist
	nonExistentFile := "non-existent.txt"

	if FileExists(tempDir, nonExistentFile) {
		t.Errorf("Expected FileExists to return false for non-existent file")
	}

	// Test 3: Directory instead of file
	subDir := "subdir"
	os.Mkdir(filepath.Join(tempDir, subDir), 0755)

	if !FileExists(tempDir, subDir) {
		t.Errorf("Expected FileExists to return true for existing directory")
	}

	// Test 4: Non-existent directory
	nonExistentDir := "/path/that/does/not/exist"

	if FileExists(nonExistentDir, "any-file.txt") {
		t.Errorf("Expected FileExists to return false for non-existent directory")
	}

	// Test 5: Empty filename (returns true because it checks if directory exists)
	if !FileExists(tempDir, "") {
		t.Errorf("Expected FileExists to return true for empty filename (checks directory)")
	}

	// Test 6: Test with subdirectory path
	subDirPath := "nested/deep/path"
	os.MkdirAll(filepath.Join(tempDir, subDirPath), 0755)
	nestedFile := "nested-file.txt"
	os.WriteFile(filepath.Join(tempDir, subDirPath, nestedFile), []byte("nested content"), 0644)

	if !FileExists(filepath.Join(tempDir, subDirPath), nestedFile) {
		t.Errorf("Expected FileExists to return true for file in nested directory")
	}

	// Test 7: Hidden files (starting with .)
	hiddenFile := ".hidden-file"
	os.WriteFile(filepath.Join(tempDir, hiddenFile), []byte("hidden content"), 0644)

	if !FileExists(tempDir, hiddenFile) {
		t.Errorf("Expected FileExists to return true for hidden file")
	}

	t.Logf("✓ FileExists utility function working correctly")
}

// TestFileExistsEdgeCases tests edge cases for FileExists
func TestFileExistsEdgeCases(t *testing.T) {
	tempDir := t.TempDir()

	// Test 1: File with special characters
	specialFile := "file-with-@#$%^&*()_+chars.txt"
	os.WriteFile(filepath.Join(tempDir, specialFile), []byte("special content"), 0644)

	if !FileExists(tempDir, specialFile) {
		t.Errorf("Expected FileExists to return true for file with special characters")
	}

	// Test 2: Very long filename
	longFile := "very-long-filename-" + string(make([]byte, 100)) + ".txt"
	// Fill with repeating pattern to avoid null bytes
	for i := range longFile {
		if longFile[i] == 0 {
			longFile = longFile[:i] + "a" + longFile[i+1:]
		}
	}
	longFile = "very-long-filename-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.txt"

	os.WriteFile(filepath.Join(tempDir, longFile), []byte("long filename content"), 0644)

	if !FileExists(tempDir, longFile) {
		t.Errorf("Expected FileExists to return true for file with long name")
	}

	// Test 3: Case sensitivity (should be case sensitive on most systems)
	caseFile := "CaseSensitive.txt"
	os.WriteFile(filepath.Join(tempDir, caseFile), []byte("case content"), 0644)

	if !FileExists(tempDir, caseFile) {
		t.Errorf("Expected FileExists to return true for correctly cased filename")
	}

	// This test may behave differently on case-insensitive filesystems
	if FileExists(tempDir, "casesensitive.txt") {
		t.Logf("Note: Filesystem appears to be case-insensitive")
	}

	t.Logf("✓ FileExists edge cases handled correctly")
}
