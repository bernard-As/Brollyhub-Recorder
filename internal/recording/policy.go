package recording

import (
	"fmt"

	pb "github.com/brollyhub/recording/proto"
)

// Policy represents recording policy settings
type Policy struct {
	Enabled                      bool
	WhoCanRecord                 WhoCanRecord
	AutoRecord                   bool
	AutoRecordQuickAccess        bool
	AutoRecordLateComposite      bool
	RecordAudio                  bool
	RecordVideo                  bool
	RecordScreenshare            bool
	WhoCanAccessRecordings       AccessLevel
	AllowedAccessorIds           []string
	QuickAccessLayout            RecordingLayout
	QuickAccessPartDurationSec   int32
	LateCompositePartDurationSec int32
}

// AccessLevel defines post-meeting recording access
type AccessLevel int

const (
	AccessLevelUnspecified AccessLevel = iota
	AccessAll
	AccessHost
	AccessLoggedIn
	AccessSelected
)

// String returns the string representation of AccessLevel
func (a AccessLevel) String() string {
	switch a {
	case AccessAll:
		return "all"
	case AccessHost:
		return "host"
	case AccessLoggedIn:
		return "logged_in"
	case AccessSelected:
		return "selected"
	default:
		return "unspecified"
	}
}

// WhoCanRecord defines who has permission to record
type WhoCanRecord int

const (
	WhoCanRecordUnspecified WhoCanRecord = iota
	WhoCanRecordHostOnly
	WhoCanRecordCoHostAndHost
	WhoCanRecordAnyone
)

// Role represents a participant's role in the room
type Role int

const (
	RoleParticipant Role = iota
	RoleCoHost
	RoleHost
)

// FromProto converts a protobuf RecordingPolicy to internal Policy
func PolicyFromProto(p *pb.RecordingPolicy) *Policy {
	if p == nil {
		return DefaultPolicy()
	}

	return (&Policy{
		Enabled:                      p.Enabled,
		WhoCanRecord:                 WhoCanRecord(p.WhoCanRecord),
		AutoRecord:                   p.AutoRecord,
		AutoRecordQuickAccess:        p.AutoRecordQuickAccess,
		AutoRecordLateComposite:      p.AutoRecordLateComposite,
		RecordAudio:                  p.RecordAudio,
		RecordVideo:                  p.RecordVideo,
		RecordScreenshare:            p.RecordScreenshare,
		WhoCanAccessRecordings:       AccessLevel(p.WhoCanAccessRecordings),
		AllowedAccessorIds:           p.AllowedAccessorIds,
		QuickAccessLayout:            RecordingLayout(p.QuickAccessLayout),
		QuickAccessPartDurationSec:   p.QuickAccessPartDurationSec,
		LateCompositePartDurationSec: p.LateCompositePartDurationSec,
	}).withDefaults()
}

func (p *Policy) withDefaults() *Policy {
	if p.QuickAccessLayout == LayoutUnspecified {
		p.QuickAccessLayout = LayoutGrid
	}
	if p.QuickAccessPartDurationSec == 0 {
		p.QuickAccessPartDurationSec = 720
	}
	if p.LateCompositePartDurationSec == 0 {
		p.LateCompositePartDurationSec = 720
	}
	return p
}

// ToProto converts internal Policy to protobuf RecordingPolicy
func (p *Policy) ToProto() *pb.RecordingPolicy {
	return &pb.RecordingPolicy{
		Enabled:                      p.Enabled,
		WhoCanRecord:                 pb.WhoCanRecord(p.WhoCanRecord),
		AutoRecord:                   p.AutoRecord,
		AutoRecordQuickAccess:        p.AutoRecordQuickAccess,
		AutoRecordLateComposite:      p.AutoRecordLateComposite,
		RecordAudio:                  p.RecordAudio,
		RecordVideo:                  p.RecordVideo,
		RecordScreenshare:            p.RecordScreenshare,
		WhoCanAccessRecordings:       pb.AccessLevel(p.WhoCanAccessRecordings),
		AllowedAccessorIds:           p.AllowedAccessorIds,
		QuickAccessLayout:            pb.RecordingLayout(p.QuickAccessLayout),
		QuickAccessPartDurationSec:   p.QuickAccessPartDurationSec,
		LateCompositePartDurationSec: p.LateCompositePartDurationSec,
	}
}

