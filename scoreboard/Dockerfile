# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Download dependencies
COPY go.mod ./
# No go.sum since we don't have external direct dependencies other than gorilla/websocket, which will be fetched
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o scoreboard cmd/main.go

# Final image stage
FROM alpine:3.19

LABEL maintainer="Scoreboard Team"
LABEL description="Scorebot-Scoreboard-Lite: Standalone WebSocket proxy for PvJ Scoreboard"

WORKDIR /app

# Copy the compiled binary from builder
COPY --from=builder /app/scoreboard .

# Expose port
EXPOSE 8080

# Run the scoreboard binary
ENTRYPOINT ["./scoreboard"]
