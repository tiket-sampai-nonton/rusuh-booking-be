# syntax=docker/dockerfile:1.7
# Build context: root repository.
#   docker build -f services/booking/deployments/Dockerfile -t rusuh-booking-booking .
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY pkg/go.mod pkg/go.sum* pkg/
COPY services/booking/go.mod services/booking/go.sum* services/booking/
WORKDIR /src/services/booking
ENV CGO_ENABLED=0 GOWORK=off
RUN --mount=type=cache,target=/root/go/pkg/mod go mod download
WORKDIR /src
COPY pkg/ pkg/
COPY services/booking/ services/booking/
WORKDIR /src/services/booking
RUN --mount=type=cache,target=/root/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/booking ./cmd

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/booking /usr/local/bin/booking
USER nonroot:nonroot
EXPOSE 8082
HEALTHCHECK --interval=10s --timeout=4s --retries=3 CMD ["/usr/local/bin/booking", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/booking"]
CMD ["api"]
