# Build stage with proto generation
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Install protoc and required tools
RUN apk add --no-cache git protobuf protobuf-dev
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
RUN go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Copy go mod files first for caching
COPY go.mod go.sum ./
RUN go mod download

# Copy proto files and generate
COPY proto/ ./proto/
RUN protoc --proto_path=proto \
    --go_out=proto --go_opt=paths=source_relative \
    --go-grpc_out=proto --go-grpc_opt=paths=source_relative \
    proto/recording_sfu.proto

# Copy rest of source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o recording-service ./cmd/recording-service

# Runtime stage
FROM alpine:3.19

WORKDIR /app

# Install ca-certificates for HTTPS
RUN apk --no-cache add ca-certificates wget

# Copy binary from builder
COPY --from=builder /app/recording-service .
COPY --from=builder /app/config.yaml .

# Expose gRPC port
EXPOSE 50054

# Expose health check port
EXPOSE 50055

# Health check
HEALTHCHECK --interval=10s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -O- http://localhost:50055/health || exit 1

# Run the service
ENTRYPOINT ["./recording-service"]
CMD ["-config", "config.yaml"]
