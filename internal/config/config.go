package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all configuration for the recording service
type Config struct {
	GRPC      GRPCConfig      `yaml:"grpc"`
	Storage   StorageConfig   `yaml:"storage"`
	Recording RecordingConfig `yaml:"recording"`
	Logging   LoggingConfig   `yaml:"logging"`
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
}

// Address returns the gRPC server address
func (c *GRPCConfig) Address() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}
