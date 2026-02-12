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

package main

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

// buildTestCommand creates a *cli.Command with flags set for testing requireSecrets()
func buildTestCommand(
	t *testing.T,
	ignoreEmpty bool,
	silent bool,
	secretsFile string,
	inlineSecrets []string,
) *cli.Command {
	var capturedCmd *cli.Command

	// Create a test app with the necessary flags
	app := &cli.Command{
		Name: "test",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name: "ignore-empty-secrets",
			},
			&cli.BoolFlag{
				Name: "silent",
			},
			&cli.StringFlag{
				Name: "secrets-file",
			},
			&cli.StringSliceFlag{
				Name: "secrets",
			},
			&cli.StringSliceFlag{
				Name: "secret-mount",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			// Capture the command after flags are parsed
			capturedCmd = cmd
			return nil
		},
	}

	// Build args array
	args := []string{"test"}
	if ignoreEmpty {
		args = append(args, "--ignore-empty-secrets")
	}
	if silent {
		args = append(args, "--silent")
	}
	if secretsFile != "" {
		args = append(args, "--secrets-file", secretsFile)
	}
	for _, secret := range inlineSecrets {
		args = append(args, "--secrets", secret)
	}

	// Run the app to parse flags
	err := app.Run(context.Background(), args)
	if err != nil {
		t.Fatalf("Failed to run test command: %v", err)
	}

	if capturedCmd == nil {
		t.Fatal("Failed to capture command")
	}

	return capturedCmd
}

