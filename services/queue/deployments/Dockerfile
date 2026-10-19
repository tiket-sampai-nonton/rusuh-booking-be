# syntax=docker/dockerfile:1.7
# Build context: root repository.
#   docker build -f services/queue/deployments/Dockerfile -t rusuh-booking-queue .
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY pkg/go.mod pkg/go.sum* pkg/
COPY services/queue/go.mod services/queue/go.sum* services/queue/
WORKDIR /src/services/queue
ENV CGO_ENABLED=0 GOWORK=off
RUN --mount=type=cache,target=/root/go/pkg/mod go mod download
WORKDIR /src
COPY pkg/ pkg/
COPY services/queue/ services/queue/
WORKDIR /src/services/queue
RUN --mount=type=cache,target=/root/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/queue ./cmd

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/queue /usr/local/bin/queue
USER nonroot:nonroot
EXPOSE 8081
HEALTHCHECK --interval=10s --timeout=4s --retries=3 CMD ["/usr/local/bin/queue", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/queue"]
CMD ["api"]
