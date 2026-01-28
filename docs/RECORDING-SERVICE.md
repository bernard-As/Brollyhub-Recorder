# Brollyhub Recording Service - Phase 1 MVP

## Overview

The Recording Service captures individual media tracks from SFU rooms for post-processing. It receives RTP packets via gRPC from the SFU and stores them in MinIO for later transcoding.

## Architecture

```
SFU (MediaSoup)                          Recording Service (Go)
┌──────────────────┐                     ┌─────────────────────┐
│ Room.js          │   gRPC Stream       │ gRPC Server         │
│ DirectTransport  │ ─────────────────►  │ (:50075)            │
│ consumer.on(rtp) │   RTP + Events      │                     │
└──────────────────┘                     │ ┌─────────────────┐ │
                                         │ │ Recording Mgr   │ │
                                         │ │ ├─ RoomRecording│ │
                                         │ │ └─ TrackWriter  │ │
                                         │ └────────┬────────┘ │
                                         └──────────┼──────────┘
                                                    │
                    ┌───────────────────────────────┘
                    ▼
         ┌─────────────────────┐
         │ MinIO (minio:9100)  │
         │ recordings-private/ │
         │  └─ rooms/{id}/     │
         │     ├─ metadata.json│
         │     ├─ timeline.json│
         │     └─ tracks/*.rtp │
         └─────────────────────┘
```

## Components

### Go Service (`D:\recording\`)

| Directory | Purpose |
|-----------|---------|
| `cmd/recording-service/` | Entry point with graceful shutdown |
| `internal/config/` | YAML + env configuration |
| `internal/grpc/` | gRPC server and message handlers |
| `internal/recording/` | Recording manager, room state, track writers |
| `internal/storage/` | MinIO/S3 storage interface |
| `internal/rtp/` | RTP parsing and custom file format |
| `proto/` | Protocol buffer definitions |

### SFU Integration (`D:\sfu\`)

| File | Changes |
|------|---------|
| `lib/grpc/recording-server.js` | gRPC client for Recording Service |
| `lib/Room.js` | Recording methods (setupRecordingGrpcTransport, etc.) |
| `proto/recording_sfu.proto` | Shared proto definition |

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `RECORDING_GRPC_HOST` | 0.0.0.0 | gRPC server host |
| `RECORDING_GRPC_PORT` | 50075 | gRPC server port |
| `RECORDING_S3_ENDPOINT` | minio:9100 | MinIO endpoint |
| `RECORDING_S3_BUCKET` | recordings-private | Storage bucket |
| `RECORDING_S3_ACCESS_KEY` | minioadmin | MinIO access key |
| `RECORDING_S3_SECRET_KEY` | minioadmin123 | MinIO secret key |
| `RECORDING_S3_USE_SSL` | false | Use HTTPS for MinIO |
| `RECORDING_LOG_LEVEL` | info | Log level (debug/info/warn/error) |

### config.yaml

```yaml
grpc:
  host: "0.0.0.0"
  port: 50075
  max_message_size: 10485760
  keepalive_time: 30s
  keepalive_timeout: 10s

storage:
  endpoint: "minio:9100"
  bucket: "recordings-private"
  access_key: "minioadmin"
  secret_key: "minioadmin123"

recording:
  buffer_size: 65536
  flush_interval: 1s
  max_tracks_per_room: 50
```

## Storage Structure

```
recordings-private/
└── rooms/{room_id}/{recording_id}/
    ├── .recording          # Active recording marker
    ├── .completed          # Finalized marker
    ├── metadata.json       # Room info, participants, tracks
    ├── timeline.json       # Events for sync
    ├── policy.json         # Policy snapshot (see below)
    └── tracks/
        ├── {peer_id}-audio.rtp
        ├── {peer_id}-video.rtp
        └── {peer_id}-screenshare.rtp
```

### policy.json Structure

```json
{
  "recording_id": "uuid",
  "room_id": "room-id",
  "enabled": true,
  "who_can_record": "host_only",
  "auto_record": true,
  "record_audio": true,
  "record_video": true,
  "record_screenshare": true,
  "who_can_access_recordings": "host",
  "allowed_accessor_ids": [],
  "snapshot_time": "2026-01-28T12:00:00Z"
}
```

## RTP File Format

Custom format with 32-byte header followed by packet records:

```
+------------------+
| Header (32 bytes)|
|  Magic: "BRTP"   |
|  Version: 1      |
|  SSRC: uint32    |
|  PayloadType     |
|  Codec (8 bytes) |
|  StartTime: int64|
+------------------+
| Packet Records   |
|  ServerTS (8B)   |
|  Length (2B)     |
|  RTP Data (N)    |
+------------------+
```

## gRPC Protocol

### Messages from SFU to Recording Service

| Message | Purpose |
|---------|---------|
| `RegisterRequest` | SFU registration |
| `StartRecordingRequest` | Start recording for a room |
| `StopRecordingRequest` | Stop recording |
| `SubscribeTrackRequest` | Subscribe to a track |
| `UnsubscribeTrackRequest` | Unsubscribe from a track |
| `RtpPacket` | RTP packet data |
| `RoomEvent` | Room events (peer join/leave, etc.) |
| `Heartbeat` | Keep-alive |

### Messages from Recording Service to SFU

