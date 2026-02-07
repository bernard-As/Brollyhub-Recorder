package compositer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/brollyhub/recording/internal/config"
	"go.uber.org/zap"
)

type Client struct {
	baseURL    string
	serviceKey string
	timeout    time.Duration
	httpClient *http.Client
	logger     *zap.Logger
	enabled    bool
}

type ExportRequest struct {
	JobType      string  `json:"job_type,omitempty"`
	Format       string  `json:"format,omitempty"`
	StartTimeSec float64 `json:"start_time_sec,omitempty"`
	EndTimeSec   float64 `json:"end_time_sec,omitempty"`
	Layout       string  `json:"layout,omitempty"`
	OutputKey    string  `json:"output_key,omitempty"`
	SpotlightKey string  `json:"spotlight_key,omitempty"`
	Tiles        []any   `json:"tiles,omitempty"`
	AudioSource  string  `json:"audio_source_key,omitempty"`
}

func NewClient(cfg config.CompositerConfig, logger *zap.Logger) *Client {
	if logger == nil {
		logger = zap.NewNop()
	}
	if !cfg.Enabled {
		return &Client{enabled: false, logger: logger}
	}

	scheme := "http"
	if cfg.UseSSL {
		scheme = "https"
	}
	baseURL := fmt.Sprintf("%s://%s:%d", scheme, cfg.Host, cfg.Port)

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		serviceKey: cfg.ServiceKey,
		timeout:    timeout,
		httpClient: &http.Client{Timeout: timeout},
		logger:     logger,
		enabled:    true,
	}
}

func (c *Client) Enabled() bool {
	return c != nil && c.enabled
}

func (c *Client) CreateExport(ctx context.Context, roomID, recordingID string, payload ExportRequest) error {
	if c == nil || !c.enabled {
		return nil
	}

	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/recordings/%s/export", c.baseURL, recordingID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	if c.serviceKey != "" {
		req.Header.Set("X-Compositer-Service-Key", c.serviceKey)
		if roomID != "" {
			req.Header.Set("X-Compositer-Room-Id", roomID)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	return fmt.Errorf("compositer export failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(data)))
}
