package market

import "github.com/google/uuid"

// eventRingBuffer is a fixed-capacity FIFO ring buffer of UUIDs for O(1) deduplication eviction.
// When the buffer is full, the oldest entry is evicted in O(1) without any slice shifts.
const ringBufferCapacity = 50_000

type eventRingBuffer struct {
	slots [ringBufferCapacity]uuid.UUID
	head  int // next write position (oldest entry)
	count int // number of live entries
}

// add inserts an event ID, returning the evicted UUID (or uuid.Nil if not yet full).
func (r *eventRingBuffer) add(id uuid.UUID) (evicted uuid.UUID) {
	if r.count == ringBufferCapacity {
		// Buffer full — evict the oldest entry at head.
		evicted = r.slots[r.head]
		r.slots[r.head] = id
		r.head = (r.head + 1) % ringBufferCapacity
	} else {
		// Buffer not yet full — write at (head + count) % capacity.
		pos := (r.head + r.count) % ringBufferCapacity
		r.slots[pos] = id
		r.count++
	}
	return evicted
}
