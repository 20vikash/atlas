package console

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

const (
	// drainChunkBytes is the read size of the drain loop and the size of one
	// broadcast chunk.
	drainChunkBytes = 32 << 10

	// viewerBufferBytes is the output held for one viewer before it is dropped.
	viewerBufferBytes = 256 << 10

	// viewerQueueChunks is the queue depth for one viewer. It is one chunk more
	// than viewerBufferBytes holds, so a buffer smaller than one chunk still
	// queues a chunk instead of dropping the viewer on its first write.
	viewerQueueChunks = viewerBufferBytes/drainChunkBytes + 1

	// inputBufferBytes is the read size for viewer keystrokes.
	inputBufferBytes = 4 << 10

	// maxViewers caps the viewers on one console.
	maxViewers = 16

	// noSlaveRetryDelay is the pause before the drain loop reads the PTY again
	// while no slave is open.
	noSlaveRetryDelay = 20 * time.Millisecond

	// drainStopTimeout bounds the wait for the drain loop after the master closes.
	drainStopTimeout = time.Second
)

// console owns one VM's PTY master.
type console struct {
	master *os.File
	link   string

	writeMutex sync.Mutex

	mutex   sync.Mutex
	closed  bool
	ring    *ringBuffer
	viewers map[*viewer]struct{}

	drainDone chan struct{}
}

// viewer receives output for one attached client. Slow viewers are dropped.
type viewer struct {
	output  chan []byte
	dropped chan struct{}
}

// newConsole takes ownership of master and starts draining it.
func newConsole(master *os.File, link string, scrollbackBytes int) *console {
	c := &console{
		master:    master,
		link:      link,
		ring:      newRingBuffer(scrollbackBytes),
		viewers:   make(map[*viewer]struct{}),
		drainDone: make(chan struct{}),
	}
	go c.drain()

	return c
}

// attach streams the console to one client until it disconnects.
func (c *console) attach(ctx context.Context, client io.ReadWriter, resize <-chan Winsize) error {
	attached, history, err := c.addViewer()
	if err != nil {
		return err
	}
	defer c.removeViewer(attached)

	if len(history) > 0 {
		if _, err := client.Write(history); err != nil {
			return err
		}
	}

	attachContext, cancel := context.WithCancel(ctx)
	defer cancel()

	go c.copyInput(attachContext, client, cancel)

	for {
		select {
		case <-attachContext.Done():
			return nil
		case <-attached.dropped:
			return nil
		case size := <-resize:
			_ = pty.Setsize(c.master, &pty.Winsize{Rows: size.Rows, Cols: size.Cols})
		case chunk := <-attached.output:
			if _, err := client.Write(chunk); err != nil {
				return err
			}
		}
	}
}

// close releases the PTY master and removes the slave link.
func (c *console) close() error {
	c.mutex.Lock()
	if c.closed {
		c.mutex.Unlock()
		return nil
	}
	c.closed = true
	c.mutex.Unlock()

	err := c.master.Close()

	// Stop waiting if a blocked PTY read does not return.
	select {
	case <-c.drainDone:
	case <-time.After(drainStopTimeout):
	}

	if removeErr := os.Remove(c.link); removeErr != nil && !os.IsNotExist(removeErr) && err == nil {
		err = removeErr
	}

	return err
}

// drain records history and broadcasts output without blocking Firecracker.
// Linux returns EIO while no PTY slave is open, so the loop retries on EIO.
func (c *console) drain() {
	defer close(c.drainDone)

	buffer := make([]byte, drainChunkBytes)
	for {
		count, err := c.master.Read(buffer)
		if count > 0 {
			c.broadcast(buffer[:count])
		}
		if err == nil {
			continue
		}

		// After close, EIO means the master is gone rather than idle.
		if errors.Is(err, syscall.EIO) && !c.isClosed() {
			time.Sleep(noSlaveRetryDelay)
			continue
		}

		return
	}
}

// broadcast records a chunk in the scrollback and sends it to every viewer.
func (c *console) broadcast(data []byte) {
	chunk := make([]byte, len(data))
	copy(chunk, data)

	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.ring.write(chunk)
	for attached := range c.viewers {
		select {
		case attached.output <- chunk:
		default:
			// Drop slow viewers so the drain keeps running.
			delete(c.viewers, attached)
			close(attached.dropped)
		}
	}
}

// addViewer registers one viewer and returns the scrollback it receives first.
func (c *console) addViewer() (*viewer, []byte, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if c.closed {
		return nil, nil, ErrConsoleNotFound
	}
	if len(c.viewers) >= maxViewers {
		return nil, nil, ErrConsoleBusy
	}

	history := c.ring.snapshot()
	attached := &viewer{
		output:  make(chan []byte, viewerQueueChunks),
		dropped: make(chan struct{}),
	}
	c.viewers[attached] = struct{}{}

	return attached, history, nil
}

// removeViewer stops delivery to one viewer.
func (c *console) removeViewer(attached *viewer) {
	c.mutex.Lock()
	delete(c.viewers, attached)
	c.mutex.Unlock()
}

// copyInput forwards viewer input to the PTY master.
func (c *console) copyInput(ctx context.Context, client io.Reader, cancel context.CancelFunc) {
	defer cancel()

	buffer := make([]byte, inputBufferBytes)
	for {
		count, err := client.Read(buffer)
		if count > 0 {
			c.writeMaster(buffer[:count])
		}
		if err != nil {
			return
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// writeMaster serializes viewer input onto the shared PTY master.
func (c *console) writeMaster(data []byte) {
	c.writeMutex.Lock()
	defer c.writeMutex.Unlock()

	_, _ = c.master.Write(data)
}

// isClosed reports whether the console has released its PTY master.
func (c *console) isClosed() bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	return c.closed
}
