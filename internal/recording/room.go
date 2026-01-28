package recording

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/brollyhub/recording/internal/storage"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// RoomRecording manages recording state for a single room
type RoomRecording struct {
	mu            sync.RWMutex
	roomID        string
	recordingID   string
	policy        *Policy
	startTime     time.Time
	requestedBy   string
	status        RecordingStatus
	tracks        map[string]*TrackWriter // keyed by producerID
	participants  map[string]*Participant
	timeline      *Timeline
	storage       storage.Storage
	storageCtx    context.Context
	storageCancel context.CancelFunc
	logger        *zap.Logger
	bufferSize    int
	flushInterval time.Duration
}

// RecordingStatus represents the current status of a recording
type RecordingStatus int

const (
	StatusPending RecordingStatus = iota
	StatusRecording
	StatusStopping
	StatusCompleted
	StatusFailed
)

func (s RecordingStatus) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusRecording:
		return "recording"
	case StatusStopping:
		return "stopping"
	case StatusCompleted:
		return "completed"
	case StatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Participant represents a room participant
type Participant struct {
	PeerID      string
	DisplayName string
	JoinedAt    time.Time
	LeftAt      *time.Time
}

// Timeline tracks events during recording
type Timeline struct {
	Events []TimelineEvent
}

// TimelineEvent represents a single event in the timeline
type TimelineEvent struct {
	Timestamp time.Time
	Type      string
	Data      map[string]interface{}
}

// RoomRecordingConfig holds configuration for room recording
type RoomRecordingConfig struct {
	RoomID        string
	Policy        *Policy
	RequestedBy   string
	Storage       storage.Storage
	Logger        *zap.Logger
	BufferSize    int
	FlushInterval time.Duration
}

// NewRoomRecording creates a new room recording session
func NewRoomRecording(cfg RoomRecordingConfig) (*RoomRecording, error) {
	if cfg.Storage == nil {
		return nil, fmt.Errorf("storage is required")
	}

	recordingID := uuid.New().String()
	ctx, cancel := context.WithCancel(context.Background())

	// Create recording in storage
	if err := cfg.Storage.CreateRecording(ctx, cfg.RoomID, recordingID); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create recording in storage: %w", err)
	}

	r := &RoomRecording{
		roomID:        cfg.RoomID,
		recordingID:   recordingID,
		policy:        cfg.Policy,
		startTime:     time.Now(),
		requestedBy:   cfg.RequestedBy,
		status:        StatusRecording,
		tracks:        make(map[string]*TrackWriter),
		participants:  make(map[string]*Participant),
		timeline:      &Timeline{Events: []TimelineEvent{}},
		storage:       cfg.Storage,
		storageCtx:    ctx,
		storageCancel: cancel,
		logger:        cfg.Logger,
		bufferSize:    cfg.BufferSize,
		flushInterval: cfg.FlushInterval,
	}

	// Write initial policy snapshot
	if err := r.writePolicySnapshot(); err != nil {
		r.logger.Warn("Failed to write policy snapshot", zap.Error(err))
	}

	r.addTimelineEvent("recording_started", map[string]interface{}{
		"requested_by": cfg.RequestedBy,
	})

	r.logger.Info("Room recording started",
		zap.String("room_id", cfg.RoomID),
		zap.String("recording_id", recordingID),
		zap.String("requested_by", cfg.RequestedBy))

	return r, nil
}

// RecordingID returns the recording ID
func (r *RoomRecording) RecordingID() string {
	return r.recordingID
}

// RoomID returns the room ID
func (r *RoomRecording) RoomID() string {
	return r.roomID
}

// Status returns the current recording status
func (r *RoomRecording) Status() RecordingStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

// StartTime returns when recording started
func (r *RoomRecording) StartTime() time.Time {
	return r.startTime
}

// AddTrack adds a new track to the recording
func (r *RoomRecording) AddTrack(producerID, peerID, codec string, ssrc uint32, payloadType uint8, trackType TrackType) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.status != StatusRecording {
		return fmt.Errorf("recording is not active")
	}

	// Check if track type should be recorded
	if !r.policy.ShouldRecordTrack(trackType) {
		r.logger.Debug("Track type not enabled for recording",
			zap.String("producer_id", producerID),
			zap.String("track_type", trackType.String()))
		return nil
	}

	// Check if track already exists
	if _, exists := r.tracks[producerID]; exists {
		return fmt.Errorf("track already exists: %s", producerID)
	}

	trackID := fmt.Sprintf("%s-%s", peerID, trackType.String())

	// Create a buffer for the track
	trackWriter, err := NewTrackWriter(TrackWriterConfig{
		TrackID:       trackID,
		ProducerID:    producerID,
		PeerID:        peerID,
		TrackType:     trackType,
		Codec:         codec,
		SSRC:          ssrc,
		PayloadType:   payloadType,
		BufferSize:    r.bufferSize,
		FlushInterval: r.flushInterval,
		Writer:        &discardWriter{}, // Will be replaced with actual storage writer
		Logger:        r.logger,
	})
	if err != nil {
		return fmt.Errorf("failed to create track writer: %w", err)
	}

	r.tracks[producerID] = trackWriter

	r.addTimelineEvent("track_added", map[string]interface{}{
		"producer_id":  producerID,
		"peer_id":      peerID,
		"track_type":   trackType.String(),
		"codec":        codec,
		"ssrc":         ssrc,
		"payload_type": payloadType,
	})

	r.logger.Info("Track added to recording",
		zap.String("room_id", r.roomID),
		zap.String("producer_id", producerID),
		zap.String("peer_id", peerID),
		zap.String("track_type", trackType.String()))

	return nil
}

