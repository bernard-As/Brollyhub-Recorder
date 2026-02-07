package grpc

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/brollyhub/recording/internal/compositer"
	"github.com/brollyhub/recording/internal/config"
	"github.com/brollyhub/recording/internal/recording"
	"github.com/brollyhub/recording/internal/shelves"
	pb "github.com/brollyhub/recording/proto"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

// Server implements the gRPC recording service
type Server struct {
	pb.UnimplementedRecordingSfuBridgeServer

	mu               sync.RWMutex
	config           *config.GRPCConfig
	grpcServer       *grpc.Server
	manager          *recording.Manager
	logger           *zap.Logger
	serviceID        string
	connectedSFU     *SFUConnection
	shelvesClient    *shelves.Client
	compositerClient *compositer.Client
	heartbeatTicker  *time.Ticker
	liveMu           sync.Mutex
	liveSessions     map[string]*liveSession
	done             chan struct{}
}

// SFUConnection represents a connected SFU instance
type SFUConnection struct {
	ID     string
	Host   string
	Port   int32
	Stream pb.RecordingSfuBridge_ConnectServer
}

// ServerConfig holds server configuration
type ServerConfig struct {
	Config           *config.GRPCConfig
	Manager          *recording.Manager
	Logger           *zap.Logger
	ShelvesClient    *shelves.Client
	CompositerClient *compositer.Client
}

// NewServer creates a new gRPC server
func NewServer(cfg ServerConfig) *Server {
	return &Server{
		config:           cfg.Config,
		manager:          cfg.Manager,
		logger:           cfg.Logger,
		serviceID:        uuid.New().String(),
		shelvesClient:    cfg.ShelvesClient,
		compositerClient: cfg.CompositerClient,
		liveSessions:     make(map[string]*liveSession),
		done:             make(chan struct{}),
	}
}

// Start starts the gRPC server
func (s *Server) Start() error {
	listener, err := net.Listen("tcp", s.config.Address())
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.config.Address(), err)
	}

	// Create gRPC server with options following flare-server.js pattern
	opts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(s.config.MaxMessageSize),
		grpc.MaxSendMsgSize(s.config.MaxMessageSize),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    s.config.KeepaliveTime,
			Timeout: s.config.KeepaliveTimeout,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	}

	s.grpcServer = grpc.NewServer(opts...)
	pb.RegisterRecordingSfuBridgeServer(s.grpcServer, s)

	s.logger.Info("gRPC server starting",
		zap.String("address", s.config.Address()),
		zap.String("service_id", s.serviceID))

	return s.grpcServer.Serve(listener)
}

// Stop gracefully stops the gRPC server
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Info("Stopping gRPC server")

	// Signal done
	close(s.done)

	// Stop heartbeat ticker
	if s.heartbeatTicker != nil {
		s.heartbeatTicker.Stop()
	}

	// Gracefully stop server
	stopped := make(chan struct{})
	go func() {
		s.grpcServer.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
		s.logger.Info("gRPC server stopped gracefully")
	case <-ctx.Done():
		s.grpcServer.Stop()
		s.logger.Warn("gRPC server forced to stop")
	}

	return nil
}

// Connect implements the bidirectional streaming RPC
func (s *Server) Connect(stream pb.RecordingSfuBridge_ConnectServer) error {
	s.logger.Info("New SFU connection attempt")

	// Create error channel for async error handling
	errChan := make(chan error, 1)

	// Handle incoming messages
	go func() {
		for {
			msg, err := stream.Recv()
			if err == io.EOF {
				s.logger.Info("SFU stream closed")
				errChan <- nil
				return
			}
			if err != nil {
				s.logger.Error("Error receiving from SFU", zap.Error(err))
				errChan <- err
				return
			}

			if err := s.handleMessage(stream, msg); err != nil {
				s.logger.Error("Error handling message", zap.Error(err))
				// Send error to SFU but don't close stream
				s.sendError(stream, "HANDLER_ERROR", err.Error(), "", "")
			}
		}
	}()

	// Wait for stream to end
	err := <-errChan

	// Cleanup on disconnect
	s.handleDisconnect()

	return err
}

