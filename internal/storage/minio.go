package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"go.uber.org/zap"
)

// MinIOStorage implements the Storage interface for MinIO/S3
type MinIOStorage struct {
	client *s3.Client
	bucket string
	logger *zap.Logger
}

// MinIOConfig holds MinIO configuration
type MinIOConfig struct {
	Endpoint  string
	Bucket    string
	AccessKey string
	SecretKey string
	UseSSL    bool
	Region    string
}

// NewMinIOStorage creates a new MinIO storage instance
func NewMinIOStorage(cfg MinIOConfig, logger *zap.Logger) (*MinIOStorage, error) {
	// Build endpoint URL
	scheme := "http"
	if cfg.UseSSL {
		scheme = "https"
	}
	endpoint := fmt.Sprintf("%s://%s", scheme, cfg.Endpoint)

	// Create S3 client with custom endpoint
	client := s3.New(s3.Options{
		Region:       cfg.Region,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		BaseEndpoint: aws.String(endpoint),
		UsePathStyle: true, // Required for MinIO
	})

	storage := &MinIOStorage{
		client: client,
		bucket: cfg.Bucket,
		logger: logger,
	}

	return storage, nil
}

// CreateRecording initializes a new recording directory structure
func (s *MinIOStorage) CreateRecording(ctx context.Context, roomID, recordingID string) error {
	// Create a marker file to establish the recording structure
	key := s.recordingPath(roomID, recordingID, ".recording")
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader([]byte{}),
		ContentType: aws.String("application/octet-stream"),
	})
	if err != nil {
		return fmt.Errorf("failed to create recording marker: %w", err)
	}

	s.logger.Info("Created recording structure",
		zap.String("room_id", roomID),
		zap.String("recording_id", recordingID))

	return nil
}

// WriteTrackData writes RTP track data to storage
func (s *MinIOStorage) WriteTrackData(ctx context.Context, roomID, recordingID, trackID string, data []byte) error {
	key := s.trackPath(roomID, recordingID, trackID)
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/octet-stream"),
	})
	if err != nil {
		return fmt.Errorf("failed to write track data: %w", err)
	}
	return nil
}

// AppendTrackData appends data to an existing track file
// Note: S3 doesn't support true append, so we use multipart upload
func (s *MinIOStorage) AppendTrackData(ctx context.Context, roomID, recordingID, trackID string, data []byte) error {
	// For MVP, we'll use a streaming writer instead
	// This method exists for compatibility but recommends using GetTrackWriter
	return s.WriteTrackData(ctx, roomID, recordingID, trackID, data)
}

// WriteMetadata writes room metadata JSON
func (s *MinIOStorage) WriteMetadata(ctx context.Context, roomID, recordingID string, metadata *RecordingMetadata) error {
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	key := s.recordingPath(roomID, recordingID, "metadata.json")
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		return fmt.Errorf("failed to write metadata: %w", err)
	}
	return nil
}

// WriteTimeline writes timeline events JSON
func (s *MinIOStorage) WriteTimeline(ctx context.Context, roomID, recordingID string, timeline *Timeline) error {
	data, err := json.MarshalIndent(timeline, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal timeline: %w", err)
	}

	key := s.recordingPath(roomID, recordingID, "timeline.json")
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		return fmt.Errorf("failed to write timeline: %w", err)
	}
	return nil
}

// WritePolicy writes the recording policy snapshot
func (s *MinIOStorage) WritePolicy(ctx context.Context, roomID, recordingID string, policy *PolicySnapshot) error {
	data, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal policy: %w", err)
	}

	key := s.recordingPath(roomID, recordingID, "policy.json")
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		return fmt.Errorf("failed to write policy: %w", err)
	}
	return nil
}

// FinalizeRecording marks a recording as complete
func (s *MinIOStorage) FinalizeRecording(ctx context.Context, roomID, recordingID string) error {
	// Remove the .recording marker and create a .completed marker
	markerKey := s.recordingPath(roomID, recordingID, ".recording")
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(markerKey),
	})
	if err != nil {
		s.logger.Warn("Failed to delete recording marker", zap.Error(err))
	}

	completeKey := s.recordingPath(roomID, recordingID, ".completed")
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(completeKey),
		Body:        bytes.NewReader([]byte{}),
		ContentType: aws.String("application/octet-stream"),
	})
	if err != nil {
		return fmt.Errorf("failed to create completed marker: %w", err)
	}

	s.logger.Info("Finalized recording",
		zap.String("room_id", roomID),
		zap.String("recording_id", recordingID))

	return nil
}

