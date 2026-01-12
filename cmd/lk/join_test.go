// Copyright 2021-2023 LiveKit, Inc.
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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSocketFormat(t *testing.T) {
	result := isSocketFormat("")
	assert.False(t, result, "Empty string should return false")

	result = isSocketFormat("://")
	assert.True(t, result, "Just the delimiter should return true")

	result = isSocketFormat("foo:/bar")
	assert.False(t, result, "Invalid delimiter should return false")

	result = isSocketFormat("foo://bar")
	assert.True(t, result, "Longer string with delimiter should return true")
}

func TestParseSocketString(t *testing.T) {
	mimeType, socketType, address, err := parseSocketFromName("://")
	assert.Equal(t, mimeType, "")
	assert.Equal(t, socketType, "")
	assert.Equal(t, address, "")
	assert.NotEqual(t, err, nil, "Expected an error due to empty protocol string")

	mimeType, socketType, address, err = parseSocketFromName("foo://")
	assert.Equal(t, mimeType, "")
	assert.Equal(t, socketType, "")
	assert.Equal(t, address, "")
	assert.NotEqual(t, err, nil, "Expected an error due to invalid protocol string")

	mimeType, socketType, address, err = parseSocketFromName("foo://")
	assert.Equal(t, mimeType, "")
	assert.Equal(t, socketType, "")
	assert.Equal(t, address, "")
	assert.NotEqual(t, err, nil, "Expected an error due to invalid protocol string")

	mimeType, socketType, address, err = parseSocketFromName("h264://")
	assert.Equal(t, mimeType, "")
	assert.Equal(t, socketType, "")
	assert.Equal(t, address, "")
	assert.NotEqual(t, err, nil, "Expected error for h264 socket with empty address")

	mimeType, socketType, address, err = parseSocketFromName("h264:///path/to/socket")
	assert.Equal(t, mimeType, "h264")
	assert.Equal(t, socketType, "unix")
	assert.Equal(t, address, "/path/to/socket")
	assert.Equal(t, err, nil, "Expected no error for valid h264 socket")

	mimeType, socketType, address, err = parseSocketFromName("opus://foobar.com:1234")
	assert.Equal(t, mimeType, "opus")
	assert.Equal(t, socketType, "tcp")
	assert.Equal(t, address, "foobar.com:1234")
	assert.Equal(t, err, nil, "Expected no error for valid opus TCP socket")

	mimeType, socketType, address, err = parseSocketFromName("opus://foobar.com:1234")
	assert.Equal(t, mimeType, "opus")
	assert.Equal(t, socketType, "tcp")
	assert.Equal(t, address, "foobar.com:1234")
	assert.Equal(t, err, nil, "Expected no error for valid opus TCP socket")

	mimeType, socketType, address, err = parseSocketFromName("vp8://foobar.com:1234")
	assert.Equal(t, mimeType, "vp8")
	assert.Equal(t, socketType, "tcp")
	assert.Equal(t, address, "foobar.com:1234")
	assert.Equal(t, err, nil, "Expected no error for valid vp8 TCP socket")
}

func TestParseSimulcastURL(t *testing.T) {
	// Test TCP format
	parts, err := parseSimulcastURL("h264://localhost:8080/640x480")
	assert.NoError(t, err, "Expected no error for valid TCP simulcast URL")
	assert.Equal(t, "h264", parts.codec)
	assert.Equal(t, "tcp", parts.network)
	assert.Equal(t, "localhost:8080", parts.address)
	assert.Equal(t, uint32(640), parts.width)
	assert.Equal(t, uint32(480), parts.height)

	// Test Unix socket format with multiple slashes
	parts, err = parseSimulcastURL("h264:///tmp/my.socket/1280x720")
	assert.NoError(t, err, "Expected no error for valid Unix socket simulcast URL")
	assert.Equal(t, "h264", parts.codec)
	assert.Equal(t, "unix", parts.network)
	assert.Equal(t, "/tmp/my.socket", parts.address)
	assert.Equal(t, uint32(1280), parts.width)
	assert.Equal(t, uint32(720), parts.height)

	// Test Unix socket format with nested paths
	parts, err = parseSimulcastURL("h264:///tmp/deep/nested/path/my.socket/1920x1080")
	assert.NoError(t, err, "Expected no error for valid nested path Unix socket simulcast URL")
	assert.Equal(t, "h264", parts.codec)
	assert.Equal(t, "unix", parts.network)
	assert.Equal(t, "/tmp/deep/nested/path/my.socket", parts.address)
	assert.Equal(t, uint32(1920), parts.width)
	assert.Equal(t, uint32(1080), parts.height)

	// Test simple socket name without path
	parts, err = parseSimulcastURL("h264://mysocket/640x480")
	assert.NoError(t, err, "Expected no error for simple socket name")
	assert.Equal(t, "h264", parts.codec)
	assert.Equal(t, "unix", parts.network)
	assert.Equal(t, "mysocket", parts.address)
	assert.Equal(t, uint32(640), parts.width)
	assert.Equal(t, uint32(480), parts.height)

	// H265 variants
	parts, err = parseSimulcastURL("h265://localhost:8080/640x480")
	assert.NoError(t, err, "Expected no error for valid TCP simulcast URL (h265)")
	assert.Equal(t, "h265", parts.codec)
	assert.Equal(t, "tcp", parts.network)
	assert.Equal(t, "localhost:8080", parts.address)
	assert.Equal(t, uint32(640), parts.width)
	assert.Equal(t, uint32(480), parts.height)

	parts, err = parseSimulcastURL("h265:///tmp/my.socket/1280x720")
	assert.NoError(t, err, "Expected no error for valid Unix socket simulcast URL (h265)")
	assert.Equal(t, "h265", parts.codec)
	assert.Equal(t, "unix", parts.network)
	assert.Equal(t, "/tmp/my.socket", parts.address)
	assert.Equal(t, uint32(1280), parts.width)
	assert.Equal(t, uint32(720), parts.height)

	// Test invalid format
	_, err = parseSimulcastURL("h264://localhost:8080")
	assert.Error(t, err, "Expected error for URL without dimensions")

	_, err = parseSimulcastURL("opus:///tmp/socket/640x480")
	assert.Error(t, err, "Expected error for non-h264/h265 protocol")

	_, err = parseSimulcastURL("h264:///tmp/socket/invalidxinvalid")
	assert.Error(t, err, "Expected error for invalid dimensions")
}
