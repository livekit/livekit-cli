//go:build console

// Package console implements the audio pipeline for the lk console command.
// It connects microphone input and speaker output via PortAudio, applies
// WebRTC audio processing (echo cancellation, noise suppression), and
// communicates with an agent over TCP using raw PCM frames.
package console

import (
	"context"
	"encoding/binary"
	"io"
	"log"
	"math"
	"net"
	"sync"

	"github.com/livekit/livekit-cli/v2/pkg/apm"
	"github.com/livekit/livekit-cli/v2/pkg/portaudio"
)

const (
	SampleRate       = 48000
	Channels         = 1
	FrameDurationMs  = 20
	SamplesPerFrame  = SampleRate * FrameDurationMs / 1000 // 960
	APMFrameSamples  = SampleRate / 100                    // 480 (10ms)
	RingBufferFrames = 10                                  // ~200ms buffer
	NumFFTBands      = 14
)

type AudioPipeline struct {
	inputStream  *portaudio.Stream
	outputStream *portaudio.Stream
	apmInst      *apm.APM
	conn         net.Conn

	captureRing  *RingBuffer
	playbackRing *RingBuffer

	mu       sync.Mutex
	fftBands [NumFFTBands]float64
	muted    bool
	level    float64 // capture level in dB

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type PipelineConfig struct {
	InputDevice  *portaudio.DeviceInfo
	OutputDevice *portaudio.DeviceInfo
	NoAEC        bool
	Conn         net.Conn
}

func NewPipeline(cfg PipelineConfig) (*AudioPipeline, error) {
	inputStream, err := portaudio.OpenInputStream(cfg.InputDevice, SampleRate, Channels, SamplesPerFrame)
	if err != nil {
		return nil, err
	}

	outputStream, err := portaudio.OpenOutputStream(cfg.OutputDevice, SampleRate, Channels, SamplesPerFrame)
	if err != nil {
		inputStream.Close()
		return nil, err
	}

	var apmInst *apm.APM
	if !cfg.NoAEC {
		apmCfg := apm.DefaultConfig()
		apmCfg.CaptureChannels = Channels
		apmCfg.RenderChannels = Channels
		apmInst, err = apm.NewAPM(apmCfg)
		if err != nil {
			log.Printf("warning: failed to create APM, running without AEC: %v", err)
		}
	}

	if apmInst != nil {
		inInfo := inputStream.Info()
		outInfo := outputStream.Info()
		delayMs := int((inInfo.InputLatency + outInfo.OutputLatency).Milliseconds())
		apmInst.SetStreamDelayMs(delayMs)
	}

	p := &AudioPipeline{
		inputStream:  inputStream,
		outputStream: outputStream,
		apmInst:      apmInst,
		conn:         cfg.Conn,
		captureRing:  NewRingBuffer(SamplesPerFrame * RingBufferFrames),
		playbackRing: NewRingBuffer(SamplesPerFrame * RingBufferFrames),
	}
	return p, nil
}

func (p *AudioPipeline) Start(ctx context.Context) error {
	ctx, p.cancel = context.WithCancel(ctx)

	if err := p.inputStream.Start(); err != nil {
		return err
	}
	if err := p.outputStream.Start(); err != nil {
		p.inputStream.Stop()
		return err
	}

	p.wg.Add(4)
	go p.captureReader(ctx)
	go p.captureWorker(ctx)
	go p.playbackWorker(ctx)
	go p.playbackWriter(ctx)

	<-ctx.Done()
	return nil
}

func (p *AudioPipeline) Stop() {
	if p.cancel != nil {
		p.cancel()
	}

	// Stop PortAudio streams first (prevents callbacks into dead goroutines)
	p.inputStream.Stop()
	p.outputStream.Stop()

	// Wake up any blocked ring buffer readers
	p.captureRing.cond.Broadcast()
	p.playbackRing.cond.Broadcast()

	p.wg.Wait()

	p.inputStream.Close()
	p.outputStream.Close()

	if p.apmInst != nil {
		p.apmInst.Close()
	}

	WriteMessage(p.conn, MsgEOF, nil)
}

func (p *AudioPipeline) SetMuted(muted bool) {
	p.mu.Lock()
	p.muted = muted
	p.mu.Unlock()
}

func (p *AudioPipeline) Muted() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.muted
}

func (p *AudioPipeline) Level() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.level
}

func (p *AudioPipeline) FFTBands() [NumFFTBands]float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fftBands
}

func (p *AudioPipeline) captureReader(ctx context.Context) {
	defer p.wg.Done()
	buf := make([]int16, SamplesPerFrame*Channels)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := p.inputStream.Read(buf); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("capture read error: %v", err)
			return
		}
		p.captureRing.Write(buf)
	}
}

