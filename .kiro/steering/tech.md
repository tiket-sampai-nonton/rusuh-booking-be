---
inclusion: always
---

# Tech

- Go 1.26, net/http, Cobra (verb CLI), pgx v5, valkey-go, amqp091-go, testify + mockgen (`go tool mockgen`).
- PostgreSQL 16 (otoritas stok, uang, akun, sesi login), Valkey 8.1 (antrean, rate limit, cache, OTP sementara), RabbitMQ (event; outbox hanya di Booking dan Payment).
- Event sourcing hanya untuk agregat Booking dan Payment (`pkg/eventstore`, ADR-019); stok tetap relasional.
- Env mengikuti D-24: `APP_*`, `LOG_*`, `POSTGRES_*` (+`POSTGRES_SCHEMA`), `VALKEY_SERVER` (JSON), `RABBITMQ_*`; semua divalidasi saat start.
- Health: satu endpoint `GET /health` (200 UP / 503 DOWN) untuk ALB dan HEALTHCHECK container (D-23).
- Edge: CloudFront + WAF + ALB. Cloud: AWS EC2 ASG + Terraform (`deployments/terraform`).
- Lokal: Docker Compose (`make up`). CI: GitHub Actions, satu pipeline bertahap (`.github/workflows/ci.yml`).
- Library pihak ketiga hanya yang ADOPT di tech radar; tambahkan dengan alasan di PR.
- Sumber: `docs/analisa/03-HLD-AD-RBT-001.md`, `docs/analisa/05-ADR-RBT.md`.
