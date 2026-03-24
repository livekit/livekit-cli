//go:build console

// Package console implements the audio pipeline for the lk console command.
// It connects microphone input and speaker output via PortAudio, applies
// WebRTC audio processing (echo cancellation, noise suppression), and
// communicates with an agent over TCP using protobuf-framed SessionMessages.
//
// Architecture (3 goroutines, matching the Python console's PortAudio model):
//
//	micLoop     — reads PortAudio input into the capture ring buffer.
//	speakerLoop — reads both rings, runs ProcessRender + ProcessCapture in
//	              lockstep, writes to speakers, sends capture to agent.
//	              Paced by outputStream.Write at the hardware output rate.
//	tcpReader   — reads TCP messages: audio → playback ring, events → TUI.
//
// All APM calls happen in speakerLoop, so they are single-threaded and
// guaranteed 1:1.
package console

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"sync"
	"time"

	agent "github.com/livekit/protocol/livekit/agent"

	"github.com/livekit/livekit-cli/v2/pkg/apm"
	"github.com/livekit/livekit-cli/v2/pkg/portaudio"
)

const (
	SampleRate      = 48000
	Channels        = 1
	FrameDurationMs = 30
	SamplesPerFrame = SampleRate * FrameDurationMs / 1000 // 1440
	APMFrameSamples = SampleRate / 100                    // 480 (10ms)
	NumFFTBands     = 14

	CaptureRingFrames  = 50   // ~1.5s — small, just absorbs jitter between mic and speaker loops
	PlaybackRingFrames = 4000 // ~120s — large, TTS pushes faster than real-time
)

type AudioPipeline struct {
	inputStream  *portaudio.Stream
	outputStream *portaudio.Stream
	apmInst      *apm.APM
	noAEC        bool
	conn         net.Conn
	connMu       sync.Mutex // protects writes to conn

	captureRing  *RingBuffer
	playbackRing *RingBuffer

	// Events channel receives AgentSessionEvents from the agent for the TUI.
	Events chan *agent.AgentSessionEvent

	// Responses channel receives SessionResponses (request completions) for the TUI.
	Responses chan *agent.SessionResponse

	// ready is closed when the agent session is established (first TCP message).
	ready     chan struct{}
	readyOnce sync.Once

	// flushCancel cancels the current waitForDrainAndAck goroutine.
	// Only accessed from the tcpReader goroutine.
	flushCancel context.CancelFunc

	mu sync.Mutex
	fftBands [NumFFTBands]float64
	muted    bool
	level    float64 // capture level in dB
	playing  bool    // true when outputting real audio (not silence)

	cancel   context.CancelFunc
	audioCtx context.Context // stored so EnableAudio can start goroutines
	wg       sync.WaitGroup
}

type PipelineConfig struct {
	InputDevice  *portaudio.DeviceInfo // nil to skip audio (text-only)
	OutputDevice *portaudio.DeviceInfo // nil to skip audio (text-only)
	NoAEC        bool
	Conn         net.Conn
}

func NewPipeline(cfg PipelineConfig) (*AudioPipeline, error) {
	ap := &AudioPipeline{
		conn:      cfg.Conn,
		noAEC:     cfg.NoAEC,
		Events:    make(chan *agent.AgentSessionEvent, 64),
		Responses: make(chan *agent.SessionResponse, 16),
		ready:     make(chan struct{}),
	}

	if cfg.InputDevice != nil && cfg.OutputDevice != nil {
		if err := ap.initAudio(cfg.InputDevice, cfg.OutputDevice, cfg.NoAEC); err != nil {
			return nil, err
		}
	}

	return ap, nil
}

