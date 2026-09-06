package console

// ringBuffer keeps the most recent console bytes for scrollback.
type ringBuffer struct {
	data  []byte
	start int
	size  int
}

// newRingBuffer returns a buffer that holds up to capacity bytes.
func newRingBuffer(capacity int) *ringBuffer {
	if capacity <= 0 {
		capacity = 1
	}

	return &ringBuffer{data: make([]byte, capacity)}
}

// write appends a chunk and discards the oldest bytes that no longer fit.
func (r *ringBuffer) write(chunk []byte) {
	capacity := len(r.data)

	// Keep only the tail when the chunk exceeds capacity.
	if len(chunk) >= capacity {
		copy(r.data, chunk[len(chunk)-capacity:])
		r.start = 0
		r.size = capacity
		return
	}

	for _, value := range chunk {
		writeIndex := (r.start + r.size) % capacity
		r.data[writeIndex] = value
		if r.size < capacity {
			r.size++
		} else {
			r.start = (r.start + 1) % capacity
		}
	}
}

// snapshot returns the buffered bytes in order, oldest first.
func (r *ringBuffer) snapshot() []byte {
	history := make([]byte, r.size)
	for index := 0; index < r.size; index++ {
		history[index] = r.data[(r.start+index)%len(r.data)]
	}

	return history
}
