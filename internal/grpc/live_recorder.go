package grpc

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/brollyhub/recording/internal/compositer"
	"github.com/brollyhub/recording/internal/recording"
	"github.com/brollyhub/recording/internal/shelves"
	pb "github.com/brollyhub/recording/proto"
	"go.uber.org/zap"
)

type liveSession struct {
	mu                sync.RWMutex
	roomID            string
	recordingID       string
	startedAt         time.Time
	policy            recording.Policy
	quickPartDuration time.Duration
	nextQuickIndex    int32
	quickParts        []shelves.RecordingPart
	cancel            context.CancelFunc
}

func (s *Server) startLiveRecorder(rec *recording.RoomRecording) {
	if rec == nil {
		return
	}

	policy := rec.PolicySnapshot()
	if !policy.AutoRecordQuickAccess && !policy.AutoRecordLateComposite {
		return
	}

	if s.compositerClient == nil || !s.compositerClient.Enabled() {
		s.logger.Warn("Live recorder disabled: compositer client not configured",
			zap.String("room_id", rec.RoomID()),
			zap.String("recording_id", rec.RecordingID()))
		return
	}

	if !policy.AutoRecordQuickAccess {
		if err := rec.Snapshot("recording", time.Now()); err != nil {
			s.logger.Warn("Failed to write live recording snapshot",
				zap.String("room_id", rec.RoomID()),
				zap.String("recording_id", rec.RecordingID()),
				zap.Error(err))
		}
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	session := &liveSession{
		roomID:      rec.RoomID(),
		recordingID: rec.RecordingID(),
		startedAt:   rec.StartTime(),
		policy:      policy,
		cancel:      cancel,
	}

	if policy.AutoRecordQuickAccess && policy.QuickAccessPartDurationSec > 0 {
		session.quickPartDuration = time.Duration(policy.QuickAccessPartDurationSec) * time.Second
		session.nextQuickIndex = 1
	}

	s.liveMu.Lock()
	if existing := s.liveSessions[rec.RoomID()]; existing != nil && existing.cancel != nil {
		existing.cancel()
	}
	s.liveSessions[rec.RoomID()] = session
	s.liveMu.Unlock()

	go s.runLiveRecorder(ctx, rec, session)
}

func (s *Server) stopLiveRecorder(roomID string) *liveSession {
	s.liveMu.Lock()
	session := s.liveSessions[roomID]
	if session != nil {
		delete(s.liveSessions, roomID)
	}
	s.liveMu.Unlock()

	if session != nil && session.cancel != nil {
		session.cancel()
	}

	return session
}

func (s *Server) runLiveRecorder(ctx context.Context, rec *recording.RoomRecording, session *liveSession) {
	if rec == nil || session == nil {
		return
	}

	if err := rec.Snapshot("recording", time.Now()); err != nil {
		s.logger.Warn("Failed to write live recording snapshot",
			zap.String("room_id", session.roomID),
			zap.String("recording_id", session.recordingID),
			zap.Error(err))
	}

	if !session.policy.AutoRecordQuickAccess || session.quickPartDuration <= 0 {
		return
	}

	for {
		partEnd := session.startedAt.Add(time.Duration(session.nextQuickIndex) * session.quickPartDuration)
		wait := time.Until(partEnd)
		if wait < 0 {
			wait = 0
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			s.processQuickAccessBoundary(ctx, rec, session, partEnd)
		}
	}
}

func (s *Server) processQuickAccessBoundary(ctx context.Context, rec *recording.RoomRecording, session *liveSession, partEnd time.Time) {
	partStartOffset := time.Duration(session.nextQuickIndex-1) * session.quickPartDuration
	partEndOffset := time.Duration(session.nextQuickIndex) * session.quickPartDuration

	rec.RotateSegmentsAt(partEnd)
	if err := rec.Snapshot("recording", partEnd); err != nil {
		s.logger.Warn("Failed to write live recording snapshot",
			zap.String("room_id", session.roomID),
			zap.String("recording_id", session.recordingID),
			zap.Error(err))
	}

	part := buildPart(
		session.roomID,
		session.recordingID,
		session.nextQuickIndex,
		partStartOffset,
		partEndOffset,
		"quick_access",
		"Quick Access",
	)
	session.mu.Lock()
	session.quickParts = append(session.quickParts, part)
	quickPartsSnapshot := append([]shelves.RecordingPart(nil), session.quickParts...)
	session.mu.Unlock()

	s.logger.Info("Quick access part ready",
		zap.String("room_id", session.roomID),
		zap.String("recording_id", session.recordingID),
		zap.Int32("part_index", part.Index),
		zap.Int64("start_offset_ms", part.StartOffsetMs),
		zap.Int64("end_offset_ms", part.EndOffsetMs),
	)

	layout := session.policy.QuickAccessLayout.String()
	if layout == "unspecified" {
		layout = "grid"
	}

	if stats := rec.Stats(); stats.TrackCount > 0 {
		if err := s.queuePartExport(ctx, session.roomID, session.recordingID, part, layout, "quick_access"); err != nil {
			s.logger.Warn("Failed to queue live quick access export",
				zap.String("room_id", session.roomID),
				zap.String("recording_id", session.recordingID),
				zap.Int32("part_index", part.Index),
				zap.Error(err))
		}
	}

	s.notifyRoomRecording(
		session.roomID,
		session.recordingID,
		pb.RecordingStatus_RECORDING_STATUS_RECORDING,
		session.startedAt,
		time.Time{},
		quickPartsSnapshot,
		nil,
	)

	session.nextQuickIndex++
}

func (s *Server) queueFinalExports(
	roomID string,
	recordingID string,
	duration time.Duration,
	policy recording.Policy,
	liveSession *liveSession,
	quickParts []shelves.RecordingPart,
	lateParts []shelves.RecordingPart,
) {
	if s.compositerClient == nil || !s.compositerClient.Enabled() {
		return
	}

	if duration <= 0 {
		return
	}

	s.logger.Info("Queueing final recording exports",
		zap.String("room_id", roomID),
		zap.String("recording_id", recordingID),
		zap.Int("quick_parts", len(quickParts)),
		zap.Int("late_parts", len(lateParts)),
	)

	queuedQuick := map[int32]struct{}{}
	if liveSession != nil {
		liveSession.mu.RLock()
		partsSnapshot := append([]shelves.RecordingPart(nil), liveSession.quickParts...)
		liveSession.mu.RUnlock()
		for _, part := range partsSnapshot {
			queuedQuick[part.Index] = struct{}{}
		}
	}

	layout := policy.QuickAccessLayout.String()
	if layout == "unspecified" {
		layout = "grid"
	}

	if policy.AutoRecordQuickAccess {
		for _, part := range quickParts {
			if _, ok := queuedQuick[part.Index]; ok {
				continue
			}
			if err := s.queuePartExport(context.Background(), roomID, recordingID, part, layout, "quick_access"); err != nil {
				s.logger.Warn("Failed to queue quick access export",
					zap.String("room_id", roomID),
					zap.String("recording_id", recordingID),
					zap.Int32("part_index", part.Index),
					zap.Error(err))
			}
		}
	}

	if policy.AutoRecordLateComposite {
		for _, part := range lateParts {
			if err := s.queuePartExport(context.Background(), roomID, recordingID, part, "grid", "late_composite"); err != nil {
				s.logger.Warn("Failed to queue late composite export",
					zap.String("room_id", roomID),
					zap.String("recording_id", recordingID),
					zap.Int32("part_index", part.Index),
					zap.Error(err))
			}
		}
	}
}

func (s *Server) queuePartExport(ctx context.Context, roomID, recordingID string, part shelves.RecordingPart, layout string, jobType string) error {
	if s.compositerClient == nil || !s.compositerClient.Enabled() {
		return nil
	}

	startSec := float64(part.StartOffsetMs) / 1000.0
	endSec := float64(part.EndOffsetMs) / 1000.0

	payload := compositer.ExportRequest{
		JobType:      jobType,
		Format:       "mp4",
		StartTimeSec: startSec,
		EndTimeSec:   endSec,
		Layout:       layout,
		OutputKey:    part.Key,
	}

	return s.compositerClient.CreateExport(ctx, roomID, recordingID, payload)
}

func buildPart(
	roomID string,
	recordingID string,
	partIndex int32,
	startOffset time.Duration,
	endOffset time.Duration,
	kind string,
	labelPrefix string,
) shelves.RecordingPart {
	keyPrefix := "quick_access"
	if kind == "late_composite" {
		keyPrefix = "processed/exports"
	}
	key := fmt.Sprintf("rooms/%s/%s/%s/part-%04d.mp4", roomID, recordingID, keyPrefix, partIndex)

	return shelves.RecordingPart{
		Index:         partIndex,
		Key:           key,
		StartOffsetMs: startOffset.Milliseconds(),
		EndOffsetMs:   endOffset.Milliseconds(),
		DurationMs:    (endOffset - startOffset).Milliseconds(),
		Label:         fmt.Sprintf("%s Part %d", labelPrefix, partIndex),
	}
}
