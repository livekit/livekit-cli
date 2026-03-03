//go:build console

package console

import (
	"sync"
	"sync/atomic"
)

// RingBuffer is a SPSC ring buffer for int16 audio samples.
// Overwrites oldest data when full (lossy).
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
	w := int(rb.w.Load())
	for i := 0; i < n; i++ {
		rb.buf[(w+i)%rb.size] = samples[i]
	}
	rb.w.Add(int64(n))

	rb.cond.Signal()
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

func (rb *RingBuffer) Reset() {
	rb.r.Store(0)
	rb.w.Store(0)
}
