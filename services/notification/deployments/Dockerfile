# Build context: root repository.
#   docker build -f services/notification/deployments/Dockerfile -t rusuh-booking-notification .
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY pkg/ pkg/
COPY services/notification/ services/notification/
WORKDIR /src/services/notification
ENV CGO_ENABLED=0 GOWORK=off
RUN go build -trimpath -ldflags="-s -w" -o /out/service ./cmd

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/service /service
USER nonroot:nonroot
EXPOSE 8084
HEALTHCHECK --interval=10s --timeout=3s --retries=3 CMD ["/service", "--healthcheck"]
ENTRYPOINT ["/service"]
