# Recording Composition Architecture (Hybrid Plan)

## Purpose
This document describes the agreed plan for interactive recording composition and export:
- **Hybrid workflow**: browser-based composition for fast, interactive preview.
- **Server-side export**: recompute the final MP4 from source tracks for quality and consistency.

This plan complements the existing end-to-end flow and is intended to be implemented incrementally.

---

## Summary of the Plan (Option D: Hybrid)

1) **Ingest + Storage (already exists)**
   - RTP is captured by Recording Service and stored in MinIO under:
     `rooms/{room_id}/{recording_id}/tracks/`
   - Metadata, timeline, and policy are stored at:
     `metadata.json`, `timeline.json`, `policy.json`

2) **Preview Assets (new)**
   - A composition worker generates **per-peer muxed A/V preview assets** (HLS fMP4).
   - This allows the browser to seek and scrub smoothly while laying out tracks in canvas/WebGL.

3) **Interactive Composition (new, frontend)**
   - Frontend renders **one element per peer** (plus screenshare).
   - User can scrub time, pin speakers, and reposition layout.
   - Layout choices are saved as a **Composition State** object.

4) **Final Export (new, server)**
   - Export recomputes the final MP4 from source tracks (raw RTP or decoded masters).
   - Uses the same Composition State to ensure consistent output.

---

## Architecture: Services and Responsibilities

### Recording Service (Go)
- No change in responsibility.
- Continues to ingest RTP and write metadata/timeline/policy.

### Composition Worker (new service)
- Runs FFmpeg jobs to:
  - Convert RTP tracks into **preview assets** (per-peer HLS).
  - Generate export MP4 using Composition State.
- Uses Redis (or similar) as a job queue.

### Shelves Backend (Django)
- Stores room recording metadata and exposes virtual "Recording" item in shelves.
- Will own new API endpoints for composition status and export URLs.

### Huddle Frontend
- Renders preview composition using per-peer muxed A/V streams.
- Saves Composition State and requests export.

---

## Delivery Methods (Decision)

### Media Delivery
- **HTTP-based HLS (CMAF/fMP4)** for preview playback and seeking.
- Reasons:
  - Browser-friendly, CDN/cacheable.
  - Supports random access and scrubbing.
  - Scales better than RTP replay.

### Control Plane
- **HTTP REST** for job creation and layout updates.
- **WebSocket or SSE** for job progress updates (optional).

### Internal Service Communication
- **gRPC** remains for service-to-service calls (as today).

---

## Preview Asset Strategy

### Per-Peer Muxed A/V
To reduce client load, each peer’s audio and video are muxed together:
- 1 stream per participant:
  `rooms/{room_id}/{recording_id}/preview/peers/{peer_id}/av.m3u8`
- Screenshare (often separate timeline/bitrate):
  `rooms/{room_id}/{recording_id}/preview/screenshare/{peer_id}/video.m3u8`

Optional:
- Audio-only stream for advanced layouts:
  `.../preview/peers/{peer_id}/audio.m3u8`

### Segment Settings (recommended baseline)
- Segment duration: 2s or 4s.
- Keyframe interval aligns with segment duration.
- Codec: H.264 + AAC for broad compatibility.

---

## Composition State (Frontend-Saved)

The frontend should save a JSON object that can be reused by export:

```json
{
  "recording_id": "uuid",
  "room_id": "room-123",
  "layout": "speaker",
  "timeline": [
    {"t": 0, "type": "layout", "layout": "grid"},
    {"t": 120000, "type": "pin", "peer_id": "peer-7"}
  ],
  "tracks": [
    {"peer_id": "peer-1", "muted": false},
    {"peer_id": "peer-2", "muted": true}
  ],
  "export": {
    "resolution": "1920x1080",
    "fps": 30
  }
}
```

This state is:
- Used by the browser to render preview.
- Sent to the server to drive export.

---

## Export Pipeline (Server)

1) Load `metadata.json`, `timeline.json`, and Composition State.
2) Decode source tracks (raw RTP or decoded masters).
3) Apply layout transitions and pinning logic (FFmpeg filter_complex).
4) Render final MP4.
5) Upload export to:
   `rooms/{room_id}/{recording_id}/processed/exports/{export_id}.mp4`

Optional:
- Generate thumbnails and/or audio-only exports.

---

## API Sketch (Shelves or Recording Backend)

### Create Preview Assets
`POST /recordings/{recording_id}/preview`
- Enqueues preview generation job.

### Save Composition State
`PUT /recordings/{recording_id}/composition`
- Stores the latest layout configuration.

### Export
`POST /recordings/{recording_id}/export`
- Enqueues export job based on saved composition state.

### Status
`GET /recordings/{recording_id}`
- Returns:
  - preview_status
  - export_status
  - preview URLs
  - export URLs

---

## API Contracts (Detailed)

All endpoints are owned by Shelves (or a dedicated Recording API gateway).

### 1) Issue Composition Token
`POST /recordings/{recording_id}/composition/token`

Response:
```json
{
  "token": "eyJhbGciOi...",
  "expires_at": "2026-02-01T10:30:00Z",
  "scope": ["preview:read", "composition:write", "export:create"],
  "recording_id": "rec-123",
  "room_id": "room-456"
}
```

