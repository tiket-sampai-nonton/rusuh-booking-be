# Build context: root repository.
#   docker build -f services/payment/deployments/Dockerfile -t rusuh-booking-payment .
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY pkg/ pkg/
COPY services/payment/ services/payment/
WORKDIR /src/services/payment
ENV CGO_ENABLED=0 GOWORK=off
RUN go build -trimpath -ldflags="-s -w" -o /out/service ./cmd

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/service /service
USER nonroot:nonroot
EXPOSE 8083
HEALTHCHECK --interval=10s --timeout=3s --retries=3 CMD ["/service", "--healthcheck"]
ENTRYPOINT ["/service"]
