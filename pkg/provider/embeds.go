// Copyright 2022-2023 LiveKit, Inc.
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
	"embed"
	"fmt"
	"math"
	"strconv"

	"go.uber.org/atomic"

	"github.com/livekit/protocol/livekit"
)

const (
	h264Codec = "h264"
	vp8Codec  = "vp8"
)

type videoSpec struct {
	codec  string
	prefix string
	height int
	width  int
	kbps   int
	fps    int
}

func (v *videoSpec) Name() string {
	ext := "h264"
	if v.codec == vp8Codec {
		ext = "ivf"
	}
	size := strconv.Itoa(v.height)
	if v.height > v.width {
		size = fmt.Sprintf("p%d", v.width)
	}
	return fmt.Sprintf("resources/%s_%s_%d.%s", v.prefix, size, v.kbps, ext)
}

func (v *videoSpec) ToVideoLayer(quality livekit.VideoQuality) *livekit.VideoLayer {
	return &livekit.VideoLayer{
		Quality: quality,
		Height:  uint32(v.height),
		Width:   uint32(v.width),
		Bitrate: v.bitrate(),
	}
}

func (v *videoSpec) bitrate() uint32 {
	return uint32(v.kbps * 1000)
}

func circlesSpec(width, kbps, fps int) *videoSpec {
	return &videoSpec{
		codec:  h264Codec,
		prefix: "circles",
		height: width * 4 / 3,
		width:  width,
		kbps:   kbps,
		fps:    fps,
	}
}

func createSpecs(prefix string, codec string, bitrates ...int) []*videoSpec {
	var specs []*videoSpec
	videoFps := []int{
		15, 20, 30,
	}
	for i, b := range bitrates {
		dimMultiple := int(math.Pow(2, float64(i)))
		specs = append(specs, &videoSpec{
			prefix: prefix,
			codec:  codec,
			kbps:   b,
			fps:    videoFps[i],
			height: 180 * dimMultiple,
			width:  180 * dimMultiple * 16 / 9,
		})
	}
	return specs
}

var (
	//go:embed resources
	res embed.FS

	videoSpecs [][]*videoSpec
	videoIndex atomic.Int64
	audioNames []string
	audioIndex atomic.Int64
)

func init() {
	videoSpecs = [][]*videoSpec{
		createSpecs("butterfly", h264Codec, 150, 400, 2000),
		createSpecs("cartoon", h264Codec, 120, 400, 1500),
		createSpecs("crescent", vp8Codec, 150, 600, 2000),
		createSpecs("neon", vp8Codec, 150, 600, 2000),
		createSpecs("tunnel", vp8Codec, 150, 600, 2000),
		{
			circlesSpec(180, 200, 15),
			circlesSpec(360, 700, 20),
			circlesSpec(540, 2000, 30),
		},
	}
	audioNames = []string{
		"change-amelia",
		"change-benjamin",
		"change-elena",
		"change-clint",
		"change-emma",
		"change-ken",
		"change-sophie",
	}
}

func randomVideoSpecsForCodec(videoCodec string) []*videoSpec {
	filtered := make([][]*videoSpec, 0)
	for _, specs := range videoSpecs {
		if videoCodec == "" || specs[0].codec == videoCodec {
			filtered = append(filtered, specs)
		}
	}
	chosen := int(videoIndex.Inc()) % len(filtered)
	return filtered[chosen]
}

func CreateVideoLoopers(resolution string, codecFilter string, simulcast bool) ([]VideoLooper, error) {
	specs := randomVideoSpecsForCodec(codecFilter)
	numToKeep := 0
	switch resolution {
	case "medium":
		numToKeep = 2
	case "low":
		numToKeep = 1
	default:
		numToKeep = 3
	}
	specs = specs[:numToKeep]
	if !simulcast {
		specs = specs[numToKeep-1:]
	}
	loopers := make([]VideoLooper, 0)
	for _, spec := range specs {
		f, err := res.Open(spec.Name())
		if err != nil {
			return nil, err
		}
		defer f.Close()
		if spec.codec == h264Codec {
			looper, err := NewH264VideoLooper(f, spec)
			if err != nil {
				return nil, err
			}
			loopers = append(loopers, looper)
		} else if spec.codec == vp8Codec {
			looper, err := NewVP8VideoLooper(f, spec)
			if err != nil {
				return nil, err
			}
			loopers = append(loopers, looper)
		}
	}
	return loopers, nil
}

func CreateAudioLooper() (*OpusAudioLooper, error) {
	chosenName := audioNames[int(audioIndex.Load())%len(audioNames)]
	audioIndex.Inc()
	f, err := res.Open(fmt.Sprintf("resources/%s.ogg", chosenName))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return NewOpusAudioLooper(f)
}
