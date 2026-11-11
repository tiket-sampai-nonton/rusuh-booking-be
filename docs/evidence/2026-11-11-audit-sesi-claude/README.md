# Audit mandiri, rework, dan validasi — sesi Claude 2026-11-11

Penulis: Claude Code (Opus 5.5), atas permintaan pemilik (`arnu12rahman`). Belum di-commit. Log mentah ada di
`before/` (sebelum rework) dan `after/` (sesudah rework) di folder ini.

## 1. Ringkasan eksekutif

Cakupan audit: semua yang diubah sesi ini di `rusuh-booking-be` (tidak ada kode FE yang diubah):

| Area | Isi |
|---|---|
| A. Cookie visitor | Katalog publik tidak lagi 401 karena cookie `rb_visitor` basi; cookie kedaluwarsa dibersihkan |
| B. Deploy dev | `check-env.sh` (env wajib baru), smoke test menunggu semua container healthy, restart observability bila konfigurasinya berubah |
| C. NFR-09 | Metrik antrean, konversi, expired, missed turn per event; dashboard dan alert Grafana; CloudWatch di AWS |
| D. Batas attempt pembayaran | `PAYMENT_MAX_ATTEMPTS`, `409 PAYMENT_ATTEMPT_LIMIT` |
| E. UUIDv7 tahap 1 | `pkg/rabbitmq.NewID` = v7; default id outbox payment = v7 (ADR-032) |

Audit menemukan 11 temuan (0 Critical, 1 High, 3 Medium, 7 Low) di pekerjaan sesi ini. Semuanya sudah diperbaiki
dan diuji ulang. Sisa risiko adalah hal yang butuh lingkungan atau akses yang tidak ada di sini (deploy dev/AWS,
token SonarCloud, kunci Midtrans), plus temuan lama di luar cakupan. **Status: PASS WITH ISSUES** (bagian 12).

## 2. Requirement Traceability Matrix

| Requirement | Implementasi | Bukti | Status |
|---|---|---|---|
| NFR-09 / US-081 / SRS-Q-MNT-02: metrik antrean, konversi, expired, missed turn per event near-real-time | `FunnelCollector` (booking), `DepthCollector` (queue), dashboard `rbt-bisnis`, dashboard CloudWatch | Unit 100% kedua collector; integration `FunnelStats`; runtime: booking-api dan queue-api sungguhan mengekspos metrik (`after/runtime-*.txt`); Grafana 12 memuat dashboard | Terpenuhi lokal; belum di dev/AWS |
| SRS-F-90-06: alert ke email tim | `provisioning/alerting/rbt-nfr09.yaml`, SMTP Resend; alarm CloudWatch ke SNS | 3 aturan `health: ok` di Grafana 12 (`after/grafana-provisioning.txt`) | Sebagian: TC-OBS-02 (kirim email) NOT_RUN |
| SRS-F-90-01 (rekonsiliasi) | Di luar cakupan sesi ini | — | Tidak disentuh |
| Requirement payment 12 (spec): batas attempt per booking | `checkAttemptLimit` + penjaga atomik di `CreateNext` | TC-PAY-12, TC-PAY-13; `TestPaymentJourney` | Terpenuhi |
| Rencana 2026-11-10 langkah 1 (E2E k6 bayar ulang, Sandbox) | — | — | Belum (butuh stack penuh dan kunci Midtrans) |
| Rencana langkah 3 (koordinasi FE) | Kontrak dan dokumen FE diperbarui | `payment.yaml`, `08b` | Kode FE belum |
| ADR-032 tahap 1 (UUIDv7) | `NewID`, migrasi payment 0005 | `id_test.go`; 2000 ID SQL di PG16; integration payment, booking, outbox, RabbitMQ | Terpenuhi; tahap lanjut di ADR-032 |
| D-24 (env divalidasi saat start) di dev | `check-env.sh` di `deploy.sh`/`preflight.sh` | 3 kasus tiruan; `preflight.sh` di server dev lolos (dijalankan pemilik) | Terpenuhi |
| Prinsip produk "jaga pengalaman pengguna" (katalog kosong karena cookie basi) | `isPublicCatalogRead`, `rejectVisitor` | TC-CAT-16, TC-SEC-04; runtime 200 untuk katalog (`after/runtime-booking-cookie.txt`) | Terpenuhi |
| TC-SEC-03 (tanpa PII di log/metrik) | Label metrik hanya `event_id`, `status`, `reason` | Review kode | Terpenuhi |

