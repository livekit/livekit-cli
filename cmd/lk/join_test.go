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