func (p *AudioPipeline) captureWorker(ctx context.Context) {
	defer p.wg.Done()
	frame := make([]int16, SamplesPerFrame*Channels)
	apmBuf := make([]int16, APMFrameSamples*Channels)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !p.captureRing.Read(frame) {
			return
		}

		p.mu.Lock()
		muted := p.muted
		p.mu.Unlock()

		if muted {
			for i := range frame {
				frame[i] = 0
			}
		}

		// Process through APM in 10ms chunks
		if p.apmInst != nil {
			for i := 0; i < SamplesPerFrame; i += APMFrameSamples {
				copy(apmBuf, frame[i:i+APMFrameSamples])
				if err := p.apmInst.ProcessCapture(apmBuf); err != nil {
					log.Printf("APM capture error: %v", err)
				}
				copy(frame[i:], apmBuf)
			}
		}

		p.computeMetrics(frame)

		payload := SamplesToBytes(frame)
		if err := WriteMessage(p.conn, MsgCapture, payload); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("TCP send error: %v", err)
			return
		}
	}
}

func (p *AudioPipeline) playbackWorker(ctx context.Context) {
	defer p.wg.Done()
	apmBuf := make([]int16, APMFrameSamples*Channels)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msgType, payload, err := ReadMessage(p.conn)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if err == io.EOF {
				log.Printf("Agent disconnected")
				return
			}
			// Timeout is expected when no data is flowing
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			log.Printf("TCP recv error: %v", err)
			return
		}

		switch msgType {
		case MsgRender:
			samples := BytesToSamples(payload)

			if p.apmInst != nil {
				for i := 0; i < len(samples); i += APMFrameSamples {
					end := i + APMFrameSamples
					if end > len(samples) {
						end = len(samples)
					}
					chunk := samples[i:end]
					if len(chunk) == APMFrameSamples {
						copy(apmBuf, chunk)
						if err := p.apmInst.ProcessRender(apmBuf); err != nil {
							log.Printf("APM render error: %v", err)
						}
						copy(chunk, apmBuf)
					}
				}
			}

			p.playbackRing.Write(samples)

		case MsgEOF:
			log.Printf("Agent sent EOF")
			return

		case MsgConfig:
			// TODO: handle config messages
		}
	}
}

func (p *AudioPipeline) playbackWriter(ctx context.Context) {
	defer p.wg.Done()
	buf := make([]int16, SamplesPerFrame*Channels)
	silence := make([]int16, SamplesPerFrame*Channels)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// If not enough data, play silence to avoid blocking
		if p.playbackRing.Available() < SamplesPerFrame*Channels {
			if err := p.outputStream.Write(silence); err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("playback write error: %v", err)
				return
			}
			continue
		}

		p.playbackRing.Read(buf)
		if err := p.outputStream.Write(buf); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("playback write error: %v", err)
			return
		}
	}
}

func (p *AudioPipeline) computeMetrics(samples []int16) {
	var sum float64
	for _, s := range samples {
		v := float64(s) / 32768.0
		sum += v * v
	}
	rms := math.Sqrt(sum / float64(len(samples)))
	db := 20 * math.Log10(rms+1e-10)

	// Simple band-energy estimation using overlapping windows
	// Not a real FFT, but good enough for a visualizer
	var bands [NumFFTBands]float64
	bandSize := len(samples) / NumFFTBands
	if bandSize < 1 {
		bandSize = 1
	}
	for b := 0; b < NumFFTBands; b++ {
		start := b * bandSize
		end := start + bandSize
		if end > len(samples) {
			end = len(samples)
		}
		var bandSum float64
		for i := start; i < end; i++ {
			v := float64(samples[i]) / 32768.0
			bandSum += v * v
		}
		bandRMS := math.Sqrt(bandSum / float64(end-start))
		bands[b] = math.Min(bandRMS*8.0, 1.0)
	}

	p.mu.Lock()
	p.level = db
	p.fftBands = bands
	p.mu.Unlock()
}

func ComputeFFTBands(samples []int16) [NumFFTBands]float64 {
	var bands [NumFFTBands]float64
	bandSize := len(samples) / NumFFTBands
	if bandSize < 1 {
		bandSize = 1
	}
	for b := 0; b < NumFFTBands; b++ {
		start := b * len(samples) / NumFFTBands
		end := (b + 1) * len(samples) / NumFFTBands
		if end > len(samples) {
			end = len(samples)
		}
		var sum float64
		for i := start; i < end; i++ {
			v := float64(samples[i]) / 32768.0
			sum += v * v
		}
		rms := math.Sqrt(sum / float64(end-start))
		bands[b] = math.Min(rms*8.0, 1.0)
	}
	return bands
}

func ComputeLevelDB(samples []int16) float64 {
	var sum float64
	for _, s := range samples {
		v := float64(s) / 32768.0
		sum += v * v
	}
	rms := math.Sqrt(sum / float64(len(samples)))
	return 20 * math.Log10(rms+1e-10)
}

func Int16LEToBytes(samples []int16) []byte {
	buf := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(s))
	}
	return buf
}
