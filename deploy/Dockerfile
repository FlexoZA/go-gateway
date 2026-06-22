# Generic gateway image. One image = one unit type, selected at build time via
# the UNIT build arg (which cmd/<UNIT> to compile). The same Dockerfile builds
# every unit-type server.
#
#   docker build -f deploy/Dockerfile --build-arg UNIT=howen -t device-gateway-howen .
#
# GPS-only unit types produce a static binary and run on distroless. Unit types
# that need video (ffmpeg) should override the runtime stage base image.
ARG GO_VERSION=1.23

FROM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src
# Cache module downloads (no third-party deps today, but future-proofed).
COPY go.mod ./
RUN go mod download
COPY . .
ARG UNIT=howen
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/${UNIT}

# Static ffmpeg used to remux device H.264 into HLS for live video. It's a
# self-contained static binary, so it runs on distroless/static and keeps the
# image shell-less. (GPS-only unit types don't need it but the cost is small.)
FROM mwader/static-ffmpeg:7.1 AS ffmpeg

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=ffmpeg /ffmpeg /usr/local/bin/ffmpeg
COPY --from=build /out/gateway /gateway
ENV FFMPEG_PATH=/usr/local/bin/ffmpeg
# Documentation only; ports are set via env and mapped in compose.
# 33000 control · 33001 media · 8080 HTTP API
EXPOSE 33000 33001 8080
USER nonroot:nonroot
ENTRYPOINT ["/gateway"]
