package storage

import (
	"context"
	"io"
	"time"
)

// Storage defines the interface for recording storage operations
type Storage interface {
	// CreateRecording initializes a new recording directory structure
	CreateRecording(ctx context.Context, roomID, recordingID string) error

	// WriteTrackData writes RTP track data to storage
	WriteTrackData(ctx context.Context, roomID, recordingID, trackID string, data []byte) error

	// AppendTrackData appends data to an existing track file
	AppendTrackData(ctx context.Context, roomID, recordingID, trackID string, data []byte) error

	// WriteMetadata writes room metadata JSON
	WriteMetadata(ctx context.Context, roomID, recordingID string, metadata *RecordingMetadata) error

	// WriteTimeline writes timeline events JSON
	WriteTimeline(ctx context.Context, roomID, recordingID string, timeline *Timeline) error

	// WritePolicy writes the recording policy snapshot
	WritePolicy(ctx context.Context, roomID, recordingID string, policy *PolicySnapshot) error

	// FinalizeRecording marks a recording as complete
	FinalizeRecording(ctx context.Context, roomID, recordingID string) error

	// GetTrackWriter returns an io.Writer for streaming track data
	GetTrackWriter(ctx context.Context, roomID, recordingID, trackID string) (io.WriteCloser, error)

	// Health checks storage connectivity
	Health(ctx context.Context) error
}

// RecordingMetadata contains information about a recording session
type RecordingMetadata struct {
	RecordingID string                 `json:"recording_id"`
	RoomID      string                 `json:"room_id"`
	StartTime   time.Time              `json:"start_time"`
	EndTime     *time.Time             `json:"end_time,omitempty"`
	Status      string                 `json:"status"` // "recording", "completed", "failed"
	Participants []ParticipantInfo     `json:"participants"`
	Tracks      []TrackInfo            `json:"tracks"`
	Stats       *RecordingStats        `json:"stats,omitempty"`
	RequestedBy string                 `json:"requested_by"`
	StoppedBy   string                 `json:"stopped_by,omitempty"`
}

// ParticipantInfo contains information about a participant
type ParticipantInfo struct {
	PeerID      string     `json:"peer_id"`
	DisplayName string     `json:"display_name"`
	JoinedAt    time.Time  `json:"joined_at"`
	LeftAt      *time.Time `json:"left_at,omitempty"`
}

// TrackInfo contains information about a recorded track
type TrackInfo struct {
	TrackID     string     `json:"track_id"`
	ProducerID  string     `json:"producer_id"`
	PeerID      string     `json:"peer_id"`
	Type        string     `json:"type"` // "audio", "video", "screenshare"
	Codec       string     `json:"codec"`
	SSRC        uint32     `json:"ssrc"`
	PayloadType uint8      `json:"payload_type"`
	StartTime   time.Time  `json:"start_time"`
	EndTime     *time.Time `json:"end_time,omitempty"`
	FileName    string     `json:"file_name"`
	Segments    []TrackSegmentInfo `json:"segments,omitempty"`
}

// TrackSegmentInfo contains information about a single track segment.
type TrackSegmentInfo struct {
	FileName    string     `json:"file_name"`
	StartTime   time.Time  `json:"start_time"`
	EndTime     *time.Time `json:"end_time,omitempty"`
	Bytes       int64      `json:"bytes"`
	PacketCount int64      `json:"packet_count"`
}

// RecordingStats contains recording statistics
type RecordingStats struct {
	DurationMs int64 `json:"duration_ms"`
	TotalBytes int64 `json:"total_bytes"`
	TrackCount int   `json:"track_count"`
	PeerCount  int   `json:"peer_count"`
}

// Timeline contains chronological events during recording
type Timeline struct {
	RecordingID string          `json:"recording_id"`
	RoomID      string          `json:"room_id"`
	Events      []TimelineEvent `json:"events"`
}

// TimelineEvent represents a single timeline event
type TimelineEvent struct {
	Timestamp time.Time              `json:"timestamp"`
	Type      string                 `json:"type"`
	Data      map[string]interface{} `json:"data"`
}

// PolicySnapshot contains the recording policy at start time
type PolicySnapshot struct {
	RecordingID            string    `json:"recording_id"`
	RoomID                 string    `json:"room_id"`
	Enabled                bool      `json:"enabled"`
	WhoCanRecord           string    `json:"who_can_record"`
	AutoRecord             bool      `json:"auto_record"`
	RecordAudio            bool      `json:"record_audio"`
	RecordVideo            bool      `json:"record_video"`
	RecordScreenshare      bool      `json:"record_screenshare"`
	WhoCanAccessRecordings string    `json:"who_can_access_recordings"`
	AllowedAccessorIds     []string  `json:"allowed_accessor_ids,omitempty"`
	SnapshotTime           time.Time `json:"snapshot_time"`
}
