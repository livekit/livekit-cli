package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestLocalAgentStartupNeverFetchesAnUncachedOrExpiredBanner(t *testing.T) {
	setHome(t, t.TempDir())
	previous := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("local agent startup tried to fetch the promotional banner")
		return nil, nil
	})}
	t.Cleanup(func() { http.DefaultClient = previous })
	path, err := bannerCachePath()
	require.NoError(t, err)
	for _, command := range []string{"dev", "start", "console", "debugger"} {
		for _, stale := range []bool{false, true} {
			if stale {
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
				require.NoError(t, os.WriteFile(path, []byte(`[{"message":"expired offer"}]`), 0o600))
				old := time.Now().Add(-2 * bannerTTL)
				require.NoError(t, os.Chtimes(path, old, old))
			} else {
				_ = os.Remove(path)
			}
			app := &cli.Command{
				Name: "lk",
				Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
					require.Nil(t, loadBanner(ctx, cmd))
					return ctx, nil
				},
				Commands: []*cli.Command{{Name: "agent", Commands: []*cli.Command{{Name: command, Action: func(context.Context, *cli.Command) error { return nil }}}}},
			}
			require.NoError(t, app.Run(context.Background(), []string{"lk", "agent", command}))
		}
	}
}

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
		{"signed-out entry hidden from signed-in users", `[{"message":"sign in","audiences":["signed-out"]},{"message":"hi"}]`, "2.19.5", []string{"user-auth"}, []string{"hi"}},
		{"signed-out entry shown to signed-out users", `[{"message":"sign in","audiences":["signed-out"]},{"message":"hi"}]`, "2.19.5", []string{"signed-out"}, []string{"sign in", "hi"}},
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

func TestBannerAudiences(t *testing.T) {
	if got := bannerAudiences(true); !reflect.DeepEqual(got, []string{bannerAudienceUserAuth}) {
		t.Errorf("signed in: got %v, want [%s]", got, bannerAudienceUserAuth)
	}
	if got := bannerAudiences(false); !reflect.DeepEqual(got, []string{bannerAudienceSignedOut}) {
		t.Errorf("signed out: got %v, want [%s]", got, bannerAudienceSignedOut)
	}
}
