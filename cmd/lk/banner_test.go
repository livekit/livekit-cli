package main

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/Masterminds/semver/v3"
)

func TestBannerMessages(t *testing.T) {
	cases := []struct {
		name, raw, version string
		audiences          []string
		want               []string
	}{
		{"no constraint shows to everyone", `[{"message":"hi"}]`, "2.18.6", nil, []string{"hi"}},
		{"matching constraint", `[{"message":"upgrade","versions":"< 3.0.0"}]`, "2.18.6", nil, []string{"upgrade"}},
		{"non-matching constraint", `[{"message":"upgrade","versions":"< 3.0.0"}]`, "3.0.0", nil, nil},
		{"every matching entry, in order", `[
			{"message":"simulate is out","versions":"< 2.18.0"},
			{"message":"3.0 is out","versions":"< 3.0.0"},
			{"message":"hello 3.x","versions":">= 3.0.0"}
		]`, "2.17.0", nil, []string{"simulate is out", "3.0 is out"}},
		{"empty message skipped", `[{"message":"","versions":""},{"message":"hi"}]`, "2.18.6", nil, []string{"hi"}},
		{"empty list", `[]`, "2.18.6", nil, nil},
		{"invalid json", `[`, "2.18.6", nil, nil},
		{"invalid constraint skipped", `[{"message":"hi","versions":"???"},{"message":"ok"}]`, "2.18.6", nil, []string{"ok"}},
		{"audience hidden from users outside it", `[{"message":"beta","audiences":["user-auth"]},{"message":"hi"}]`, "2.18.6", nil, []string{"hi"}},
		{"audience shown, in file order, to users in it", `[{"message":"beta","audiences":["user-auth"]},{"message":"hi"}]`, "2.18.6", []string{"user-auth"}, []string{"beta", "hi"}},
		{"any of several audiences matches", `[{"message":"beta","audiences":["someone-else","user-auth"]}]`, "2.18.6", []string{"user-auth"}, []string{"beta"}},
		{"audiences still honor versions", `[{"message":"beta","audiences":["user-auth"],"versions":">= 3.0.0"}]`, "2.18.6", []string{"user-auth"}, nil},
		{"unknown audience matches no one", `[{"message":"x","audiences":["someone-else"]},{"message":"hi"}]`, "2.18.6", []string{"user-auth"}, []string{"hi"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := bannerMessages([]byte(c.raw), c.version, c.audiences); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

// CLIs that predate Audiences ignore it, so an audience-targeted entry in the
// published banner.json must carry a Versions constraint that keeps them from
// showing it to everyone.
func TestBannerFileAudiencesExcludeOldCLIs(t *testing.T) {
	raw, err := os.ReadFile("../../banner.json")
	if err != nil {
		t.Fatal(err)
	}
	var banners []banner
	if err := json.Unmarshal(raw, &banners); err != nil {
		t.Fatalf("banner.json: %v", err)
	}
	since := semver.MustParse(bannerAudiencesSince)
	lastUnaware := semver.New(since.Major(), since.Minor(), since.Patch()-1, "", "")
	for _, b := range banners {
		if len(b.Audiences) == 0 {
			continue
		}
		if b.Versions == "" {
			t.Errorf("entry %q has audiences but no versions constraint; add \">= %s\"", b.Message, bannerAudiencesSince)
			continue
		}
		c, err := semver.NewConstraint(b.Versions)
		if err != nil {
			t.Errorf("entry %q: invalid versions %q: %v", b.Message, b.Versions, err)
			continue
		}
		if c.Check(lastUnaware) {
			t.Errorf("entry %q: versions %q admits %s, which ignores audiences; use \">= %s\"", b.Message, b.Versions, lastUnaware, bannerAudiencesSince)
		}
	}
}
