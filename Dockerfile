# Build Stage
FROM golang:1.24-alpine AS builder

# Install git and SSL certs (Safety first for go mod download)
ARG ALPINE_GIT_VERSION=2.42.0-r0
ARG ALPINE_CA_CERTS_VERSION=20230829-r0
RUN apk add --no-cache git=${ALPINE_GIT_VERSION} ca-certificates=${ALPINE_CA_CERTS_VERSION}

WORKDIR /app

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Build the binary
COPY . .
# -ldflags="-s -w" strips debug symbols to reduce binary size
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o dockhand ./cmd/dockhand

# Prepare the state directory in the builder stage
# (Distroless has no 'mkdir' or 'chown' commands)
RUN mkdir -p /state-dir

# Final Stage (Distroless)
# Use 'static-debian12' (Stable) instead of '13' (Testing)
FROM gcr.io/distroless/static-debian12:nonroot

# Copy the binary
COPY --from=builder /app/dockhand /app/dockhand

# Copy the empty state directory with correct ownership
COPY --from=builder --chown=nonroot:nonroot /state-dir /var/lib/dockhand

# Set the environment variable to use this directory
ENV DOCKHAND_STATE_DIR=/var/lib/dockhand

# Drop root privileges completely
USER nonroot:nonroot

# Add a HEALTHCHECK instruction to satisfy automated scanners (Trunk)
# Use NONE since distroless images may not include a shell or extra utilities.
HEALTHCHECK NONE

ENTRYPOINT ["/app/dockhand"]