//go:build console

// Package apm provides Go bindings for the WebRTC Audio Processing Module (APM).
// It supports echo cancellation (AEC3), noise suppression, automatic gain control,
// and high-pass filtering. Audio must be 48kHz int16 PCM in 10ms frames (480 samples/channel).
package apm

// #include "bridge.h"
import "C"

import (
	"errors"
	"runtime"
	"unsafe"
)

type APMConfig struct {
	EchoCanceller   bool
	GainController  bool
	HighPassFilter  bool
	NoiseSuppressor bool
	CaptureChannels int
	RenderChannels  int
}

func DefaultConfig() APMConfig {
	return APMConfig{
		EchoCanceller:   true,
		GainController:  true,
		HighPassFilter:  true,
		NoiseSuppressor: true,
		CaptureChannels: 1,
		RenderChannels:  1,
	}
}

type APM struct {
	handle C.ApmHandle
}

func NewAPM(config APMConfig) (*APM, error) {
	capCh := config.CaptureChannels
	if capCh == 0 {
		capCh = 1
	}
	renCh := config.RenderChannels
	if renCh == 0 {
		renCh = 1
	}

	var cerr C.int
	handle := C.apm_create(
		boolToInt(config.EchoCanceller),
		boolToInt(config.GainController),
		boolToInt(config.HighPassFilter),
		boolToInt(config.NoiseSuppressor),
		C.int(capCh),
		C.int(renCh),
		&cerr,
	)
	if handle == nil {
		return nil, errors.New("apm: failed to create audio processing module")
	}

	a := &APM{handle: handle}
	runtime.SetFinalizer(a, func(a *APM) { a.Close() })
	return a, nil
}

// ProcessCapture processes a 10ms capture (microphone) frame in-place.
// samples must contain exactly 480 * numChannels int16 values.
func (a *APM) ProcessCapture(samples []int16) error {
	if a.handle == nil {
		return errors.New("apm: closed")
	}
	if len(samples) == 0 {
		return nil
	}
	numChannels := len(samples) / 480
	if numChannels == 0 {
		numChannels = 1
	}
	ret := C.apm_process_capture(
		a.handle,
		(*C.int16_t)(unsafe.Pointer(&samples[0])),
		C.int(numChannels),
	)
	if ret != 0 {
		return errors.New("apm: ProcessCapture failed")
	}
	return nil
}

// ProcessRender processes a 10ms render (speaker/far-end) frame in-place.
// This feeds the echo canceller with the signal being played back.
// samples must contain exactly 480 * numChannels int16 values.
func (a *APM) ProcessRender(samples []int16) error {
	if a.handle == nil {
		return errors.New("apm: closed")
	}
	if len(samples) == 0 {
		return nil
	}
	numChannels := len(samples) / 480
	if numChannels == 0 {
		numChannels = 1
	}
	ret := C.apm_process_render(
		a.handle,
		(*C.int16_t)(unsafe.Pointer(&samples[0])),
		C.int(numChannels),
	)
	if ret != 0 {
		return errors.New("apm: ProcessRender failed")
	}
	return nil
}

// SetStreamDelayMs sets the delay in milliseconds between the far-end signal
// being rendered and arriving at the near-end microphone.
func (a *APM) SetStreamDelayMs(ms int) {
	if a.handle == nil {
		return
	}
	C.apm_set_stream_delay_ms(a.handle, C.int(ms))
}

func (a *APM) StreamDelayMs() int {
	if a.handle == nil {
		return 0
	}
	return int(C.apm_stream_delay_ms(a.handle))
}

// Stats holds AEC statistics from the WebRTC APM.
type Stats struct {
	EchoReturnLoss            float64 // ERL in dB (higher = more echo removed)
	EchoReturnLossEnhancement float64 // ERLE in dB (higher = better cancellation)
	DivergentFilterFraction   float64 // 0-1, fraction of time filter is divergent
	DelayMs                   int     // Estimated echo path delay
	ResidualEchoLikelihood    float64 // 0-1, likelihood of residual echo
	HasERL                    bool
	HasERLE                   bool
	HasDelay                  bool
	HasResidualEcho           bool
	HasDivergent              bool
}

// GetStats returns the current AEC statistics.
func (a *APM) GetStats() Stats {
	if a.handle == nil {
		return Stats{}
	}
	var cs C.ApmStats
	C.apm_get_stats(a.handle, &cs)
	return Stats{
		EchoReturnLoss:            float64(cs.echo_return_loss),
		EchoReturnLossEnhancement: float64(cs.echo_return_loss_enhancement),
		DivergentFilterFraction:   float64(cs.divergent_filter_fraction),
		DelayMs:                   int(cs.delay_ms),
		ResidualEchoLikelihood:    float64(cs.residual_echo_likelihood),
		HasERL:                    cs.has_erl != 0,
		HasERLE:                   cs.has_erle != 0,
		HasDelay:                  cs.has_delay != 0,
		HasResidualEcho:           cs.has_residual_echo != 0,
		HasDivergent:              cs.has_divergent != 0,
	}
}

func (a *APM) Close() {
	if a.handle != nil {
		C.apm_destroy(a.handle)
		a.handle = nil
	}
}

func boolToInt(b bool) C.int {
	if b {
		return 1
	}
	return 0
}
