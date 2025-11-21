# Multi-stage build for ShibuDb
FROM golang:1.23-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=1 GOOS=linux go build -a -installsuffix cgo -o shibudb main.go

# Final stage
FROM alpine:latest

# Install runtime dependencies
RUN apk --no-cache add ca-certificates tzdata

# Create non-root user
RUN addgroup -g 1001 -S shibudb && \
    adduser -u 1001 -S shibudb -G shibudb

# Create necessary directories
RUN mkdir -p /usr/local/var/lib/shibudb && \
    mkdir -p /usr/local/var/log && \
    mkdir -p /usr/local/var/run && \
    chown -R shibudb:shibudb /usr/local/var

# Copy binary from builder stage
COPY --from=builder /app/shibudb /usr/local/bin/shibudb

# Copy FAISS libraries if needed
COPY --from=builder /app/resources/lib/linux/amd64/* /usr/lib/

# Set ownership
RUN chown shibudb:shibudb /usr/local/bin/shibudb

# Switch to non-root user
USER shibudb

# Expose default port
EXPOSE 8080

# Set environment variables
ENV SHIBUDB_DATA_DIR=/usr/local/var/lib/shibudb
ENV SHIBUDB_LOG_DIR=/usr/local/var/log
ENV SHIBUDB_PID_DIR=/usr/local/var/run

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD pgrep shibudb || exit 1

# Default command
ENTRYPOINT ["/usr/local/bin/shibudb"]
CMD ["start", "8080"] 