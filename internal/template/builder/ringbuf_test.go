package builder

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func mkEntry(i int) LogEntry {
	return LogEntry{
		Timestamp: time.Unix(int64(i), 0),
		Level:     "info",
		Message:   fmt.Sprintf("line %d", i),
	}
}

func TestRingBufferAppendAndRead(t *testing.T) {
	rb := newRingBuffer(10)
	for i := 0; i < 5; i++ {
		rb.Append(mkEntry(i))
	}
	if rb.Len() != 5 {
		t.Fatalf("Len=%d", rb.Len())
	}
	entries, next := rb.Read(0, 0)
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}
	if next != 5 {
		t.Fatalf("expected next=5, got %d", next)
	}
	for i, e := range entries {
		if e.Message != fmt.Sprintf("line %d", i) {
			t.Fatalf("entry %d: %q", i, e.Message)
		}
	}
}

func TestRingBufferPartialRead(t *testing.T) {
	rb := newRingBuffer(10)
	for i := 0; i < 5; i++ {
		rb.Append(mkEntry(i))
	}
	entries, next := rb.Read(2, 2)
	if len(entries) != 2 || entries[0].Message != "line 2" || entries[1].Message != "line 3" {
		t.Fatalf("unexpected slice: %+v", entries)
	}
	if next != 4 {
		t.Fatalf("expected next=4, got %d", next)
	}
}

func TestRingBufferWrapAround(t *testing.T) {
	rb := newRingBuffer(3)
	for i := 0; i < 10; i++ {
		rb.Append(mkEntry(i))
	}
	// Only the last 3 should remain: 7, 8, 9.
	entries, next := rb.Read(0, 0)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d: %+v", len(entries), entries)
	}
	if entries[0].Message != "line 7" || entries[2].Message != "line 9" {
		t.Fatalf("wrap-around ordering broken: %+v", entries)
	}
	if next != 10 {
		t.Fatalf("expected next=10, got %d", next)
	}
}

func TestRingBufferStaleCursorSnapsForward(t *testing.T) {
	rb := newRingBuffer(3)
	for i := 0; i < 10; i++ {
		rb.Append(mkEntry(i))
	}
	// A caller with a stale cursor at 0 should get the 3 most recent
	// entries (line 7, 8, 9), not an error.
	entries, next := rb.Read(0, 0)
	if len(entries) != 3 || entries[0].Message != "line 7" {
		t.Fatalf("stale cursor not snapped: %+v", entries)
	}
	if next != 10 {
		t.Fatalf("expected next=10, got %d", next)
	}
}

func TestRingBufferReadBeyondEnd(t *testing.T) {
	rb := newRingBuffer(10)
	rb.Append(mkEntry(0))
	rb.Append(mkEntry(1))
	entries, next := rb.Read(10, 0)
	if len(entries) != 0 {
		t.Fatalf("expected empty, got %+v", entries)
	}
	if next != 2 {
		t.Fatalf("expected next=2, got %d", next)
	}
}

func TestRingBufferIncrementalReads(t *testing.T) {
	rb := newRingBuffer(100)
	rb.Append(mkEntry(0))
	rb.Append(mkEntry(1))
	// Caller polls at cursor 0
	entries, next := rb.Read(0, 0)
	if len(entries) != 2 {
		t.Fatalf("first read: %+v", entries)
	}
	// More entries arrive
	rb.Append(mkEntry(2))
	rb.Append(mkEntry(3))
	// Next poll with the returned cursor picks up just the new ones
	entries, next = rb.Read(next, 0)
	if len(entries) != 2 || entries[0].Message != "line 2" || entries[1].Message != "line 3" {
		t.Fatalf("second read: %+v", entries)
	}
	if next != 4 {
		t.Fatalf("expected next=4, got %d", next)
	}
}

func TestRingBufferConcurrent(t *testing.T) {
	rb := newRingBuffer(1000)
	var wg sync.WaitGroup
	for w := 0; w < 10; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				rb.Append(LogEntry{Message: fmt.Sprintf("w%d-%d", id, i)})
			}
		}(w)
	}
	// Concurrent reader
	wg.Add(1)
	go func() {
		defer wg.Done()
		var offset int64
		for i := 0; i < 100; i++ {
			_, offset = rb.Read(offset, 20)
		}
	}()
	wg.Wait()
	if rb.Written() != 1000 {
		t.Fatalf("expected 1000 writes, got %d", rb.Written())
	}
}
