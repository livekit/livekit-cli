// Copyright 2022-2024 LiveKit, Inc.
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

package provider

import (
	"bytes"
	"context"
	"io"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/pion/webrtc/v4/pkg/media/ivfreader"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

type VP8VideoLooper struct {
	lksdk.BaseSampleProvider
	buffer        []byte
	frameDuration time.Duration
	spec          *videoSpec
	reader        *ivfreader.IVFReader
	ivfTimebase   float64
	lastTimestamp uint64
}

func NewVP8VideoLooper(input io.Reader, spec *videoSpec) (*VP8VideoLooper, error) {
	l := &VP8VideoLooper{
		spec:          spec,
		frameDuration: time.Second / time.Duration(spec.fps),
	}

	buf := bytes.NewBuffer(nil)

	if _, err := io.Copy(buf, input); err != nil {
		return nil, err
	}
	l.buffer = buf.Bytes()

	return l, nil
}

func (l *VP8VideoLooper) Codec() webrtc.RTPCodecCapability {
	return webrtc.RTPCodecCapability{
		MimeType:  "video/vp8",
		ClockRate: 90000,
		RTCPFeedback: []webrtc.RTCPFeedback{
			{Type: webrtc.TypeRTCPFBNACK},
			{Type: webrtc.TypeRTCPFBNACK, Parameter: "pli"},
		},
	}
}

func (l *VP8VideoLooper) NextSample(_ctx context.Context) (media.Sample, error) {
	return l.nextSample(true)
}

func (l *VP8VideoLooper) ToLayer(quality livekit.VideoQuality) *livekit.VideoLayer {
	return l.spec.ToVideoLayer(quality)
}

func (l *VP8VideoLooper) nextSample(rewindEOF bool) (media.Sample, error) {
	sample := media.Sample{}
	if l.reader == nil {
		var err error
		var ivfheader *ivfreader.IVFFileHeader
		l.reader, ivfheader, err = ivfreader.NewWith(bytes.NewReader(l.buffer))
		if err != nil {
			return sample, err
		}
		l.ivfTimebase = float64(ivfheader.TimebaseNumerator) / float64(ivfheader.TimebaseDenominator)
	}

	frame, header, err := l.reader.ParseNextFrame()
	if err == io.EOF && rewindEOF {
		l.reader = nil
		return l.nextSample(false)
	}
	if err != nil {
		return sample, err
	}
	delta := header.Timestamp - l.lastTimestamp
	sample.Data = frame
	// this should be correct too, but we'll use the known frame-rates below
	sample.Duration = time.Duration(l.ivfTimebase*float64(delta)*1000) * time.Millisecond
	l.lastTimestamp = header.Timestamp
	sample.Duration = l.frameDuration
	return sample, nil
}
