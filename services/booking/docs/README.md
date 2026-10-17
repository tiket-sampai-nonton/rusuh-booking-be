# Booking Service

- Owner: BE-2
- Port default: 8082
- Mode: api | worker
- Dependency: postgres, rabbitmq, valkey
- Desain: `docs/sdd/04-SDD-RBT-001.md` §4.1 (struktur) dan bagian Booking Service.
- Spec Kiro: `specs/booking-service/`.

```bash
cp .env.example .env
go run ./cmd --mode=api
curl localhost:8082/healthz
curl localhost:8082/readyz
```
