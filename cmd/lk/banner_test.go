package main

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/livekit/livekit-cli/v2/pkg/util"
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

func TestBannerGateHoldsOutputUntilRelease(t *testing.T) {
	out = util.NewPrinter(nil, nil, false)
	// One buffer behind both writers so ordering across stdout and stderr is visible.
	var sink bytes.Buffer
	g := &bannerGate{}
	stdout, stderr := gatedWriter{g, &sink}, gatedWriter{g, &sink}

	fmt.Fprint(stdout, "result\n")
	fmt.Fprint(stderr, "status\n")
	if sink.Len() != 0 {
		t.Fatalf("output leaked before release: %q", sink.String())
	}

	g.release([]byte(`[{"message":"notice"}]`), &sink)
	got := sink.String()
	notice, result, status := strings.Index(got, "notice"), strings.Index(got, "result\n"), strings.Index(got, "status\n")
	if notice < 0 || result < notice || status < result {
		t.Fatalf("want banner, then held writes in order; got %q", got)
	}

	sink.Reset()
	fmt.Fprint(stdout, "after\n")
	if sink.String() != "after\n" {
		t.Fatalf("write after release not passed through: %q", sink.String())
	}
}

func TestBannerGateReleaseWithoutBanner(t *testing.T) {
	out = util.NewPrinter(nil, nil, false)
	var sink bytes.Buffer
	g := &bannerGate{}
	fmt.Fprint(gatedWriter{g, &sink}, "result\n")
	g.release(nil, &sink)
	if sink.String() != "result\n" {
		t.Fatalf("want held output only, got %q", sink.String())
	}
}