// RemoveTrack removes a track from the recording
func (r *RoomRecording) RemoveTrack(producerID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	track, exists := r.tracks[producerID]
	if !exists {
		return nil // Track doesn't exist, nothing to remove
	}

	// Close the track and get the data
	data, err := track.Close()
	if err != nil {
		r.logger.Warn("Failed to close track writer",
			zap.String("producer_id", producerID),
			zap.Error(err))
	}

	// Write track data to storage
	if len(data) > 0 {
		trackID := track.FileName()
		if err := r.storage.WriteTrackData(r.storageCtx, r.roomID, r.recordingID, trackID, data); err != nil {
			r.logger.Error("Failed to write track data to storage",
				zap.String("producer_id", producerID),
				zap.Error(err))
		}
	}

	delete(r.tracks, producerID)

	stats := track.Stats()
	r.addTimelineEvent("track_removed", map[string]interface{}{
		"producer_id":  producerID,
		"peer_id":      stats.PeerID,
		"track_type":   stats.TrackType.String(),
		"packet_count": stats.PacketCount,
		"total_bytes":  stats.TotalBytes,
		"duration_ms":  stats.Duration.Milliseconds(),
	})

	r.logger.Info("Track removed from recording",
		zap.String("room_id", r.roomID),
		zap.String("producer_id", producerID),
		zap.Int64("packets", stats.PacketCount),
		zap.Int64("bytes", stats.TotalBytes))

	return nil
}

// WritePacket writes an RTP packet to the appropriate track
func (r *RoomRecording) WritePacket(producerID string, data []byte, serverTimestamp int64) error {
	r.mu.RLock()
	track, exists := r.tracks[producerID]
	status := r.status
	r.mu.RUnlock()

	if status != StatusRecording {
		return nil // Silently drop packets when not recording
	}

	if !exists {
		return nil // Track not found, might not be subscribed yet
	}

	return track.WritePacket(data, serverTimestamp)
}

// AddParticipant adds a participant to the recording
func (r *RoomRecording) AddParticipant(peerID, displayName string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	r.participants[peerID] = &Participant{
		PeerID:      peerID,
		DisplayName: displayName,
		JoinedAt:    now,
	}

	r.addTimelineEvent("peer_joined", map[string]interface{}{
		"peer_id":      peerID,
		"display_name": displayName,
	})
}

// RemoveParticipant marks a participant as having left
func (r *RoomRecording) RemoveParticipant(peerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if p, exists := r.participants[peerID]; exists {
		now := time.Now()
		p.LeftAt = &now

		r.addTimelineEvent("peer_left", map[string]interface{}{
			"peer_id": peerID,
		})
	}
}

// SetActiveSpeaker records an active speaker change
func (r *RoomRecording) SetActiveSpeaker(peerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.addTimelineEvent("active_speaker", map[string]interface{}{
		"peer_id": peerID,
	})
}

// Stop stops the recording and finalizes all data
func (r *RoomRecording) Stop(stoppedBy string) error {
	r.mu.Lock()

	if r.status != StatusRecording {
		r.mu.Unlock()
		return fmt.Errorf("recording is not active")
	}

	r.status = StatusStopping
	r.mu.Unlock()

	r.addTimelineEvent("recording_stopped", map[string]interface{}{
		"stopped_by": stoppedBy,
	})

	// Close all tracks
	r.mu.Lock()
	for producerID, track := range r.tracks {
		data, err := track.Close()
		if err != nil {
			r.logger.Warn("Failed to close track", zap.String("producer_id", producerID), zap.Error(err))
			continue
		}

		if len(data) > 0 {
			trackID := track.FileName()
			if err := r.storage.WriteTrackData(r.storageCtx, r.roomID, r.recordingID, trackID, data); err != nil {
				r.logger.Error("Failed to write track data", zap.String("producer_id", producerID), zap.Error(err))
			}
		}
	}
	r.tracks = make(map[string]*TrackWriter)
	r.mu.Unlock()

	// Write metadata
	if err := r.writeMetadata(stoppedBy); err != nil {
		r.logger.Error("Failed to write metadata", zap.Error(err))
	}

	// Write timeline
	if err := r.writeTimeline(); err != nil {
		r.logger.Error("Failed to write timeline", zap.Error(err))
	}

	// Finalize recording
	if err := r.storage.FinalizeRecording(r.storageCtx, r.roomID, r.recordingID); err != nil {
		r.mu.Lock()
		r.status = StatusFailed
		r.mu.Unlock()
		return fmt.Errorf("failed to finalize recording: %w", err)
	}

	r.mu.Lock()
	r.status = StatusCompleted
	r.storageCancel()
	r.mu.Unlock()

	r.logger.Info("Recording completed",
		zap.String("room_id", r.roomID),
		zap.String("recording_id", r.recordingID),
		zap.Duration("duration", time.Since(r.startTime)))

	return nil
}

