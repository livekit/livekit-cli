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
	"github.com/pion/webrtc/v4/pkg/media/h264reader"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

type H264VideoLooper struct {
	lksdk.BaseSampleProvider
	buffer        []byte
	frameDuration time.Duration
	spec          *videoSpec
	reader        *h264reader.H264Reader
}

func NewH264VideoLooper(input io.Reader, spec *videoSpec) (*H264VideoLooper, error) {
	l := &H264VideoLooper{
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

func (l *H264VideoLooper) Codec() webrtc.RTPCodecCapability {
	return webrtc.RTPCodecCapability{
		MimeType:    "video/h264",
		ClockRate:   90000,
		Channels:    0,
		SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f",
		RTCPFeedback: []webrtc.RTCPFeedback{
			{Type: webrtc.TypeRTCPFBNACK},
			{Type: webrtc.TypeRTCPFBNACK, Parameter: "pli"},
		},
	}
}

func (l *H264VideoLooper) NextSample(_ctx context.Context) (media.Sample, error) {
	return l.nextSample(true)
}

func (l *H264VideoLooper) ToLayer(quality livekit.VideoQuality) *livekit.VideoLayer {
	return l.spec.ToVideoLayer(quality)
}

func (l *H264VideoLooper) nextSample(rewindEOF bool) (media.Sample, error) {
	sample := media.Sample{}
	if l.reader == nil {
		var err error
		l.reader, err = h264reader.NewReader(bytes.NewReader(l.buffer))
		if err != nil {
			return sample, err
		}
	}
	nal, err := l.reader.NextNAL()
	if err == io.EOF && rewindEOF {
		l.reader = nil
		return l.nextSample(false)
	}
	if err != nil {
		return sample, err
	}

	isFrame := false
	switch nal.UnitType {
	case h264reader.NalUnitTypeCodedSliceDataPartitionA,
		h264reader.NalUnitTypeCodedSliceDataPartitionB,
		h264reader.NalUnitTypeCodedSliceDataPartitionC,
		h264reader.NalUnitTypeCodedSliceIdr,
		h264reader.NalUnitTypeCodedSliceNonIdr:
		isFrame = true
	}

	sample.Data = nal.Data
	if isFrame {
		// return it without duration
		sample.Duration = l.frameDuration
	}
	return sample, nil
}
