package main

import (
	"reflect"
	"testing"
)

func TestBannerMessages(t *testing.T) {
	cases := []struct {
		name, raw, version string
		want               []string
	}{
		{"no constraint shows to everyone", `[{"message":"hi"}]`, "2.18.6", []string{"hi"}},
		{"matching constraint", `[{"message":"upgrade","versions":"< 3.0.0"}]`, "2.18.6", []string{"upgrade"}},
		{"non-matching constraint", `[{"message":"upgrade","versions":"< 3.0.0"}]`, "3.0.0", nil},
		{"every matching entry, in order", `[
			{"message":"simulate is out","versions":"< 2.18.0"},
			{"message":"3.0 is out","versions":"< 3.0.0"},
			{"message":"hello 3.x","versions":">= 3.0.0"}
		]`, "2.17.0", []string{"simulate is out", "3.0 is out"}},
		{"empty message skipped", `[{"message":"","versions":""},{"message":"hi"}]`, "2.18.6", []string{"hi"}},
		{"empty list", `[]`, "2.18.6", nil},
		{"invalid json", `[`, "2.18.6", nil},
		{"invalid constraint skipped", `[{"message":"hi","versions":"???"},{"message":"ok"}]`, "2.18.6", []string{"ok"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := bannerMessages([]byte(c.raw), c.version); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
