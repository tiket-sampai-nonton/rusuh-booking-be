# Evidence: Pengaturan antrean per event — endpoint admin internal + join ketat (ADR-031)

Tanggal: 2026-11-06. Branch `feature/queue-settings-admin` (basis `feature/rework` bbfa5d7). Lingkup: `services/queue`
(+ dokumen, satu aturan Caddy dev, contoh env, bootstrap e2e). Tidak ada perubahan di `services/booking/`, `pkg/`,
`deployments/terraform/`, maupun migrasi 0001–0003.

## 1. Diskusi dan keputusan yang menghasilkan perubahan ini

Pertanyaan pemilik berurutan: mengapa join membuat `event_settings`, apa dasar desainnya, mengapa tidak publish pesan
saat event dibuat lalu consumer di Queue, apakah pembuatan cukup memakai nilai bawaan, di service mana
`event_settings` seharusnya hidup (Queue atau Booking) dan mengapa; lalu keputusan: endpoint baru adalah internal,
join ketat ("strict one"), tidak ada event (worker membaca PG tiap tick), dan env nilai bawaan kapasitas tidak
diperlukan karena nilai diisi lewat endpoint. Hasilnya: ADR-031 di `docs/analisa/05-ADR-RBT.md`.

Keputusan inti: (1) `GET`/`PUT /internal/v1/admin/events/{eventId}/queue-settings` di Queue; (2) join tanpa baris
pengaturan -> `409 QUEUE_NOT_CONFIGURED`, `Settings.Ensure` dihapus; (3) audit per field di
`queue.event_settings_audit` (append-only, satu transaksi); (4) kunci `oneActivePerVisitor`/`oneActivePerEmail` saat
penjualan terbuka; (5) tanpa event RabbitMQ, tanpa env kapasitas bawaan. `ADMIN_EMAILS` di Queue adalah allow-list
autentikasi, bukan nilai bawaan kapasitas.

## 2. Pemetaan test (merah dulu, lalu hijau)

| Properti (design) | Test |
|---|---|
| P1 batas dan aturan lintas-field pada nilai gabungan | `entity/settings_rules_test.go` (`TestValidateSupplied_Bounds`, `TestValidateSettings_CrossRules`, `TestDecide_MergedCrossRuleWithOmittedField`) |
| P2/P3 input salah / `Supply` gagal tidak menulis | `usecase/queue_settings_test.go` (`...InvalidInputNeverCallsBookingOrApply`, `...SupplyErrors`) |
| P4 matriks kunci + batas `salesStartsAt` | `TestDecide_LockMatrix`, `TestSetQueueSettings_SaleOpenBoundaryAndActor`, integrasi `...DecideErrorWritesNothing` |
| P5 baris + audit atomik | `event_settings_admin_repo_integration_test.go::AuditFailureRollsBackRow` (trigger gagal buatan) |
| P6 kardinalitas audit, no-op | `...CreateWritesRowAndAllFieldsAudit`, `...UpdateAuditsOnlyChangedFields`, `...IdenticalRepeatIsNoOp` |
| P7 actor = subject token, bukan dari body | `settings_admin_handler_test.go` (`actor in body is an unknown field`, `PutCreatedVsUpdated` memeriksa actor = subject), integrasi (filter `actor='acct-1'`/`'b'` pada audit) |
| P8 audit append-only | `...AuditTableIsAppendOnly`, `embed_test.go` |
| P9 autentikasi admin | `security/admin_verifier_test.go`, `settings_admin_handler_test.go::AuthRejectsBeforeAnythingElse`, `NilVerifierDeniesAll`, `delivery/admin_wiring_test.go` |
| P10/P12 join ketat | `usecase/join_test.go::TestJoin_UnconfiguredEventIsRefused` (+ `Times(0)` untuk `Supply`, `Store.Join`, `ReserveBatch`, token, audit) |
| P11 satu-satunya penulis | grep §4 |
| P13 PUT serempak | `...ConcurrentPutsAreSerialized` (`-race`) |
| P14 worker | `worker/promoter_settings_test.go` |
| P15 `DefaultSettings` = bawaan kolom; migrasi up/down/up | `TestDefaultSettings_MatchColumnDefaults`, `TestMigration0004_DownThenUpAgain` (integrasi), `embed_test.go` |

## 3. Perintah gerbang dan hasilnya

Lihat `.agents/tasks/feature/queue-settings-admin/verification.md` untuk perintah persis, exit code asli, ringkasan
test, dan hash commit. Infrastruktur uji: PostgreSQL 16, Valkey 8, dan RabbitMQ khusus uji (kontainer sementara),
bukan instance Redis/Valkey proyek lain di mesin.

## 4. Pemeriksaan statis

- `git diff --stat bbfa5d7 -- services/booking pkg deployments/terraform` -> kosong.
- `grep -rn "Ensure(" services/queue --include=*.go` -> kosong.
- `INSERT INTO event_settings ` di `services/queue/internal` (bukan test) -> hanya `event_settings_admin_repo.go`.
- Tidak ada import `services/booking` di Queue (satu komentar menyebutnya).
- Tidak ada kunci env baru selain `ADMIN_EMAILS` (allow-list); tidak ada binding/publisher RabbitMQ baru.

## 5. Batasan yang diketahui dan belum diverifikasi

- Suite E2E (`tests/e2e`, tag `e2e`) diperbarui (PUT setelah publish, tes `TestQueueSettings_StrictJoinAndAdminEndpoint`,
  `ADMIN_EMAILS` untuk Queue di `tools/e2e.sh`) tetapi TIDAK dikompilasi/dijalankan di sini: modul `tests/e2e` tidak
  ada di `go.work` dan `go.mod`-nya tidak memuat `godog`/`pgx`/`amqp` (kondisi sebelum perubahan ini). Hanya `gofmt`.
- Batas race di `salesStartsAt` (snapshot `Supply` sebelum transaksi) diterima dan dicatat di ADR-031.
- `make migrate-queue` terhadap DB Compose tidak dijalankan; pengganti: `TestMigration0004_DownThenUpAgain` menjalankan
  `Down(1)` lalu `Up` pada DB uji sementara dan memeriksa tabel + fungsi trigger hilang lalu kembali.
- Validasi silang `ADMISSION_TOKEN_TTL_SECONDS < claim_window_seconds` saat simpan: di luar cakupan (dicatat ADR-031).
