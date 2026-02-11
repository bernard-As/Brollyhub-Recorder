# Recording End-to-End Flow (Recording Service -> Shelves -> Huddle UI)

## Purpose
This document describes the full recording pipeline across:
- Recording Service (Go)
- Shelves backend (Django + gRPC)
- Huddle frontend (room shelf UI + file preview)

It is intended to show how RTP is captured, written to MinIO/S3, how metadata is upserted into Shelves, and how the UI exposes a virtual "Meeting Recording" item that opens the Recording Composer UI.

This reflects the current codebase behavior as of 2026-02-07.

---

## High-Level Data Flow

1) SFU (MediaSoup) sends RTP + room events to Recording Service (gRPC).
2) Recording Service writes raw track data to MinIO (S3-compatible) and snapshots metadata/timeline.
3) If `auto_record_quick_access=true`, the live recorder:
   - rotates segments at fixed part boundaries,
   - snapshots metadata/timeline, and
   - queues Compositer exports for each part.
4) Recording Service notifies Shelves backend via gRPC with:
   - room_id, recording_id, status, start/stop times
   - s3_prefix, metadata_key, timeline_key
   - quick_access_parts, late_composite_parts (when available)
5) If `auto_record_late_composite=true`, final composited parts are queued at stop time.
6) Shelves backend stores RoomRecording + parts and injects a virtual "Recording" item into room shelf responses.
7) Huddle frontend renders that virtual item in the room shelf and opens a Recording Composer UI in File Preview.

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
- During recording (if quick access enabled):
  - Periodic part boundary timers rotate segments and snapshot metadata/timeline.
  - Compositer exports are queued per part.
- StopRecording => finalize tracks, write metadata + timeline + policy, write .completed marker.
  - Build quick access + late composite parts lists for the full duration.
  - Queue any remaining exports.

### Compositer Integration (Quick Access + Late Composite)
When quick-access or late-composite is enabled in policy, the Recording Service
queues Compositer exports with an explicit output key for each part.

**Policy Fields (recording):**
- `auto_record_quick_access` (bool) → enable live part exports
- `auto_record_late_composite` (bool) → enable post-stop part exports
- `quick_access_part_duration_sec` (int, default 720)
- `late_composite_part_duration_sec` (int, default 720)
- `quick_access_layout` (string, default `grid`)

**Job Types:**
- `quick_access`: created during live recording at each part boundary
- `late_composite`: created after recording stops for final exports

**Export Requests (Compositer):**
- `format=mp4`, `layout=grid` (or `quick_access_layout`)
- `start_time_sec`, `end_time_sec` derived from part offsets
- `output_key` set to the part path (ensures deterministic storage)

**Auth:**
- Recording Service uses `X-Compositer-Service-Key` and `X-Compositer-Room-Id`.

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
  quick_access/
    part-0001.mp4
    part-0002.mp4
  processed/exports/
    part-0001.mp4
    part-0002.mp4
```

Generated in:
- `D:\recording\internal\storage\minio.go`

### Metadata and Timeline
- `metadata.json` contains participants, tracks, stats, and timestamps.
- `timeline.json` contains chronological room events (peer joined/left, producer events, active speaker, etc.).
- `policy.json` is a snapshot of the room recording policy at start time.
- During live recording, metadata/timeline are re-snapshotted at quick-access part boundaries to enable partial exports.

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
- quick_access_parts (optional)
- late_composite_parts (optional)

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
    available: status == "completed",
    quick_access_parts: [...],
    late_composite_parts: [...]
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

Quick access + late composite parts are now produced by Compositer and exposed via Shelves.
The UI can use those parts to show short previews or post-meeting exports.

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
- Quick access/late composite parts may be present even while status is `recording`.

---

## Suggested Verification

1) Start a recording in a room.
2) Confirm MinIO objects exist:
   - rooms/{room_id}/{recording_id}/metadata.json
   - rooms/{room_id}/{recording_id}/timeline.json
   - rooms/{room_id}/{recording_id}/tracks/*.rtp
3) If quick access is enabled, confirm parts appear:
   - rooms/{room_id}/{recording_id}/quick_access/part-0001.mp4
4) Confirm Shelves received gRPC upsert:
   - RoomRecording row exists with s3_prefix/metadata_key/timeline_key.
   - quick_access_parts / late_composite_parts arrays present when enabled
5) Fetch room shelf detail and verify:
   - latest_recording is populated
   - virtual Recording item present in items list
6) In Huddle UI:
   - Recording item appears in Meeting Files
   - Clicking opens Recording Composer UI

---

## Known Gaps (Expected Future Work)

- No per-peer HLS preview for interactive composition (still a planned path).
- The UI does not yet implement a full timeline-based playback view.

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
