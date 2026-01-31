package shelves

import (
	"context"
	"fmt"
	"time"

	"github.com/brollyhub/recording/internal/config"
	pb "github.com/brollyhub/recording/proto"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	conn    *grpc.ClientConn
	client  pb.RecordingShelfBridgeClient
	timeout time.Duration
	logger  *zap.Logger
	enabled bool
}

type UpsertRequest struct {
	RoomID      string
	RecordingID string
	Status      pb.RecordingStatus
	StartedAt   int64
	CompletedAt int64
	S3Prefix    string
	MetadataKey string
	TimelineKey string
	ServiceID   string
}

func NewClient(cfg config.ShelvesConfig, logger *zap.Logger) (*Client, error) {
	if !cfg.Enabled {
		return &Client{enabled: false, logger: logger}, nil
	}

	address := cfg.Address()
	conn, err := grpc.Dial(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to shelves gRPC (%s): %w", address, err)
	}

	return &Client{
		conn:    conn,
		client:  pb.NewRecordingShelfBridgeClient(conn),
		timeout: cfg.Timeout,
		logger:  logger,
		enabled: true,
	}, nil
}

func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) UpsertRoomRecording(ctx context.Context, req UpsertRequest) error {
	if c == nil || !c.enabled {
		return nil
	}

	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok && c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	_, err := c.client.UpsertRoomRecording(ctx, &pb.UpsertRoomRecordingRequest{
		RoomId:      req.RoomID,
		RecordingId: req.RecordingID,
		Status:      req.Status,
		StartedAt:   req.StartedAt,
		CompletedAt: req.CompletedAt,
		S3Prefix:    req.S3Prefix,
		MetadataKey: req.MetadataKey,
		TimelineKey: req.TimelineKey,
		ServiceId:   req.ServiceID,
	})
	if err != nil {
		c.logger.Warn("Shelves recording upsert failed", zap.Error(err))
		return err
	}

	return nil
}