// TestRequireSecrets tests the requireSecrets() function with the --ignore-empty-secrets flag
func TestRequireSecrets(t *testing.T) {
	tests := []struct {
		name               string
		ignoreEmpty        bool
		silent             bool
		envFileContent     string   // .env file content to create
		inlineSecrets      []string // --secrets flag values
		required           bool     // required parameter
		lazy               bool     // lazy parameter
		expectedError      bool
		expectedErrorMsg   string   // partial match
		expectedSecrets    []string // expected secret names (must be present)
		notExpectedSecrets []string // secret names that must NOT be present
	}{
		// Core 2x2 Matrix
		{
			name:               "Case 1: Empty secrets with ignore-empty-secrets flag",
			ignoreEmpty:        true,
			silent:             false,
			envFileContent:     "KEY1=value1\nEMPTY_KEY=\nKEY2=value2",
			required:           false,
			lazy:               false,
			expectedError:      false,
			expectedSecrets:    []string{"KEY1", "KEY2"},
			notExpectedSecrets: []string{"EMPTY_KEY"},
		},
		{
			name:             "Case 2: Empty secrets without flag - should error",
			ignoreEmpty:      false,
			silent:           false,
			envFileContent:   "KEY1=value1\nEMPTY_KEY=\nKEY2=value2",
			required:         false,
			lazy:             false,
			expectedError:    true,
			expectedErrorMsg: "secret EMPTY_KEY is empty",
		},
		{
			name:               "Case 3: No empty secrets with ignore-empty-secrets flag",
			ignoreEmpty:        true,
			silent:             false,
			envFileContent:     "KEY1=value1\nKEY2=value2",
			required:           false,
			lazy:               false,
			expectedError:      false,
			expectedSecrets:    []string{"KEY1", "KEY2"},
			notExpectedSecrets: []string{},
		},
		{
			name:               "Case 4: No empty secrets without flag (baseline)",
			ignoreEmpty:        false,
			silent:             false,
			envFileContent:     "KEY1=value1\nKEY2=value2",
			required:           false,
			lazy:               false,
			expectedError:      false,
			expectedSecrets:    []string{"KEY1", "KEY2"},
			notExpectedSecrets: []string{},
		},
		// Extended Cases
		{
			name:             "Case 5: All empty with flag - should error no secrets",
			ignoreEmpty:      true,
			silent:           false,
			envFileContent:   "EMPTY1=\nEMPTY2=",
			required:         true,
			lazy:             false,
			expectedError:    true,
			expectedErrorMsg: "no secrets provided",
		},
		{
			name:               "Case 6: Mixed empty/non-empty with flag",
			ignoreEmpty:        true,
			silent:             false,
			envFileContent:     "EMPTY1=\nVALID=value\nEMPTY2=\nALSO_VALID=value2",
			required:           false,
			lazy:               false,
			expectedError:      false,
			expectedSecrets:    []string{"VALID", "ALSO_VALID"},
			notExpectedSecrets: []string{"EMPTY1", "EMPTY2"},
		},
		{
			name:               "Case 7: Multiple empty secrets tracked",
			ignoreEmpty:        true,
			silent:             false,
			envFileContent:     "E1=\nE2=\nE3=\nVALID=value",
			required:           false,
			lazy:               false,
			expectedError:      false,
			expectedSecrets:    []string{"VALID"},
			notExpectedSecrets: []string{"E1", "E2", "E3"},
		},
		{
			name:               "Case 8: Inline secrets not affected by flag",
			ignoreEmpty:        true,
			silent:             false,
			envFileContent:     "", // No env file
			inlineSecrets:      []string{"EMPTY_INLINE=", "VALID_INLINE=value"},
			required:           false,
			lazy:               false,
			expectedError:      false,
			expectedSecrets:    []string{"EMPTY_INLINE", "VALID_INLINE"},
			notExpectedSecrets: []string{},
		},
		{
			name:             "Case 9: Error message mentions --ignore-empty-secrets flag",
			ignoreEmpty:      false,
			silent:           false,
			envFileContent:   "EMPTY=",
			required:         false,
			lazy:             false,
			expectedError:    true,
			expectedErrorMsg: "--ignore-empty-secrets",
		},
		{
			name:               "Case 10: Silent mode suppresses skip message",
			ignoreEmpty:        true,
			silent:             true,
			envFileContent:     "EMPTY=\nVALID=value",
			required:           false,
			lazy:               false,
			expectedError:      false,
			expectedSecrets:    []string{"VALID"},
			notExpectedSecrets: []string{"EMPTY"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup temporary directory
			tempDir, err := os.MkdirTemp("", "agent-secrets-test")
			require.NoError(t, err)
			defer os.RemoveAll(tempDir)

			// Change to temp directory
			oldWd, _ := os.Getwd()
			err = os.Chdir(tempDir)
			require.NoError(t, err)
			defer os.Chdir(oldWd)

			// Create .env file if specified
			var secretsFile string
			if tt.envFileContent != "" {
				secretsFile = ".env"
				err := os.WriteFile(secretsFile, []byte(tt.envFileContent), 0644)
				require.NoError(t, err)
			}

			// Build test command with proper flags
			cmd := buildTestCommand(t, tt.ignoreEmpty, tt.silent, secretsFile, tt.inlineSecrets)

			// Call the REAL requireSecrets function
			secrets, err := requireSecrets(
				context.Background(),
				cmd,
				tt.required,
				tt.lazy,
			)

			// Assertions
			if tt.expectedError {
				assert.Error(t, err)
				if tt.expectedErrorMsg != "" {
					assert.Contains(t, err.Error(), tt.expectedErrorMsg)
				}
			} else {
				assert.NoError(t, err)

				// Verify expected secrets count
				assert.Equal(t, len(tt.expectedSecrets), len(secrets),
					"Expected %d secrets, got %d", len(tt.expectedSecrets), len(secrets))

				// Collect secret names for assertions
				secretNames := make([]string, len(secrets))
				for i, s := range secrets {
					secretNames[i] = s.Name
				}

				// Verify expected secret names are present
				for _, expected := range tt.expectedSecrets {
					assert.Contains(t, secretNames, expected,
						"Expected secret %s to be present", expected)
				}

				// Verify that empty secrets are NOT present
				for _, notExpected := range tt.notExpectedSecrets {
					assert.NotContains(t, secretNames, notExpected,
						"Secret %s should NOT be present (should have been filtered out)", notExpected)
				}
			}
		})
	}
}

// TestRequireSecrets_InlineOverridesFile tests that inline secrets override file secrets
func TestRequireSecrets_InlineOverridesFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "agent-secrets-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	oldWd, _ := os.Getwd()
	err = os.Chdir(tempDir)
	require.NoError(t, err)
	defer os.Chdir(oldWd)

	// Create .env with KEY=file_value
	err = os.WriteFile(".env", []byte("KEY=file_value"), 0644)
	require.NoError(t, err)

	// Create command with inline secret KEY=inline_value
	cmd := buildTestCommand(t, true, false, ".env", []string{"KEY=inline_value"})

	secrets, err := requireSecrets(context.Background(), cmd, false, false)
	require.NoError(t, err)
	require.Len(t, secrets, 1)
	assert.Equal(t, "KEY", secrets[0].Name)
	assert.Equal(t, "inline_value", string(secrets[0].Value))
}
