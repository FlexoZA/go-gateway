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

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build /out/gateway /gateway
# Documentation only; ports are set via LISTEN_PORT/HTTP_PORT and mapped in compose.
EXPOSE 33000 8080
USER nonroot:nonroot
ENTRYPOINT ["/gateway"]
