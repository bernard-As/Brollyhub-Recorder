package grpc

import (
	"time"

	"github.com/brollyhub/recording/internal/recording"
	pb "github.com/brollyhub/recording/proto"
	"go.uber.org/zap"
)

// handleRegister handles SFU registration
func (s *Server) handleRegister(stream pb.RecordingSfuBridge_ConnectServer, req *pb.RegisterRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if another SFU is already connected
	if s.connectedSFU != nil {
		s.logger.Warn("Replacing existing SFU connection",
			zap.String("old_sfu_id", s.connectedSFU.ID),
			zap.String("new_sfu_id", req.SfuId))
	}

	// Store the connection
	s.connectedSFU = &SFUConnection{
		ID:     req.SfuId,
		Host:   req.SfuHost,
		Port:   req.SfuPort,
		Stream: stream,
	}

	s.logger.Info("SFU registered",
		zap.String("sfu_id", req.SfuId),
		zap.String("sfu_host", req.SfuHost),
		zap.Int32("sfu_port", req.SfuPort))

	// Start heartbeat
	s.startHeartbeat(stream)

	// Send response
	return stream.Send(&pb.RecordingToSfu{
		Message: &pb.RecordingToSfu_RegisterResponse{
			RegisterResponse: &pb.RegisterResponse{
				Success:            true,
				RecordingServiceId: s.serviceID,
				Message:            "Registration successful",
			},
		},
	})
}

// handleStartRecording handles recording start requests
func (s *Server) handleStartRecording(stream pb.RecordingSfuBridge_ConnectServer, req *pb.StartRecordingRequest) error {
	s.logger.Info("Start recording request",
		zap.String("room_id", req.RoomId),
		zap.String("requested_by", req.RequestedBy))

	// Convert policy from proto
	policy := recording.PolicyFromProto(req.Policy)

	// Start recording
	rec, err := s.manager.StartRecording(req.RoomId, req.RequestedBy, policy)
	if err != nil {
		s.logger.Error("Failed to start recording",
			zap.String("room_id", req.RoomId),
			zap.Error(err))

		return stream.Send(&pb.RecordingToSfu{
			Message: &pb.RecordingToSfu_StartRecordingResponse{
				StartRecordingResponse: &pb.StartRecordingResponse{
					Success: false,
					RoomId:  req.RoomId,
					Message: err.Error(),
				},
			},
		})
	}

	return stream.Send(&pb.RecordingToSfu{
		Message: &pb.RecordingToSfu_StartRecordingResponse{
			StartRecordingResponse: &pb.StartRecordingResponse{
				Success:     true,
				RecordingId: rec.RecordingID(),
				RoomId:      req.RoomId,
				Message:     "Recording started",
				StartedAt:   rec.StartTime().UnixMilli(),
			},
		},
	})
}

// handleStopRecording handles recording stop requests
func (s *Server) handleStopRecording(stream pb.RecordingSfuBridge_ConnectServer, req *pb.StopRecordingRequest) error {
	s.logger.Info("Stop recording request",
		zap.String("room_id", req.RoomId),
		zap.String("recording_id", req.RecordingId),
		zap.String("stopped_by", req.StoppedBy))

	// Get recording stats before stopping
	rec, exists := s.manager.GetRecording(req.RoomId)
	var stats recording.RecordingStats
	if exists {
		stats = rec.Stats()
	}

	// Stop recording
	err := s.manager.StopRecording(req.RoomId, req.StoppedBy)
	if err != nil {
		s.logger.Error("Failed to stop recording",
			zap.String("room_id", req.RoomId),
			zap.Error(err))

		return stream.Send(&pb.RecordingToSfu{
			Message: &pb.RecordingToSfu_StopRecordingResponse{
				StopRecordingResponse: &pb.StopRecordingResponse{
					Success:     false,
					RecordingId: req.RecordingId,
					RoomId:      req.RoomId,
					Message:     err.Error(),
				},
			},
		})
	}

	return stream.Send(&pb.RecordingToSfu{
		Message: &pb.RecordingToSfu_StopRecordingResponse{
			StopRecordingResponse: &pb.StopRecordingResponse{
				Success:     true,
				RecordingId: req.RecordingId,
				RoomId:      req.RoomId,
				Message:     "Recording stopped",
				StoppedAt:   time.Now().UnixMilli(),
				Stats: &pb.RecordingStats{
					DurationMs: stats.Duration.Milliseconds(),
					TotalBytes: stats.TotalBytes,
					TrackCount: int32(stats.TrackCount),
					PeerCount:  int32(stats.PeerCount),
				},
			},
		},
	})
}

// handleSubscribeTrack handles track subscription requests
func (s *Server) handleSubscribeTrack(stream pb.RecordingSfuBridge_ConnectServer, req *pb.SubscribeTrackRequest) error {
	s.logger.Debug("Subscribe track request",
		zap.String("room_id", req.RoomId),
		zap.String("producer_id", req.ProducerId),
		zap.String("peer_id", req.PeerId),
		zap.String("track_type", req.TrackType.String()))

	trackType := recording.TrackTypeFromProto(req.TrackType)

	err := s.manager.AddTrack(
		req.RoomId,
		req.ProducerId,
		req.PeerId,
		req.Codec,
		req.Ssrc,
		uint8(req.PayloadType),
		trackType,
	)
	if err != nil {
		s.logger.Error("Failed to subscribe track",
			zap.String("room_id", req.RoomId),
			zap.String("producer_id", req.ProducerId),
			zap.Error(err))
		return err
	}

	return nil
}

