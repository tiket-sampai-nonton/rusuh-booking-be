# syntax=docker/dockerfile:1.7
# Build context: root repository.
#   docker build -f services/user/deployments/Dockerfile -t rusuh-booking-user .
# Base image dipin dengan digest (SEC-006): build berulang menghasilkan dasar yang sama; Dependabot (docker) memperbaruinya mingguan.
FROM golang:1.27-alpine@sha256:eb61c3873c97416fff0116553fff65d67a3549f72e11d9758849a689cdd8b523 AS build
WORKDIR /src
COPY pkg/go.mod pkg/go.sum* pkg/
COPY services/user/go.mod services/user/go.sum* services/user/
WORKDIR /src/services/user
ENV CGO_ENABLED=0 GOWORK=off
RUN --mount=type=cache,target=/root/go/pkg/mod go mod download
WORKDIR /src
COPY pkg/ pkg/
COPY services/user/ services/user/
WORKDIR /src/services/user
RUN --mount=type=cache,target=/root/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/user ./cmd

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/user /usr/local/bin/user
USER nonroot:nonroot
EXPOSE 8085
HEALTHCHECK --interval=10s --timeout=4s --retries=3 CMD ["/usr/local/bin/user", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/user"]
CMD ["api"]
