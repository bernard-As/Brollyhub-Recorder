package recording

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/brollyhub/recording/internal/rtp"
	"github.com/brollyhub/recording/internal/storage"
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

	rtpWriter      *rtp.Writer
	segmentWriter  io.WriteCloser
	segmentStart   time.Time
	segmentIndex   int
	segmentBytes   int64
	segmentPackets int64
	segments       []TrackSegmentInfo
	startTime      time.Time
	lastPacket     time.Time
	endTime        *time.Time
	packetCount    int64
	totalBytes     int64

	closed bool
	logger *zap.Logger

	storage         storage.Storage
	roomID          string
	recordingID     string
	segmentDuration time.Duration
	segmentMaxBytes int64
	bufferSize      int
	flushInterval   time.Duration
}

// TrackWriterConfig holds configuration for a track writer
type TrackWriterConfig struct {
	TrackID         string
	ProducerID      string
	PeerID          string
	TrackType       TrackType
	Codec           string
	SSRC            uint32
	PayloadType     uint8
	BufferSize      int
	FlushInterval   time.Duration
	Storage         storage.Storage
	RoomID          string
	RecordingID     string
	SegmentDuration time.Duration
	SegmentMaxBytes int64
	Logger          *zap.Logger
}

// NewTrackWriter creates a new track writer
func NewTrackWriter(cfg TrackWriterConfig) (*TrackWriter, error) {
	if cfg.Storage == nil {
		return nil, fmt.Errorf("storage is required")
	}

	now := time.Now()

	t := &TrackWriter{
		trackID:         cfg.TrackID,
		producerID:      cfg.ProducerID,
		peerID:          cfg.PeerID,
		trackType:       cfg.TrackType,
		codec:           cfg.Codec,
		ssrc:            cfg.SSRC,
		payloadType:     cfg.PayloadType,
		startTime:       now,
		lastPacket:      now,
		logger:          cfg.Logger,
		storage:         cfg.Storage,
		roomID:          cfg.RoomID,
		recordingID:     cfg.RecordingID,
		segmentDuration: cfg.SegmentDuration,
		segmentMaxBytes: cfg.SegmentMaxBytes,
		bufferSize:      cfg.BufferSize,
		flushInterval:   cfg.FlushInterval,
	}

	if err := t.startNewSegment(now); err != nil {
		return nil, err
	}

	return t, nil
}

// WritePacket writes an RTP packet to the track
func (t *TrackWriter) WritePacket(data []byte, serverTimestamp int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return fmt.Errorf("track writer is closed")
	}

	if t.shouldRotate(int64(len(data))) {
		if err := t.rotateSegment(); err != nil {
			return err
		}
	}

	if err := t.rtpWriter.WritePacket(data, serverTimestamp); err != nil {
		return fmt.Errorf("failed to write RTP packet: %w", err)
	}

	t.lastPacket = time.Now()
	t.packetCount++
	t.totalBytes += int64(len(data))
	t.segmentPackets++
	t.segmentBytes += int64(len(data))

	return nil
}

// Flush flushes buffered data
func (t *TrackWriter) Flush() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.rtpWriter.Flush()
}

// Close closes the track writer and returns the buffered data
func (t *TrackWriter) Close() (TrackStats, error) {
	return t.CloseAt(time.Time{})
}

// CloseAt closes the track writer using a specific end time (or now if zero).
func (t *TrackWriter) CloseAt(endTime time.Time) (TrackStats, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return t.statsLocked(), nil
	}

	t.closed = true

	if err := t.closeSegmentAt(endTime); err != nil {
		return TrackStats{}, err
	}

	if endTime.IsZero() {
		now := time.Now()
		t.endTime = &now
	} else {
		t.endTime = &endTime
	}

	return t.statsLocked(), nil
}