func (p *AudioPipeline) initAudio(inputDev, outputDev *portaudio.DeviceInfo, noAEC bool) error {
	inputStream, err := portaudio.OpenInputStream(inputDev, SampleRate, Channels, SamplesPerFrame)
	if err != nil {
		return err
	}

	outputStream, err := portaudio.OpenOutputStream(outputDev, SampleRate, Channels, SamplesPerFrame)
	if err != nil {
		inputStream.Close()
		return err
	}

	var apmInst *apm.APM
	if !noAEC {
		apmCfg := apm.DefaultConfig()
		apmCfg.CaptureChannels = Channels
		apmCfg.RenderChannels = Channels
		apmInst, err = apm.NewAPM(apmCfg)
		if err != nil {
			apmInst = nil // run without AEC
		}
	}

	if apmInst != nil {
		inInfo := inputStream.Info()
		outInfo := outputStream.Info()
		delayMs := int((inInfo.InputLatency + outInfo.OutputLatency).Milliseconds())
		apmInst.SetStreamDelayMs(delayMs)
	}

	p.inputStream = inputStream
	p.outputStream = outputStream
	p.apmInst = apmInst
	p.captureRing = NewRingBuffer(SamplesPerFrame * CaptureRingFrames)
	p.playbackRing = NewRingBuffer(SamplesPerFrame * PlaybackRingFrames)
	return nil
}

// EnableAudio lazily initializes audio devices. Returns an error if
// PortAudio is not available or devices cannot be opened.
func (p *AudioPipeline) EnableAudio() error {
	if p.HasAudio() {
		return nil
	}

	if err := portaudio.Initialize(); err != nil {
		return fmt.Errorf("failed to initialize PortAudio: %w", err)
	}

	inputDev, err := portaudio.DefaultInputDevice()
	if err != nil {
		portaudio.Terminate()
		return fmt.Errorf("input device: %w", err)
	}
	outputDev, err := portaudio.DefaultOutputDevice()
	if err != nil {
		portaudio.Terminate()
		return fmt.Errorf("output device: %w", err)
	}

	if err := p.initAudio(inputDev, outputDev, p.noAEC); err != nil {
		portaudio.Terminate()
		return err
	}

	// Start the audio loops
	if err := p.outputStream.Start(); err != nil {
		return err
	}
	if err := p.inputStream.Start(); err != nil {
		p.outputStream.Stop()
		return err
	}

	p.wg.Add(2)
	ctx := p.audioCtx
	go p.micLoop(ctx)
	go p.speakerLoop(ctx)

	return nil
}

// HasAudio reports whether the audio pipeline is active.
func (p *AudioPipeline) HasAudio() bool {
	return p.inputStream != nil
}

func (p *AudioPipeline) Start(ctx context.Context) error {
	ctx, p.cancel = context.WithCancel(ctx)
	p.audioCtx = ctx

	// Always run the TCP reader for events/responses.
	p.wg.Add(1)
	go p.tcpReader(ctx)

	// Start audio loops if devices are available.
	if p.HasAudio() {
		if err := p.outputStream.Start(); err != nil {
			return err
		}
		if err := p.inputStream.Start(); err != nil {
			p.outputStream.Stop()
			return err
		}
		p.wg.Add(2)
		go p.micLoop(ctx)
		go p.speakerLoop(ctx)
	}

	<-ctx.Done()
	return nil
}

func (p *AudioPipeline) Stop() {
	if p.cancel != nil {
		p.cancel()
	}

	if p.HasAudio() {
		p.inputStream.Abort()
		p.outputStream.Abort()
	}
	p.conn.Close()
	if p.captureRing != nil {
		p.captureRing.cond.Broadcast()
	}

	p.wg.Wait()

	if p.HasAudio() {
		p.inputStream.Close()
		p.outputStream.Close()
	}
	if p.apmInst != nil {
		p.apmInst.Close()
	}
}

func (p *AudioPipeline) writeMessage(msg *agent.AgentSessionMessage) error {
	p.connMu.Lock()
	defer p.connMu.Unlock()
	return WriteSessionMessage(p.conn, msg)
}

func (p *AudioPipeline) SendRequest(req *agent.SessionRequest) error {
	return p.writeMessage(&agent.AgentSessionMessage{
		Message: &agent.AgentSessionMessage_Request{Request: req},
	})
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

func (p *AudioPipeline) IsPlaying() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.playing
}

func (p *AudioPipeline) AECStats() *apm.Stats {
	if p.apmInst == nil {
		return nil
	}
	s := p.apmInst.GetStats()
	return &s
}

// micLoop reads mic input at hardware rate and writes to the capture ring.
// Muting is applied here so speakerLoop always sees clean data.
func (p *AudioPipeline) micLoop(ctx context.Context) {
	defer p.wg.Done()
	buf := make([]int16, SamplesPerFrame*Channels)

	for {
		if ctx.Err() != nil {
			return
		}
		if err := p.inputStream.Read(buf); err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}

		p.mu.Lock()
		muted := p.muted
		p.mu.Unlock()

		if muted {
			for i := range buf {
				buf[i] = 0
			}
		}

		p.captureRing.Write(buf)
	}
}

