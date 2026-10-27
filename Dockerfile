# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build statically linked binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /netsathi-server ./cmd/server

# Final lightweight runtime image
FROM alpine:3.19

WORKDIR /app

# Install ca-certificates and timezone data
RUN apk --no-cache add ca-certificates tzdata

# Create data directories
RUN mkdir -p /app/data/chunks /app/data/completed /app/web

# Copy binary from builder
COPY --from=builder /netsathi-server /app/netsathi-server
COPY --from=builder /app/web /app/web

# Expose port
EXPOSE 8080

# Environment variables
ENV PORT=8080
ENV DATA_DIR=/app/data
ENV WEB_DIR=/app/web

# Run server
ENTRYPOINT ["/app/netsathi-server", "-port=8080", "-data=/app/data", "-web=/app/web"]
