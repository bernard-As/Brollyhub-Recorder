package recording

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/brollyhub/recording/internal/storage"
	"go.uber.org/zap"
)

// Manager coordinates all recording sessions
type Manager struct {
	mu            sync.RWMutex
	rooms         map[string]*RoomRecording // keyed by roomID
	storage       storage.Storage
	logger        *zap.Logger
	bufferSize    int
	flushInterval time.Duration
	segmentDuration time.Duration
	segmentMaxBytes int64
}

// ManagerConfig holds configuration for the recording manager
type ManagerConfig struct {
	Storage       storage.Storage
	Logger        *zap.Logger
	BufferSize    int
	FlushInterval time.Duration
	SegmentDuration time.Duration
	SegmentMaxBytes int64
}

// NewManager creates a new recording manager
func NewManager(cfg ManagerConfig) *Manager {
	return &Manager{
		rooms:         make(map[string]*RoomRecording),
		storage:       cfg.Storage,
		logger:        cfg.Logger,
		bufferSize:    cfg.BufferSize,
		flushInterval: cfg.FlushInterval,
		segmentDuration: cfg.SegmentDuration,
		segmentMaxBytes: cfg.SegmentMaxBytes,
	}
}

// StartRecording starts recording for a room
func (m *Manager) StartRecording(roomID, requestedBy string, policy *Policy) (*RoomRecording, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if recording already exists
	if _, exists := m.rooms[roomID]; exists {
		return nil, fmt.Errorf("recording already active for room: %s", roomID)
	}

	// Create new room recording
	recording, err := NewRoomRecording(RoomRecordingConfig{
		RoomID:        roomID,
		Policy:        policy,
		RequestedBy:   requestedBy,
		Storage:       m.storage,
		Logger:        m.logger.With(zap.String("room_id", roomID)),
		BufferSize:    m.bufferSize,
		FlushInterval: m.flushInterval,
		SegmentDuration: m.segmentDuration,
		SegmentMaxBytes: m.segmentMaxBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create room recording: %w", err)
	}

	m.rooms[roomID] = recording

	m.logger.Info("Recording started",
		zap.String("room_id", roomID),
		zap.String("recording_id", recording.RecordingID()),
		zap.String("requested_by", requestedBy))

	return recording, nil
}

// StopRecording stops recording for a room
func (m *Manager) StopRecording(roomID, stoppedBy string) error {
	m.mu.Lock()
	recording, exists := m.rooms[roomID]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("no active recording for room: %s", roomID)
	}
	delete(m.rooms, roomID)
	m.mu.Unlock()

	if err := recording.Stop(stoppedBy); err != nil {
		return fmt.Errorf("failed to stop recording: %w", err)
	}

	m.logger.Info("Recording stopped",
		zap.String("room_id", roomID),
		zap.String("recording_id", recording.RecordingID()),
		zap.String("stopped_by", stoppedBy))

	return nil
}

// GetRecording returns the active recording for a room
func (m *Manager) GetRecording(roomID string) (*RoomRecording, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	recording, exists := m.rooms[roomID]
	return recording, exists
}

// HasActiveRecording checks if a room has an active recording
func (m *Manager) HasActiveRecording(roomID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, exists := m.rooms[roomID]
	return exists
}

// HasTrack checks if a producer track exists for a room recording.
func (m *Manager) HasTrack(roomID, producerID string) bool {
	recording, exists := m.GetRecording(roomID)
	if !exists {
		return false
	}

	return recording.HasTrack(producerID)
}

// AddTrack adds a track to a room's recording
func (m *Manager) AddTrack(roomID, producerID, peerID, codec string, ssrc uint32, payloadType uint8, trackType TrackType, eventTime time.Time) error {
	recording, exists := m.GetRecording(roomID)
	if !exists {
		return nil // No active recording, silently ignore
	}

	return recording.AddTrack(producerID, peerID, codec, ssrc, payloadType, trackType, eventTime)
}