// Stats returns current recording statistics
func (r *RoomRecording) Stats() RecordingStats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var totalBytes int64
	for _, track := range r.tracks {
		stats := track.Stats()
		totalBytes += stats.TotalBytes
	}

	return RecordingStats{
		RecordingID: r.recordingID,
		RoomID:      r.roomID,
		StartTime:   r.startTime,
		Duration:    time.Since(r.startTime),
		TrackCount:  len(r.tracks),
		PeerCount:   len(r.participants),
		TotalBytes:  totalBytes,
		Status:      r.status,
	}
}

// RecordingStats contains statistics about a recording
type RecordingStats struct {
	RecordingID string
	RoomID      string
	StartTime   time.Time
	Duration    time.Duration
	TrackCount  int
	PeerCount   int
	TotalBytes  int64
	Status      RecordingStatus
}

func (r *RoomRecording) addTimelineEvent(eventType string, data map[string]interface{}) {
	r.timeline.Events = append(r.timeline.Events, TimelineEvent{
		Timestamp: time.Now(),
		Type:      eventType,
		Data:      data,
	})
}

func (r *RoomRecording) writePolicySnapshot() error {
	snapshot := &storage.PolicySnapshot{
		RecordingID:            r.recordingID,
		RoomID:                 r.roomID,
		Enabled:                r.policy.Enabled,
		WhoCanRecord:           r.policy.WhoCanRecord.String(),
		AutoRecord:             r.policy.AutoRecord,
		RecordAudio:            r.policy.RecordAudio,
		RecordVideo:            r.policy.RecordVideo,
		RecordScreenshare:      r.policy.RecordScreenshare,
		WhoCanAccessRecordings: r.policy.WhoCanAccessRecordings.String(),
		AllowedAccessorIds:     r.policy.AllowedAccessorIds,
		SnapshotTime:           r.startTime,
	}

	return r.storage.WritePolicy(r.storageCtx, r.roomID, r.recordingID, snapshot)
}

func (r *RoomRecording) writeMetadata(stoppedBy string) error {
	now := time.Now()

	participants := make([]storage.ParticipantInfo, 0, len(r.participants))
	for _, p := range r.participants {
		participants = append(participants, storage.ParticipantInfo{
			PeerID:      p.PeerID,
			DisplayName: p.DisplayName,
			JoinedAt:    p.JoinedAt,
			LeftAt:      p.LeftAt,
		})
	}

	tracks := make([]storage.TrackInfo, 0)
	for _, t := range r.tracks {
		info := t.TrackInfo()
		tracks = append(tracks, storage.TrackInfo{
			TrackID:     info.TrackID,
			ProducerID:  info.ProducerID,
			PeerID:      info.PeerID,
			Type:        info.TrackType.String(),
			Codec:       info.Codec,
			SSRC:        info.SSRC,
			PayloadType: info.PayloadType,
			StartTime:   info.StartTime,
			FileName:    t.FileName() + ".rtp",
		})
	}

	stats := r.Stats()
	metadata := &storage.RecordingMetadata{
		RecordingID:  r.recordingID,
		RoomID:       r.roomID,
		StartTime:    r.startTime,
		EndTime:      &now,
		Status:       "completed",
		Participants: participants,
		Tracks:       tracks,
		RequestedBy:  r.requestedBy,
		StoppedBy:    stoppedBy,
		Stats: &storage.RecordingStats{
			DurationMs: stats.Duration.Milliseconds(),
			TotalBytes: stats.TotalBytes,
			TrackCount: stats.TrackCount,
			PeerCount:  stats.PeerCount,
		},
	}

	return r.storage.WriteMetadata(r.storageCtx, r.roomID, r.recordingID, metadata)
}

func (r *RoomRecording) writeTimeline() error {
	events := make([]storage.TimelineEvent, len(r.timeline.Events))
	for i, e := range r.timeline.Events {
		events[i] = storage.TimelineEvent{
			Timestamp: e.Timestamp,
			Type:      e.Type,
			Data:      e.Data,
		}
	}

	timeline := &storage.Timeline{
		RecordingID: r.recordingID,
		RoomID:      r.roomID,
		Events:      events,
	}

	return r.storage.WriteTimeline(r.storageCtx, r.roomID, r.recordingID, timeline)
}

// discardWriter is a no-op writer for initial buffer
type discardWriter struct{}

func (d *discardWriter) Write(p []byte) (n int, err error) {
	return len(p), nil
}