// handleUnsubscribeTrack handles track unsubscription requests
func (s *Server) handleUnsubscribeTrack(stream pb.RecordingSfuBridge_ConnectServer, req *pb.UnsubscribeTrackRequest) error {
	s.logger.Info("Unsubscribe track request",
		zap.String("room_id", req.RoomId),
		zap.String("producer_id", req.ProducerId))

	err := s.manager.RemoveTrack(req.RoomId, req.ProducerId)
	if err != nil {
		s.logger.Error("Failed to unsubscribe track",
			zap.String("room_id", req.RoomId),
			zap.String("producer_id", req.ProducerId),
			zap.Error(err))
		return err
	}

	s.logger.Info("Track unsubscribed successfully",
		zap.String("room_id", req.RoomId),
		zap.String("producer_id", req.ProducerId))

	return nil
}

// handleRtpPacket handles incoming RTP packets
func (s *Server) handleRtpPacket(pkt *pb.RtpPacket) error {
	// Write packet to recording - this is the hot path, minimize logging
	return s.manager.WritePacket(
		pkt.RoomId,
		pkt.ProducerId,
		pkt.RtpData,
		pkt.Timestamp,
	)
}

// handleRoomEvent handles room events for timeline tracking
func (s *Server) handleRoomEvent(event *pb.RoomEvent) error {
	switch e := event.Event.(type) {
	case *pb.RoomEvent_ProducerCreated:
		s.logger.Debug("Producer created event",
			zap.String("room_id", event.RoomId),
			zap.String("producer_id", e.ProducerCreated.ProducerId))

		trackType := recording.TrackTypeFromProto(e.ProducerCreated.TrackType)
		return s.manager.AddTrack(
			event.RoomId,
			e.ProducerCreated.ProducerId,
			e.ProducerCreated.PeerId,
			e.ProducerCreated.Codec,
			e.ProducerCreated.Ssrc,
			uint8(e.ProducerCreated.PayloadType),
			trackType,
		)

	case *pb.RoomEvent_ProducerClosed:
		s.logger.Info("Producer closed event",
			zap.String("room_id", event.RoomId),
			zap.String("producer_id", e.ProducerClosed.ProducerId),
			zap.String("peer_id", e.ProducerClosed.PeerId))
		err := s.manager.RemoveTrack(event.RoomId, e.ProducerClosed.ProducerId)
		if err != nil {
			s.logger.Error("Failed to remove track on producer close",
				zap.String("room_id", event.RoomId),
				zap.String("producer_id", e.ProducerClosed.ProducerId),
				zap.Error(err))
		}
		return err

	case *pb.RoomEvent_PeerJoined:
		s.logger.Info("Peer joined event",
			zap.String("room_id", event.RoomId),
			zap.String("peer_id", e.PeerJoined.PeerId),
			zap.String("display_name", e.PeerJoined.DisplayName))
		s.manager.AddParticipant(event.RoomId, e.PeerJoined.PeerId, e.PeerJoined.DisplayName)

	case *pb.RoomEvent_PeerLeft:
		s.logger.Info("Peer left event",
			zap.String("room_id", event.RoomId),
			zap.String("peer_id", e.PeerLeft.PeerId))
		s.manager.RemoveParticipant(event.RoomId, e.PeerLeft.PeerId)

	case *pb.RoomEvent_ActiveSpeaker:
		s.manager.SetActiveSpeaker(event.RoomId, e.ActiveSpeaker.PeerId)

	case *pb.RoomEvent_ProducerPaused:
		s.logger.Info("Producer paused event",
			zap.String("room_id", event.RoomId),
			zap.String("producer_id", e.ProducerPaused.ProducerId),
			zap.String("peer_id", e.ProducerPaused.PeerId))

	case *pb.RoomEvent_ProducerResumed:
		s.logger.Info("Producer resumed event",
			zap.String("room_id", event.RoomId),
			zap.String("producer_id", e.ProducerResumed.ProducerId),
			zap.String("peer_id", e.ProducerResumed.PeerId))

	default:
		s.logger.Warn("Unknown room event type")
	}

	return nil
}

// handleHeartbeat handles heartbeat messages
func (s *Server) handleHeartbeat(stream pb.RecordingSfuBridge_ConnectServer, hb *pb.Heartbeat) error {
	s.logger.Debug("Heartbeat received", zap.Int64("timestamp", hb.Timestamp))

	// Respond with our own heartbeat
	return stream.Send(&pb.RecordingToSfu{
		Message: &pb.RecordingToSfu_Heartbeat{
			Heartbeat: &pb.Heartbeat{
				Timestamp: time.Now().UnixMilli(),
			},
		},
	})
}
