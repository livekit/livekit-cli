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

package loadtester

import (
	"bytes"
	"encoding/binary"
	"errors"
	"time"

	"github.com/pion/webrtc/v4/pkg/media"
)

// LoadTestProvider is designed to be used with the load tester.
// It provides packets that are encoded with Sequence and timing information, in order determine RTT and loss
type LoadTestProvider struct {
	BytesPerSample uint32
	SampleDuration time.Duration
}

func NewLoadTestProvider(bitrate uint32) (*LoadTestProvider, error) {
	bytesPerSample := bitrate / 8 / 30
	if bytesPerSample < 8 {
		return nil, errors.New("bitrate lower than minimum of 1920")
	}

	return &LoadTestProvider{
		SampleDuration: time.Second / 30,
		BytesPerSample: bytesPerSample,
	}, nil
}

func (p *LoadTestProvider) NextSample() (media.Sample, error) {
	// sample format:
	// 0xfafafa + 0000... + 8 bytes for ts
	buf := bytes.NewBuffer(nil)
	buf.Write([]byte{
		0xfa, 0xfa, 0xfa, 0xfa,
	})
	buf.Write(make([]byte, p.BytesPerSample-12))
	ts := make([]byte, 8)
	binary.LittleEndian.PutUint64(ts, uint64(time.Now().UnixNano()))
	buf.Write(ts)

	return media.Sample{
		Data:     buf.Bytes(),
		Duration: p.SampleDuration,
	}, nil
}

func (p *LoadTestProvider) OnBind() error {
	return nil
}

func (p *LoadTestProvider) OnUnbind() error {
	return nil
}

type LoadTestDepacketizer struct {
}

func (d *LoadTestDepacketizer) Unmarshal(packet []byte) ([]byte, error) {
	return packet, nil
}

func (d *LoadTestDepacketizer) IsPartitionHead(payload []byte) bool {
	if len(payload) < 4 {
		return false
	}
	for i := 0; i < 4; i++ {
		if payload[i] != 0xfa {
			return false
		}
	}
	return true
}

func (d *LoadTestDepacketizer) IsPartitionTail(marker bool, payload []byte) bool {
	size := len(payload)
	if size < 10 {
		return false
	}

	// two 0 bytes followed by 8 bytes of ts
	if payload[size-10] != 0 || payload[size-9] != 0 {
		return false
	}
	// parse timestamp
	ts := binary.LittleEndian.Uint64(payload[size-8:])
	return ts > uint64(time.Now().Add(-time.Minute).UnixNano()) && ts < uint64(time.Now().UnixNano())
}
