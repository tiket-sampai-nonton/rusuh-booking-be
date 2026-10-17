# Queue Service

- Owner: BE-1
- Port default: 8081
- Mode: api
- Dependency: valkey, postgres, rabbitmq
- Desain: `docs/sdd/04-SDD-RBT-001.md` §4.1 (struktur) dan bagian Queue Service.
- Spec Kiro: `specs/queue-service/`.

```bash
cp .env.example .env
go run ./cmd
curl localhost:8081/healthz
curl localhost:8081/readyz
```
