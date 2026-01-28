# Brollyhub Recording Service - Architecture Plan

## Executive Summary

This document outlines the architecture for a recording service that:
1. Records individual media tracks (audio/video) from SFU rooms
2. Stores raw tracks with timeline metadata
3. Enables post-processing with any desired layout
4. Integrates with existing Brollyhub infrastructure (MinIO, Redis, network)
5. Respects room policies for recording permissions

---

## Table of Contents

1. [Current SFU Architecture Analysis](#1-current-sfu-architecture-analysis)
2. [Infrastructure Integration](#2-infrastructure-integration)
3. [Recording Architecture Design](#3-recording-architecture-design)
4. [Room Policy Integration](#4-room-policy-integration)
5. [Communication Protocol](#5-communication-protocol)
6. [Technology Stack Decision](#6-technology-stack-decision)
7. [Data Flow & Storage](#7-data-flow--storage)
8. [Post-Processing Pipeline](#8-post-processing-pipeline)
9. [Implementation Phases](#9-implementation-phases)
10. [Alternative Approaches](#10-alternative-approaches)

---

## 1. Current SFU Architecture Analysis

### 1.1 Media Flow Overview

```
┌─────────────────────────────────────────────────────────────────────────┐
│                         CURRENT SFU ARCHITECTURE                         │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  Browser Clients (N)                                                     │
│       │                                                                  │
│       │ WebRTC (SRTP/DTLS)                                              │
│       ▼                                                                  │
│  ┌─────────────────────────────────────────────────────────────┐        │
│  │                    MediaSoup SFU                             │        │
│  │  ┌─────────────────────────────────────────────────────┐    │        │
│  │  │ WebRtcTransport (per peer)                          │    │        │
│  │  │  └─► Producer (audio/video)                         │    │        │
│  │  │       ├─► Consumer → Peer B                         │    │        │
│  │  │       ├─► Consumer → Peer C                         │    │        │
│  │  │       └─► [DirectTransport Consumer → FLARE Bot]    │    │        │
│  │  └─────────────────────────────────────────────────────┘    │        │
│  │                                                              │        │
│  │  Room.js manages:                                            │        │
│  │   • _protooRoom (signaling)                                  │        │
│  │   • _mediasoupRouter (media routing)                         │        │
│  │   • _roomPolicy (from Huddle)                                │        │
│  │   • peer.data.producers (Map)                                │        │
│  │   • _usageStats (billing metrics)                            │        │
│  └─────────────────────────────────────────────────────────────┘        │
│       │                                                                  │
│       │ gRPC Streams                                                     │
│       ▼                                                                  │
│  ┌────────────┐  ┌────────────┐  ┌────────────┐                         │
│  │ Huddle     │  │ FLARE      │  │ AI Sidecar │                         │
│  │ (50052)    │  │ (50053)    │  │ (50061)    │                         │
│  └────────────┘  └────────────┘  └────────────┘                         │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

### 1.2 Existing RTP Extraction Pattern (Room.js:1710-1782)

The FLARE bot integration demonstrates the exact pattern for RTP extraction:

```javascript
// Room.js - _pipeProducerToBot()
async _pipeProducerToBot({ producer }) {
  // 1. Create DirectTransport consumer on producer
  const consumer = await transport.consume({
    producerId: producer.id,
    rtpCapabilities: this._mediasoupRouter.rtpCapabilities
  });

  // 2. Extract codec metadata
  const opusCodec = consumer.rtpParameters.codecs.find(
    c => c.mimeType.toLowerCase() === "audio/opus"
  );
  const payloadType = opusCodec?.payloadType;
  const ssrc = consumer.rtpParameters.encodings?.[0]?.ssrc;

  // 3. Listen to RTP events - THIS IS THE KEY DATA
  consumer.on('rtp', rtpPacket => {
    // rtpPacket is raw Buffer containing RTP data
    global.flareGrpcServer?.sendRtpPacket(
      this._roomId,
      producer.id,
      peerId,
      rtpPacket,    // RAW RTP BUFFER
      payloadType,
      ssrc
    );
  });
}
```

---

## 2. Infrastructure Integration

### 2.1 Existing Brollyhub Infrastructure

The recording service integrates with existing services from `brollyhub-central`:

```
┌─────────────────────────────────────────────────────────────────────────┐
│                    BROLLYHUB INFRASTRUCTURE                              │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  Network: brollyhub-central_brollyhub_network                           │
│                                                                          │
│  ┌─────────────────────────────────────────────────────────────────┐    │
│  │ Existing Services                                                │    │
│  │                                                                  │    │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐              │    │
│  │  │ MinIO       │  │ Redis       │  │ Huddle gRPC │              │    │
│  │  │ API: 9100   │  │ Port: 6378  │  │ Port: 50052 │              │    │
│  │  │ UI:  9101   │  │ (internal   │  │             │              │    │
│  │  │             │  │  6379)      │  │             │              │    │
│  │  └─────────────┘  └─────────────┘  └─────────────┘              │    │
│  │                                                                  │    │
│  │  Existing Buckets:                                               │    │
│  │  • shelves-private, shelves-public, shelves-temp                 │    │
│  │  • huddle-private, huddle-public, huddle-temp                    │    │
│  │                                                                  │    │
│  └─────────────────────────────────────────────────────────────────┘    │
│                                                                          │
│  ┌─────────────────────────────────────────────────────────────────┐    │
│  │ NEW: Recording Service                                           │    │
│  │                                                                  │    │
│  │  ┌─────────────────┐  ┌─────────────────┐                       │    │
│  │  │ Recording       │  │ Post-Processor  │                       │    │
│  │  │ Service         │  │ Workers         │                       │    │
│  │  │ gRPC: 50054     │  │                 │                       │    │
│  │  └─────────────────┘  └─────────────────┘                       │    │
│  │         │                     │                                  │    │
│  │         │   Uses existing:    │                                  │    │
│  │         ├───► MinIO (9100)    │                                  │    │
│  │         │     New bucket: recordings-private                     │    │
│  │         │                     │                                  │    │
│  │         └───► Redis-Recording (Port 6380)  ◄─────────────────────┘    │
│  │              (Separate instance for job queue)                   │    │
│  │                                                                  │    │
│  └─────────────────────────────────────────────────────────────────┘    │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

### 2.2 Port Allocation

| Service | Port | Description |
|---------|------|-------------|
| MinIO API | 9100 | Existing - reuse for recordings |
| MinIO Console | 9101 | Existing - for admin |
| Redis (brollyhub) | 6378 (→6379) | Existing - DO NOT USE |
| Redis (recording) | **6380** (→6379) | NEW - recording job queue |
| Recording gRPC | **50054** | NEW - SFU ↔ Recording |
| Huddle gRPC | 50052 | Existing - policy validation |

### 2.3 MinIO Bucket Strategy

```
MinIO (minio:9100)
├── shelves-private/     (existing)
├── shelves-public/      (existing)
├── shelves-temp/        (existing - 1 day TTL)
├── huddle-private/      (existing)
├── huddle-public/       (existing)
├── huddle-temp/         (existing - 1 day TTL)
│
└── recordings-private/  (NEW)
    └── rooms/
        └── {room_id}/
            ├── metadata.json
            ├── tracks/
            │   ├── {peer_id}-audio.rtp
            │   ├── {peer_id}-video.rtp
            │   └── {peer_id}-screen.rtp
            └── processed/
                ├── composite.mp4
                └── thumbnails/
```

---

## 3. Recording Architecture Design

### 3.1 High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        RECORDING ARCHITECTURE                                │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌──────────────────┐                                                        │
│  │   MediaSoup SFU  │                                                        │
│  │                  │                                                        │
│  │  _roomPolicy ────┼──► Recording policy check                              │
│  │  Producer(audio) ├──┐                                                     │
│  │  Producer(video) ├──┤  DirectTransport                                    │
│  │  Producer(screen)├──┤  Consumers (zero-copy)                              │
│  └──────────────────┘  │                                                     │
│                        │                                                     │
│                        ▼  gRPC Stream (binary RTP)                           │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                    RECORDING SERVICE (Go)                            │    │
│  │                    Port: 50054                                       │    │
│  │                                                                      │    │
│  │  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐      │    │
│  │  │ gRPC Server     │  │ Track Manager   │  │ Storage Manager │      │    │
│  │  │                 │  │                 │  │                 │      │    │
│  │  │ • Policy check  │  │ • Demux by SSRC │  │ • MinIO (9100)  │      │    │
│  │  │ • RTP Packets   │──▶ • Buffer pkts  │──▶ • Multipart     │      │    │
│  │  │ • Room Events   │  │ • Timeline sync │  │   upload        │      │    │
│  │  │ • Metadata      │  │ • Codec state   │  │                 │      │    │
│  │  └─────────────────┘  └─────────────────┘  └─────────────────┘      │    │
│  │                                                                      │    │
│  │  ┌─────────────────────────────────────────────────────────────┐    │    │
│  │  │                    Per-Room Recording State                  │    │    │
│  │  │                                                              │    │    │
│  │  │  Room: abc-123                                               │    │    │
│  │  │  ├── Policy: { recording_enabled: true, who_can_record: ... }│    │    │
│  │  │  ├── Track: peer1-audio (SSRC: 12345, Opus)                 │    │    │
│  │  │  │    └── [RTP packets → audio.opus.rtp]                    │    │    │
│  │  │  ├── Track: peer1-video (SSRC: 12346, VP8)                  │    │    │
│  │  │  └── Metadata: timeline.json                                 │    │    │
│  │  └─────────────────────────────────────────────────────────────┘    │    │
│  │                                                                      │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                        │                                                     │
│                        ▼                                                     │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │         MinIO (minio:9100) - recordings-private bucket              │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                        │                                                     │
│                        ▼ Job Queue (Redis 6380)                              │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                    POST-PROCESSING SERVICE                           │    │
│  │                                                                      │    │
│  │  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐      │    │
│  │  │ Layout Engine   │  │ FFmpeg Pipeline │  │ Output Handler  │      │    │
│  │  │ • Grid layout   │──▶ • Decode tracks │──▶ • MP4/WebM      │      │    │
│  │  │ • Speaker view  │  │ • Apply layout │  │ • Upload MinIO  │      │    │
│  │  └─────────────────┘  └─────────────────┘  └─────────────────┘      │    │
│  │                                                                      │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                              │
│  Network: brollyhub-central_brollyhub_network                               │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 3.2 Individual Track Recording Approach

**Why Individual Tracks (not Composite)?**

| Aspect | Individual Tracks | Composite (Real-time) |
|--------|------------------|----------------------|
| Flexibility | Any layout post-hoc | Fixed during recording |
| CPU Load | Minimal (no encoding) | High (real-time compositing) |
| Quality | Original quality preserved | Re-encoding losses |
| Storage | Higher (N tracks) | Lower (1 file) |
| Post-edit | Full flexibility | Cannot change layout |
| Failure modes | Graceful (partial tracks) | All-or-nothing |

**Recommendation: Individual track recording** for maximum flexibility.

---

## 4. Room Policy Integration

### 4.1 Policy Transport Flow

Based on `sfu/docs/ROOM_POLICIES.md`, the policy flows as:

```
┌─────────────────────────────────────────────────────────────────┐
│                    POLICY TRANSPORT FLOW                         │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  1. Huddle Backend                                               │
│     └── ValidateRoom() / GetRoomConfig()                         │
│         └── Returns: policy_json in RoomDetails                  │
│                                                                  │
│  2. SFU Room.js                                                  │
│     └── _loadRoomPolicy()                                        │
│         └── Parses: this._roomPolicy                             │
│         └── Stores: this._roomPolicyJson                         │
│                                                                  │
│  3. Recording Service (NEW)                                      │
│     └── Receives policy in StartRecordingResponse                │
│     └── Enforces:                                                │
│         • features.recording.enabled                             │
│         • features.recording.who_can_record                      │
│         • features.recording.auto_record                         │
│         • features.recording.record_audio_only                   │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### 4.2 Recording Policy Schema (Proposed Extension)

```json
{
  "features": {
    "screen_share": {
      "enabled": true,
      "who_can_share": "all"
    },
    "recording": {
      "enabled": true,
      "auto_record": false,
      "who_can_record": "host",
      "who_can_download": "all",
      "record_audio": true,
      "record_video": true,
      "record_screenshare": true,
      "retention_days": 30,
      "notify_participants": true
    }
  },
  "roles": {
    "host_id": "user_123",
    "co_host_ids": ["user_456"],
    "panelist_ids": []
  }
}
```

### 4.3 Policy Enforcement Points

```
┌─────────────────────────────────────────────────────────────────┐
│                    POLICY ENFORCEMENT                            │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  CHECK 1: Recording Enabled                                      │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │ When: StartRecordingRequest received                     │    │
│  │ Check: policy.features.recording.enabled === true        │    │
│  │ Fail: Return error "Recording disabled for this room"    │    │
│  └─────────────────────────────────────────────────────────┘    │
│                                                                  │
│  CHECK 2: Who Can Record                                         │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │ When: StartRecordingRequest received                     │    │
│  │ Check: requester_id matches who_can_record policy        │    │
│  │                                                          │    │
│  │ who_can_record values:                                   │    │
│  │ • "host" → only host_id can start                        │    │
│  │ • "co_host" → host_id OR co_host_ids                     │    │
│  │ • "panelist" → host OR co_host OR panelist_ids           │    │
│  │ • "all" → any participant                                │    │
│  │                                                          │    │
│  │ Fail: Return error "Not authorized to record"            │    │
│  └─────────────────────────────────────────────────────────┘    │
│                                                                  │
│  CHECK 3: Auto-Record                                            │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │ When: First producer created (room starts)               │    │
│  │ Check: policy.features.recording.auto_record === true    │    │
│  │ Action: Automatically start recording                    │    │
│  └─────────────────────────────────────────────────────────┘    │
│                                                                  │
│  CHECK 4: Media Type Filtering                                   │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │ When: ProducerCreated event                              │    │
│  │ Check: policy.features.recording.record_video            │    │
│  │        policy.features.recording.record_audio            │    │
│  │        policy.features.recording.record_screenshare      │    │
│  │ Action: Skip subscription if media type disabled         │    │
│  └─────────────────────────────────────────────────────────┘    │
│                                                                  │
│  CHECK 5: Screen Share Detection (from Room.js)                  │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │ Detection methods:                                       │    │
│  │ • appData.share === true                                 │    │
│  │ • peer_id starts with "sharescreen_"                     │    │
│  └─────────────────────────────────────────────────────────┘    │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### 4.4 Policy-Aware Recording Flow

```javascript
// SFU Room.js - Policy-aware recording integration
async _handleRecordingRequest(peerId, request) {
  // 1. Check if recording enabled
  const recordingPolicy = this._roomPolicy?.features?.recording;
  if (!recordingPolicy?.enabled) {
    throw new Error('Recording is disabled for this room');
  }

  // 2. Check who can record
  const whoCanRecord = recordingPolicy.who_can_record || 'host';
  const canRecord = this._canPeerRecord(peerId, whoCanRecord);
  if (!canRecord) {
    throw new Error('Not authorized to start recording');
  }

  // 3. Forward to Recording Service with policy
  global.recordingGrpcServer?.startRecording({
    roomId: this._roomId,
    requesterId: peerId,
    policyJson: this._roomPolicyJson,
    config: {
      recordAudio: recordingPolicy.record_audio !== false,
      recordVideo: recordingPolicy.record_video !== false,
      recordScreenshare: recordingPolicy.record_screenshare !== false,
    }
  });
}

_canPeerRecord(peerId, whoCanRecord) {
  const normalizedPeerId = this._normalizeParticipantPeerId(peerId);
  const isHost = this._isHost(normalizedPeerId);
  const isCoHost = this._isPolicyRoleMember(normalizedPeerId, 'co_host');
  const isPanelist = this._isPolicyRoleMember(normalizedPeerId, 'panelist');

  switch (whoCanRecord) {
    case 'host': return isHost;
    case 'co_host': return isHost || isCoHost;
    case 'panelist': return isHost || isCoHost || isPanelist;
    case 'all': return true;
    default: return isHost;
  }
}
```

---

## 5. Communication Protocol

### 5.1 Recording Service gRPC Protocol

```protobuf
// recording_sfu.proto
syntax = "proto3";

package recording.sfu;

option go_package = "recording/proto/sfu";

// =============================================================================
// MAIN SERVICE
// =============================================================================

service RecordingSfuBridge {
  // Bidirectional stream for all SFU-Recording communication
  rpc Connect(stream RecordingToSfu) returns (stream SfuToRecording);

  // Health check
  rpc HealthCheck(RecordingHealthCheckRequest) returns (RecordingHealthCheckResponse);
}

// =============================================================================
// RECORDING SERVICE → SFU MESSAGES
// =============================================================================

message RecordingToSfu {
  string request_id = 1;

  oneof payload {
    // Registration
    RecordingServiceRegister register = 10;

    // Recording control
    StartRecordingRequest start_recording = 20;
    StopRecordingRequest stop_recording = 21;
    PauseRecordingRequest pause_recording = 22;
    ResumeRecordingRequest resume_recording = 23;

    // Track subscription
    SubscribeTrackRequest subscribe_track = 30;
    UnsubscribeTrackRequest unsubscribe_track = 31;

    // Control
    Heartbeat heartbeat = 40;
  }
}

// =============================================================================
// SFU → RECORDING SERVICE MESSAGES
// =============================================================================

message SfuToRecording {
  string request_id = 1;

  oneof payload {
    // Registration response
    RecordingServiceRegisterResponse register_response = 10;

    // Recording responses
    StartRecordingResponse start_recording_response = 20;
    TrackSubscribed track_subscribed = 21;

    // Room events (push from SFU)
    RecordingRoomEvent room_event = 30;

    // RTP packets (high frequency)
    RecordingRtpPacket rtp_packet = 40;

    // Control
    HeartbeatAck heartbeat_ack = 50;

    // Errors
    RecordingError error = 60;
  }
}

// =============================================================================
// REGISTRATION
// =============================================================================

message RecordingServiceRegister {
  string service_id = 1;
  string version = 2;
  repeated string capabilities = 3;    // "audio", "video", "screenshare"
  int32 max_concurrent_rooms = 4;
  int64 timestamp = 5;
}

message RecordingServiceRegisterResponse {
  bool success = 1;
  string sfu_id = 2;
  string message = 3;
  int32 active_rooms = 4;
  int64 timestamp = 5;
}

// =============================================================================
// RECORDING CONTROL (Policy-Aware)
// =============================================================================

message StartRecordingRequest {
  string room_id = 1;
  string requester_id = 2;             // Peer requesting recording
  RecordingConfig config = 3;
}

message RecordingConfig {
  bool record_audio = 1;
  bool record_video = 2;
  bool record_screenshare = 3;
  repeated string participant_ids = 4;  // Empty = all participants
  string storage_path = 5;              // Override default path
}

message StartRecordingResponse {
  bool success = 1;
  string recording_id = 2;
  string error_message = 3;
  repeated TrackInfo available_tracks = 4;

  // Policy information
  RecordingPolicy policy = 10;
}

message RecordingPolicy {
  bool enabled = 1;
  string who_can_record = 2;           // "host", "co_host", "panelist", "all"
  string who_can_download = 3;
  bool auto_record = 4;
  bool notify_participants = 5;
  int32 retention_days = 6;

  // Role information for authorization
  string host_id = 10;
  repeated string co_host_ids = 11;
  repeated string panelist_ids = 12;
}

message StopRecordingRequest {
  string room_id = 1;
  string recording_id = 2;
  string requester_id = 3;
  bool trigger_post_processing = 4;
}

message PauseRecordingRequest {
  string room_id = 1;
  string recording_id = 2;
}

message ResumeRecordingRequest {
  string room_id = 1;
  string recording_id = 2;
}

// =============================================================================
// TRACK SUBSCRIPTION
// =============================================================================

message SubscribeTrackRequest {
  string room_id = 1;
  string producer_id = 2;
  string recording_id = 3;
}

message UnsubscribeTrackRequest {
  string room_id = 1;
  string producer_id = 2;
  string recording_id = 3;
}

message TrackSubscribed {
  string room_id = 1;
  string producer_id = 2;
  string consumer_id = 3;
  TrackInfo track_info = 4;
}

message TrackInfo {
  string producer_id = 1;
  string peer_id = 2;
  string kind = 3;                      // "audio", "video"
  string track_type = 4;                // "mic", "camera", "screenshare"
  int32 payload_type = 5;
  uint32 ssrc = 6;
  string codec = 7;                     // "opus", "vp8", "vp9", "h264"
  int32 clock_rate = 8;
  int32 channels = 9;
  string display_name = 10;
  bool is_screenshare = 11;             // Detected via appData.share or peer_id prefix
}

// =============================================================================
// ROOM EVENTS
// =============================================================================

message RecordingRoomEvent {
  string room_id = 1;
  int64 timestamp = 2;

  oneof event {
    RoomCreated room_created = 10;
    RoomClosed room_closed = 11;
    RoomPolicyUpdated room_policy_updated = 12;  // Policy can change mid-session
    PeerJoined peer_joined = 13;
    PeerLeft peer_left = 14;
    ProducerCreated producer_created = 15;
    ProducerClosed producer_closed = 16;
    ProducerPaused producer_paused = 17;
    ProducerResumed producer_resumed = 18;
    ActiveSpeaker active_speaker = 19;
  }
}

message RoomCreated {
  string room_id = 1;
  string room_name = 2;
  string host_id = 3;
  string meeting_type = 4;
  RecordingPolicy policy = 5;          // Initial policy
  int64 timestamp = 6;
}

message RoomClosed {
  string room_id = 1;
  string reason = 2;
  int32 duration_seconds = 3;
  int64 timestamp = 4;
}

message RoomPolicyUpdated {
  string room_id = 1;
  RecordingPolicy policy = 2;
  int64 timestamp = 3;
}

message PeerJoined {
  string peer_id = 1;
  string display_name = 2;
  string device_flag = 3;
  bool is_host = 4;
  bool is_co_host = 5;
  bool is_panelist = 6;
  int64 timestamp = 7;
}

message PeerLeft {
  string peer_id = 1;
  string reason = 2;
  int32 duration_seconds = 3;
  int64 timestamp = 4;
}

message ProducerCreated {
  string producer_id = 1;
  string peer_id = 2;
  string kind = 3;
  string track_type = 4;
  int32 payload_type = 5;
  uint32 ssrc = 6;
  string codec = 7;
  int32 clock_rate = 8;
  int32 channels = 9;
  string display_name = 10;
  bool is_screenshare = 11;
  int64 timestamp = 12;
}

message ProducerClosed {
  string producer_id = 1;
  string peer_id = 2;
  string reason = 3;
  int64 timestamp = 4;
}

message ProducerPaused {
  string producer_id = 1;
  string peer_id = 2;
  int64 timestamp = 3;
}

message ProducerResumed {
  string producer_id = 1;
  string peer_id = 2;
  int64 timestamp = 3;
}

message ActiveSpeaker {
  string peer_id = 1;
  int32 volume = 2;
  int64 timestamp = 3;
}

// =============================================================================
// RTP PACKET
// =============================================================================

message RecordingRtpPacket {
  string room_id = 1;
  string recording_id = 2;
  string producer_id = 3;
  string peer_id = 4;
  bytes rtp_data = 5;                   // Raw RTP packet
  int32 payload_type = 6;
  uint32 ssrc = 7;
  int64 server_timestamp = 8;
  uint32 rtp_timestamp = 9;
  uint32 sequence_number = 10;
}

// =============================================================================
// CONTROL & ERROR
// =============================================================================

message Heartbeat {
  string service_id = 1;
  int64 timestamp = 2;
  int32 active_recordings = 3;
  int64 bytes_written = 4;
}

message HeartbeatAck {
  int64 timestamp = 1;
  int64 server_time = 2;
}

message RecordingHealthCheckRequest {
  string service_id = 1;
}

message RecordingHealthCheckResponse {
  bool healthy = 1;
  string service_id = 2;
  int32 active_recordings = 3;
  int64 total_bytes_written = 4;
  int64 uptime_seconds = 5;
}

message RecordingError {
  string room_id = 1;
  string recording_id = 2;
  int32 code = 3;
  string message = 4;
  string details = 5;
  int64 timestamp = 6;
}

// Error codes:
// 1xxx - Connection errors
// 2xxx - Room errors
// 3xxx - Track/Producer errors
// 4xxx - Storage errors
// 5xxx - Policy errors (5001: disabled, 5002: not authorized)
```

---

## 6. Technology Stack Decision

### 6.1 Recommended Stack: **Go**

| Component | Technology | Rationale |
|-----------|------------|-----------|
| Language | Go 1.23 | gRPC native, Pion RTP, efficient concurrency |
| RTP Parsing | pion/rtp | Production-grade, used by Discord/Twitch |
| gRPC | google.golang.org/grpc | First-class support |
| S3 Client | aws-sdk-go-v2 | MinIO compatible |
| Metrics | prometheus/client_golang | Standard observability |
| Logging | uber-go/zap | Structured JSON logs |
| FFmpeg | CLI subprocess | Proven reliability |

### 6.2 Architecture Stack

```
┌─────────────────────────────────────────────────────────────────┐
│                    TECHNOLOGY STACK                              │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  Recording Service (Go)                                          │
│  ├── gRPC: google.golang.org/grpc                                │
│  ├── RTP Parsing: github.com/pion/rtp                            │
│  ├── S3 Client: github.com/aws/aws-sdk-go-v2 (MinIO compat)      │
│  ├── Metrics: github.com/prometheus/client_golang                │
│  └── Logging: go.uber.org/zap                                    │
│                                                                  │
│  Post-Processing Service (Go + FFmpeg)                           │
│  ├── FFmpeg: CLI subprocess                                      │
│  ├── Job Queue: Redis (redis:6380)                               │
│  └── Layout Engine: Custom Go filter_complex generator           │
│                                                                  │
│  Storage                                                         │
│  ├── Primary: MinIO (minio:9100) - recordings-private bucket     │
│  ├── Local Fallback: /recordings volume mount                    │
│  └── Metadata: Stored in MinIO as JSON                           │
│                                                                  │
│  Container                                                       │
│  ├── Base: golang:1.23-alpine                                    │
│  ├── FFmpeg: static build (~50MB)                                │
│  └── Total Image: ~100MB                                         │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

---

## 7. Data Flow & Storage

### 7.1 Storage Format

#### Directory Structure (MinIO)

```
minio:9100/recordings-private/
└── rooms/
    └── {room_id}/
        ├── metadata.json              # Room & participant metadata
        ├── timeline.json              # Event timeline for sync
        ├── policy.json                # Room policy snapshot
        ├── tracks/
        │   ├── {peer_id}-audio.rtp    # Raw RTP with custom header
        │   ├── {peer_id}-video.rtp    # Raw RTP with custom header
        │   └── {peer_id}-screen.rtp   # Screenshare track
        └── processed/                  # Post-processing outputs
            ├── composite.mp4
            ├── audio-only.mp3
            └── thumbnails/
```

#### Metadata JSON Schema

```json
{
  "$schema": "recording-metadata-v1",
  "room": {
    "room_id": "abc-123-def",
    "room_name": "Team Standup",
    "host_id": "user_456",
    "meeting_type": "scheduled",
    "started_at": "2024-01-15T10:00:00Z",
    "ended_at": "2024-01-15T10:45:00Z",
    "duration_seconds": 2700
  },
  "policy_snapshot": {
    "recording_enabled": true,
    "who_can_record": "host",
    "auto_record": false,
    "retention_days": 30
  },
  "participants": [
    {
      "peer_id": "peer_001",
      "display_name": "Alice",
      "joined_at": "2024-01-15T10:00:05Z",
      "left_at": "2024-01-15T10:44:30Z",
      "is_host": true,
      "is_co_host": false,
      "tracks": [
        {
          "track_id": "peer_001-audio",
          "kind": "audio",
          "codec": "opus",
          "file": "tracks/peer_001-audio.rtp",
          "started_at": "2024-01-15T10:00:10Z",
          "ended_at": "2024-01-15T10:44:30Z",
          "bytes": 15234567
        }
      ]
    }
  ],
  "timeline": {
    "events": [
      {"t": 0, "type": "room_created"},
      {"t": 5000, "type": "peer_joined", "peer_id": "peer_001", "is_host": true},
      {"t": 10000, "type": "recording_started", "requester_id": "peer_001"},
      {"t": 12000, "type": "producer_started", "peer_id": "peer_001", "kind": "audio"},
      {"t": 2700000, "type": "recording_stopped"},
      {"t": 2700000, "type": "room_closed"}
    ]
  },
  "recording": {
    "recording_id": "rec_789",
    "started_by": "peer_001",
    "service_version": "1.0.0"
  }
}
```

---

## 8. Post-Processing Pipeline

### 8.1 Job Queue Architecture (Redis 6380)

```
┌─────────────────────────────────────────────────────────────────┐
│                    JOB QUEUE ARCHITECTURE                        │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  Redis (recording-redis:6380)                                    │
│  └── Queue: recording:jobs:pending                               │
│      └── Jobs: [{room_id, layout, priority, created_at}, ...]   │
│                                                                  │
│  ┌─────────────────────────────────────────────────────────────┐ │
│  │ Post-Processing Workers (N instances)                        │ │
│  │                                                              │ │
│  │ ┌─────────┐ ┌─────────┐ ┌─────────┐                         │ │
│  │ │Worker 1 │ │Worker 2 │ │Worker 3 │                         │ │
│  │ │(FFmpeg) │ │(FFmpeg) │ │(FFmpeg) │                         │ │
│  │ └─────────┘ └─────────┘ └─────────┘                         │ │
│  │                                                              │ │
│  │ Workflow:                                                    │ │
│  │ 1. BLPOP recording:jobs:pending                              │ │
│  │ 2. Fetch raw tracks from MinIO (minio:9100)                  │ │
│  │ 3. Run FFmpeg pipeline                                       │ │
│  │ 4. Upload result to MinIO                                    │ │
│  │ 5. Notify completion (webhook/pubsub)                        │ │
│  └─────────────────────────────────────────────────────────────┘ │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

---

## 9. Implementation Phases

### Phase 1: Core Recording (MVP) - 2-3 weeks

**Deliverables:**
- [ ] Recording Service gRPC server (Go)
- [ ] SFU integration (recording-server.js)
- [ ] Proto file compilation
- [ ] Basic RTP file writer
- [ ] MinIO integration (recordings-private bucket)
- [ ] Policy validation (enabled, who_can_record)
- [ ] Docker container
- [ ] Basic health check

**Directory Structure:**
```
recording/
├── cmd/
│   └── recording-service/
│       └── main.go
├── internal/
│   ├── grpc/
│   │   ├── server.go
│   │   └── handlers.go
│   ├── recording/
│   │   ├── manager.go
│   │   ├── room.go
│   │   ├── track.go
│   │   └── policy.go           # Policy enforcement
│   ├── storage/
│   │   ├── interface.go
│   │   └── minio.go
│   └── rtp/
│       ├── parser.go
│       └── writer.go
├── proto/
│   └── recording_sfu.proto
├── Dockerfile
├── docker-compose.yml
└── config.yaml
```

### Phase 2: Storage & Reliability - 1-2 weeks

**Deliverables:**
- [ ] S3 multipart upload to MinIO
- [ ] Track recovery on service restart
- [ ] Prometheus metrics endpoint
- [ ] Structured logging (JSON)
- [ ] Auto-record support

### Phase 3: Post-Processing Service - 2-3 weeks

**Deliverables:**
- [ ] RTP to raw media extraction
- [ ] Timeline synchronization
- [ ] FFmpeg pipeline wrapper
- [ ] Grid/Speaker layouts
- [ ] Job queue (Redis 6380)
- [ ] Worker service

### Phase 4: Advanced Features - 2-4 weeks

**Deliverables:**
- [ ] Custom layout API
- [ ] Transcript integration
- [ ] Thumbnail generation
- [ ] Recording API (frontend integration)
- [ ] Download authorization (who_can_download policy)

---

## 10. Alternative Approaches

### Comparison Table

| Approach | Dev Time | Cost/hr (50 users) | Flexibility | Policy Support |
|----------|----------|-------------------|-------------|----------------|
| Custom (Go) | 8-12 weeks | $0.50 (compute) | High | Full |
| Jibri-style | 4-6 weeks | $2.00 (VMs) | Low | Limited |
| Cloud Service | 1 week | $30-60 | Low | None |

**Recommendation:** Custom Go service for best flexibility/cost/policy balance.

---

## Appendix A: Docker Configuration

### docker-compose.yml

```yaml
version: '3.8'

services:
  recording-service:
    build:
      context: .
      dockerfile: Dockerfile
    container_name: recording-service
    ports:
      - "50054:50054"
    environment:
      # gRPC
      RECORDING_GRPC_PORT: "50054"
      RECORDING_SFU_HOST: "sfu"
      RECORDING_SFU_PORT: "50055"

      # MinIO (reuse existing)
      RECORDING_S3_ENDPOINT: "minio:9100"
      RECORDING_S3_BUCKET: "recordings-private"
      RECORDING_S3_ACCESS_KEY: "${MINIO_ACCESS_KEY:-minioadmin}"
      RECORDING_S3_SECRET_KEY: "${MINIO_SECRET_KEY:-minioadmin123}"
      RECORDING_S3_USE_SSL: "false"

      # Redis (separate instance)
      RECORDING_REDIS_HOST: "recording-redis"
      RECORDING_REDIS_PORT: "6379"

      # Logging
      RECORDING_LOG_LEVEL: "info"
      RECORDING_LOG_FORMAT: "json"
    volumes:
      - recording-data:/recordings
    depends_on:
      recording-redis:
        condition: service_healthy
    networks:
      - brollyhub-central_brollyhub_network
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://localhost:50054/health"]
      interval: 30s
      timeout: 10s
      retries: 3

  recording-redis:
    image: redis:7-alpine
    container_name: recording-redis
    ports:
      - "127.0.0.1:6380:6379"  # Different port from brollyhub redis (6378)
    volumes:
      - recording-redis-data:/data
    networks:
      - brollyhub-central_brollyhub_network
    restart: unless-stopped
    healthcheck:
      test: ["CMD-SHELL", "redis-cli ping || exit 1"]
      interval: 5s
      timeout: 3s
      retries: 10

  post-processor:
    build:
      context: .
      dockerfile: Dockerfile.worker
    container_name: post-processor
    environment:
      # Redis (same as recording service)
      PROCESSOR_REDIS_HOST: "recording-redis"
      PROCESSOR_REDIS_PORT: "6379"

      # MinIO (reuse existing)
      PROCESSOR_S3_ENDPOINT: "minio:9100"
      PROCESSOR_S3_BUCKET: "recordings-private"
      PROCESSOR_S3_ACCESS_KEY: "${MINIO_ACCESS_KEY:-minioadmin}"
      PROCESSOR_S3_SECRET_KEY: "${MINIO_SECRET_KEY:-minioadmin123}"

      PROCESSOR_WORKERS: "2"
    depends_on:
      recording-redis:
        condition: service_healthy
    networks:
      - brollyhub-central_brollyhub_network
    restart: unless-stopped

  # MinIO bucket initialization (one-time)
  recording-minio-init:
    image: minio/mc:latest
    container_name: recording-minio-init
    entrypoint: >
      /bin/sh -c "
      echo 'Waiting for MinIO...';
      sleep 5;
      /usr/bin/mc alias set myminio http://minio:9100 $${MINIO_ACCESS_KEY:-minioadmin} $${MINIO_SECRET_KEY:-minioadmin123};
      echo 'Recording MinIO initialization!';
      /usr/bin/mc mb -p myminio/recordings-private 2>/dev/null || echo '✓ Bucket recordings-private exists';
      echo '✓ Recording MinIO initialization complete!';
      "
    networks:
      - brollyhub-central_brollyhub_network
    depends_on:
      - recording-service

volumes:
  recording-data:
  recording-redis-data:

networks:
  brollyhub-central_brollyhub_network:
    external: true
```

### Dockerfile

```dockerfile
# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git protoc protobuf-dev

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
RUN go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
RUN protoc --go_out=. --go-grpc_out=. proto/*.proto

RUN CGO_ENABLED=0 GOOS=linux go build -o /recording-service ./cmd/recording-service

# Runtime stage
FROM alpine:3.19

RUN apk add --no-cache ffmpeg ca-certificates

WORKDIR /app

COPY --from=builder /recording-service .

ENV RECORDING_GRPC_PORT=50054

EXPOSE 50054

ENTRYPOINT ["/app/recording-service"]
```

---

## Appendix B: Environment Variables Summary

| Variable | Default | Description |
|----------|---------|-------------|
| `RECORDING_GRPC_PORT` | 50054 | Recording service gRPC port |
| `RECORDING_SFU_HOST` | sfu | SFU hostname |
| `RECORDING_SFU_PORT` | 50055 | SFU recording gRPC port |
| `RECORDING_S3_ENDPOINT` | minio:9100 | MinIO endpoint |
| `RECORDING_S3_BUCKET` | recordings-private | Storage bucket |
| `RECORDING_S3_ACCESS_KEY` | minioadmin | MinIO access key |
| `RECORDING_S3_SECRET_KEY` | minioadmin123 | MinIO secret key |
| `RECORDING_REDIS_HOST` | recording-redis | Redis host |
| `RECORDING_REDIS_PORT` | 6379 | Redis port (internal) |
| `RECORDING_LOG_LEVEL` | info | Log level |
| `RECORDING_LOG_FORMAT` | json | Log format |

---

*Document Version: 1.1*
*Created: 2024-01-15*
*Updated: Integration with brollyhub-central infrastructure*
