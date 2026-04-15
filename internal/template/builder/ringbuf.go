package builder

import (
	"sync"
	"time"
)

// LogEntry is one structured line from a docker build stream. The
// fields mirror what the E2B SDK's TemplateBuildLogEntry expects
// (timestamp, level, message). Source is included so we can tag
// lines from stderr vs stdout but the SDK ignores it when filtering.
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Source    string    `json:"source,omitempty"`
	Message   string    `json:"message"`
}

// ringBuffer is a bounded append-only log buffer. New entries are
// appended at the head and older entries drop off the tail once the
// capacity is exhausted. Readers see a monotonically-increasing cursor
// that equals the absolute index of each entry, so pagination survives
// overwrites: the cursor an old client cached may no longer resolve,
// and readers get back the earliest still-present entry instead.
//
// This is the same semantics the E2B SDK expects when it polls
// /status?logsOffset=N — the server returns all entries at or after N
// up to the head, wherever the ring currently sits.
type ringBuffer struct {
	mu       sync.RWMutex
	capacity int
	entries  []LogEntry // ring storage, len == capacity once full
	head     int        // next write index (0..capacity-1)
	written  int        // total count of entries ever appended
}

func newRingBuffer(capacity int) *ringBuffer {
	if capacity <= 0 {
		capacity = 5000
	}
	return &ringBuffer{
		capacity: capacity,
		entries:  make([]LogEntry, 0, capacity),
	}
}

// Append adds an entry to the buffer. Returns the absolute index the
// entry was assigned — callers can record this for diagnostics, but
// the primary use is the pagination cursor.
func (rb *ringBuffer) Append(entry LogEntry) int64 {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if len(rb.entries) < rb.capacity {
		rb.entries = append(rb.entries, entry)
	} else {
		rb.entries[rb.head] = entry
		rb.head = (rb.head + 1) % rb.capacity
	}
	idx := int64(rb.written)
	rb.written++
	return idx
}

// Read returns entries starting at the given absolute offset, up to
// limit entries. Returns the entries and the next cursor the caller
// should pass on a follow-up call. When limit <= 0 it defaults to
// capacity (i.e. everything currently in the buffer from the offset).
//
// If offset is older than the earliest still-held entry, Read snaps
// to the earliest available cursor — stale clients see a catch-up
// slice instead of an error.
func (rb *ringBuffer) Read(offset int64, limit int) ([]LogEntry, int64) {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	total := int64(rb.written)
	if offset < 0 {
		offset = 0
	}
	earliest := total - int64(len(rb.entries))
	if offset < earliest {
		offset = earliest
	}
	if offset >= total {
		return nil, total
	}
	available := int(total - offset)
	if limit <= 0 || limit > available {
		limit = available
	}

	out := make([]LogEntry, 0, limit)
	// Translate absolute offsets to ring slots. The ring is logically
	// [earliest ... earliest+len(entries)) and physically starts at
	// rb.head when len==capacity.
	for i := 0; i < limit; i++ {
		absolute := offset + int64(i)
		var physical int
		if len(rb.entries) < rb.capacity {
			physical = int(absolute)
		} else {
			physical = (rb.head + int(absolute-earliest)) % rb.capacity
		}
		out = append(out, rb.entries[physical])
	}
	return out, offset + int64(limit)
}

// Len returns the number of entries currently in the buffer.
func (rb *ringBuffer) Len() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return len(rb.entries)
}

// Written returns the total number of entries ever appended,
// including those that have been evicted.
func (rb *ringBuffer) Written() int64 {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return int64(rb.written)
}
