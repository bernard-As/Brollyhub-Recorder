package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all configuration for the recording service
type Config struct {
	GRPC       GRPCConfig       `yaml:"grpc"`
	Storage    StorageConfig    `yaml:"storage"`
	Recording  RecordingConfig  `yaml:"recording"`
	Shelves    ShelvesConfig    `yaml:"shelves"`
	Compositer CompositerConfig `yaml:"compositer"`
	Logging    LoggingConfig    `yaml:"logging"`
}

// GRPCConfig holds gRPC server configuration
type GRPCConfig struct {
	Host             string        `yaml:"host"`
	Port             int           `yaml:"port"`
	MaxMessageSize   int           `yaml:"max_message_size"`
	KeepaliveTime    time.Duration `yaml:"keepalive_time"`
	KeepaliveTimeout time.Duration `yaml:"keepalive_timeout"`
}

// StorageConfig holds MinIO/S3 configuration
type StorageConfig struct {
	Endpoint  string `yaml:"endpoint"`
	Bucket    string `yaml:"bucket"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	UseSSL    bool   `yaml:"use_ssl"`
	Region    string `yaml:"region"`
}

// RecordingConfig holds recording-specific configuration
type RecordingConfig struct {
	BufferSize        int           `yaml:"buffer_size"`
	FlushInterval     time.Duration `yaml:"flush_interval"`
	MaxTracksPerRoom  int           `yaml:"max_tracks_per_room"`
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval"`
	SegmentDuration   time.Duration `yaml:"segment_duration"`
	SegmentMaxBytes   int64         `yaml:"segment_max_bytes"`
}

// ShelvesConfig holds Shelves gRPC configuration
type ShelvesConfig struct {
	Host    string        `yaml:"host"`
	Port    int           `yaml:"port"`
	Timeout time.Duration `yaml:"timeout"`
	Enabled bool          `yaml:"enabled"`
}

// CompositerConfig holds Compositer HTTP configuration
type CompositerConfig struct {
	Host       string        `yaml:"host"`
	Port       int           `yaml:"port"`
	Timeout    time.Duration `yaml:"timeout"`
	Enabled    bool          `yaml:"enabled"`
	ServiceKey string        `yaml:"service_key"`
	UseSSL     bool          `yaml:"use_ssl"`
}

// LoggingConfig holds logging configuration
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
	Output string `yaml:"output"`
}

// Load reads configuration from file and applies environment overrides
func Load(path string) (*Config, error) {
	cfg := &Config{}

	// Set defaults
	cfg.setDefaults()

	// Load from file if exists
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("failed to read config file: %w", err)
			}
			// File doesn't exist, continue with defaults
		} else {
			if err := yaml.Unmarshal(data, cfg); err != nil {
				return nil, fmt.Errorf("failed to parse config file: %w", err)
			}
		}
	}

	// Apply environment overrides
	cfg.applyEnvOverrides()

	return cfg, nil
}

func (c *Config) setDefaults() {
	c.GRPC = GRPCConfig{
		Host:             "0.0.0.0",
		Port:             50054,
		MaxMessageSize:   10 * 1024 * 1024, // 10MB
		KeepaliveTime:    30 * time.Second,
		KeepaliveTimeout: 10 * time.Second,
	}

	c.Storage = StorageConfig{
		Endpoint:  "minio:9100",
		Bucket:    "recordings-private",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin123",
		UseSSL:    false,
		Region:    "us-east-1",
	}

	c.Recording = RecordingConfig{
		BufferSize:        64 * 1024, // 64KB
		FlushInterval:     time.Second,
		MaxTracksPerRoom:  50,
		HeartbeatInterval: 10 * time.Second,
		SegmentDuration:   7 * time.Minute,
		SegmentMaxBytes:   70 * 1024 * 1024, // 70MB
	}

	c.Shelves = ShelvesConfig{
		Host:    "shelves-grpc",
		Port:    50070,
		Timeout: 5 * time.Second,
		Enabled: true,
	}

	c.Compositer = CompositerConfig{
		Host:       "compositer",
		Port:       50085,
		Timeout:    30 * time.Second,
		Enabled:    true,
		ServiceKey: "",
		UseSSL:     false,
	}

	c.Logging = LoggingConfig{
		Level:  "info",
		Format: "json",
		Output: "stdout",
	}
}

