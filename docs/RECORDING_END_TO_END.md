# Recording End-to-End Flow (Recording Service -> Shelves -> Huddle UI)

## Purpose
This document describes the full recording pipeline across:
- Recording Service (Go)
- Shelves backend (Django + gRPC)
- Huddle frontend (room shelf UI + file preview)

It is intended to show how RTP is captured, written to MinIO/S3, how metadata is upserted into Shelves, and how the UI exposes a virtual "Meeting Recording" item that opens the Recording Composer UI.

This reflects the current codebase behavior as of 2026-02-01.

---

## High-Level Data Flow

1) SFU (MediaSoup) sends RTP + room events to Recording Service (gRPC).
2) Recording Service writes raw track data + metadata/timeline/policy to MinIO (S3-compatible).
3) Recording Service notifies Shelves backend via gRPC with:
   - room_id, recording_id
   - status, start/stop times
   - s3_prefix, metadata_key, timeline_key
4) Shelves backend stores a RoomRecording row and injects a virtual "Recording" item into room shelf responses.
5) Huddle frontend renders that virtual item in the room shelf and opens a Recording Composer UI in File Preview.

---

## Recording Service (Go)

### Key Components
- gRPC server: `D:\recording\internal\grpc\server.go`
- Message handlers: `D:\recording\internal\grpc\handlers.go`
- Recording manager: `D:\recording\internal\recording\manager.go`
- Room recording logic: `D:\recording\internal\recording\room.go`
- MinIO/S3 storage: `D:\recording\internal\storage\minio.go`
- Shelves gRPC client: `D:\recording\internal\shelves\client.go`

### Recording Lifecycle
- StartRecording (from SFU) => create RoomRecording, start storage context, open track writers.
- Track subscribe events create track writers (per producer).
- RTP packets are written to track writers (streamed S3 multipart upload).
- StopRecording => finalize tracks, write metadata + timeline + policy, write .completed marker.

### Storage Layout (MinIO/S3)
In `MinIOStorage.recordingPath(...)`:

```
rooms/{room_id}/{recording_id}/
  .recording
  .completed
  metadata.json
  timeline.json
  policy.json
  tracks/
    {peer_id}-{track_type}-{segment_index}.rtp
```

Generated in:
- `D:\recording\internal\storage\minio.go`

### Metadata and Timeline
- `metadata.json` contains participants, tracks, stats, and timestamps.
- `timeline.json` contains chronological room events (peer joined/left, producer events, active speaker, etc.).
- `policy.json` is a snapshot of the room recording policy at start time.

Written in:
- `D:\recording\internal\recording\room.go`
- `D:\recording\internal\storage\minio.go`

---

## Shelves Integration (gRPC)

### Proto
- `D:\shelves\backend\grpc_server\proto\recording.proto`

RPC:
```
UpsertRoomRecording(UpsertRoomRecordingRequest)
```
Fields:
- room_id
- recording_id
- status
- started_at (ms)
- completed_at (ms)
- s3_prefix
- metadata_key
- timeline_key
- service_id

### Recording Service -> Shelves Call
In `D:\recording\internal\grpc\handlers.go`:

```
notifyRoomRecording(roomID, recordingID, status, startedAt, completedAt)
```
Builds:
- s3_prefix   = rooms/{room_id}/{recording_id}/
- metadata_key = rooms/{room_id}/{recording_id}/metadata.json
- timeline_key = rooms/{room_id}/{recording_id}/timeline.json

Then calls:
- `s.shelvesClient.UpsertRoomRecording(...)`
- Client implementation: `D:\recording\internal\shelves\client.go`

### Shelves Storage
`D:\shelves\backend\shelves\models.py`
- `RoomRecording` model stores:
  - room_id, recording_id
  - status, started_at, completed_at
  - s3_prefix, metadata_key, timeline_key

gRPC handler:
- `D:\shelves\backend\grpc_server\servicers\recording_servicer.py`
- `UpsertRoomRecording(...)` upserts RoomRecording.

---

## Shelves API: Room Shelf Response

`D:\shelves\backend\shelves\serializers.py`

When a Shelf is linked to a RoomShelf:
- It injects a virtual "Recording" item into the shelf items list.
- It also provides fields:
  - recordings_available
  - recordings_count
  - latest_recording

