---
inclusion: always
---

# Testing dan Review

- Unit test usecase memakai mock dari `_mock/`; integration test adapter memakai PostgreSQL, Valkey, RabbitMQ nyata.
- Setiap requirement SRS punya test case (TC ID) di Test Plan `docs/test/06-TP-RBT-001.md`.
- `make ci` wajib lolos sebelum PR. PR direview owner lain (CODEOWNERS).
- Evidence (load test, uji kegagalan) disimpan di `docs/evidence/`.
