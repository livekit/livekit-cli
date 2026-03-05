// Copyright 2026 LiveKit, Inc.
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
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestParseMajorMinor(t *testing.T) {
	tests := []struct {
		input     string
		wantMajor int
		wantMinor int
		wantOK    bool
	}{
		{"1.2.3", 1, 2, true},
		{"0.9.0", 0, 9, true},
		{"10.20.30", 10, 20, true},
		{"1.2", 1, 2, true},
		{"1", 0, 0, false},
		{"", 0, 0, false},
		{"abc.def", 0, 0, false},
		{"1.abc", 0, 0, false},
		{"abc.2", 0, 0, false},
		{"1.2.3-beta.1", 1, 2, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			major, minor, ok := parseMajorMinor(tt.input)
			if ok != tt.wantOK {
				t.Errorf("parseMajorMinor(%q): ok = %v, want %v", tt.input, ok, tt.wantOK)
				return
			}
			if major != tt.wantMajor {
				t.Errorf("parseMajorMinor(%q): major = %d, want %d", tt.input, major, tt.wantMajor)
			}
			if minor != tt.wantMinor {
				t.Errorf("parseMajorMinor(%q): minor = %d, want %d", tt.input, minor, tt.wantMinor)
			}
		})
	}
}

func TestIsNotFoundErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "non-RPC error",
			err:  errors.New("something went wrong"),
			want: false,
		},
		{
			name: "method not found",
			err:  &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "method not found"},
			want: true,
		},
		{
			name: "resource not found",
			err:  &jsonrpc.Error{Code: mcp.CodeResourceNotFound, Message: "resource not found"},
			want: true,
		},
		{
			name: "invalid params with not found",
			err:  &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "tool not found"},
			want: true,
		},
		{
			name: "invalid params with unknown",
			err:  &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "Unknown tool"},
			want: true,
		},
		{
			name: "invalid params unrelated",
			err:  &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "missing required param"},
			want: false,
		},
		{
			name: "other RPC error",
			err:  &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "internal error"},
			want: false,
		},
		{
			name: "wrapped RPC error",
			err:  fmt.Errorf("call failed: %w", &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "method not found"}),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNotFoundErr(tt.err)
			if got != tt.want {
				t.Errorf("isNotFoundErr(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestHeaderTransport(t *testing.T) {
	transport := &headerTransport{
		base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if got := req.Header.Get("x-custom"); got != "value" {
				t.Errorf("header x-custom = %q, want %q", got, "value")
			}
			if got := req.Header.Get("x-other"); got != "other-value" {
				t.Errorf("header x-other = %q, want %q", got, "other-value")
			}
			return &http.Response{StatusCode: 200}, nil
		}),
		headers: map[string]string{
			"x-custom": "value",
			"x-other":  "other-value",
		},
	}

	req, _ := http.NewRequest("GET", "https://example.com", nil)
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// roundTripFunc adapts a function to the http.RoundTripper interface.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
