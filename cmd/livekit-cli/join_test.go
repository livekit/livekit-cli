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
  mime_type, socket_type, address, err := parseSocketFromName("://")
  assert.Equal(t, mime_type, "")
  assert.Equal(t, socket_type, "")
  assert.Equal(t, address, "")
  assert.NotEqual(t, err, nil, "Expected an error due to empty protocol string")

  mime_type, socket_type, address, err = parseSocketFromName("foo://")
  assert.Equal(t, mime_type, "")
  assert.Equal(t, socket_type, "")
  assert.Equal(t, address, "")
  assert.NotEqual(t, err, nil, "Expected an error due to invalid protocol string")

  mime_type, socket_type, address, err = parseSocketFromName("foo://")
  assert.Equal(t, mime_type, "")
  assert.Equal(t, socket_type, "")
  assert.Equal(t, address, "")
  assert.NotEqual(t, err, nil, "Expected an error due to invalid protocol string")

  mime_type, socket_type, address, err = parseSocketFromName("h264://")
  assert.Equal(t, mime_type, "")
  assert.Equal(t, socket_type, "")
  assert.Equal(t, address, "")
  assert.NotEqual(t, err, nil, "Expected error for h264 socket with empty address")

  mime_type, socket_type, address, err = parseSocketFromName("h264:///path/to/socket")
  assert.Equal(t, mime_type, "h264")
  assert.Equal(t, socket_type, "unix")
  assert.Equal(t, address, "/path/to/socket")
  assert.Equal(t, err, nil, "Expected no error for valid h264 socket")

  mime_type, socket_type, address, err = parseSocketFromName("opus://foobar.com:1234")
  assert.Equal(t, mime_type, "opus")
  assert.Equal(t, socket_type, "tcp")
  assert.Equal(t, address, "foobar.com:1234")
  assert.Equal(t, err, nil, "Expected no error for valid opus TCP socket")

  mime_type, socket_type, address, err = parseSocketFromName("opus://foobar.com:1234")
  assert.Equal(t, mime_type, "opus")
  assert.Equal(t, socket_type, "tcp")
  assert.Equal(t, address, "foobar.com:1234")
  assert.Equal(t, err, nil, "Expected no error for valid opus TCP socket")


  mime_type, socket_type, address, err = parseSocketFromName("vp8://foobar.com:1234")
  assert.Equal(t, mime_type, "vp8")
  assert.Equal(t, socket_type, "tcp")
  assert.Equal(t, address, "foobar.com:1234")
  assert.Equal(t, err, nil, "Expected no error for valid vp8 TCP socket")
}
