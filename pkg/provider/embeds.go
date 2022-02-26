package provider

import (
	"embed"
	"fmt"
	"os"

	"github.com/livekit/protocol/livekit"
)

type videoSpec struct {
	prefix string
	height int
	kbps   int
	fps    int
}

func butterflySpec(height, kbps, fps int) *videoSpec {
	return &videoSpec{
		prefix: "butterfly",
		height: height,
		kbps:   kbps,
		fps:    fps,
	}
}

func (v *videoSpec) Name() string {
	return fmt.Sprintf("resources/%s_%d_%d.h264", v.prefix, v.height, v.kbps)
}

func (v *videoSpec) ToVideoLayer(quality livekit.VideoQuality) *livekit.VideoLayer {
	return &livekit.VideoLayer{
		Quality: quality,
		Height:  uint32(v.height),
		Width:   uint32(v.height * 16 / 9),
		Bitrate: v.bitrate(),
	}
}

func (v *videoSpec) bitrate() uint32 {
	return uint32(v.kbps * 1000)
}

var (
	//go:embed resources
	res embed.FS

	// map of key => bitrate
	butterflyFiles = []*videoSpec{
		butterflySpec(180, 150, 15),
		butterflySpec(360, 400, 20),
		butterflySpec(540, 800, 25),
		butterflySpec(720, 2000, 30),
		butterflySpec(1080, 3000, 30),
	}
)

func ButterflyLooper(height int) (*H264VideoLooper, error) {
	var spec *videoSpec
	for _, s := range butterflyFiles {
		if s.height == height {
			spec = s
			break
		}
	}
	if spec == nil {
		return nil, os.ErrNotExist
	}
	f, err := res.Open(spec.Name())
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return NewH264VideoLooper(f, spec)
}

func ButterflyLooperForBitrate(bitrate uint32) (*H264VideoLooper, error) {
	var spec *videoSpec
	for _, s := range butterflyFiles {
		spec = s
		if s.bitrate() >= bitrate {
			break
		}
	}
	if spec == nil {
		return nil, os.ErrNotExist
	}
	f, err := res.Open(spec.Name())
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return NewH264VideoLooper(f, spec)
}
