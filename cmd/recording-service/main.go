package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/brollyhub/recording/internal/config"
	grpcserver "github.com/brollyhub/recording/internal/grpc"
	"github.com/brollyhub/recording/internal/recording"
	"github.com/brollyhub/recording/internal/shelves"
	"github.com/brollyhub/recording/internal/storage"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	// Parse flags
	configPath := flag.String("config", "config.yaml", "Path to config file")
	flag.Parse()

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Setup logger
	logger, err := setupLogger(cfg.Logging)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to setup logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	logger.Info("Starting Recording Service",
		zap.String("grpc_address", cfg.GRPC.Address()),
		zap.String("storage_endpoint", cfg.Storage.Endpoint),
		zap.String("storage_bucket", cfg.Storage.Bucket))

	// Setup storage
	store, err := storage.NewMinIOStorage(storage.MinIOConfig{
		Endpoint:  cfg.Storage.Endpoint,
		Bucket:    cfg.Storage.Bucket,
		AccessKey: cfg.Storage.AccessKey,
		SecretKey: cfg.Storage.SecretKey,
		UseSSL:    cfg.Storage.UseSSL,
		Region:    cfg.Storage.Region,
	}, logger)
	if err != nil {
		logger.Fatal("Failed to create storage", zap.Error(err))
	}

	// Ensure bucket exists (create if not)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := store.EnsureBucket(ctx); err != nil {
		logger.Warn("Failed to ensure bucket exists - service will continue but recordings may fail",
			zap.Error(err))
	} else {
		logger.Info("Storage bucket ready", zap.String("bucket", cfg.Storage.Bucket))
	}
	cancel()

	// Setup recording manager
	manager := recording.NewManager(recording.ManagerConfig{
		Storage:         store,
		Logger:          logger,
		BufferSize:      cfg.Recording.BufferSize,
		FlushInterval:   cfg.Recording.FlushInterval,
		SegmentDuration: cfg.Recording.SegmentDuration,
		SegmentMaxBytes: cfg.Recording.SegmentMaxBytes,
	})

	// Setup Shelves notifier client (optional)
	shelvesClient, err := shelves.NewClient(cfg.Shelves, logger)
	if err != nil {
		logger.Warn("Failed to connect to Shelves gRPC", zap.Error(err))
	}

	// Setup gRPC server
	server := grpcserver.NewServer(grpcserver.ServerConfig{
		Config:        &cfg.GRPC,
		Manager:       manager,
		Logger:        logger,
		ShelvesClient: shelvesClient,
	})

	// Start health check HTTP server
	healthServer := startHealthServer(cfg.GRPC.Port+1, server, manager, logger)

	// Start gRPC server in goroutine
	errChan := make(chan error, 1)
	go func() {
		if err := server.Start(); err != nil {
			errChan <- err
		}
	}()

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errChan:
		logger.Fatal("gRPC server error", zap.Error(err))
	case sig := <-sigChan:
		logger.Info("Received shutdown signal", zap.String("signal", sig.String()))
	}

	// Graceful shutdown
	logger.Info("Starting graceful shutdown")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Stop health server
	if err := healthServer.Shutdown(shutdownCtx); err != nil {
		logger.Warn("Health server shutdown error", zap.Error(err))
	}

	// Stop gRPC server
	if err := server.Stop(shutdownCtx); err != nil {
		logger.Warn("gRPC server shutdown error", zap.Error(err))
	}

	// Shutdown recording manager (stops all active recordings)
	if err := manager.Shutdown(shutdownCtx); err != nil {
		logger.Warn("Recording manager shutdown error", zap.Error(err))
	}

	if shelvesClient != nil {
		if err := shelvesClient.Close(); err != nil {
			logger.Warn("Shelves client shutdown error", zap.Error(err))
		}
	}

	logger.Info("Recording Service stopped")
}

func setupLogger(cfg config.LoggingConfig) (*zap.Logger, error) {
	var level zapcore.Level
	switch cfg.Level {
	case "debug":
		level = zapcore.DebugLevel
	case "info":
		level = zapcore.InfoLevel
	case "warn":
		level = zapcore.WarnLevel
	case "error":
		level = zapcore.ErrorLevel
	default:
		level = zapcore.InfoLevel
	}

	var zapCfg zap.Config
	if cfg.Format == "json" {
		zapCfg = zap.NewProductionConfig()
	} else {
		zapCfg = zap.NewDevelopmentConfig()
	}
	zapCfg.Level = zap.NewAtomicLevelAt(level)

	return zapCfg.Build()
}

func startHealthServer(port int, server *grpcserver.Server, manager *recording.Manager, logger *zap.Logger) *http.Server {
	mux := http.NewServeMux()

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		health := map[string]interface{}{
			"healthy":           true,
			"service_id":        server.ServiceID(),
			"sfu_connected":     server.IsConnected(),
			"active_recordings": manager.ActiveRecordings(),
		}

		// Check storage health
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := manager.Health(ctx); err != nil {
			health["healthy"] = false
			health["storage_error"] = err.Error()
		}

		w.Header().Set("Content-Type", "application/json")
		if !health["healthy"].(bool) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		json.NewEncoder(w).Encode(health)
	})

	// Stats endpoint
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		stats := manager.GetStats()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"active_recordings": len(stats),
			"recordings":        stats,
		})
	})

	addr := fmt.Sprintf(":%d", port)
	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		logger.Info("Health server starting", zap.String("address", addr))
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			logger.Error("Health server error", zap.Error(err))
		}
	}()

	return srv
}
