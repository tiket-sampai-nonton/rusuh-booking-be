# Notification Service

- Owner: BE-1
- Port default: 8084
- Mode: api
- Dependency: postgres, rabbitmq
- Desain: `docs/sdd/04-SDD-RBT-001.md` §4.1 (struktur) dan bagian Notification Service.
- Spec Kiro: `specs/notification-service/`.

```bash
cp .env.example .env
go run ./cmd
curl localhost:8084/healthz
curl localhost:8084/readyz
```