## 3. Temuan audit dan severity

| ID | Sev | Lokasi | Temuan | Dampak |
|---|---|---|---|---|
| F1 | High | `booking/.../http/middleware.go` | Cookie `rb_visitor` dihapus untuk SEMUA token yang ditolak (termasuk tanda tangan salah), dengan `SameSite` dari env (booking dev tidak mengatur `COOKIE_SAMESITE` → `Lax`) | Kunci publik booking salah atau rotasi tidak sinkron → booking menghapus identitas antrean semua pengunjung (kehilangan posisi). Di skenario lintas-site yang ingin ditolong, `Lax` membuat penghapusan diblokir browser |
| F2 | Medium | `payment/.../postgres/attempt_repository.go` | Batas attempt hanya dicek di usecase; dua permintaan paralel dengan key berbeda bisa sama-sama lolos, dan bila attempt pertama cepat gagal, `INSERT` kedua mendapat `attempt_no` batas+1 | Batas bisa terlampaui satu per request paralel |
| F3 | Medium | 4 test (`pkg/outbox`, `pkg/rabbitmq`, `payment/midtrans/sandbox_test.go`) | `NewID()[:8]` dipakai untuk keunikan; pada UUIDv7 itu stempel waktu (sama ±65 detik) | Nama queue test bentrok; order id Midtrans Sandbox kembar dan ditolak (test sandbox flaky). Regresi akibat E |
| F4 | Medium | `ResolveVisitor`, `CreateAttempt.run` | Cognitive complexity naik di atas ambang Sonar S3776 (15): 15→21 dan 14→17 | Code smell baru di Sonar, lebih sulit direview |
| F5 | Low | `pkg/rabbitmq/envelope.go` | gosec G115 (konversi `uint64`→`byte`) di `newIDAt` | Peringatan scanner; kode benar tetapi sulit dibuktikan sekilas |
| F6 | Low | `queue/.../telemetry/depth_collector.go` | HELP `rbt_queue_active_count` menyebut ADMITTED/CLAIMED; sebenarnya `ZCARD active` juga memuat MISSED yang belum disapu | Dokumentasi metrik menyesatkan |
| F7 | Low | `terraform/modules/compute/user_data.sh.tftpl` | Gagal memasang CloudWatch Agent menggagalkan `user_data` (`set -e`) | cloud-init berstatus gagal walau aplikasi sudah jalan |
| F8 | Low | `middleware.go` | Sonar S1192 (CRITICAL code smell di main): literal `"WWW-Authenticate"` 4 kali di file yang diubah | Code smell |
| F9 | Low | Beberapa fungsi baru | Cabang error tanpa test: `checkAttemptLimit` (read gagal), `DepthCollector.read` (`ActiveCount` gagal), `telemetry.MustRegister` (Default), `makeRoom` (`FindOpen` gagal) | Logika bisnis kritis tanpa bukti di cabang gagal |
| F11 | Low | `deployments/dev/{deploy,gen-secrets}.sh` | shellcheck SC2155 (`export` + substitusi menutupi kegagalan `id`), SC2034 (`out` tak terpakai; `kv` tidak `local`) | Kegagalan `id` tidak menghentikan deploy; variabel bocor ke scope global |
| F10 | Low | `docs/analisa/06-TP-RBT-001.md` | Tidak ada TC untuk batas attempt, katalog dengan cookie basi, penghapusan cookie; status TC-OBS belum mencerminkan kenyataan | Test plan tidak sesuai implementasi |

