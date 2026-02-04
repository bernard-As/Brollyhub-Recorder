# Repository Guidelines

This repository contains the Brollyhub recording service, a Go-based gRPC service that captures RTP tracks and stores recordings for post-processing. Use this guide to keep contributions consistent and easy to review.

## Project Structure & Module Organization
- `cmd/recording-service/` entry point and process lifecycle.
- `internal/` core service logic (config, gRPC, recording, storage, RTP parsing/writing).
- `proto/` gRPC/protobuf definitions and generated Go code.
- `scripts/` protobuf generation helpers.
- `docs/` architecture and feature documentation.
- `config.yaml`, `Dockerfile`, `docker-compose.yml` runtime and container configuration.

## Build, Test, and Development Commands
- `docker-compose up -d --build` build and run the stack locally.
- `docker-compose logs -f recording-service` follow service logs.
- `go build -o recording-service ./cmd/recording-service` build the binary.
- `./recording-service -config config.yaml` run the service locally.
- `powershell -ExecutionPolicy Bypass -File scripts/generate-proto.ps1` regenerate protobufs on Windows.
- `docker-compose build` generates protobufs during Docker builds.
- `curl http://localhost:50076/health` check service health.

## Coding Style & Naming Conventions
- Use standard Go formatting via `gofmt` (tabs, not spaces).
- Follow Go naming: exported identifiers are `CamelCase`, unexported are `lowerCamel`.
- Keep package boundaries aligned with `internal/` modules; avoid cross-cutting imports that bypass the intended layering.

## Testing Guidelines
- No `*_test.go` files are currently in the repo.
- When adding tests, use Go’s standard test layout (`*_test.go`) and run `go test ./...`.

## Commit & Pull Request Guidelines
- Recent commit messages are a single `.` with no formal convention; prefer descriptive, imperative summaries (for example `Add MinIO retry logic`).
- PRs should include a short summary, the exact commands used to test, and any config or doc updates.
- If protobufs or APIs change, include regenerated files in `proto/` and mention it in the PR description.

## Configuration Notes
- Runtime settings live in `config.yaml` and can be overridden by environment variables (see `CLAUDE.md`).
- Local dependencies include MinIO and Redis via `docker-compose.yml`.
