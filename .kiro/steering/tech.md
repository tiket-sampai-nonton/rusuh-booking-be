---
inclusion: always
---

# Tech

- Go 1.24, net/http, PostgreSQL 16 (otoritas stok dan uang), Valkey 8.1 (antrean, rate limit, cache saja), RabbitMQ (event; outbox hanya di Booking dan Payment).
- Edge: CloudFront + WAF + ALB. Cloud: AWS EC2 ASG + Terraform (`deployments/terraform`).
- Lokal: Docker Compose (`make up`). CI: GitHub Actions per service.
- Library pihak ketiga hanya yang ADOPT di tech radar; tambahkan dengan alasan di PR.
- Sumber: `docs/hld/03-HLD-AD-RBT-001.md`, `docs/adr/05-ADR-RBT.md`.