## 4. Root cause

- F1: perbaikan awal fokus pada gejala (cookie basi) tanpa memodelkan kegagalan sisi server (kunci salah atau rotasi) dan tanpa memeriksa env `COOKIE_SAMESITE` di booking.
- F2: aturan bisnis ditegakkan di lapisan baca-lalu-tulis tanpa pagar di statement tulis.
- F3: perubahan format ID global tanpa mencari pemakai yang mengandalkan prefix ID.
- F4: cabang baru ditambahkan ke fungsi yang sudah dekat ambang tanpa mengukur kompleksitas.
- F5–F10: tidak menjalankan gosec, gocognit, dan coverage per fungsi sebelum menyatakan selesai.

## 5. Rework dan remediation log

| ID | Perbaikan | Validasi |
|---|---|---|
| F1 | Cookie hanya dihapus bila `errors.Is(err, token.ErrExpired)` dan token berasal dari cookie; cookie penghapus selalu `SameSite=None; Secure; HttpOnly`. `//nolint:gosec` G124 dengan alasan tertulis | Test merah lalu hijau (`TestResolveVisitor_ExpiresOnlyAnExpiredCookie`, `..._KeepsCookieUnlessExpired`); runtime: route sesi 401 tanpa `Set-Cookie` |
| F2 | `NewAttempt.MaxAttemptNo`; `INSERT ... WHERE $8 = 0 OR next.no <= $8`; tanpa baris → `outbound.ErrAttemptLimit` → 409 | `TestCreateNext_RefusesAttemptPastTheCapAtomically` (Postgres 16), `TestCreateAttempt_LimitHitAtInsertIsStillA409` |
| F3 | Pakai ekor acak `NewID()[28:]` | Vet dengan tag `integration sandbox`; `pkg/rabbitmq` dan `pkg/outbox` integration lolos dengan RabbitMQ sungguhan |
| F4 | `ResolveVisitor` dipecah (`visitorToken`, `verifyVisitor`, `rejectVisitor`); `run` dipecah (`makeRoom`, `createNextError`); `Collect` (15) dipecah (`tally`) | gocognit: `ResolveVisitor` 21→7, `run` 17→8, `Collect` 15→9; semua test tetap hijau |
| F5 | `binary.BigEndian.PutUint64` (dikoreksi pasca-CI, bagian 13) | Test v7 hijau; gosec bawaan golangci-lint bersih, tetapi gosec v2.22.0 CI menilai HIGH |
| F6 | HELP, test, dan deskripsi panel Grafana diperbaiki | Test collector hijau |
| F7 | Blok agent di subshell `set -e` sendiri, gagal hanya peringatan | `terraform validate`; render template + `bash -n`; simulasi perilaku |
| F8 | Konstanta `headerWWWAuthenticate` (hanya di file yang diubah) | Lint dan test hijau |
| F9 | 4 test baru | Coverage fungsi terkait 100% (bagian 9) |
| F11 | Pisahkan assignment dari `export`; `local ... kv` | shellcheck 0, `bash -n` |
| F10 | TC-OBS-01 (status jujur), TC-OBS-02 (catatan), TC-PAY-12, TC-PAY-13, TC-CAT-16, TC-SEC-04; spec payment menyebut penjaga atomik | Review dokumen |

## 6. File yang berubah

Kode Go: `pkg/telemetry/telemetry{,_test}.go`, `pkg/rabbitmq/{envelope.go,id_test.go,rabbitmq_integration_test.go}`,
`pkg/outbox/outbox_integration_test.go`; booking: `infrastructure/http/{middleware,visitor_test}.go`,
`infrastructure/telemetry/*`, `infrastructure/postgres/funnel_stats{,_integration_test}.go`, `port/outbound/funnel_stats.go`,
`infrastructure/ids/ids.go` (komentar), `delivery/server.go`, `_mock/.../funnel_stats_mock.go`, `go.mod`; queue:
`infrastructure/telemetry/depth_collector{,_test}.go`, `port/outbound/depth.go`, `delivery/{queue_module,server}.go`,
`_mock/.../depth_mock.go`, `go.mod`; payment: `config/{payment,load_test}.go`, `delivery/payment_module.go`,
`application/usecase/create_attempt{,_test}.go`, `port/{inbound/errors,outbound/attempt_repository}.go`,
`infrastructure/postgres/{attempt_repository,repositories_integration_test}.go`, `infrastructure/midtrans/sandbox_test.go`,
`deployments/migrations/{0005_outbox_uuid_v7.*,embed_test.go}`, `.env.example`.

