package features

import "sync"

// Tick represents a normalized trade tick.
type Tick struct {
	Price     float64 `json:"price"`
	Volume    int64   `json:"volume"`
	Timestamp int64   `json:"timestamp"`
}

// TickRing is a thread-safe, fixed-size ring buffer for high-frequency ticks.
type TickRing struct {
	mu    sync.RWMutex
	data  []Tick
	head  int
	count int
	size  int
}

// NewTickRing creates a ring buffer with the given capacity.
func NewTickRing(size int) *TickRing {
	if size <= 0 {
		size = 500
	}
	return &TickRing{
		data: make([]Tick, size),
		size: size,
	}
}

// Push adds a new tick to the ring buffer.
func (r *TickRing) Push(t Tick) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[r.head] = t
	r.head = (r.head + 1) % r.size
	if r.count < r.size {
		r.count++
	}
}

// Latest returns the most recently inserted tick.
func (r *TickRing) Latest() (Tick, bool) {
	if r == nil {
		return Tick{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.count == 0 {
		return Tick{}, false
	}
	idx := (r.head - 1 + r.size) % r.size
	return r.data[idx], true
}

// Len returns the number of ticks currently held in the ring buffer.
func (r *TickRing) Len() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.count
}

// Slice returns all stored ticks ordered from oldest to newest.
func (r *TickRing) Slice() []Tick {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.count == 0 {
		return nil
	}
	out := make([]Tick, r.count)
	start := (r.head - r.count + r.size) % r.size
	for i := 0; i < r.count; i++ {
		out[i] = r.data[(start+i)%r.size]
	}
	return out
}