// speakerLoop runs all APM processing and output. Paced by outputStream.Write
// at the hardware output rate (~30ms). Each iteration:
//  1. Reads capture from captureRing (non-blocking, silence if empty)
//  2. Reads playback from playbackRing (non-blocking, silence if empty)
//  3. ProcessRender then ProcessCapture (single-threaded, 1:1)
//  4. Writes playback to speakers
//  5. Sends processed capture to agent
func (p *AudioPipeline) speakerLoop(ctx context.Context) {
	defer p.wg.Done()
	captureBuf := make([]int16, SamplesPerFrame*Channels)
	playbackBuf := make([]int16, SamplesPerFrame*Channels)
	apmBuf := make([]int16, APMFrameSamples*Channels)
	ready := false

	for {
		if ctx.Err() != nil {
			return
		}

		// Read capture (non-blocking); pad remainder with silence.
		cn := p.captureRing.ReadAvailable(captureBuf)
		for i := cn; i < len(captureBuf); i++ {
			captureBuf[i] = 0
		}

		// Read playback (non-blocking); pad remainder with silence.
		pn := p.playbackRing.ReadAvailable(playbackBuf)
		for i := pn; i < len(playbackBuf); i++ {
			playbackBuf[i] = 0
		}

		p.mu.Lock()
		p.playing = pn > 0
		p.mu.Unlock()

		// ProcessRender then ProcessCapture — both in this goroutine,
		// right next to each other, no mutex needed.
		if p.apmInst != nil {
			for i := 0; i < SamplesPerFrame; i += APMFrameSamples {
				copy(apmBuf, playbackBuf[i:i+APMFrameSamples])
				_ = p.apmInst.ProcessRender(apmBuf)

				copy(apmBuf, captureBuf[i:i+APMFrameSamples])
				_ = p.apmInst.ProcessCapture(apmBuf)
				copy(captureBuf[i:], apmBuf)
			}
		}

		// Write playback to speakers — blocks at hardware rate.
		if err := p.outputStream.Write(playbackBuf); err != nil {
			if ctx.Err() != nil {
				return
			}
		}

		// Send processed capture to agent (only after session is ready).
		if !ready {
			select {
			case <-p.ready:
				ready = true
			default:
				continue
			}
		}

		p.computeMetrics(captureBuf)

		_ = p.writeMessage(&agent.AgentSessionMessage{
			Message: &agent.AgentSessionMessage_AudioInput{
				AudioInput: &agent.AgentSessionMessage_ConsoleIO_AudioFrame{
					Data:              SamplesToBytes(captureBuf),
					SampleRate:        SampleRate,
					NumChannels:       Channels,
					SamplesPerChannel: uint32(SamplesPerFrame),
				},
			},
		})
	}
}

// tcpReader reads messages from the agent over TCP and dispatches them.
func (p *AudioPipeline) tcpReader(ctx context.Context) {
	defer p.wg.Done()

	for {
		msg, err := ReadSessionMessage(p.conn)
		if err != nil {
			return
		}

		p.readyOnce.Do(func() { close(p.ready) })

		switch m := msg.Message.(type) {
		case *agent.AgentSessionMessage_AudioOutput:
			p.playbackRing.Write(BytesToSamples(m.AudioOutput.Data))

		case *agent.AgentSessionMessage_Event:
			select {
			case p.Events <- m.Event:
			default:
			}

		case *agent.AgentSessionMessage_AudioPlaybackClear:
			if p.flushCancel != nil {
				p.flushCancel()
				p.flushCancel = nil
			}
			p.playbackRing.Reset()

		case *agent.AgentSessionMessage_AudioPlaybackFlush:
			if p.flushCancel != nil {
				p.flushCancel()
			}
			flushCtx, cancel := context.WithCancel(ctx)
			p.flushCancel = cancel
			go p.waitForDrainAndAck(flushCtx)

		case *agent.AgentSessionMessage_Response:
			// Forward response so the TUI knows the request completed.
			// Don't synthesize ConversationItemAdded — those arrive via the
			// event stream already.
			if m.Response != nil {
				select {
				case p.Responses <- m.Response:
				default:
				}
			}
		}
	}
}

