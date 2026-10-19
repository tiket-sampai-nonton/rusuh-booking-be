---
inclusion: always
---

# Testing dan Review

- Unit test memakai testify (`assert`/`require`) dan table-driven; setiap fungsi punya test regresi (D-27). Mock dari `_mock/` (mockgen).
- Integration test adapter memakai PostgreSQL, Valkey, RabbitMQ nyata dengan build tag `integration` (`make test-integration`; di CI stage case-coverage).
- Setiap requirement SRS punya test case (TC ID) di Test Plan `docs/test/06-TP-RBT-001.md`.
- `make ci` wajib lolos sebelum PR. PR direview owner lain (CODEOWNERS).
- Evidence (load test, uji kegagalan) disimpan di `docs/evidence/`.