// handleMessage dispatches incoming messages to appropriate handlers
func (s *Server) handleMessage(stream pb.RecordingSfuBridge_ConnectServer, msg *pb.SfuToRecording) error {
	switch m := msg.Message.(type) {
	case *pb.SfuToRecording_Register:
		return s.handleRegister(stream, m.Register)

	case *pb.SfuToRecording_StartRecording:
		return s.handleStartRecording(stream, m.StartRecording)

	case *pb.SfuToRecording_StopRecording:
		return s.handleStopRecording(stream, m.StopRecording)

	case *pb.SfuToRecording_SubscribeTrack:
		return s.handleSubscribeTrack(stream, m.SubscribeTrack)

	case *pb.SfuToRecording_UnsubscribeTrack:
		return s.handleUnsubscribeTrack(stream, m.UnsubscribeTrack)

	case *pb.SfuToRecording_RtpPacket:
		return s.handleRtpPacket(m.RtpPacket)

	case *pb.SfuToRecording_RoomEvent:
		return s.handleRoomEvent(m.RoomEvent)

	case *pb.SfuToRecording_Heartbeat:
		return s.handleHeartbeat(stream, m.Heartbeat)

	default:
		s.logger.Warn("Unknown message type received")
		return nil
	}
}

// handleDisconnect handles SFU disconnection
func (s *Server) handleDisconnect() {
	s.mu.Lock()
	sfu := s.connectedSFU
	s.connectedSFU = nil
	s.mu.Unlock()

	if sfu != nil {
		s.logger.Info("SFU disconnected",
			zap.String("sfu_id", sfu.ID),
			zap.String("sfu_host", sfu.Host))

		// Stop all active recordings from this SFU
		for _, roomID := range s.manager.ListActiveRecordings() {
			rec, exists := s.manager.GetRecording(roomID)
			var startTime time.Time
			var recordingID string
			if exists {
				startTime = rec.StartTime()
				recordingID = rec.RecordingID()
			}
			s.stopLiveRecorder(roomID)
			if err := s.manager.StopRecording(roomID, "sfu_disconnected"); err != nil {
				s.logger.Warn("Failed to stop recording on disconnect",
					zap.String("room_id", roomID),
					zap.Error(err))
				continue
			}
			if recordingID != "" {
				s.notifyRoomRecording(roomID, recordingID, pb.RecordingStatus_RECORDING_STATUS_FAILED, startTime, time.Now(), nil, nil)
			}
		}
	}
}

// sendError sends an error message to the SFU
func (s *Server) sendError(stream pb.RecordingSfuBridge_ConnectServer, code, message, roomID, recordingID string) {
	err := stream.Send(&pb.RecordingToSfu{
		Message: &pb.RecordingToSfu_Error{
			Error: &pb.ErrorMessage{
				Code:        code,
				Message:     message,
				RoomId:      roomID,
				RecordingId: recordingID,
			},
		},
	})
	if err != nil {
		s.logger.Error("Failed to send error message", zap.Error(err))
	}
}

// startHeartbeat starts sending periodic heartbeats to the SFU
func (s *Server) startHeartbeat(stream pb.RecordingSfuBridge_ConnectServer) {
	s.heartbeatTicker = time.NewTicker(10 * time.Second)
	go func() {
		for {
			select {
			case <-s.heartbeatTicker.C:
				err := stream.Send(&pb.RecordingToSfu{
					Message: &pb.RecordingToSfu_Heartbeat{
						Heartbeat: &pb.Heartbeat{
							Timestamp: time.Now().UnixMilli(),
						},
					},
				})
				if err != nil {
					s.logger.Warn("Failed to send heartbeat", zap.Error(err))
					return
				}
			case <-s.done:
				return
			}
		}
	}()
}

// IsConnected returns whether an SFU is currently connected
func (s *Server) IsConnected() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connectedSFU != nil
}

// ServiceID returns the service ID
func (s *Server) ServiceID() string {
	return s.serviceID
}