func (p *AudioPipeline) sendPlaybackFinished() {
	_ = p.writeMessage(&agent.AgentSessionMessage{
		Message: &agent.AgentSessionMessage_AudioPlaybackFinished{
			AudioPlaybackFinished: &agent.AgentSessionMessage_ConsoleIO_AudioPlaybackFinished{},
		},
	})
}

func (p *AudioPipeline) waitForDrainAndAck(ctx context.Context) {
	for p.playbackRing.Available() > 0 {
		select {
		case <-ctx.Done():
			return
		default:
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-ctx.Done():
		return
	default:
	}
	p.sendPlaybackFinished()
}

func (p *AudioPipeline) computeMetrics(samples []int16) {
	n := len(samples)
	sr := float64(SampleRate)

	// Convert to float64, normalize, apply Hanning window
	x := make([]float64, n)
	for i, s := range samples {
		v := float64(s) / 32768.0
		w := 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(n)))
		x[i] = v * w
	}

	// Real FFT
	X, nfft := rfft(x)

	// Magnitude spectrum, scaled by 2/n
	mag := make([]float64, len(X))
	scale := 2.0 / float64(n)
	for i, c := range X {
		r, im := real(c), imag(c)
		mag[i] = math.Sqrt(r*r+im*im) * scale
	}
	mag[0] *= 0.5
	if n%2 == 0 {
		mag[len(mag)-1] *= 0.5
	}

	// Geometric frequency band edges: 20 Hz → Nyquist*0.96
	nb := NumFFTBands
	nyquist := sr * 0.5 * 0.96
	logLow := math.Log(20.0)
	logHigh := math.Log(nyquist)
	edges := make([]float64, nb+1)
	for i := 0; i <= nb; i++ {
		edges[i] = math.Exp(logLow + float64(i)*(logHigh-logLow)/float64(nb))
	}

	// Bin power into frequency bands
	binFreq := sr / float64(nfft)
	sump := make([]float64, nb)
	cnts := make([]float64, nb)
	for i, m := range mag {
		freq := float64(i) * binFreq
		// Find band via edges (equivalent to np.digitize - 1, clipped)
		band := nb - 1
		for b := 1; b <= nb; b++ {
			if freq < edges[b] {
				band = b - 1
				break
			}
		}
		if band < 0 {
			band = 0
		}
		sump[band] += m * m
		cnts[band]++
	}

	// Mean power → dB → normalize to [0,1]
	const floorDB, hotDB = -70.0, -20.0
	var bands [NumFFTBands]float64
	for b := 0; b < nb; b++ {
		c := cnts[b]
		if c == 0 {
			c = 1
		}
		pmean := sump[b] / c
		db := 10.0 * math.Log10(pmean + 1e-12)
		lev := (db - floorDB) / (hotDB - floorDB)
		lev = math.Max(0, math.Min(1, lev))
		// Power-law compression
		lev = math.Max(math.Pow(lev, 0.75)-0.02, 0)
		bands[b] = lev
	}

	// Peak normalization (cap scale at 3x to avoid blowing up silence)
	peak := 0.0
	for _, v := range bands {
		if v > peak {
			peak = v
		}
	}
	normScale := math.Min(0.95/(peak+1e-6), 3.0)
	for b := range bands {
		bands[b] = math.Min(bands[b]*normScale, 1.0)
	}

	// Exponential decay smoothing (~100ms time constant)
	decay := math.Exp(-float64(n) / sr / 0.1)

	// RMS level in dB
	var sum float64
	for _, s := range samples {
		v := float64(s) / 32768.0
		sum += v * v
	}
	rms := math.Sqrt(sum / float64(n))
	db := 20 * math.Log10(rms+1e-10)

	p.mu.Lock()
	for b := 0; b < nb; b++ {
		if bands[b] > p.fftBands[b]*decay {
			p.fftBands[b] = bands[b]
		} else {
			p.fftBands[b] *= decay
		}
	}
	p.level = db
	p.mu.Unlock()
}

func SamplesToBytes(samples []int16) []byte {
	buf := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(s))
	}
	return buf
}

func BytesToSamples(data []byte) []int16 {
	n := len(data) / 2
	samples := make([]int16, n)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(data[i*2:]))
	}
	return samples
}