// RemoveTrack removes a track from a room's recording
func (m *Manager) RemoveTrack(roomID, producerID, peerID string, trackType TrackType, eventTime time.Time) error {
	recording, exists := m.GetRecording(roomID)
	if !exists {
		m.logger.Warn("RemoveTrack called but no active recording",
			zap.String("room_id", roomID),
			zap.String("producer_id", producerID))
		return nil
	}

	return recording.RemoveTrack(producerID, peerID, trackType, eventTime)
}

// WritePacket writes an RTP packet to the appropriate track
func (m *Manager) WritePacket(roomID, producerID string, data []byte, serverTimestamp int64) error {
	recording, exists := m.GetRecording(roomID)
	if !exists {
		return nil // No active recording, silently drop
	}

	return recording.WritePacket(producerID, data, serverTimestamp)
}

// AddParticipant adds a participant to a room's recording
func (m *Manager) AddParticipant(roomID, peerID, displayName string, eventTime time.Time) {
	recording, exists := m.GetRecording(roomID)
	if !exists {
		return // No active recording, silently ignore
	}

	recording.AddParticipant(peerID, displayName, eventTime)
}

// RemoveParticipant marks a participant as having left
func (m *Manager) RemoveParticipant(roomID, peerID string, eventTime time.Time) {
	recording, exists := m.GetRecording(roomID)
	if !exists {
		return // No active recording, silently ignore
	}

	recording.RemoveParticipant(peerID, eventTime)
}

// SetActiveSpeaker records an active speaker change
func (m *Manager) SetActiveSpeaker(roomID, peerID string, eventTime time.Time) {
	recording, exists := m.GetRecording(roomID)
	if !exists {
		return // No active recording, silently ignore
	}

	recording.SetActiveSpeaker(peerID, eventTime)
}

// RecordProducerPaused records a paused event for timeline.
func (m *Manager) RecordProducerPaused(roomID, peerID, producerID string, trackType TrackType, eventTime time.Time) {
	recording, exists := m.GetRecording(roomID)
	if !exists {
		return
	}

	recording.RecordProducerPaused(peerID, producerID, trackType, eventTime)
}

// RecordProducerResumed records a resumed event for timeline.
func (m *Manager) RecordProducerResumed(roomID, peerID, producerID string, trackType TrackType, eventTime time.Time) {
	recording, exists := m.GetRecording(roomID)
	if !exists {
		return
	}

	recording.RecordProducerResumed(peerID, producerID, trackType, eventTime)
}

// ActiveRecordings returns the number of active recordings
func (m *Manager) ActiveRecordings() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.rooms)
}

// ListActiveRecordings returns a list of room IDs with active recordings
func (m *Manager) ListActiveRecordings() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	roomIDs := make([]string, 0, len(m.rooms))
	for roomID := range m.rooms {
		roomIDs = append(roomIDs, roomID)
	}
	return roomIDs
}

// GetStats returns stats for all active recordings
func (m *Manager) GetStats() []RecordingStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := make([]RecordingStats, 0, len(m.rooms))
	for _, recording := range m.rooms {
		stats = append(stats, recording.Stats())
	}
	return stats
}

// Shutdown gracefully stops all recordings
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	rooms := make([]*RoomRecording, 0, len(m.rooms))
	for _, recording := range m.rooms {
		rooms = append(rooms, recording)
	}
	m.rooms = make(map[string]*RoomRecording)
	m.mu.Unlock()

	m.logger.Info("Shutting down recording manager", zap.Int("active_recordings", len(rooms)))

	var lastErr error
	for _, recording := range rooms {
		if err := recording.Stop("system_shutdown"); err != nil {
			m.logger.Error("Failed to stop recording during shutdown",
				zap.String("room_id", recording.RoomID()),
				zap.Error(err))
			lastErr = err
		}
	}

	return lastErr
}

// Health checks the health of the recording manager
func (m *Manager) Health(ctx context.Context) error {
	return m.storage.Health(ctx)
}