// DefaultPolicy returns the default recording policy
func DefaultPolicy() *Policy {
	return (&Policy{
		Enabled:                      true,
		WhoCanRecord:                 WhoCanRecordHostOnly,
		AutoRecord:                   false,
		AutoRecordQuickAccess:        false,
		AutoRecordLateComposite:      false,
		RecordAudio:                  true,
		RecordVideo:                  true,
		RecordScreenshare:            true,
		QuickAccessLayout:            LayoutGrid,
		QuickAccessPartDurationSec:   720,
		LateCompositePartDurationSec: 720,
	}).withDefaults()
}

// RecordingLayout defines supported layout modes for quick access exports.
type RecordingLayout int

const (
	LayoutUnspecified RecordingLayout = iota
	LayoutGrid
)

func (l RecordingLayout) String() string {
	switch l {
	case LayoutGrid:
		return "grid"
	default:
		return "unspecified"
	}
}

// CanRecord checks if a peer with the given role can start recording
func (p *Policy) CanRecord(role Role) error {
	if !p.Enabled {
		return fmt.Errorf("recording is disabled for this room")
	}

	switch p.WhoCanRecord {
	case WhoCanRecordHostOnly:
		if role != RoleHost {
			return fmt.Errorf("only hosts can record")
		}
	case WhoCanRecordCoHostAndHost:
		if role != RoleHost && role != RoleCoHost {
			return fmt.Errorf("only hosts and co-hosts can record")
		}
	case WhoCanRecordAnyone:
		// Anyone can record
	default:
		return fmt.Errorf("invalid recording policy")
	}

	return nil
}

// ShouldRecordTrack checks if a track type should be recorded
func (p *Policy) ShouldRecordTrack(trackType TrackType) bool {
	switch trackType {
	case TrackTypeAudio:
		return p.RecordAudio
	case TrackTypeVideo:
		return p.RecordVideo
	case TrackTypeScreenshare:
		return p.RecordScreenshare
	default:
		return false
	}
}

// TrackType represents the type of media track
type TrackType int

const (
	TrackTypeUnspecified TrackType = iota
	TrackTypeAudio
	TrackTypeVideo
	TrackTypeScreenshare
)

// TrackTypeFromProto converts protobuf TrackType to internal TrackType
func TrackTypeFromProto(t pb.TrackType) TrackType {
	switch t {
	case pb.TrackType_AUDIO:
		return TrackTypeAudio
	case pb.TrackType_VIDEO:
		return TrackTypeVideo
	case pb.TrackType_SCREENSHARE:
		return TrackTypeScreenshare
	default:
		return TrackTypeUnspecified
	}
}

// ToProto converts internal TrackType to protobuf
func (t TrackType) ToProto() pb.TrackType {
	switch t {
	case TrackTypeAudio:
		return pb.TrackType_AUDIO
	case TrackTypeVideo:
		return pb.TrackType_VIDEO
	case TrackTypeScreenshare:
		return pb.TrackType_SCREENSHARE
	default:
		return pb.TrackType_TRACK_TYPE_UNSPECIFIED
	}
}

// String returns the string representation of TrackType
func (t TrackType) String() string {
	switch t {
	case TrackTypeAudio:
		return "audio"
	case TrackTypeVideo:
		return "video"
	case TrackTypeScreenshare:
		return "screenshare"
	default:
		return "unknown"
	}
}

// WhoCanRecordString returns the string representation
func (w WhoCanRecord) String() string {
	switch w {
	case WhoCanRecordHostOnly:
		return "host_only"
	case WhoCanRecordCoHostAndHost:
		return "co_host_and_host"
	case WhoCanRecordAnyone:
		return "anyone"
	default:
		return "unspecified"
	}
}