The virtual item includes:
```
{
  id: "recording:{room_id}",
  name: "Meeting Recording",
  type: "Recording",
  is_virtual: true,
  recording: {
    room_id,
    recording_id,
    status,
    started_at,
    completed_at,
    available: status == "completed"
  }
}
```

---

## Huddle Frontend

### Shelf Item Rendering
- Room shelves are loaded in:
  - `D:\huddle\frontend\src\shared\store\room\shelves\index.ts`
- The "Recording" type is supported as a `ShelfFile`.

Display paths:
- Grid/List/Bookcase:
  - `D:\huddle\frontend\src\pages\room\components\MainContent\sideBar\shelves\components\FileGridItem.tsx`
  - `D:\huddle\frontend\src\pages\room\components\MainContent\sideBar\shelves\components\FileListItem.tsx`

Clicking the item triggers:
```
sharedMediaLayoutStore.enableFilePreview({
  ...,
  type: "Recording",
  recording: { ... }
})
```

### File Preview
- `D:\huddle\frontend\src\pages\room\components\MainContent\VideoWrapper\ExtraMedia\FilePreview\index.tsx`
- If `file.type === "Recording"`, it renders the Recording Composer UI.

### Recording Composer (UI Only)
- `D:\huddle\frontend\src\pages\room\components\MainContent\VideoWrapper\ExtraMedia\FilePreview\RecordingComposer.tsx`
- Status UI:
  - Processing if `status === "recording"` or `available === false`
  - Ready if `status === "completed"`
  - Failed if `status === "failed"`

Currently there is no backend API call to fetch a composed preview or MP4. This is a UI placeholder for a future post-processing pipeline.

---

## MinIO/S3 Configuration

### Recording Service
Configured in:
- `D:\recording\internal\config\config.go`
- `D:\recording\config.yaml`
- `D:\recording\docker-compose.yml`

Important env/config values:
- RECORDING_S3_ENDPOINT
- RECORDING_S3_BUCKET (default: recordings-private)
- RECORDING_S3_ACCESS_KEY
- RECORDING_S3_SECRET_KEY
- RECORDING_S3_USE_SSL

### Shelves Backend (for files, not recordings)
Configured in:
- `D:\shelves\backend\shelves_backend\settings.py`
- `D:\shelves\backend\shelves\minio_security.py`

Note: Shelves stores normal files in shelves-private bucket. Recording artifacts remain in recordings-private and are accessed via s3_prefix/metadata/timeline keys, not through Item records.

---

## Status and Failure Handling

### Recording Service
- Status transitions:
  - recording -> completed (normal stop)
  - recording -> failed (SFU disconnect or finalize error)
- On SFU disconnect, active recordings are stopped and Shelves is notified with status FAILED.

### Shelves Backend
- RoomRecording is updated by gRPC events.
- Only the latest recording per room is exposed in the virtual shelf item.

### UI
- Recording item appears only when `latest_recording` exists.
- Recording composer shows status but does not fetch media output.

---

## Suggested Verification

1) Start a recording in a room.
2) Confirm MinIO objects exist:
   - rooms/{room_id}/{recording_id}/metadata.json
   - rooms/{room_id}/{recording_id}/timeline.json
   - rooms/{room_id}/{recording_id}/tracks/*.rtp
3) Confirm Shelves received gRPC upsert:
   - RoomRecording row exists with s3_prefix/metadata_key/timeline_key.
4) Fetch room shelf detail and verify:
   - latest_recording is populated
   - virtual Recording item present in items list
5) In Huddle UI:
   - Recording item appears in Meeting Files
   - Clicking opens Recording Composer UI

---

## Known Gaps (Expected Future Work)

- No API to generate or stream a composed MP4 preview from raw RTP.
- No UI wiring to request a composed preview from backend.
- Composition/pipeline is described in architecture docs but not yet implemented.

---

## Composition Architecture (Current Plan)

The agreed plan is a **Hybrid composition model**:
- Interactive preview in the browser (per-peer HLS streams).
- Final export recomputed server-side for quality and consistency.

See:
- `D:\recording\docs\RECORDING_COMPOSITION.md`

---

## Related Docs
- `D:\recording\docs\RECORDING-SERVICE.md`
- `D:\recording\docs\ARCHITECTURE-PLAN.md`
- `D:\RECORDING_INTEGRATION_SUMMARY.md`
- `D:\ROOM_SHELF_SETUP_GUIDE.md`
