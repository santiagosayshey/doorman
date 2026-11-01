# syntax=docker/dockerfile:1

# Build a static binary. CGO is off, so it needs no C library at runtime.
FROM golang:1.27.3 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/doorman ./cmd/doorman \
 && mkdir -p /out/logs

# Distroless: certificates, timezone data and a nonroot user (65532), and no
# shell or package manager. Run as another user with `user:` in compose.
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/doorman /doorman
# An empty /logs the default user can write to, for when nothing is mounted.
COPY --from=build --chown=65532:65532 /out/logs /logs

EXPOSE 8800 8801
ENTRYPOINT ["/doorman"]
CMD ["-c", "/config/doorman.yaml"]

# The image has no curl, so Doorman checks itself on its admin port.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD ["/doorman", "health", "-c", "/config/doorman.yaml"]
