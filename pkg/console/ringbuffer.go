//go:build console

package console

import (
	"sync"
	"sync/atomic"
)

// RingBuffer is a SPSC ring buffer for int16 audio samples.
// When the writer outruns the reader, the reader skips ahead to avoid stale data.
type RingBuffer struct {
	buf  []int16
	size int
	r    atomic.Int64
	w    atomic.Int64
	mu   sync.Mutex // only for condition variable
	cond *sync.Cond
}

func NewRingBuffer(size int) *RingBuffer {
	rb := &RingBuffer{
		buf:  make([]int16, size),
		size: size,
	}
	rb.cond = sync.NewCond(&rb.mu)
	return rb
}

func (rb *RingBuffer) Write(samples []int16) int {
	n := len(samples)
	if n > rb.size {
		samples = samples[n-rb.size:]
		n = rb.size
	}
	w := int(rb.w.Load())
	for i := 0; i < n; i++ {
		rb.buf[(w+i)%rb.size] = samples[i]
	}
	rb.w.Add(int64(n))
	rb.cond.Signal()
	return n
}

// ReadAvailable copies up to len(out) available samples into out (non-blocking).
// Returns the number of samples actually copied.
func (rb *RingBuffer) ReadAvailable(out []int16) int {
	avail := int(rb.w.Load() - rb.r.Load())
	if avail <= 0 {
		return 0
	}
	// If writer has lapped us, skip ahead
	if avail > rb.size {
		skip := int64(avail - rb.size)
		rb.r.Add(skip)
		avail = rb.size
	}
	n := len(out)
	if n > avail {
		n = avail
	}
	r := int(rb.r.Load())
	for i := 0; i < n; i++ {
		out[i] = rb.buf[(r+i)%rb.size]
	}
	rb.r.Add(int64(n))
	return n
}

// Read blocks until len(out) samples are available, then copies them.
func (rb *RingBuffer) Read(out []int16) bool {
	needed := len(out)
	copied := 0
	for copied < needed {
		avail := int(rb.w.Load() - rb.r.Load())
		if avail <= 0 {
			rb.mu.Lock()
			for rb.w.Load()-rb.r.Load() <= 0 {
				rb.cond.Wait()
			}
			rb.mu.Unlock()
			continue
		}
		if avail > rb.size {
			skip := int64(avail - rb.size)
			rb.r.Add(skip)
			avail = rb.size
		}
		toCopy := needed - copied
		if toCopy > avail {
			toCopy = avail
		}
		r := int(rb.r.Load())
		for i := 0; i < toCopy; i++ {
			out[copied+i] = rb.buf[(r+i)%rb.size]
		}
		rb.r.Add(int64(toCopy))
		copied += toCopy
	}
	return true
}

func (rb *RingBuffer) Available() int {
	return int(rb.w.Load() - rb.r.Load())
}

// WaitForData blocks until samples are available in the buffer.
// Returns true if data is available, false if woken up with no data
// (e.g., after Reset or Broadcast for shutdown).
func (rb *RingBuffer) WaitForData() bool {
	if rb.w.Load()-rb.r.Load() > 0 {
		return true
	}
	rb.mu.Lock()
	for rb.w.Load()-rb.r.Load() <= 0 {
		rb.cond.Wait()
		// After wakeup, re-check. If still empty (Reset/shutdown), return false.
		if rb.w.Load()-rb.r.Load() <= 0 {
			rb.mu.Unlock()
			return false
		}
	}
	rb.mu.Unlock()
	return true
}

func (rb *RingBuffer) Reset() {
	rb.r.Store(rb.w.Load())
	rb.cond.Broadcast()
}
