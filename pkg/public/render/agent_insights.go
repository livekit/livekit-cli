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

package render

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/livekit/livekit-cli/v2/pkg/public/oapi"
	"github.com/livekit/livekit-cli/v2/pkg/util"
)

var (
	insightsSummaryHeaders = []string{"Sessions", "Turns", "Interruptions", "Input Tokens", "Output Tokens", "Cached Input Tokens", "TTS Characters"}
	insightsLatencyHeaders = []string{"Latency", "p50", "p95", "p99", "Mean", "Max", "Count"}
	insightsAgentHeaders   = []string{"Agent", "Agent ID", "Deployment", "Sessions", "Turns", "Interruptions", "Input Tokens", "Output Tokens", "E2E p50", "E2E p95"}
	insightsModelHeaders   = []string{"Kind", "Provider", "Model", "Requests", "Sessions", "Latency p50", "Latency p95", "Usage"}
)

// AgentInsights prints a project's agent insights: a table of the totals, one
// of the latencies that have measurements, one of the agents and one of the
// models; --json prints the API's response. empty says why there is no data,
// on stderr in both modes so --json output stays parseable, and then no
// tables print.
func AgentInsights(p *util.Printer, asJSON bool, resp oapi.LivekitPublicapiAnalyticsV1AgentInsightsGetResponse, empty string) error {
	if asJSON {
		if err := util.PrintJSONTo(p.ResultWriter(), resp); err != nil {
			return err
		}
	}
	if empty != "" {
		p.Status(empty)
		return nil
	}
	if asJSON {
		return nil
	}

	s := util.Deref(resp.Summary)
	p.Result(util.CreateTable().Headers(insightsSummaryHeaders...).Row(
		formatCount(s.Sessions), formatCount(s.Turns), formatCount(s.Interruptions),
		formatCount(s.InputTokens), formatCount(s.OutputTokens), formatCount(s.CachedInputTokens), formatCount(s.TtsCharacters),
	))

	latencies := util.CreateTable().Headers(insightsLatencyHeaders...)
	measured := false
	for _, l := range []struct {
		name  string
		stats *oapi.LivekitPublicapiAnalyticsV1LatencyStats
	}{
		{"End-to-end", s.E2eLatency},
		{"LLM time to first token", s.LlmTtft},
		{"TTS time to first byte", s.TtsTtfb},
		{"Realtime time to first token", s.RealtimeTtft},
		{"Transcription delay", s.TranscriptionDelay},
		{"End-of-utterance delay", s.EndOfUtteranceDelay},
		{"Tool call duration", s.ToolDuration},
	} {
		st := util.Deref(l.stats)
		if countOf(st.Count) == 0 {
			continue
		}
		measured = true
		latencies.Row(l.name, latencyMs(st.P50Ms), latencyMs(st.P95Ms), latencyMs(st.P99Ms),
			latencyMs(st.MeanMs), latencyMs(st.MaxMs), formatCount(st.Count))
	}
	if measured {
		p.Result(latencies)
	}

	if agents := util.Deref(resp.Agents); len(agents) > 0 {
		t := util.CreateTable().Headers(insightsAgentHeaders...)
		for _, a := range agents {
			e2e := util.Deref(a.E2eLatency)
			p50, p95 := "-", "-"
			if countOf(e2e.Count) > 0 {
				p50, p95 = latencyMs(e2e.P50Ms), latencyMs(e2e.P95Ms)
			}
			deployment := util.Deref(a.DeploymentId)
			if deployment == "" {
				deployment = "production"
			}
			t.Row(dashText(a.AgentName), dashText(a.AgentId), oneLine(deployment), formatCount(a.Sessions), formatCount(a.Turns),
				formatCount(a.Interruptions), formatCount(a.InputTokens), formatCount(a.OutputTokens), p50, p95)
		}
		p.Result(t)
	}

	if models := util.Deref(resp.Models); len(models) > 0 {
		t := util.CreateTable().Headers(insightsModelHeaders...)
		for _, m := range models {
			lat := util.Deref(m.Latency)
			p50, p95 := "-", "-"
			if countOf(lat.Count) > 0 {
				p50, p95 = latencyMs(lat.P50Ms), latencyMs(lat.P95Ms)
			}
			// The API counts no requests for an STT: they are always 0.
			requests := formatCount(m.Requests)
			if util.Deref(m.Component) == oapi.MODELCOMPONENTSTT {
				requests = "-"
			}
			t.Row(modelKind(m.Component), dashText(m.Provider), dashText(m.Model), requests, formatCount(m.Sessions),
				p50, p95, modelUsage(m))
		}
		p.Result(t)
	}
	return nil
}

// formatCount renders a 64-bit count, which the API sends as a decimal string; an
// absent count is 0, as protojson leaves zeros out.
func formatCount(v *string) string {
	return strconv.FormatInt(countOf(v), 10)
}

// countOf parses a 64-bit count; an absent or unreadable one is 0.
func countOf(v *string) int64 {
	n, _ := strconv.ParseInt(util.Deref(v), 10, 64)
	return n
}

// latencyMs renders a latency in milliseconds.
func latencyMs(ms *float64) string {
	return formatMs(util.Deref(ms))
}

// modelKind names a model's kind: LLM, TTS, STT or REALTIME.
func modelKind(c *oapi.LivekitPublicapiAnalyticsV1ModelComponent) string {
	if c == nil || *c == oapi.MODELCOMPONENTUNSPECIFIED {
		return "-"
	}
	return strings.TrimPrefix(string(*c), "MODEL_COMPONENT_")
}

// modelUsage renders what a model's kind counts: tokens for an LLM or a
// realtime model, characters for a TTS, and seconds of audio for an STT.
func modelUsage(m oapi.LivekitPublicapiAnalyticsV1AgentInsightsModel) string {
	switch util.Deref(m.Component) {
	case oapi.MODELCOMPONENTLLM, oapi.MODELCOMPONENTREALTIME:
		usage := fmt.Sprintf("%s in, %s out tokens", formatCount(m.InputTokens), formatCount(m.OutputTokens))
		if countOf(m.CachedInputTokens) > 0 {
			usage += fmt.Sprintf(" (%s cached)", formatCount(m.CachedInputTokens))
		}
		return usage
	case oapi.MODELCOMPONENTTTS:
		return formatCount(m.TtsCharacters) + " characters"
	case oapi.MODELCOMPONENTSTT:
		return fmt.Sprintf("%.1fs of audio", util.Deref(m.SttAudioSeconds))
	default:
		return "-"
	}
}
