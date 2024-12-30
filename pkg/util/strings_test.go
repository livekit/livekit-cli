package util

import (
	"slices"
	"strings"
	"testing"
)

func TestMapStrings(t *testing.T) {
	initial := []string{"a1", "b2", "c3"}
	mapped := MapStrings(initial, func(s string) string {
		return strings.ToUpper(s)
	})
	if len(mapped) != len(initial) {
		t.Error("mapStrings should return a slice of the same length")
	}
	if !slices.Equal([]string{"A1", "B2", "C3"}, mapped) {
		t.Error("mapStrings should apply the function to all elements")
	}
}

func TestEllipziseTo(t *testing.T) {
	str := "This is some long string that should be ellipsized"
	ellipsized := EllipsizeTo(str, 12)
	if len(ellipsized) != 12 {
		t.Error("ellipsizeTo should return a string of the specified length")
	}
	if ellipsized != "This is s..." {
		t.Error("ellipsizeTo should ellipsize the string")
	}
}

func TestWrapToLines(t *testing.T) {
	str := "This is a long string that should be wrapped to multiple lines"
	wrapped := WrapToLines(str, 10)
	if len(wrapped) != 8 {
		t.Error("wrapToLines should return a slice of lines")
	}
	if !slices.Equal([]string{
		"This is a",
		"long",
		"string",
		"that",
		"should be",
		"wrapped to",
		"multiple",
		"lines",
	}, wrapped) {
		t.Error("wrapToLines should wrap the string to the specified width")
	}
}