func (c *Config) applyEnvOverrides() {
	// GRPC
	if v := os.Getenv("RECORDING_GRPC_HOST"); v != "" {
		c.GRPC.Host = v
	}
	if v := os.Getenv("RECORDING_GRPC_PORT"); v != "" {
		var port int
		if _, err := fmt.Sscanf(v, "%d", &port); err == nil {
			c.GRPC.Port = port
		}
	}

	// Storage
	if v := os.Getenv("RECORDING_S3_ENDPOINT"); v != "" {
		c.Storage.Endpoint = v
	}
	if v := os.Getenv("RECORDING_S3_BUCKET"); v != "" {
		c.Storage.Bucket = v
	}
	if v := os.Getenv("RECORDING_S3_ACCESS_KEY"); v != "" {
		c.Storage.AccessKey = v
	}
	if v := os.Getenv("RECORDING_S3_SECRET_KEY"); v != "" {
		c.Storage.SecretKey = v
	}
	if v := os.Getenv("RECORDING_S3_USE_SSL"); v == "true" {
		c.Storage.UseSSL = true
	}
	if v := os.Getenv("RECORDING_S3_REGION"); v != "" {
		c.Storage.Region = v
	}

	// Logging
	if v := os.Getenv("RECORDING_LOG_LEVEL"); v != "" {
		c.Logging.Level = v
	}

	// Recording
	if v := os.Getenv("RECORDING_SEGMENT_DURATION"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Recording.SegmentDuration = d
		}
	}
	if v := os.Getenv("RECORDING_SEGMENT_MAX_BYTES"); v != "" {
		var bytes int64
		if _, err := fmt.Sscanf(v, "%d", &bytes); err == nil {
			c.Recording.SegmentMaxBytes = bytes
		}
	}

	// Shelves gRPC
	if v := os.Getenv("RECORDING_SHELVES_GRPC_HOST"); v != "" {
		c.Shelves.Host = v
	}
	if v := os.Getenv("RECORDING_SHELVES_GRPC_PORT"); v != "" {
		var port int
		if _, err := fmt.Sscanf(v, "%d", &port); err == nil {
			c.Shelves.Port = port
		}
	}
	if v := os.Getenv("RECORDING_SHELVES_GRPC_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Shelves.Timeout = d
		}
	}
	if v := os.Getenv("RECORDING_SHELVES_GRPC_ENABLED"); v != "" {
		c.Shelves.Enabled = v == "true"
	}

	// Compositer HTTP
	if v := os.Getenv("RECORDING_COMPOSITER_HOST"); v != "" {
		c.Compositer.Host = v
	}
	if v := os.Getenv("RECORDING_COMPOSITER_PORT"); v != "" {
		var port int
		if _, err := fmt.Sscanf(v, "%d", &port); err == nil {
			c.Compositer.Port = port
		}
	}
	if v := os.Getenv("RECORDING_COMPOSITER_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Compositer.Timeout = d
		}
	}
	if v := os.Getenv("RECORDING_COMPOSITER_ENABLED"); v != "" {
		c.Compositer.Enabled = v == "true"
	}
	if v := os.Getenv("RECORDING_COMPOSITER_SERVICE_KEY"); v != "" {
		c.Compositer.ServiceKey = v
	}
	if v := os.Getenv("RECORDING_COMPOSITER_USE_SSL"); v != "" {
		c.Compositer.UseSSL = v == "true"
	}
}

// Address returns the gRPC server address
func (c *GRPCConfig) Address() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// Address returns the Shelves gRPC server address.
func (c *ShelvesConfig) Address() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// Address returns the Compositer base address.
func (c *CompositerConfig) Address() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}
