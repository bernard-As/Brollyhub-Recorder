# Brollyhub Recording Service

A high-performance recording service for capturing individual media tracks from SFU rooms, enabling flexible post-processing with any desired layout.

## Architecture

See [docs/ARCHITECTURE-PLAN.md](docs/ARCHITECTURE-PLAN.md) for the complete architecture design.

## Quick Overview

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        RECORDING SERVICE OVERVIEW                            │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  SFU (MediaSoup)                                                             │
│  ├── Producer (audio/video)                                                  │
│  │   └── DirectTransport Consumer ──► gRPC Stream ──┐                        │
│  └── Room Policy (who_can_record)                   │                        │
│                                                     ▼                        │
│                                    ┌────────────────────────────┐            │
│                                    │   Recording Service (Go)   │            │
│                                    │   Port: 50054              │            │
│                                    │                            │            │
│                                    │   • Policy enforcement     │            │
│                                    │   • RTP packet capture     │            │
│                                    │   • Timeline sync          │            │
│                                    └─────────────┬──────────────┘            │
│                                                  │                           │
│                    ┌─────────────────────────────┴─────────────────────┐     │
│                    ▼                                                   ▼     │
│         ┌─────────────────────┐                          ┌──────────────────┐│
│         │ MinIO (minio:9100)  │                          │ Redis (6380)     ││
│         │ recordings-private  │                          │ Job Queue        ││
│         └─────────────────────┘                          └──────────────────┘│
│                    │                                                   │     │
│                    └───────────────────┬───────────────────────────────┘     │
│                                        ▼                                     │
│                          ┌────────────────────────────┐                      │
│                          │   Post-Processor Workers   │                      │
│                          │   (FFmpeg)                 │                      │
│                          │                            │                      │
│                          │   • Grid layout            │                      │
│                          │   • Speaker view           │                      │
│                          │   • Custom layouts         │                      │
│                          └────────────────────────────┘                      │
│                                                                              │
│  Network: brollyhub-central_brollyhub_network                               │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

## Key Features

- **Individual Track Recording**: Each participant's audio/video recorded separately
- **Post-Processing Flexibility**: Apply any layout after the fact
- **Policy-Aware**: Respects room policies (who can record, auto-record)
- **Infrastructure Integration**: Uses existing MinIO and network

## Infrastructure

| Component | Port | Description |
|-----------|------|-------------|
| Recording Service | 50054 | gRPC server for SFU communication |
| Recording Redis | 6380 | Job queue (separate from brollyhub redis) |
| MinIO | 9100 | Storage (existing, new bucket: recordings-private) |

## Getting Started

### Quick Start (Docker)

```bash
# Build and start the recording service
docker-compose up -d --build

# Check status
docker-compose ps
docker logs recording-service

# Health check
curl http://localhost:50055/health
```

### Local Development

```powershell
# Install proto tools (Windows)
winget install Google.Protobuf
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.35.2
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1

# Generate proto files
powershell -ExecutionPolicy Bypass -File "D:/recording/scripts/generate-proto.ps1"

# Build
go build -o recording-service ./cmd/recording-service

# Run
./recording-service -config config.yaml
```

## Documentation

- [Architecture Plan](docs/ARCHITECTURE-PLAN.md) - Full design document
- Room Policies - See `sfu/docs/ROOM_POLICIES.md` for policy enforcement

## Technology Stack

| Component | Technology | Version |
|-----------|------------|---------|
| Language | Go | 1.23 |
| gRPC | google.golang.org/grpc | v1.67.0 |
| Protobuf | google.golang.org/protobuf | v1.35.2 |
| RTP Parsing | pion/rtp | v1.8.3 |
| Storage | MinIO (S3-compatible) | - |
| Post-Processing | FFmpeg | - |
| Job Queue | Redis | 7-alpine |

### Proto Tools

| Tool | Version | Install |
|------|---------|---------|
| protoc | v6.33+ | `winget install Google.Protobuf` |
| protoc-gen-go | v1.35.2 | `go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.35.2` |
| protoc-gen-go-grpc | v1.5.1 | `go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1` |
