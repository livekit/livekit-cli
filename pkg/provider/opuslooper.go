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
	"github.com/pion/webrtc/v4/pkg/media/oggreader"

	lksdk "github.com/livekit/server-sdk-go/v2"
)

const (
	defaultOpusFrameDuration = 20 * time.Millisecond
)

type OpusAudioLooper struct {
	lksdk.BaseSampleProvider
	buffer      []byte
	reader      *oggreader.OggReader
	lastGranule uint64
}

func NewOpusAudioLooper(input io.Reader) (*OpusAudioLooper, error) {
	l := &OpusAudioLooper{}

	buf := bytes.NewBuffer(nil)

	if _, err := io.Copy(buf, input); err != nil {
		return nil, err
	}
	l.buffer = buf.Bytes()

	return l, nil
}

func (l *OpusAudioLooper) Codec() webrtc.RTPCodecCapability {
	return webrtc.RTPCodecCapability{
		MimeType: "audio/opus",
	}
}

func (l *OpusAudioLooper) NextSample(_ctx context.Context) (media.Sample, error) {
	return l.nextSample(true)
}

func (l *OpusAudioLooper) nextSample(rewindEOF bool) (media.Sample, error) {
	sample := media.Sample{}
	if l.reader == nil {
		var err error
		l.lastGranule = 0
		l.reader, _, err = oggreader.NewWith(bytes.NewReader(l.buffer))
		if err != nil {
			return sample, err
		}
	}

	pageData, pageHeader, err := l.reader.ParseNextPage()
	if err == io.EOF && rewindEOF {
		l.reader = nil
		return l.nextSample(false)
	}
	if err != nil {
		return sample, err
	}
	sampleCount := float64(pageHeader.GranulePosition - l.lastGranule)
	l.lastGranule = pageHeader.GranulePosition

	sample.Data = pageData
	sample.Duration = time.Duration((sampleCount/48000)*1000) * time.Millisecond
	if sample.Duration == 0 {
		sample.Duration = defaultOpusFrameDuration
	}
	return sample, nil
}
