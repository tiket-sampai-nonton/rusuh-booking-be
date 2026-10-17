# Payment Service

- Owner: BE-2
- Port default: 8083
- Mode: api | worker
- Dependency: postgres, rabbitmq
- Desain: `docs/sdd/04-SDD-RBT-001.md` §4.1 (struktur) dan bagian Payment Service.
- Spec Kiro: `specs/payment-service/`.

```bash
cp .env.example .env
go run ./cmd --mode=api
curl localhost:8083/healthz
curl localhost:8083/readyz
```