Deploy/observability: `deployments/dev/{check-env.sh,deploy.sh,preflight.sh,gen-secrets.sh,README.md,env/grafana.env.example,env/payment.env.example}`,
`deployments/observability/grafana/{dashboards/rbt-bisnis.json,provisioning/alerting/rbt-nfr09.yaml}`, `docker-compose.yaml`,
`deployments/terraform/{main.tf,README.md,modules/{compute/main.tf,compute/user_data.sh.tftpl,iam/main.tf,observability/main.tf}}`.

Dokumen: `docs/analisa/{02-SRS,04a-SDD-QUEUE,04b-SDD-BOOKING,04c-SDD-PAYMENT,05-ADR,06-TP,08b}*.md`,
`docs/contracts/openapi/payment.yaml`, `specs/payment-service/{design,requirements}.md`, `docs/ai-log/README.md`.

## 7. Lint, keamanan, dan dependensi

| Pemeriksaan | Sebelum | Sesudah | Catatan |
|---|---|---|---|
| `gofmt` file Go yang berubah | bersih | bersih | `after/gofmt.txt` |
| `go vet ./...` dan dengan tag `integration sandbox` (5 modul) | — | exit 0 | `after/vet-*.txt` |
| `golangci-lint` 2.15.0 (config repo, 5 modul) | 0 issue | 0 issue | Biner `~/go/bin` (go1.26) panik pada modul go1.27; dipakai biner Homebrew (go1.27.2) |
| gosec (lewat golangci-lint; biner gosec mandiri internal error) | pkg 6, booking 8, queue 11, payment 8 | pkg 5, booking 6, queue 11, payment 8, notification 3 | Tidak ada temuan di kode produksi yang diubah sesi ini. Sisa di file sesi ini hanya G124 pada `r.AddCookie` di request test (false positive; pola sama di test lama). Lainnya temuan lama di luar cakupan |
| `govulncheck` v1.1.4 (5 modul) | GO-2026-6859 | GO-2026-6859 | `net/http` toolchain lokal go1.27.3, diperbaiki di 1.27.4; bukan dari perubahan ini |
| shellcheck (Docker `koalaman/shellcheck:stable`) | 2 peringatan (HEAD): SC2155 `deploy.sh:11`, SC2034 `gen-secrets.sh:59` | 0 (5 skrip deploy) | Diperbaiki setelah audit (F11) |
| Terraform `fmt`/`validate`, Trivy HIGH/CRITICAL | — | lolos, 0 temuan (IAM juga MEDIUM, 0) | `after/terraform-trivy.txt`, `after/trivy-iam.txt` |
| `go.mod` | — | booking: `client_golang` jadi langsung (versi sama); `godebug` indirect (testutil) di booking dan queue | `go mod tidy` |

## 8. Sonar

`sonar-scanner` tersedia, tetapi `SONAR_TOKEN` tidak ada, sehingga perubahan lokal yang belum di-commit **tidak bisa
dianalisis**. Yang dilakukan sebagai gantinya:

- SonarCloud publik (analisis terakhir `main`): 0 bug, 0 vulnerability, 0 hotspot, 81 code smell, coverage 89,9%,
  duplikasi 3,9%. Issue di file yang disentuh sesi ini: S1192 `middleware.go` (diperbaiki, F8); S1192 dan S8196
  `queue_module.go` dan S8196 `payment_module.go` (bagian lama yang tidak diubah, di luar cakupan).
  Lihat `sonar-issues-file-tersentuh.txt`.