### 2) Create Preview Assets
`POST /recordings/{recording_id}/preview`

Request:
```json
{
  "preset": "standard", 
  "segment_seconds": 4,
  "mux_per_peer": true
}
```

Response:
```json
{
  "job_id": "job-123",
  "status": "queued"
}
```

### 3) Save Composition State
`PUT /recordings/{recording_id}/composition`

Request:
```json
{
  "layout": "speaker",
  "timeline": [
    {"t": 0, "type": "layout", "layout": "grid"},
    {"t": 120000, "type": "pin", "peer_id": "peer-7"}
  ],
  "tracks": [
    {"peer_id": "peer-1", "muted": false},
    {"peer_id": "peer-2", "muted": true}
  ],
  "export": {"resolution": "1920x1080", "fps": 30}
}
```

Response:
```json
{
  "status": "ok",
  "updated_at": "2026-02-01T10:05:00Z"
}
```

### 4) Export Final MP4
`POST /recordings/{recording_id}/export`

Request:
```json
{
  "composition_id": "latest",
  "preset": "high",
  "format": "mp4"
}
```

Response:
```json
{
  "job_id": "job-789",
  "status": "queued"
}
```

### 5) Get Recording Status
`GET /recordings/{recording_id}`

Response:
```json
{
  "recording_id": "rec-123",
  "room_id": "room-456",
  "preview_status": "ready",
  "export_status": "processing",
  "preview": {
    "peers": {
      "peer-1": {
        "av": "https://.../preview/peers/peer-1/av.m3u8"
      }
    },
    "screenshare": {}
  },
  "export": {
    "url": null
  }
}
```

### Status Enums
- `queued`, `processing`, `ready`, `failed`

---

## Storage Layout (Preview + Export)

Base prefix:
```
rooms/{room_id}/{recording_id}/
```

Preview:
```
preview/
  peers/{peer_id}/
    av.m3u8
    av_0001.m4s
    av_0002.m4s
  screenshare/{peer_id}/
    video.m3u8
    video_0001.m4s
```

Export:
```
processed/
  exports/{export_id}.mp4
  thumbnails/{export_id}/thumb_0001.jpg
```

---

## Signed URL Rules

- All preview/export URLs are signed (short TTL, e.g., 5–15 minutes).
- URLs are minted by Shelves (or API gateway) after access checks.
- Composition Service should never expose raw MinIO URLs directly.

---

## Authentication (Single-Use Token)

If composition is a separate service, use a **single-use token** issued by the backend
to authorize preview/export requests without exposing user credentials.

### Token Requirements
- **Single-use**: invalidated after first successful request.
- **Short TTL**: 30 minutes (rebuild after expiry).
- **Scoped**: includes `recording_id`, `room_id`, and permitted actions
  (e.g., `preview:read`, `composition:write`, `export:create`).
- **Audience**: `composition-service` to prevent token reuse elsewhere.

### Suggested Flow
1) Frontend requests a composition token from Shelves:
   `POST /recordings/{recording_id}/composition/token`
2) Shelves validates user access (room policy + shelf access) and issues token.
3) Frontend calls Composition Service with:
   - `Authorization: Bearer <token>`
4) Composition Service verifies token via gRPC to Shelves and **burns** it (single-use).
5) If token is expired (30 minutes), frontend requests a new token from Shelves.

### Sequence Diagram

```
User/Frontend        Shelves Backend           Composition Service
     |                     |                           |
     | POST /composition/token (recording_id)          |
     |-------------------->|                           |
     |   validate access   |                           |
     |   issue token       |                           |
     |<--------------------|                           |
     |                                                     |
     |  Authorization: Bearer <token>                      |
     |----------------------------------------------->     |
     |                                  gRPC verify token  |
     |                                  (and burn once)    |
     |                               -------------------->|
     |                               <--------------------|
     |          start serving preview/export responses     |
     |<----------------------------------------------      |
```

### Verification Strategy
- Store token hash in Redis with a 30-minute TTL.
- On first use, delete the key atomically.
- Reject replay attempts.

### Notes
- For HLS media URLs, use signed URLs tied to the same token scope or
  issue per-request signed URLs from the backend.
- Export jobs should require a fresh single-use token even if preview was authorized.

---

## Key Considerations

### Time Alignment
- Every asset must share a common "recording time zero."
- Preview segments must align across participants for accurate scrubbing.

### Performance
- Limit concurrent peer streams in the browser.
- Use lower bitrate for non-active speakers.

### Security & Access
- Access should respect room policy and shelf permissions.
- Use signed URLs for preview and export assets.

### Consistency
- Preview should be close to export output, but final export is authoritative.

### Scaling
- Composition worker should be isolated from SFU and Recording Service.
- Use autoscaling workers (CPU/GPU) for heavy export jobs.

---

## Incremental Implementation Path

1) Add preview generation job + per-peer HLS outputs.
2) UI: use per-peer HLS in composer view.
3) Save Composition State to backend.
4) Export pipeline using Composition State + FFmpeg.
