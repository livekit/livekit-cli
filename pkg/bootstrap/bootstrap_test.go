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

package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeDotEnv(t *testing.T) {
	tests := []struct {
		name        string
		existing    string
		envMap      map[string]string
		wantSubs    []string // substrings that must be present in the result
		notWantSubs []string // substrings that must NOT be present in the result
	}{
		{
			name:     "appends new keys when existing is empty",
			existing: "",
			envMap:   map[string]string{"FOO": "bar"},
			wantSubs: []string{`FOO="bar"`},
		},
		{
			name:        "updates existing key value in place",
			existing:    "FOO=oldval\n",
			envMap:      map[string]string{"FOO": "newval"},
			wantSubs:    []string{`FOO="newval"`},
			notWantSubs: []string{"oldval"},
		},
		{
			name:     "preserves comments",
			existing: "# leading comment\nFOO=old\n# trailing comment\n",
			envMap:   map[string]string{"FOO": "new"},
			wantSubs: []string{"# leading comment", "# trailing comment", `FOO="new"`},
		},
		{
			name:     "preserves blank lines",
			existing: "A=1\n\n\nB=2\n",
			envMap:   map[string]string{"A": "10"},
			// godotenv emits numeric strings without quotes (e.g. A=10).
			wantSubs: []string{"A=10", "B=2", "\n\n\n"},
		},
		{
			name:     "preserves unrecognized keys",
			existing: "KEEP_ME=keepvalue\nUPDATE_ME=old\n",
			envMap:   map[string]string{"UPDATE_ME": "new"},
			wantSubs: []string{"KEEP_ME=keepvalue", `UPDATE_ME="new"`},
		},
		{
			name:     "appends keys that aren't already present",
			existing: "A=1\n",
			envMap:   map[string]string{"A": "1", "NEW_KEY": "newval"},
			wantSubs: []string{`NEW_KEY="newval"`},
		},
		{
			name:        "preserves export directive when updating",
			existing:    "export FOO=oldval\n",
			envMap:      map[string]string{"FOO": "newval"},
			wantSubs:    []string{`export FOO="newval"`},
			notWantSubs: []string{"oldval"},
		},
		{
			name:        "preserves leading whitespace before key",
			existing:    "  FOO=oldval\n",
			envMap:      map[string]string{"FOO": "newval"},
			wantSubs:    []string{`  FOO="newval"`},
			notWantSubs: []string{"oldval"},
		},
		{
			name:        "preserves leading whitespace and export together",
			existing:    "  export FOO=oldval\n",
			envMap:      map[string]string{"FOO": "newval"},
			wantSubs:    []string{`  export FOO="newval"`},
			notWantSubs: []string{"oldval"},
		},
		{
			name:     "handles spaces around equals sign",
			existing: "FOO = oldval\n",
			envMap:   map[string]string{"FOO": "newval"},
			wantSubs: []string{`FOO="newval"`},
		},
		{
			name:     "rewrites quoted values with new quoted value",
			existing: "FOO=\"quoted old\"\n",
			envMap:   map[string]string{"FOO": "new"},
			wantSubs: []string{`FOO="new"`},
			notWantSubs: []string{
				"quoted old",
			},
		},
		{
			name:        "rewrites single-quoted values",
			existing:    "FOO='single quoted'\n",
			envMap:      map[string]string{"FOO": "new"},
			wantSubs:    []string{`FOO="new"`},
			notWantSubs: []string{"single quoted"},
		},
		{
			name:     "values with spaces are properly quoted",
			existing: "MSG=old\n",
			envMap:   map[string]string{"MSG": "hello world"},
			wantSubs: []string{`MSG="hello world"`},
		},
		{
			name:     "values with double quotes are escaped",
			existing: "Q=old\n",
			envMap:   map[string]string{"Q": `say "hi"`},
			wantSubs: []string{`Q="say \"hi\""`},
		},
		{
			name:     "values with equals signs in content are handled",
			existing: "EQ=old\n",
			envMap:   map[string]string{"EQ": "a=b=c"},
			wantSubs: []string{`EQ="a=b=c"`},
		},
		{
			name:     "values with newlines are escaped",
			existing: "MULTILINE=old\n",
			envMap:   map[string]string{"MULTILINE": "line1\nline2"},
			wantSubs: []string{`MULTILINE="line1\nline2"`},
		},
		{
			name:     "empty envMap leaves existing content intact",
			existing: "# a comment\nA=1\nB=2\n",
			envMap:   map[string]string{},
			wantSubs: []string{"# a comment", "A=1", "B=2"},
		},
		{
			name:        "multiple matching keys updated",
			existing:    "A=oldA\nB=oldB\nC=oldC\n",
			envMap:      map[string]string{"A": "newA", "B": "newB"},
			wantSubs:    []string{`A="newA"`, `B="newB"`, "C=oldC"},
			notWantSubs: []string{"oldA", "oldB"},
		},
		{
			name:     "appended keys appear after existing content",
			existing: "EXISTING=val\n",
			envMap:   map[string]string{"NEW_ONE": "1"},
			wantSubs: []string{"EXISTING=val"},
		},
		{
			name:     "key with digit and underscore in name is handled",
			existing: "NEXT_PUBLIC_KEY_1=old\n",
			envMap:   map[string]string{"NEXT_PUBLIC_KEY_1": "new"},
			wantSubs: []string{`NEXT_PUBLIC_KEY_1="new"`},
		},
		{
			name:     "ignores lines that look like comments with equals",
			existing: "# FAKE=injection\nREAL=old\n",
			envMap:   map[string]string{"REAL": "new", "FAKE": "ignored"},
			wantSubs: []string{"# FAKE=injection", `REAL="new"`, `FAKE="ignored"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mergeDotEnv(tt.existing, tt.envMap)
			if err != nil {
				t.Fatalf("mergeDotEnv returned error: %v", err)
			}
			for _, s := range tt.wantSubs {
				if !strings.Contains(got, s) {
					t.Errorf("expected output to contain %q\n---\nGot:\n%s", s, got)
				}
			}
			for _, s := range tt.notWantSubs {
				if strings.Contains(got, s) {
					t.Errorf("expected output NOT to contain %q\n---\nGot:\n%s", s, got)
				}
			}
		})
	}
}

func TestMergeDotEnv_TrailingNewline(t *testing.T) {
	tests := []struct {
		name     string
		existing string
		envMap   map[string]string
	}{
		{"existing has trailing newline", "A=1\n", map[string]string{"A": "2"}},
		{"existing has no trailing newline", "A=1", map[string]string{"A": "2"}},
		{"no merge needed, trailing newline present", "A=1\n", map[string]string{}},
		{"no merge needed, no trailing newline", "A=1", map[string]string{}},
		{"new keys appended, no trailing newline", "A=1", map[string]string{"B": "2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mergeDotEnv(tt.existing, tt.envMap)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(got, "\n") {
				t.Errorf("expected output to end with newline; got %q", got)
			}
		})
	}
}

func TestMergeDotEnv_DoesNotDuplicateKeys(t *testing.T) {
	got, err := mergeDotEnv("FOO=old\n", map[string]string{"FOO": "new"})
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(got, "FOO="); count != 1 {
		t.Errorf("expected FOO to appear exactly once, got %d occurrences\n---\n%s", count, got)
	}
}

func TestMergeDotEnv_AppendsAllNewKeysWhenNoneMatch(t *testing.T) {
	got, err := mergeDotEnv("EXISTING=val\n", map[string]string{"A": "alpha", "B": "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "EXISTING=val") {
		t.Errorf("existing key not preserved: %s", got)
	}
	if !strings.Contains(got, `A="alpha"`) {
		t.Errorf("A not appended: %s", got)
	}
	if !strings.Contains(got, `B="beta"`) {
		t.Errorf("B not appended: %s", got)
	}
}

func TestReadDotEnv_FileMissing(t *testing.T) {
	dir := t.TempDir()
	got, err := ReadDotEnv(dir, ".env.missing")
	if err != nil {
		t.Fatalf("ReadDotEnv returned error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil map for missing file, got %v", got)
	}
}

func TestReadDotEnv_ReadsExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("# header\nFOO=bar\nBAZ=\"qux quux\"\nexport HELLO=world\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadDotEnv(dir, ".env")
	if err != nil {
		t.Fatalf("ReadDotEnv returned error: %v", err)
	}
	if got["FOO"] != "bar" {
		t.Errorf("expected FOO=bar, got %q", got["FOO"])
	}
	if got["BAZ"] != "qux quux" {
		t.Errorf("expected BAZ=qux quux, got %q", got["BAZ"])
	}
	if got["HELLO"] != "world" {
		t.Errorf("expected HELLO=world (export stripped), got %q", got["HELLO"])
	}
}

func TestReadDotEnv_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env.empty")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadDotEnv(dir, ".env.empty")
	if err != nil {
		t.Fatalf("ReadDotEnv returned error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map for empty file, got %v", got)
	}
}

func TestReadDotEnv_JoinsRootDirAndPath(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "nested")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, ".env"), []byte("X=y\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadDotEnv(sub, ".env")
	if err != nil {
		t.Fatal(err)
	}
	if got["X"] != "y" {
		t.Errorf("expected X=y, got %v", got)
	}
}

func TestWriteDotEnv_FreshFile(t *testing.T) {
	for _, overwrite := range []bool{true, false} {
		name := "merge_mode"
		if overwrite {
			name = "overwrite_mode"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := WriteDotEnv(dir, ".env", map[string]string{"FOO": "bar"}, overwrite); err != nil {
				t.Fatal(err)
			}
			contents, err := os.ReadFile(filepath.Join(dir, ".env"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(contents), `FOO="bar"`) {
				t.Errorf("expected FOO=bar in output, got: %s", contents)
			}
		})
	}
}

func TestWriteDotEnv_MergesByDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	existing := "# header comment\nKEEP=keepval\nUPDATE=oldval\n\nexport DEBUG=true\n"
	if err := os.WriteFile(path, []byte(existing), 0600); err != nil {
		t.Fatal(err)
	}

	err := WriteDotEnv(dir, ".env", map[string]string{
		"UPDATE":  "newval",
		"APPEND":  "added",
		"DEBUG":   "false",
	}, false)
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)

	checks := []struct {
		name    string
		contain bool
		sub     string
	}{
		{"comment preserved", true, "# header comment"},
		{"unrecognized key preserved", true, "KEEP=keepval"},
		{"existing key updated", true, `UPDATE="newval"`},
		{"old value removed", false, "UPDATE=oldval"},
		{"new key appended", true, `APPEND="added"`},
		{"export prefix preserved", true, `export DEBUG="false"`},
		{"export old value removed", false, "DEBUG=true"},
		{"blank line preserved", true, "\n\n"},
	}
	for _, c := range checks {
		got := strings.Contains(s, c.sub)
		if got != c.contain {
			t.Errorf("%s: contains(%q)=%v, want %v\n---\n%s", c.name, c.sub, got, c.contain, s)
		}
	}
}

func TestWriteDotEnv_OverwriteFullyReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	existing := "# old comment\nKEEP=value\nUPDATE=old\n"
	if err := os.WriteFile(path, []byte(existing), 0600); err != nil {
		t.Fatal(err)
	}

	if err := WriteDotEnv(dir, ".env", map[string]string{"ONLY": "value"}, true); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if strings.Contains(s, "# old comment") {
		t.Errorf("comment should have been wiped by overwrite, got: %s", s)
	}
	if strings.Contains(s, "KEEP") {
		t.Errorf("preserved key should have been wiped by overwrite, got: %s", s)
	}
	if strings.Contains(s, "UPDATE") {
		t.Errorf("old key should have been wiped by overwrite, got: %s", s)
	}
	if !strings.Contains(s, `ONLY="value"`) {
		t.Errorf("expected only new key, got: %s", s)
	}
}

func TestWriteDotEnv_MergeIntoMissingFile(t *testing.T) {
	dir := t.TempDir()
	if err := WriteDotEnv(dir, ".env", map[string]string{"FOO": "bar"}, false); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), `FOO="bar"`) {
		t.Errorf("expected FOO=bar in fresh write, got: %s", contents)
	}
}

func TestWriteDotEnv_MergePreservesUnrecognized(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("CUSTOM_VAR=custom_value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := WriteDotEnv(dir, ".env", map[string]string{"OTHER": "value"}, false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	s := string(got)
	if !strings.Contains(s, "CUSTOM_VAR=custom_value") {
		t.Errorf("expected unrecognized key to be preserved, got: %s", s)
	}
	if !strings.Contains(s, `OTHER="value"`) {
		t.Errorf("expected new key to be appended, got: %s", s)
	}
}

func TestWriteDotEnv_MergeRoundtripIsStable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	existing := "# header\nA=1\nB=2\n"
	if err := os.WriteFile(path, []byte(existing), 0600); err != nil {
		t.Fatal(err)
	}
	// First merge with empty map should be effectively a no-op (content-wise).
	if err := WriteDotEnv(dir, ".env", map[string]string{}, false); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	// Second merge with the same empty map should yield identical output.
	if err := WriteDotEnv(dir, ".env", map[string]string{}, false); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Errorf("merge is not stable across runs:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if !strings.Contains(string(first), "# header") || !strings.Contains(string(first), "A=1") || !strings.Contains(string(first), "B=2") {
		t.Errorf("merge with empty map should preserve content, got: %s", first)
	}
}

func TestInstantiateDotEnv_NoExampleFile(t *testing.T) {
	dir := t.TempDir()
	got, err := InstantiateDotEnv(context.Background(), dir, ".env.example", map[string]string{"X": "y"}, false, func(k, v string) (string, error) {
		t.Fatalf("prompt should not be invoked when example file is absent")
		return v, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["X"] != "y" {
		t.Errorf("expected substitutions to be returned, got %v", got)
	}
}

func TestInstantiateDotEnv_SubstitutesWithoutPrompting(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.example"), []byte("API_KEY=<placeholder>\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := InstantiateDotEnv(context.Background(), dir, ".env.example", map[string]string{"API_KEY": "real"}, false, func(k, v string) (string, error) {
		t.Fatalf("prompt should not be invoked for keys present in substitutions; called for %s", k)
		return v, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["API_KEY"] != "real" {
		t.Errorf("expected API_KEY=real, got %v", got)
	}
}

func TestInstantiateDotEnv_PromptsForUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.example"), []byte("OPENAI_KEY=<your-key>\n"), 0600); err != nil {
		t.Fatal(err)
	}
	called := 0
	got, err := InstantiateDotEnv(context.Background(), dir, ".env.example", map[string]string{}, false, func(k, oldValue string) (string, error) {
		called++
		return "prompted-" + oldValue, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Errorf("expected prompt to be called exactly once, got %d", called)
	}
	if got["OPENAI_KEY"] != "prompted-<your-key>" {
		t.Errorf("expected prompted value, got %v", got)
	}
}

func TestInstantiateDotEnv_PriorsAsSubstitutionsSkipPrompt(t *testing.T) {
	// Simulates how manageEnv injects existing destination values as substitutions
	// so the user isn't re-prompted for values that are already set.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.example"), []byte("API_KEY=<placeholder>\nOTHER=<also>\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := InstantiateDotEnv(context.Background(), dir, ".env.example", map[string]string{
		"API_KEY": "from-priors",
	}, false, func(k, oldValue string) (string, error) {
		// Only OTHER should be prompted.
		if k != "OTHER" {
			t.Errorf("did not expect prompt for %q", k)
		}
		return "prompted", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["API_KEY"] != "from-priors" {
		t.Errorf("expected prior value for API_KEY, got %q", got["API_KEY"])
	}
	if got["OTHER"] != "prompted" {
		t.Errorf("expected prompted value for OTHER, got %q", got["OTHER"])
	}
}
