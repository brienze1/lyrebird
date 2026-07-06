# syntax=docker/dockerfile:1.7

# --platform=$BUILDPLATFORM: always run the Go toolchain natively on the
# build host, even when cross-compiling for a different target arch, so
# multi-arch buildx builds don't run the compiler itself under QEMU
# emulation. GOOS/GOARCH below (not the platform above) select the target.
FROM --platform=$BUILDPLATFORM golang:1.26.4-alpine AS builder
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0
ARG TARGETOS
ARG TARGETARCH
RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" \
    -o /out/lyrebird ./cmd/lyrebird

# distroless's final stage has no shell, so /data and /config (needed for a
# fully ephemeral "docker run" with no mounted volume, per docs/DESIGN.md)
# are pre-created and chowned here, then copied in below. UID/GID 65532 is
# distroless's documented "nonroot" identity.
RUN mkdir -p /image-data /image-config && chown -R 65532:65532 /image-data /image-config

# distroless:nonroot ships CA certs and a non-root UID (65532), unlike
# scratch — needed once outbound TLS proxying lands (M1+), so it's taken now
# rather than reworked later.
FROM gcr.io/distroless/static-debian12:nonroot AS final
LABEL org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.source="https://github.com/brienze1/lyrebird"
COPY --from=builder /out/lyrebird /lyrebird
COPY LICENSE /LICENSE
COPY --from=builder --chown=65532:65532 /image-data /data
COPY --from=builder --chown=65532:65532 /image-config /config
USER 65532:65532
EXPOSE 8080 9090
ENTRYPOINT ["/lyrebird"]
