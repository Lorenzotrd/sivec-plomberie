# Build stage
FROM golang:1.24-alpine AS builder

# Install git (needed for go mod download)
RUN apk add --no-cache git

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build all binaries
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o bet-bot ./cmd/bot && \
    CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o candle-rush-bot ./cmd/candle-rush-bot && \
    CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o bot-manager ./cmd/manager && \
    CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o sweep ./cmd/sweep && \
    CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o sweep-all ./cmd/sweep-all

# Runtime stage
FROM alpine:latest

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy all binaries and config from builder stage
COPY --from=builder /app/bet-bot .
COPY --from=builder /app/candle-rush-bot .
COPY --from=builder /app/bot-manager .
COPY --from=builder /app/sweep .
COPY --from=builder /app/sweep-all .
COPY --from=builder /app/config.yaml .

# Create data directory for wallet storage
RUN mkdir -p /root/data

# Default: run bet-bot (override with docker-compose command)
CMD ["./bet-bot"]
