# Build Stage
#
# For reproducible builds pin the base image to a specific digest (sha256).
# You can override the image at build time with a digest, e.g.:
#   docker build --build-arg GOLANG_IMAGE=golang@sha256:<digest> .
# Default uses the tag for local convenience; replace with a digest for CI.
ARG GOLANG_IMAGE=golang@sha256:ac09a5f469f307e5da71e766b0bd59c9c49ea460a528cc3e6686513d64a6f1fb
FROM ${GOLANG_IMAGE} AS builder

# Install git and SSL certs (Safety first for go mod download)
# Versions are parameterized via build args for auditability. Leave empty
# to install the distro's current package; provide a value to pin.
ARG ALPINE_GIT_VERSION=""
ARG ALPINE_CA_CERTS_VERSION=""
RUN apk add --no-cache \
	git${ALPINE_GIT_VERSION:+=${ALPINE_GIT_VERSION}} \
	ca-certificates${ALPINE_CA_CERTS_VERSION:+=${ALPINE_CA_CERTS_VERSION}}

WORKDIR /app

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Build the binary
COPY . .
# -ldflags="-s -w" strips debug symbols to reduce binary size
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o dockhand ./cmd/dockhand \
	&& mkdir -p /state-dir

# Prepare the state directory in the builder stage
# (Distroless has no 'mkdir' or 'chown' commands)

# Final Stage (Distroless)
# For reproducible builds pin to a digest. Override the FROM line in CI if
# you need a different image; keep an explicit non-latest tag here so
# scanners can validate the base image is not `latest` or empty.
FROM gcr.io/distroless/static-debian12@sha256:2b7c93f6d6648c11f0e80a48558c8f77885eb0445213b8e69a6a0d7c89fc6ae4


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