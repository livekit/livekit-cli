//go:build console

// Package portaudio provides Go bindings for PortAudio, compiled from vendored source.
package portaudio

/*
#cgo CFLAGS: -I${SRCDIR}/pa_src/include -I${SRCDIR}/pa_src/src/common -DPA_LITTLE_ENDIAN -Wno-unused-parameter -Wno-deprecated-declarations

#include "pa_src/src/common/pa_allocation.c"
#include "pa_src/src/common/pa_converters.c"
#include "pa_src/src/common/pa_cpuload.c"
#include "pa_src/src/common/pa_debugprint.c"
#include "pa_src/src/common/pa_dither.c"
#include "pa_src/src/common/pa_front.c"
#include "pa_src/src/common/pa_process.c"
#include "pa_src/src/common/pa_ringbuffer.c"
#include "pa_src/src/common/pa_stream.c"
#include "pa_src/src/common/pa_trace.c"

#include "portaudio.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

// Initialize must be called before any other portaudio function.
func Initialize() error {
	return paError(C.Pa_Initialize())
}

func Terminate() error {
	return paError(C.Pa_Terminate())
}

type DeviceInfo struct {
	Index             int
	Name              string
	MaxInputChannels  int
	MaxOutputChannels int
	DefaultSampleRate float64
	IsDefaultInput    bool
	IsDefaultOutput   bool
	InputLatency      time.Duration
	OutputLatency     time.Duration
}

func ListDevices() ([]DeviceInfo, error) {
	count := int(C.Pa_GetDeviceCount())
	if count < 0 {
		return nil, paError(C.PaError(count))
	}

	defaultIn := int(C.Pa_GetDefaultInputDevice())
	defaultOut := int(C.Pa_GetDefaultOutputDevice())

	devices := make([]DeviceInfo, 0, count)
	for i := 0; i < count; i++ {
		info := C.Pa_GetDeviceInfo(C.PaDeviceIndex(i))
		if info == nil {
			continue
		}
		devices = append(devices, DeviceInfo{
			Index:             i,
			Name:              C.GoString(info.name),
			MaxInputChannels:  int(info.maxInputChannels),
			MaxOutputChannels: int(info.maxOutputChannels),
			DefaultSampleRate: float64(info.defaultSampleRate),
			IsDefaultInput:    i == defaultIn,
			IsDefaultOutput:   i == defaultOut,
			InputLatency:      time.Duration(float64(info.defaultLowInputLatency) * float64(time.Second)),
			OutputLatency:     time.Duration(float64(info.defaultLowOutputLatency) * float64(time.Second)),
		})
	}
	return devices, nil
}

func DefaultInputDevice() (*DeviceInfo, error) {
	idx := int(C.Pa_GetDefaultInputDevice())
	if idx < 0 {
		return nil, errors.New("portaudio: no default input device")
	}
	info := C.Pa_GetDeviceInfo(C.PaDeviceIndex(idx))
	if info == nil {
		return nil, errors.New("portaudio: failed to get default input device info")
	}
	defaultOut := int(C.Pa_GetDefaultOutputDevice())
	d := &DeviceInfo{
		Index:             idx,
		Name:              C.GoString(info.name),
		MaxInputChannels:  int(info.maxInputChannels),
		MaxOutputChannels: int(info.maxOutputChannels),
		DefaultSampleRate: float64(info.defaultSampleRate),
		IsDefaultInput:    true,
		IsDefaultOutput:   idx == defaultOut,
		InputLatency:      time.Duration(float64(info.defaultLowInputLatency) * float64(time.Second)),
		OutputLatency:     time.Duration(float64(info.defaultLowOutputLatency) * float64(time.Second)),
	}
	return d, nil
}

func DefaultOutputDevice() (*DeviceInfo, error) {
	idx := int(C.Pa_GetDefaultOutputDevice())
	if idx < 0 {
		return nil, errors.New("portaudio: no default output device")
	}
	info := C.Pa_GetDeviceInfo(C.PaDeviceIndex(idx))
	if info == nil {
		return nil, errors.New("portaudio: failed to get default output device info")
	}
	defaultIn := int(C.Pa_GetDefaultInputDevice())
	d := &DeviceInfo{
		Index:             idx,
		Name:              C.GoString(info.name),
		MaxInputChannels:  int(info.maxInputChannels),
		MaxOutputChannels: int(info.maxOutputChannels),
		DefaultSampleRate: float64(info.defaultSampleRate),
		IsDefaultInput:    idx == defaultIn,
		IsDefaultOutput:   true,
		InputLatency:      time.Duration(float64(info.defaultLowInputLatency) * float64(time.Second)),
		OutputLatency:     time.Duration(float64(info.defaultLowOutputLatency) * float64(time.Second)),
	}
	return d, nil
}

// FindDevice finds a device by index (numeric string) or name substring (case-insensitive).
func FindDevice(query string, input bool) (*DeviceInfo, error) {
	devices, err := ListDevices()
	if err != nil {
		return nil, err
	}

	// Try numeric index first
	if idx, err := strconv.Atoi(query); err == nil {
		for i := range devices {
			if devices[i].Index == idx {
				if input && devices[i].MaxInputChannels == 0 {
					return nil, fmt.Errorf("portaudio: device %d (%s) has no input channels", idx, devices[i].Name)
				}
				if !input && devices[i].MaxOutputChannels == 0 {
					return nil, fmt.Errorf("portaudio: device %d (%s) has no output channels", idx, devices[i].Name)
				}
				return &devices[i], nil
			}
		}
		return nil, fmt.Errorf("portaudio: no device with index %d", idx)
	}

	// Name substring match (case-insensitive)
	queryLower := strings.ToLower(query)
	for i := range devices {
		if !strings.Contains(strings.ToLower(devices[i].Name), queryLower) {
			continue
		}
		if input && devices[i].MaxInputChannels > 0 {
			return &devices[i], nil
		}
		if !input && devices[i].MaxOutputChannels > 0 {
			return &devices[i], nil
		}
	}

	return nil, fmt.Errorf("portaudio: no %s device matching %q", dirStr(input), query)
}

type StreamInfo struct {
	InputLatency  time.Duration
	OutputLatency time.Duration
	SampleRate    float64
}

type Stream struct {
	stream     *C.PaStream
	sampleRate int
	channels   int
	frames     int
	isInput    bool
}

func OpenInputStream(device *DeviceInfo, sampleRate, channels, framesPerBuffer int) (*Stream, error) {
	params := C.PaStreamParameters{
		device:                    C.PaDeviceIndex(device.Index),
		channelCount:              C.int(channels),
		sampleFormat:              C.paInt16,
		suggestedLatency:          C.PaTime(device.InputLatency.Seconds()),
		hostApiSpecificStreamInfo: nil,
	}

	var stream unsafe.Pointer
	err := paError(C.Pa_OpenStream(
		&stream,
		&params, // input
		nil,     // no output
		C.double(sampleRate),
		C.ulong(framesPerBuffer),
		C.paClipOff,
		nil, nil, // blocking mode (no callback)
	))
	if err != nil {
		return nil, fmt.Errorf("portaudio: open input stream on %q: %w", device.Name, err)
	}

	return &Stream{
		stream:     (*C.PaStream)(stream),
		sampleRate: sampleRate,
		channels:   channels,
		frames:     framesPerBuffer,
		isInput:    true,
	}, nil
}

func OpenOutputStream(device *DeviceInfo, sampleRate, channels, framesPerBuffer int) (*Stream, error) {
	params := C.PaStreamParameters{
		device:                    C.PaDeviceIndex(device.Index),
		channelCount:              C.int(channels),
		sampleFormat:              C.paInt16,
		suggestedLatency:          C.PaTime(device.OutputLatency.Seconds()),
		hostApiSpecificStreamInfo: nil,
	}

	var stream unsafe.Pointer
	err := paError(C.Pa_OpenStream(
		&stream,
		nil,     // no input
		&params, // output
		C.double(sampleRate),
		C.ulong(framesPerBuffer),
		C.paClipOff,
		nil, nil, // blocking mode
	))
	if err != nil {
		return nil, fmt.Errorf("portaudio: open output stream on %q: %w", device.Name, err)
	}

	return &Stream{
		stream:     (*C.PaStream)(stream),
		sampleRate: sampleRate,
		channels:   channels,
		frames:     framesPerBuffer,
		isInput:    false,
	}, nil
}

func (s *Stream) Read(buf []int16) error {
	frames := len(buf) / s.channels
	return paError(C.Pa_ReadStream(unsafe.Pointer(s.stream), unsafe.Pointer(&buf[0]), C.ulong(frames)))
}

func (s *Stream) Write(buf []int16) error {
	frames := len(buf) / s.channels
	return paError(C.Pa_WriteStream(unsafe.Pointer(s.stream), unsafe.Pointer(&buf[0]), C.ulong(frames)))
}

func (s *Stream) Info() StreamInfo {
	info := C.Pa_GetStreamInfo(unsafe.Pointer(s.stream))
	if info == nil {
		return StreamInfo{SampleRate: float64(s.sampleRate)}
	}
	return StreamInfo{
		InputLatency:  time.Duration(float64(info.inputLatency) * float64(time.Second)),
		OutputLatency: time.Duration(float64(info.outputLatency) * float64(time.Second)),
		SampleRate:    float64(info.sampleRate),
	}
}

func (s *Stream) Start() error {
	return paError(C.Pa_StartStream(unsafe.Pointer(s.stream)))
}

// Stop waits for buffered output to drain, then stops the stream.
func (s *Stream) Stop() error {
	return paError(C.Pa_StopStream(unsafe.Pointer(s.stream)))
}

// Abort stops the stream immediately without waiting for buffers to drain.
func (s *Stream) Abort() error {
	return paError(C.Pa_AbortStream(unsafe.Pointer(s.stream)))
}

func (s *Stream) Close() error {
	if s.stream == nil {
		return nil
	}
	err := paError(C.Pa_CloseStream(unsafe.Pointer(s.stream)))
	s.stream = nil
	return err
}

func paError(code C.PaError) error {
	if code >= 0 {
		return nil
	}
	return errors.New(C.GoString(C.Pa_GetErrorText(code)))
}

func dirStr(input bool) string {
	if input {
		return "input"
	}
	return "output"
}
