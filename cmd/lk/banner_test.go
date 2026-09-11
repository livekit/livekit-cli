package main

import "testing"

func TestBannerMessage(t *testing.T) {
	cases := []struct {
		name, raw, version, want string
	}{
		{"no constraint shows to everyone", `{"message":"hi"}`, "2.18.6", "hi"},
		{"matching constraint", `{"message":"upgrade","versions":"< 3.0.0"}`, "2.18.6", "upgrade"},
		{"non-matching constraint", `{"message":"upgrade","versions":"< 3.0.0"}`, "3.0.0", ""},
		{"empty message", `{"message":"","versions":""}`, "2.18.6", ""},
		{"invalid json", `{`, "2.18.6", ""},
		{"invalid constraint", `{"message":"hi","versions":"???"}`, "2.18.6", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := bannerMessage([]byte(c.raw), c.version); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
