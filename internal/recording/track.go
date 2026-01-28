package recording

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/brollyhub/recording/internal/rtp"
	"go.uber.org/zap"
)

// TrackWriter handles writing RTP packets for a single track
type TrackWriter struct {
	mu          sync.Mutex
	trackID     string
	producerID  string
	peerID      string
	trackType   TrackType
	codec       string
	ssrc        uint32
	payloadType uint8

	rtpWriter   *rtp.Writer
	buffer      *bytes.Buffer
	startTime   time.Time
	lastPacket  time.Time
	packetCount int64
	totalBytes  int64

	closed bool
	logger *zap.Logger
}

// TrackWriterConfig holds configuration for a track writer
type TrackWriterConfig struct {
	TrackID       string
	ProducerID    string
	PeerID        string
	TrackType     TrackType
	Codec         string
	SSRC          uint32
	PayloadType   uint8
	BufferSize    int
	FlushInterval time.Duration
	Writer        io.Writer
	Logger        *zap.Logger
}

// NewTrackWriter creates a new track writer
func NewTrackWriter(cfg TrackWriterConfig) (*TrackWriter, error) {
	if cfg.Writer == nil {
		return nil, fmt.Errorf("writer is required")
	}

	now := time.Now()

	// Create a buffer to hold data before writing to storage
	buffer := bytes.NewBuffer(make([]byte, 0, cfg.BufferSize))

	// Create RTP writer
	rtpWriter, err := rtp.NewWriter(rtp.WriterConfig{
		SSRC:          cfg.SSRC,
		PayloadType:   cfg.PayloadType,
		Codec:         cfg.Codec,
		StartTime:     now,
		BufferSize:    cfg.BufferSize,
		FlushInterval: cfg.FlushInterval,
		Writer:        buffer,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create RTP writer: %w", err)
	}

	return &TrackWriter{
		trackID:     cfg.TrackID,
		producerID:  cfg.ProducerID,
		peerID:      cfg.PeerID,
		trackType:   cfg.TrackType,
		codec:       cfg.Codec,
		ssrc:        cfg.SSRC,
		payloadType: cfg.PayloadType,
		rtpWriter:   rtpWriter,
		buffer:      buffer,
		startTime:   now,
		lastPacket:  now,
		logger:      cfg.Logger,
	}, nil
}

// WritePacket writes an RTP packet to the track
func (t *TrackWriter) WritePacket(data []byte, serverTimestamp int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return fmt.Errorf("track writer is closed")
	}

	if err := t.rtpWriter.WritePacket(data, serverTimestamp); err != nil {
		return fmt.Errorf("failed to write RTP packet: %w", err)
	}

	t.lastPacket = time.Now()
	t.packetCount++
	t.totalBytes += int64(len(data))

	return nil
}

// Flush flushes buffered data
func (t *TrackWriter) Flush() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.rtpWriter.Flush()
}

// Close closes the track writer and returns the buffered data
func (t *TrackWriter) Close() ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil, nil
	}

	t.closed = true

	if err := t.rtpWriter.Close(); err != nil {
		return nil, fmt.Errorf("failed to close RTP writer: %w", err)
	}

	return t.buffer.Bytes(), nil
}

// Stats returns track statistics
func (t *TrackWriter) Stats() TrackStats {
	t.mu.Lock()
	defer t.mu.Unlock()

	duration := time.Duration(0)
	if !t.lastPacket.IsZero() {
		duration = t.lastPacket.Sub(t.startTime)
	}

	return TrackStats{
		TrackID:     t.trackID,
		ProducerID:  t.producerID,
		PeerID:      t.peerID,
		TrackType:   t.trackType,
		Codec:       t.codec,
		SSRC:        t.ssrc,
		StartTime:   t.startTime,
		LastPacket:  t.lastPacket,
		Duration:    duration,
		PacketCount: t.packetCount,
		TotalBytes:  t.totalBytes,
	}
}

// TrackStats contains statistics about a track
type TrackStats struct {
	TrackID     string
	ProducerID  string
	PeerID      string
	TrackType   TrackType
	Codec       string
	SSRC        uint32
	StartTime   time.Time
	LastPacket  time.Time
	Duration    time.Duration
	PacketCount int64
	TotalBytes  int64
}

// TrackInfo returns information about the track for metadata
func (t *TrackWriter) TrackInfo() TrackInfo {
	t.mu.Lock()
	defer t.mu.Unlock()

	return TrackInfo{
		TrackID:     t.trackID,
		ProducerID:  t.producerID,
		PeerID:      t.peerID,
		TrackType:   t.trackType,
		Codec:       t.codec,
		SSRC:        t.ssrc,
		PayloadType: t.payloadType,
		StartTime:   t.startTime,
	}
}

// TrackInfo contains information about a track
type TrackInfo struct {
	TrackID     string
	ProducerID  string
	PeerID      string
	TrackType   TrackType
	Codec       string
	SSRC        uint32
	PayloadType uint8
	StartTime   time.Time
	EndTime     *time.Time
}

// FileName generates the file name for this track
func (t *TrackWriter) FileName() string {
	return fmt.Sprintf("%s-%s", t.peerID, t.trackType.String())
}
