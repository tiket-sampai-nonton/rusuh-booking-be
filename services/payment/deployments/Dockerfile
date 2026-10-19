# syntax=docker/dockerfile:1.7
# Build context: root repository.
#   docker build -f services/payment/deployments/Dockerfile -t rusuh-booking-payment .
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY pkg/go.mod pkg/go.sum* pkg/
COPY services/payment/go.mod services/payment/go.sum* services/payment/
WORKDIR /src/services/payment
ENV CGO_ENABLED=0 GOWORK=off
RUN --mount=type=cache,target=/root/go/pkg/mod go mod download
WORKDIR /src
COPY pkg/ pkg/
COPY services/payment/ services/payment/
WORKDIR /src/services/payment
RUN --mount=type=cache,target=/root/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/payment ./cmd

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/payment /usr/local/bin/payment
USER nonroot:nonroot
EXPOSE 8083
HEALTHCHECK --interval=10s --timeout=4s --retries=3 CMD ["/usr/local/bin/payment", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/payment"]
CMD ["api"]