- Padanan lokal aturan S3776: gocognit; semua fungsi sesi ini di bawah 15 (F4).
- Duplikasi tidak diukur lokal.

## 9. Coverage dan race

`go test -race -count=1 ./...` lulus di pkg, booking, queue, payment, notification (`after/test-race-*.txt`).

| Modul | Total sebelum rework (dicatat dari run sebelum rework; lognya tertimpa run akhir di `after/`) | Sesudah |
|---|---|---|
| pkg | 77,1% | 77,2% |
| booking | 53,4% | 53,5% |
| queue | 66,4% | 66,5% |
| payment | 73,6% | 74,0% |
| notification | 70,1% | 70,1% |

Fungsi sesi ini: semua 100%, kecuali `CreateAttempt` 88,9% (cabang lama), `run` 96,9%, dan `registerFunnelMetrics` 40%
(cabang Postgres; dibuktikan lewat runtime, paket `delivery` dikecualikan dari coverage Sonar). Paket collector
booking dan queue 100%. Coverage total booking yang rendah adalah kondisi lama, bukan dari sesi ini.

## 10. Build, integration, regresi, runtime

| Uji | Hasil |
|---|---|
| `make isolation` (5 service, `GOWORK=off`) | lolos |
| `make contracts` | semua `ok` |
| Integration Postgres booking (seluruh paket, termasuk `FunnelStats`) | lolos |
| Integration Postgres payment (termasuk batas atomik, id outbox v7) | lolos |
| Integration `pkg/rabbitmq` dan `pkg/outbox` dengan RabbitMQ 3.13 sungguhan | lolos (run pertama gagal `connection refused` karena broker belum siap; lolos setelah broker siap) |
| `TestPaymentJourney` (Postgres dan RabbitMQ) | lolos |
| PromQL dashboard dan alert (`promtool` Prometheus 3.5.0) | 12 query valid |
| Grafana 12.0.0 dengan provisioning repo | 3 dashboard, 3 alert `health: ok`, tanpa error provisioning |
| Runtime booking-api (biner sungguhan, PG16, Valkey 8.1, RabbitMQ) | `/metrics`: funnel sesuai data uji (3 ACTIVE, 1 UNCLAIMED, missed turn 25%); `funnel_up 0` saat tabel belum ada tanpa merusak `/metrics`; katalog 200 dengan cookie rusak; route sesi 401 tanpa `Set-Cookie` |
| Runtime queue-api | `rbt_queue_waiting_count`, `rbt_queue_active_count`, `rbt_queue_depth_up 1` |
| UUIDv7 di SQL | 2000/2000 versi 7, varian 10, unik, selisih waktu ≤ 6 ms; migrasi down kembali ke v4 |
| Biaya query funnel | 50 ms untuk 500.000 baris (parallel seq scan, PG16) |

Tidak dijalankan: E2E k6, test `sandbox` Midtrans (butuh kunci), integration queue (Valkey), deploy dev dan AWS.

## 11. Masalah terbuka dan risiko sisa

