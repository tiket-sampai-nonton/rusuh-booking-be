# Test lintas service

- `e2e/` — E2E lintas service berbasis Go (build tag `e2e`, modul sendiri): `make test-e2e` / `tools/e2e.sh`. Skenario: perjalanan penuh sampai e-tiket, pending/deny, webhook palsu/nominal salah/duplikat, hold duplikat, akses tidak sah (booking, tiket, admin, internal), konkurensi stok, expiry + settlement terlambat (`E2E_SLOW=1`). Setiap respons divalidasi terhadap `docs/contracts/openapi` (matikan dengan `E2E_CONTRACT=0`).
- `features/` — skenario BDD (Gherkin)
- `k6/` — load test
- `contract/` — contract test antar-service
- `apidog/` — koleksi Apidog

Lihat Test Plan `docs/analisa/06-TP-RBT-001.md`.
