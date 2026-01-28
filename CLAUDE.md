# CLAUDE.md - Recording Service

## Project Overview
Go-based recording service for Brollyhub that captures individual media tracks from SFU rooms via gRPC. Stores raw RTP packets in MinIO for post-processing.

## Development Commands

### Essential Commands
```bash
# Build
go build -o recording-service ./cmd/recording-service

# Run
./recording-service -config config.yaml

# Generate proto (Windows - PowerShell)
powershell -ExecutionPolicy Bypass -File "D:/recording/scripts/generate-proto.ps1"

# Generate proto (Docker build - automatic)
docker-compose build

# Docker
docker-compose up -d
docker-compose logs -f recording-service
```

### Proto Generation

**Requirements:**
- `protoc` v6.33+ (install via `winget install Google.Protobuf`)
- `protoc-gen-go` v1.36+ (install via `go install google.golang.org/protobuf/cmd/protoc-gen-go@latest`)
- `protoc-gen-go-grpc` v1.6+ (install via `go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest`)

**Scripts:**
- `scripts/generate-proto.ps1` - PowerShell script for Windows
- `scripts/generate-proto.bat` - Batch script for Windows
- Dockerfile generates proto files automatically during build

## Architecture Overview

### Tech Stack
| Category | Technology | Version |
|----------|------------|---------|
| Language | Go | 1.23+ |
| gRPC | google.golang.org/grpc | v1.68.0 |
| Protobuf | google.golang.org/protobuf | v1.36.0 |
| Storage | MinIO (S3-compatible) | - |
| RTP Parsing | pion/rtp | v1.8.3 |
| Logging | zap | v1.26.0 |

### Proto Tools
| Tool | Version | Install Command |
|------|---------|-----------------|
| protoc | v6.33+ | `winget install Google.Protobuf` |
| protoc-gen-go | v1.36+ | `go install google.golang.org/protobuf/cmd/protoc-gen-go@latest` |
| protoc-gen-go-grpc | v1.6+ | `go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest` |

### Directory Structure
```
recording/
├── cmd/recording-service/
│   └── main.go                 # Entry point, graceful shutdown
├── internal/
│   ├── config/
│   │   └── config.go           # YAML + env config
│   ├── grpc/
│   │   ├── server.go           # gRPC server
│   │   └── handlers.go         # Message handlers
│   ├── recording/
│   │   ├── manager.go          # Recording coordinator
│   │   ├── room.go             # Per-room state
│   │   ├── track.go            # Per-track writer
│   │   └── policy.go           # Policy enforcement
│   ├── storage/
│   │   ├── interface.go        # Storage interface
│   │   └── minio.go            # MinIO implementation
│   └── rtp/
│       ├── parser.go           # RTP header parsing
│       └── writer.go           # Custom file format
├── proto/
│   ├── recording_sfu.proto     # gRPC definitions
│   ├── recording_sfu.pb.go     # Generated protobuf types
│   └── recording_sfu_grpc.pb.go # Generated gRPC service
├── scripts/
│   ├── generate-proto.ps1      # PowerShell proto generation
│   └── generate-proto.bat      # Batch proto generation
├── docs/
│   ├── ARCHITECTURE-PLAN.md    # Architecture design
│   └── RECORDING-SERVICE.md    # Full documentation
├── Dockerfile                  # Multi-stage build with proto gen
├── docker-compose.yml
├── config.yaml
└── go.mod
```

## Core Components

### Recording Manager
Coordinates all active recordings. Key methods:
- `StartRecording(roomID, requestedBy, policy)` - Start new recording
- `StopRecording(roomID, stoppedBy)` - Stop and finalize
- `WritePacket(roomID, producerID, data, timestamp)` - Handle RTP

### Room Recording
Per-room state including:
- Active tracks (Map<producerID, TrackWriter>)
- Participants (Map<peerID, Participant>)
- Timeline events
- Policy snapshot

### Track Writer
Per-track buffered RTP writer using custom file format.

### Storage Layer
MinIO/S3 interface for:
- Creating recording directories
- Writing track data (multipart upload)
- Writing metadata/timeline JSON
- Finalizing recordings

## gRPC Protocol

### Service Definition
```protobuf
service RecordingSfuBridge {
  rpc Connect(stream SfuToRecording) returns (stream RecordingToSfu);
}
```

### Key Messages
| Direction | Message | Purpose |
|-----------|---------|---------|
| SFU→Rec | StartRecordingRequest | Start recording |
| SFU→Rec | RtpPacket | RTP data |
| SFU→Rec | RoomEvent | Timeline events |
| Rec→SFU | StartRecordingResponse | Confirmation |
| Rec→SFU | ErrorMessage | Errors |

## Storage Structure

```
recordings-private/
└── rooms/{room_id}/{recording_id}/
    ├── metadata.json    # Room info, tracks, stats
    ├── timeline.json    # Events for sync
    ├── policy.json      # Policy snapshot
    └── tracks/
        └── {peer}-{type}.rtp
```

## RTP File Format

32-byte header + packet records:
```
Header: Magic(4) + Version(1) + PT(1) + Reserved(2) +
        SSRC(4) + Codec(8) + StartTime(8) + Reserved(4)
Packet: ServerTS(8) + Length(2) + RTPData(N)
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| RECORDING_GRPC_PORT | 50054 | gRPC port |
| RECORDING_S3_ENDPOINT | minio:9100 | MinIO endpoint |
| RECORDING_S3_BUCKET | recordings-private | Storage bucket |
| RECORDING_LOG_LEVEL | info | Log level |

## API Endpoints

- `GET /health` - Health check (port 50055)
- `GET /stats` - Active recording stats

## Integration Points

### SFU Integration
- `D:\sfu\lib\grpc\recording-server.js` - gRPC client
- `D:\sfu\lib\Room.js` - Recording methods

### Recording Flow
1. Peer requests recording via protoo
2. Room.js calls `_handleRecordingRequest()`
3. SFU sends `StartRecordingRequest` to Recording Service
4. Recording Service creates storage structure
5. SFU pipes producers to DirectTransport
6. Consumer.on('rtp') forwards packets via gRPC
7. Recording Service writes to MinIO
8. On stop, metadata/timeline written

## Error Handling

- Storage failures: Log warning, continue
- gRPC disconnect: Auto-reconnect with backoff
- Track errors: Clean up consumer, continue recording

## References

- SFU: `D:\sfu\CLAUDE.md`
- FLARE pattern: `D:\sfu\lib\grpc\flare-server.js`
- MinIO: `minio:9100`