// Stats returns track statistics
func (t *TrackWriter) Stats() TrackStats {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.statsLocked()
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

	segments := make([]TrackSegmentInfo, 0, len(t.segments))
	for _, segment := range t.segments {
		segments = append(segments, segment)
	}

	return TrackInfo{
		TrackID:     t.trackID,
		ProducerID:  t.producerID,
		PeerID:      t.peerID,
		TrackType:   t.trackType,
		Codec:       t.codec,
		SSRC:        t.ssrc,
		PayloadType: t.payloadType,
		StartTime:   t.startTime,
		EndTime:     t.endTime,
		Segments:    segments,
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
	Segments    []TrackSegmentInfo
}

// FileName generates the file name for this track
func (t *TrackWriter) FileName() string {
	return fmt.Sprintf("%s-%s", t.peerID, t.trackType.String())
}

// TrackSegmentInfo contains information about a single track segment.
type TrackSegmentInfo struct {
	FileName    string
	StartTime   time.Time
	EndTime     *time.Time
	Bytes       int64
	PacketCount int64
}

type countingWriteCloser struct {
	w io.WriteCloser
	n int64
}

func (c *countingWriteCloser) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func (c *countingWriteCloser) Close() error {
	return c.w.Close()
}

func (c *countingWriteCloser) Bytes() int64 {
	return c.n
}

func (t *TrackWriter) shouldRotate(nextPacketBytes int64) bool {
	if t.segmentDuration > 0 && time.Since(t.segmentStart) >= t.segmentDuration {
		return true
	}
	if t.segmentMaxBytes > 0 && (t.segmentBytes+nextPacketBytes) >= t.segmentMaxBytes {
		return true
	}
	return false
}

func (t *TrackWriter) startNewSegment(now time.Time) error {
	t.segmentIndex++
	segmentTrackID := fmt.Sprintf("%s-%06d", t.trackID, t.segmentIndex)
	writer, err := t.storage.GetTrackWriter(context.Background(), t.roomID, t.recordingID, segmentTrackID)
	if err != nil {
		return fmt.Errorf("failed to create segment writer: %w", err)
	}

	countingWriter := &countingWriteCloser{w: writer}
	rtpWriter, err := rtp.NewWriter(rtp.WriterConfig{
		SSRC:          t.ssrc,
		PayloadType:   t.payloadType,
		Codec:         t.codec,
		StartTime:     now,
		BufferSize:    t.bufferSize,
		FlushInterval: t.flushInterval,
		Writer:        countingWriter,
	})
	if err != nil {
		writer.Close()
		return fmt.Errorf("failed to create RTP writer: %w", err)
	}

	t.rtpWriter = rtpWriter
	t.segmentWriter = countingWriter
	t.segmentStart = now
	t.segmentBytes = 0
	t.segmentPackets = 0

	segmentFileName := fmt.Sprintf("%s.rtp", segmentTrackID)
	t.segments = append(t.segments, TrackSegmentInfo{
		FileName:  segmentFileName,
		StartTime: now,
	})

	return nil
}

func (t *TrackWriter) closeSegment() error {
	return t.closeSegmentAt(time.Time{})
}

func (t *TrackWriter) closeSegmentAt(endTime time.Time) error {
	if t.rtpWriter == nil || t.segmentWriter == nil {
		return nil
	}

	if err := t.rtpWriter.Close(); err != nil {
		return fmt.Errorf("failed to close RTP writer: %w", err)
	}

	if err := t.segmentWriter.Close(); err != nil {
		return fmt.Errorf("failed to close segment writer: %w", err)
	}

	if endTime.IsZero() {
		endTime = time.Now()
	}
	lastIndex := len(t.segments) - 1
	if lastIndex >= 0 {
		if cw, ok := t.segmentWriter.(*countingWriteCloser); ok {
			t.segmentBytes = cw.Bytes()
		}
		t.segments[lastIndex].EndTime = &endTime
		t.segments[lastIndex].Bytes = t.segmentBytes
		t.segments[lastIndex].PacketCount = t.segmentPackets
	}

	t.rtpWriter = nil
	t.segmentWriter = nil
	return nil
}

func (t *TrackWriter) rotateSegment() error {
	if err := t.closeSegment(); err != nil {
		return err
	}
	return t.startNewSegment(time.Now())
}

// RotateAt closes the current segment at the provided time and starts a new segment.
func (t *TrackWriter) RotateAt(cutoff time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}

	if cutoff.IsZero() {
		cutoff = time.Now()
	}
	if cutoff.Before(t.segmentStart) {
		cutoff = t.segmentStart
	}

	if err := t.closeSegmentAt(cutoff); err != nil {
		return err
	}

	return t.startNewSegment(cutoff)
}

func (t *TrackWriter) statsLocked() TrackStats {
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
