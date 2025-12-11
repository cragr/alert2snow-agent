# Stage 1: Build
# Use BUILDPLATFORM to run builder natively (no QEMU emulation)
# Go cross-compiles to TARGETPLATFORM (amd64) natively
FROM --platform=$BUILDPLATFORM golang:1.23 AS builder

ARG TARGETPLATFORM
ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /app

# Copy source code and vendored dependencies
COPY . .

# Build the binary - Go cross-compiles natively without emulation
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -mod=vendor -ldflags="-w -s" -o alert2snow-agent ./cmd/app

# Stage 2: Runtime
# This stage uses the target platform (amd64)
FROM --platform=$TARGETPLATFORM registry.access.redhat.com/ubi9/ubi-minimal:latest

# Install ca-certificates for HTTPS connections to ServiceNow
RUN microdnf install -y ca-certificates && \
    microdnf clean all && \
    rm -rf /var/cache/yum

# Create non-root user
RUN groupadd -r -g 1001 appgroup && \
    useradd -r -u 1001 -g appgroup appuser

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/alert2snow-agent .

# Set ownership
RUN chown -R appuser:appgroup /app

# Switch to non-root user
USER appuser

# Expose webhook port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/app/alert2snow-agent", "-version"] || exit 1

ENTRYPOINT ["./alert2snow-agent"]
