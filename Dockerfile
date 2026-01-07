# Build Stage
FROM golang:1.23-alpine AS builder

# Install git and SSL certs (essential for webhooks)
RUN apk add --no-cache git ca-certificates

WORKDIR /app

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Build the binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o dockhand ./cmd/dockhand

# Final Stage (Minimal & Secure)
FROM alpine:3.23.2

# Install CA certs and tzdata for timezone support
RUN apk add --no-cache ca-certificates tzdata

# Copy the binary
COPY --from=builder /app/dockhand /app/dockhand

# Create a non-root user and prepare filesystem for persistence
RUN adduser -D -g '' dockhand \
	&& mkdir -p /var/lib/dockhand \
	&& mkdir -p /app \
	&& chown -R dockhand:dockhand /var/lib/dockhand /app
USER dockhand

ENTRYPOINT ["/app/dockhand"]
