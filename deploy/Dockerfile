# Gateway image. Builds cmd/<UNIT>, selected at build time via the UNIT build arg.
# The default `gateway` binary is the MULTI-UNIT server (one process hosting every
# registered unit type — see cmd/gateway). The same Dockerfile can still build a
# single-unit binary (e.g. --build-arg UNIT=howen) if ever needed.
#
#   docker build -f deploy/Dockerfile -t device-gateway .                 # multi-unit
#   docker build -f deploy/Dockerfile --build-arg UNIT=howen -t ... .     # single unit
#
# ffmpeg is bundled for video-capable units (e.g. howen); GPS-only units ignore it.
ARG GO_VERSION=1.23

FROM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src
# Cache module downloads (no third-party deps today, but future-proofed).
COPY go.mod ./
RUN go mod download
COPY . .
ARG UNIT=gateway
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/${UNIT}
# Seed an empty clips dir so the named volume mounted here inherits nonroot
# ownership (distroless has no shell to mkdir/chown at runtime).
RUN mkdir -p /out/clips

# Static ffmpeg used to remux device H.264 into HLS for live video. It's a
# self-contained static binary, so it runs on distroless/static and keeps the
# image shell-less. (GPS-only unit types don't need it but the cost is small.)
FROM mwader/static-ffmpeg:7.1 AS ffmpeg

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=ffmpeg /ffmpeg /usr/local/bin/ffmpeg
COPY --from=build /out/gateway /gateway
# Clip storage ("bucket"): owned by nonroot so the mounted volume is writable.
COPY --from=build --chown=nonroot:nonroot /out/clips /var/lib/gateway/clips
ENV FFMPEG_PATH=/usr/local/bin/ffmpeg
ENV CLIPS_ROOT=/var/lib/gateway/clips
# Documentation only; ports are set via env and mapped in compose. The multi-unit
# gateway listens per unit: 33000 howen control · 33001 howen media · 8050 fleetiger
# · 8080 HTTP API.
EXPOSE 8050 33000 33001 8080
USER nonroot:nonroot
ENTRYPOINT ["/gateway"]
