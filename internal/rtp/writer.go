package rtp

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"
)

const (
	// File format constants
	MagicBytes   = "BRTP"
	FormatVersion = 1
	HeaderSize   = 32
	CodecMaxLen  = 8
)

// FileHeader represents the custom RTP file header
type FileHeader struct {
	Magic       [4]byte  // "BRTP"
	Version     uint8    // Format version (1)
	PayloadType uint8    // RTP payload type
	Reserved    uint16   // Reserved for future use
	SSRC        uint32   // RTP SSRC
	Codec       [8]byte  // Codec name (e.g., "opus", "vp8")
	StartTime   int64    // Recording start time (Unix ms)
	Reserved2   [4]byte  // Padding to 32 bytes
}

// PacketRecord represents a single RTP packet record in the file
type PacketRecord struct {
	ServerTimestamp int64  // Server-side capture time (Unix ms)
	Length          uint16 // RTP packet length
	// Followed by RTP data bytes
}

// Writer handles buffered writing of RTP packets to a custom file format
type Writer struct {
	mu           sync.Mutex
	buffer       *bytes.Buffer
	writer       io.Writer
	header       *FileHeader
	headerWritten bool
	packetCount  int64
	totalBytes   int64
	flushChan    chan struct{}
	closeChan    chan struct{}
	closed       bool
}

// WriterConfig holds configuration for the RTP writer
type WriterConfig struct {
	SSRC          uint32
	PayloadType   uint8
	Codec         string
	StartTime     time.Time
	BufferSize    int
	FlushInterval time.Duration
	Writer        io.Writer
}

// NewWriter creates a new RTP file writer
func NewWriter(cfg WriterConfig) (*Writer, error) {
	if cfg.Writer == nil {
		return nil, fmt.Errorf("writer is required")
	}

	header := &FileHeader{
		Version:     FormatVersion,
		PayloadType: cfg.PayloadType,
		SSRC:        cfg.SSRC,
		StartTime:   cfg.StartTime.UnixMilli(),
	}
	copy(header.Magic[:], MagicBytes)

	// Copy codec name (truncate if too long)
	codec := cfg.Codec
	if len(codec) > CodecMaxLen {
		codec = codec[:CodecMaxLen]
	}
	copy(header.Codec[:], codec)

	bufSize := cfg.BufferSize
	if bufSize <= 0 {
		bufSize = 64 * 1024 // 64KB default
	}

	w := &Writer{
		buffer:    bytes.NewBuffer(make([]byte, 0, bufSize)),
		writer:    cfg.Writer,
		header:    header,
		flushChan: make(chan struct{}, 1),
		closeChan: make(chan struct{}),
	}

	// Start flush goroutine if interval specified
	if cfg.FlushInterval > 0 {
		go w.flushLoop(cfg.FlushInterval)
	}

	return w, nil
}

// WritePacket writes an RTP packet to the buffer
func (w *Writer) WritePacket(data []byte, serverTimestamp int64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return fmt.Errorf("writer is closed")
	}

	// Write header on first packet
	if !w.headerWritten {
		if err := w.writeHeader(); err != nil {
			return fmt.Errorf("failed to write header: %w", err)
		}
		w.headerWritten = true
	}

	// Write packet record
	record := PacketRecord{
		ServerTimestamp: serverTimestamp,
		Length:          uint16(len(data)),
	}

	if err := binary.Write(w.buffer, binary.BigEndian, record.ServerTimestamp); err != nil {
		return fmt.Errorf("failed to write timestamp: %w", err)
	}
	if err := binary.Write(w.buffer, binary.BigEndian, record.Length); err != nil {
		return fmt.Errorf("failed to write length: %w", err)
	}
	if _, err := w.buffer.Write(data); err != nil {
		return fmt.Errorf("failed to write rtp data: %w", err)
	}

	w.packetCount++
	w.totalBytes += int64(len(data)) + 10 // 8 bytes timestamp + 2 bytes length

	return nil
}

func (w *Writer) writeHeader() error {
	if err := binary.Write(w.buffer, binary.BigEndian, w.header.Magic); err != nil {
		return err
	}
	if err := binary.Write(w.buffer, binary.BigEndian, w.header.Version); err != nil {
		return err
	}
	if err := binary.Write(w.buffer, binary.BigEndian, w.header.PayloadType); err != nil {
		return err
	}
	if err := binary.Write(w.buffer, binary.BigEndian, w.header.Reserved); err != nil {
		return err
	}
	if err := binary.Write(w.buffer, binary.BigEndian, w.header.SSRC); err != nil {
		return err
	}
	if err := binary.Write(w.buffer, binary.BigEndian, w.header.Codec); err != nil {
		return err
	}
	if err := binary.Write(w.buffer, binary.BigEndian, w.header.StartTime); err != nil {
		return err
	}
	if err := binary.Write(w.buffer, binary.BigEndian, w.header.Reserved2); err != nil {
		return err
	}
	return nil
}

// Flush writes buffered data to the underlying writer
func (w *Writer) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.flushLocked()
}

func (w *Writer) flushLocked() error {
	if w.buffer.Len() == 0 {
		return nil
	}

	_, err := w.writer.Write(w.buffer.Bytes())
	if err != nil {
		return fmt.Errorf("failed to flush buffer: %w", err)
	}

	w.buffer.Reset()
	return nil
}

func (w *Writer) flushLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.Flush()
		case <-w.flushChan:
			w.Flush()
		case <-w.closeChan:
			return
		}
	}
}

// TriggerFlush triggers an immediate flush
func (w *Writer) TriggerFlush() {
	select {
	case w.flushChan <- struct{}{}:
	default:
	}
}

// Close flushes remaining data and closes the writer
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}

	w.closed = true
	close(w.closeChan)

	return w.flushLocked()
}

// Stats returns current writer statistics
func (w *Writer) Stats() (packetCount, totalBytes int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.packetCount, w.totalBytes
}

// ReadHeader reads and validates a file header from a reader
func ReadHeader(r io.Reader) (*FileHeader, error) {
	header := &FileHeader{}

	if err := binary.Read(r, binary.BigEndian, header); err != nil {
		return nil, fmt.Errorf("failed to read header: %w", err)
	}

	if string(header.Magic[:]) != MagicBytes {
		return nil, fmt.Errorf("invalid magic bytes: %s", string(header.Magic[:]))
	}

	if header.Version != FormatVersion {
		return nil, fmt.Errorf("unsupported format version: %d", header.Version)
	}

	return header, nil
}

// ReadPacket reads a single packet record from a reader
func ReadPacket(r io.Reader) (data []byte, serverTimestamp int64, err error) {
	var ts int64
	var length uint16

	if err := binary.Read(r, binary.BigEndian, &ts); err != nil {
		return nil, 0, err
	}
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return nil, 0, err
	}

	data = make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, 0, err
	}

	return data, ts, nil
}