| Message | Purpose |
|---------|---------|
| `RegisterResponse` | Registration confirmation |
| `StartRecordingResponse` | Recording started confirmation |
| `StopRecordingResponse` | Recording stopped with stats |
| `ErrorMessage` | Error notification |
| `Heartbeat` | Keep-alive response |

## Recording Policy

```protobuf
message RecordingPolicy {
  bool enabled = 1;
  WhoCanRecord who_can_record = 2;           // HOST_ONLY, CO_HOST_AND_HOST, ANYONE (placeholder for future)
  bool auto_record = 3;                      // Auto-start recording on first producer
  bool record_audio = 4;                     // Record audio tracks
  bool record_video = 5;                     // Record video tracks
  bool record_screenshare = 6;               // Record screenshare tracks
  AccessLevel who_can_access_recordings = 7; // Post-meeting access control
  repeated string allowed_accessor_ids = 8;  // User IDs when ACCESS_SELECTED
}

enum AccessLevel {
  ACCESS_LEVEL_UNSPECIFIED = 0;
  ACCESS_ALL = 1;        // All participants can access
  ACCESS_HOST = 2;       // Host only (default)
  ACCESS_LOGGED_IN = 3;  // Logged in users only
  ACCESS_SELECTED = 4;   // Selected participants (use allowed_accessor_ids)
}
```

### Policy Flow

```
Huddle Backend          SFU (Room.js)           Recording Service (Go)
┌─────────────┐        ┌─────────────┐          ┌─────────────────┐
│ room_policy │───────▶│ Auto-trigger│─────────▶│ Policy Storage  │
│   Schema    │ gRPC   │ on producer │  gRPC    │ Track Filtering │
│ Validation  │        │ Policy check│          │ MinIO snapshot  │
└─────────────┘        └─────────────┘          └─────────────────┘
```

### Auto-Recording

When `auto_record` is enabled, the SFU will automatically start recording when the first participant creates a producer (audio/video) that matches the track type filters (`record_audio`, `record_video`, `record_screenshare`).

## API Endpoints

### Health Check

```
GET http://localhost:50076/health
```

Response:
```json
{
  "healthy": true,
  "service_id": "uuid",
  "sfu_connected": true,
  "active_recordings": 1
}
```

### Stats

```
GET http://localhost:50076/stats
```

Response:
```json
{
  "active_recordings": 1,
  "recordings": [
    {
      "RecordingID": "uuid",
      "RoomID": "room-1",
      "Duration": "5m30s",
      "TrackCount": 3,
      "TotalBytes": 15000000
    }
  ]
}
```

## Development

### Prerequisites

**Proto Tools (Windows):**
```powershell
# Install protoc
winget install Google.Protobuf

# Install Go plugins (pinned versions for Go 1.23 compatibility)
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.35.2
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
```

| Tool | Version | Purpose |
|------|---------|---------|
| protoc | v6.33+ | Protocol buffer compiler |
| protoc-gen-go | v1.35.2 | Go protobuf code generator |
| protoc-gen-go-grpc | v1.5.1 | Go gRPC code generator |

### Building

```bash
cd D:\recording

# Generate proto files (Windows - PowerShell)
powershell -ExecutionPolicy Bypass -File "D:/recording/scripts/generate-proto.ps1"

# Build
go build -o recording-service ./cmd/recording-service

# Run
./recording-service -config config.yaml
```

### Proto Generation

The proto files can be generated in multiple ways:

1. **PowerShell script (recommended for Windows):**
   ```powershell
   powershell -ExecutionPolicy Bypass -File "D:/recording/scripts/generate-proto.ps1"
   ```

2. **Docker build (automatic):**
   ```bash
   docker-compose build
   ```
   The Dockerfile includes proto generation in the build stage.

3. **Manual command:**
   ```bash
   protoc --proto_path=proto \
       --go_out=proto --go_opt=paths=source_relative \
       --go-grpc_out=proto --go-grpc_opt=paths=source_relative \
       proto/recording_sfu.proto
   ```

### Docker

```bash
# Build and run
docker-compose up -d

# View logs
docker-compose logs -f recording-service

# Health check
curl http://localhost:50076/health
```

## Integration with SFU

### Initialize Recording Client

In `server.js`:

```javascript
const RecordingGrpcServer = require('./lib/grpc/recording-server');

const recordingGrpcServer = new RecordingGrpcServer({
  host: process.env.RECORDING_GRPC_HOST || 'recording-service',
  port: parseInt(process.env.RECORDING_GRPC_PORT || '50075'),
  sfuId: process.env.SFU_ID || 'sfu-1'
}, rooms);

await recordingGrpcServer.start();
global.recordingGrpcServer = recordingGrpcServer;
```

### Start Recording (Room.js)

```javascript
// In protoo request handler for 'startRecording'
const response = await this._handleRecordingRequest(peerId, policy);
```

### Stop Recording (Room.js)

```javascript
const response = await this.stopRecording(peerId);
```

## Verification

| Step | Command | Expected |
|------|---------|----------|
| 1 | `docker-compose up -d` | Services start |
| 2 | `curl localhost:50076/health` | `{"healthy":true}` |
| 3 | Check SFU logs | "Recording service connected" |
| 4 | Start meeting + recording | RTP packets in logs |
| 5 | `mc ls myminio/recordings-private/rooms/` | Room directory |
| 6 | Download metadata.json | Valid JSON |