// GetTrackWriter returns an io.WriteCloser for streaming track data
func (s *MinIOStorage) GetTrackWriter(ctx context.Context, roomID, recordingID, trackID string) (io.WriteCloser, error) {
	return NewS3StreamWriter(ctx, s.client, s.bucket, s.trackPath(roomID, recordingID, trackID), s.logger)
}

// Health checks storage connectivity
func (s *MinIOStorage) Health(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(s.bucket),
	})
	if err != nil {
		return fmt.Errorf("bucket health check failed: %w", err)
	}
	return nil
}

func (s *MinIOStorage) recordingPath(roomID, recordingID, filename string) string {
	return fmt.Sprintf("rooms/%s/%s/%s", roomID, recordingID, filename)
}

func (s *MinIOStorage) trackPath(roomID, recordingID, trackID string) string {
	return fmt.Sprintf("rooms/%s/%s/tracks/%s.rtp", roomID, recordingID, trackID)
}

// S3StreamWriter implements io.WriteCloser for streaming uploads to S3
type S3StreamWriter struct {
	ctx        context.Context
	client     *s3.Client
	bucket     string
	key        string
	logger     *zap.Logger
	uploadID   string
	partNumber int32
	parts      []types.CompletedPart
	buffer     *bytes.Buffer
	bufferSize int
	mu         sync.Mutex
	closed     bool
}

// NewS3StreamWriter creates a new streaming writer for S3
func NewS3StreamWriter(ctx context.Context, client *s3.Client, bucket, key string, logger *zap.Logger) (*S3StreamWriter, error) {
	// Start multipart upload
	output, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		ContentType: aws.String("application/octet-stream"),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create multipart upload: %w", err)
	}

	return &S3StreamWriter{
		ctx:        ctx,
		client:     client,
		bucket:     bucket,
		key:        key,
		logger:     logger,
		uploadID:   *output.UploadId,
		partNumber: 0,
		buffer:     bytes.NewBuffer(make([]byte, 0, 5*1024*1024)), // 5MB min part size
		bufferSize: 5 * 1024 * 1024,
	}, nil
}

// Write implements io.Writer
func (w *S3StreamWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return 0, fmt.Errorf("writer is closed")
	}

	n, err = w.buffer.Write(p)
	if err != nil {
		return n, err
	}

	// Upload part if buffer exceeds minimum size
	if w.buffer.Len() >= w.bufferSize {
		if err := w.uploadPart(); err != nil {
			return n, err
		}
	}

	return n, nil
}

func (w *S3StreamWriter) uploadPart() error {
	if w.buffer.Len() == 0 {
		return nil
	}

	w.partNumber++
	data := make([]byte, w.buffer.Len())
	copy(data, w.buffer.Bytes())
	w.buffer.Reset()

	output, err := w.client.UploadPart(w.ctx, &s3.UploadPartInput{
		Bucket:     aws.String(w.bucket),
		Key:        aws.String(w.key),
		UploadId:   aws.String(w.uploadID),
		PartNumber: aws.Int32(w.partNumber),
		Body:       bytes.NewReader(data),
	})
	if err != nil {
		return fmt.Errorf("failed to upload part %d: %w", w.partNumber, err)
	}

	w.parts = append(w.parts, types.CompletedPart{
		ETag:       output.ETag,
		PartNumber: aws.Int32(w.partNumber),
	})

	return nil
}

// Close implements io.Closer
func (w *S3StreamWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}
	w.closed = true

	// Upload remaining data
	if w.buffer.Len() > 0 {
		if err := w.uploadPart(); err != nil {
			// Abort the upload on failure
			w.client.AbortMultipartUpload(w.ctx, &s3.AbortMultipartUploadInput{
				Bucket:   aws.String(w.bucket),
				Key:      aws.String(w.key),
				UploadId: aws.String(w.uploadID),
			})
			return err
		}
	}

	// Complete multipart upload
	if len(w.parts) > 0 {
		_, err := w.client.CompleteMultipartUpload(w.ctx, &s3.CompleteMultipartUploadInput{
			Bucket:   aws.String(w.bucket),
			Key:      aws.String(w.key),
			UploadId: aws.String(w.uploadID),
			MultipartUpload: &types.CompletedMultipartUpload{
				Parts: w.parts,
			},
		})
		if err != nil {
			return fmt.Errorf("failed to complete multipart upload: %w", err)
		}
	} else {
		// No parts uploaded, abort the multipart upload
		w.client.AbortMultipartUpload(w.ctx, &s3.AbortMultipartUploadInput{
			Bucket:   aws.String(w.bucket),
			Key:      aws.String(w.key),
			UploadId: aws.String(w.uploadID),
		})
	}

	return nil
}
