# syntax=docker/dockerfile:1.7
# Build context: root repository.
#   docker build -f services/payment/deployments/Dockerfile -t rusuh-booking-payment .
# Base image dipin dengan digest (SEC-006): build berulang menghasilkan dasar yang sama; Dependabot (docker) memperbaruinya mingguan.
FROM golang:1.27-alpine@sha256:eb61c3873c97416fff0116553fff65d67a3549f72e11d9758849a689cdd8b523 AS build
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

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/payment /usr/local/bin/payment
USER nonroot:nonroot
EXPOSE 8083
HEALTHCHECK --interval=10s --timeout=4s --retries=3 CMD ["/usr/local/bin/payment", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/payment"]
CMD ["api"]