| # | Risiko | Dampak | Tindak lanjut |
|---|---|---|---|
| R1 | Analisis Sonar atas perubahan ini belum jalan (tanpa token) | Code smell baru di luar S3776/S1192 bisa luput | Jalankan CI (Sonar) di PR |
| R2 | Belum di-deploy: dashboard, alert, CloudWatch Agent, migrasi 0005 di dev | Klaim NFR-09 baru terbukti lokal | Deploy dev; TC-OBS-02 (Test contact point); `terraform plan` di AWS |
| R3 | GO-2026-6859 (`net/http`) | Pada toolchain go1.27.3 lokal | Naikkan toolchain ke 1.27.4; pastikan image `golang:1.26-alpine` menarik patch terbaru |
| R4 | E2E bayar ulang dan Sandbox belum dijalankan | Perilaku Midtrans saat attempt kedua belum terbukti | Rencana langkah 1 |
| R5 | Query funnel seq scan | ±50 ms per 500 ribu baris per scrape (10 s dev, 30 s per instance AWS) | Pantau; bila perlu cache hasil di collector atau index `(event_id, status)` |
| R6 | Event lama tetap dilaporkan | Biaya CloudWatch ±US$0,30 per metrik per event per bulan | Hapus pengaturan antrean event lama |
| R7 | Ambang alert 30%/50% masih tebakan | Alarm palsu atau terlewat | Kalibrasi dari uji beban |
| R8 | Perubahan di area `nuzirwan` (booking, queue) dan `NewID` v7 mengubah format id booking dan JTI | Butuh review pemilik | Review PR |
| R9 | Temuan lama di luar cakupan: gosec G124 (`join_handler`, `payment_handler`), G306, G304, Sonar S1192/S8196 di `queue_module` | Tidak berubah oleh sesi ini | Backlog |
| R10 | Kode FE: tombol bayar lagi, `Idempotency-Key` baru per klik; penghapusan jalur page-token; UUIDv7 tahap lanjut | Fitur belum lengkap end-to-end | Rencana langkah 3, ADR-032 |

## 12. Penilaian akhir

**PASS WITH ISSUES.**

- Tidak ada defect Critical. Satu High dan tiga Medium dari pekerjaan sesi ini sudah diperbaiki dan dibuktikan dengan
  test (unit, integration, dan runtime).
- Quality gate lokal yang tersedia lolos: gofmt, vet, golangci-lint, test `-race`, isolation, contracts, terraform,
  Trivy, promtool, dan Grafana.
- Belum PASS penuh karena hal di bagian 11: belum ada deploy dan uji di dev/AWS (TC-OBS-02 NOT_RUN), analisis Sonar
  atas perubahan ini terhalang token, E2E pembayaran ulang belum dijalankan, dan kerentanan toolchain lokal.

## 13. Koreksi pasca-CI (PR #28)

CI (`verify-code-quality`) menolak PR di dua tempat. Dua-duanya lolos di pemeriksaan lokal saya karena alat yang saya
pakai berbeda dari gate CI:

| Gate CI | Penyebab | Kenapa luput | Perbaikan |
|---|---|---|---|
| `pkg gosec (HIGH)`: 1 temuan, G115 `int64 -> uint64` di `pkg/rabbitmq/envelope.go` | Perbaikan F5 saya memindahkan konversi `uint64->byte` menjadi `uint64(t.UnixMilli())`, yang oleh gosec v2.22.0 dinilai HIGH | Saya memverifikasi lewat gosec bawaan `golangci-lint` (versi dan severity berbeda), padahal CI memakai `gosec v2.22.0` mandiri dan menghitung severity HIGH. Klaim "gosec bersih di `envelope.go`" di bagian 7 hanya berlaku untuk alat itu | Stempel waktu dicetak langsung sebagai 12 digit hex (`%012x`) tanpa konversi bilangan bulat. Terbukti di Docker (Go 1.26, gosec v2.22.0): HIGH 0 |
| `govulncheck`: 1 temuan di 6 modul, GO-2026-6859 (`net/http`) | Dipublikasikan 2026-11-11 02:51 UTC, setelah run hijau terakhir `main` (2026-11-10). Berlaku juga untuk `main`, bukan akibat PR ini. Diperbaiki di Go 1.26.11 dan 1.27.4 | Bagian 7 mencatatnya sebagai toolchain lokal (go1.27.3); di CI `setup-go` membaca `go 1.26.10` dari `go.mod` | `go 1.26.11` di semua `go.mod` dan `go.work`. Terbukti di Docker (Go 1.26.11, govulncheck v1.1.4): 0 hasil di keenam modul |

Pelajaran: verifikasi memakai alat dan versi yang sama persis dengan gate CI (`gosec@v2.22.0`, `govulncheck@v1.1.4`,
severity HIGH), bukan padanannya.
