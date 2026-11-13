# syntax=docker/dockerfile:1.7
# Build context: root repository.
#   docker build -f services/notification/deployments/Dockerfile -t rusuh-booking-notification .
# Base image dipin dengan digest (SEC-006): build berulang menghasilkan dasar yang sama; Dependabot (docker) memperbaruinya mingguan.
FROM golang:1.26-alpine@sha256:30b1e546a9c7fa3b747c2bdba3a0c7bdf3a02ebb4c5b8405d2f948d7e8aac4ec AS build
WORKDIR /src
COPY pkg/go.mod pkg/go.sum* pkg/
COPY services/notification/go.mod services/notification/go.sum* services/notification/
WORKDIR /src/services/notification
ENV CGO_ENABLED=0 GOWORK=off
RUN --mount=type=cache,target=/root/go/pkg/mod go mod download
WORKDIR /src
COPY pkg/ pkg/
COPY services/notification/ services/notification/
WORKDIR /src/services/notification
RUN --mount=type=cache,target=/root/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/notification ./cmd

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/notification /usr/local/bin/notification
USER nonroot:nonroot
EXPOSE 8084
HEALTHCHECK --interval=10s --timeout=4s --retries=3 CMD ["/usr/local/bin/notification", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/notification"]
CMD ["api"]
