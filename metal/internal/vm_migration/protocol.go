package vmmigration

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// The disk stream runs over plain TCP on the trusted mesh. The target opens the
// connection and writes a length-prefixed JSON header. The source replies with
// one status byte: streamStatusOK is followed by the raw zfs stream, and
// streamStatusError is followed by a length-prefixed message.
const (
	streamStatusOK    byte   = 0
	streamStatusError byte   = 1
	maxFrameBytes     uint32 = 1 << 16
)

// streamHeader identifies the snapshot the target wants from the source.
type streamHeader struct {
	MigrationID      string `json:"migration_id"`
	VirtualMachineID string `json:"virtual_machine_id"`
	Sequence         int    `json:"sequence"`
	ResumeToken      string `json:"resume_token,omitempty"`
	ThroughputMiBps  int    `json:"throughput_mibps,omitempty"`
}

// writeFrame writes a length-prefixed payload.
func writeFrame(w io.Writer, payload []byte) error {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(payload)))
	if _, err := w.Write(length[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// readFrame reads a length-prefixed payload bounded by limit.
func readFrame(r io.Reader, limit uint32) ([]byte, error) {
	var length [4]byte
	if _, err := io.ReadFull(r, length[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(length[:])
	if size > limit {
		return nil, fmt.Errorf("frame of %d bytes exceeds the %d byte limit", size, limit)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// writeStreamHeader marshals and frames the request header.
func writeStreamHeader(w io.Writer, header streamHeader) error {
	payload, err := json.Marshal(header)
	if err != nil {
		return err
	}
	return writeFrame(w, payload)
}

// readStreamHeader reads and decodes the request header.
func readStreamHeader(r io.Reader) (streamHeader, error) {
	payload, err := readFrame(r, maxFrameBytes)
	if err != nil {
		return streamHeader{}, err
	}
	var header streamHeader
	if err := json.Unmarshal(payload, &header); err != nil {
		return streamHeader{}, fmt.Errorf("decode stream header: %w", err)
	}
	return header, nil
}

// statusWriter emits the OK status byte before the first stream byte. This lets
// the source send an error status instead when it fails before any data.
type statusWriter struct {
	writer  io.Writer
	started bool
}

func (s *statusWriter) Write(p []byte) (int, error) {
	if !s.started {
		if _, err := s.writer.Write([]byte{streamStatusOK}); err != nil {
			return 0, err
		}
		s.started = true
	}
	return s.writer.Write(p)
}
