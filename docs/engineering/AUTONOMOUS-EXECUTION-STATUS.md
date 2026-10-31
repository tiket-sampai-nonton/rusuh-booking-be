# Autonomous Execution Status

Terakhir diperbarui: 2026-10-31 (akhir sesi). Dipakai untuk melanjutkan pekerjaan tanpa konteks percakapan.
Branch kerja: `feature/snap-payment-ci-hardening` (satu PR, commit bertahap per task, **belum di-push**). Fokus: repo BE; repo mobile hanya referensi kontrak (tidak diubah; prioritas kontrak = mobile).
Hasil audit lengkap, bukti, dan traceability matrix: `docs/engineering/AUDIT-FINAL.md`.
Status: Backlog / Ready / In Progress / Blocked / In Review / Done. Story point Fibonacci. Done = kriteria penerimaan terbukti dengan tes yang dijalankan.

## Keputusan pemilik (diterapkan)

1. Midtrans **Snap saja**, tanpa jalur Core API ganda → selesai (PAY-1).
2. Server key Sandbox di `services/payment/.env` dipakai untuk verifikasi → selesai (PAY-2; nilai tidak pernah dicetak).
3. Coverage: **tanpa pengecualian paket**; menulis tes nyata → selesai (TST-2/TST-3).
4. Dokumen root `FE-BE-align-contract.md` / `FE-Flow-Diagrams.md` disamakan dengan salinan mobile → selesai (DOC-1; cadangan versi lama di scratchpad sesi).
5. Akses AWS: lihat "Human Confirmation Required".

## Backlog — selesai (136 SP)

| ID | Task | SP | Bukti ringkas |
|---|---|---|---|
| CI-1..4 | govulncheck (Go 1.26.10), gosec HIGH, gitleaks, gerbang kualitas | 7 | `verify-reports.sh quality` exit 0 dengan scanner versi CI |
| PAY-1 | Midtrans Snap saja (`redirectUrl`, return URL, bucket → `enabled_payments`, migrasi 0004) | 5 | unit + integration + E2E |
| PAY-2 | Verifikasi Snap di Sandbox nyata | 3 | `go test -tags sandbox` (token, semua bucket, pemilihan channel → PENDING) |
| PAY-3 | Status `orderId`, `paymentExpiresAt` | 1 | E2E + kontrak |
| PAY-4 | Dokumentasi Snap (ADR-024/025, SDD 04c, 08b) | 3 | – |
| API-1/2 | Booking/TicketList sesuai kontrak mobile; selisih sengaja dicatat | 4 | validator OpenAPI di E2E |
| BKG-1..4 | versi vs event stream, payment-lock idempoten, payload `booking.paid`, nilai balik `Settle` | 8 | tes regresi |
| BKG-5 | status event diturunkan; filter katalog sesuai SRS-F-28 | 3 | integration + mapper |
| BKG-6 | `/payable` mengembalikan status nyata (status 503 setelah PAID) | 1 | E2E |
| BKG-7 | `ExtendDeadline` selalu gagal | 1 | integration |
| BKG-8 | config di luar batas → 400 | 1 | integration + E2E |
| BKG-9 | `ReserveBatch` ulang idempoten | 2 | integration |
| BKG-10 | session sweeper EDITABLE | 2 | integration (gagal tanpa perbaikan) |
| QUE-1 | pengaturan event default + tolak event tak dikenal | 3 | unit + E2E |
| QUE-2 | email/NIK tersamar/akun ke Booking (e-tiket tidak pernah terkirim) | 3 | E2E |
| QUE-3 | promotor: prefix FIFO GRANTED dipromosikan | 3 | unit + E2E konkurensi |
| QUE-4 | `MarkNotifiedSoon` (Ex 600 ns) | 1 | integration Valkey |
| SEC-1 | token per scope; admin allow-list; guard fail closed | 5 | unit + E2E |
| SEC-2 | verifikasi token visitor (IDOR) | 3 | unit + E2E |
| SEC-3 | tiket hanya untuk pemilik / tautan email; kode tiket 80 bit; URL e-tiket | 5 | unit + E2E |
| SEC-4 | kunci PII wajib di luar lokal | 1 | unit |
| TST-1 | perbaiki integration test booking; DB sementara | 3 | tiga run berturut lulus |
| TST-2 | coverage booking 50,8 → 81,0% | 13 | `make cover` |
| TST-3 | coverage queue 64,5 → 83,6% | 8 | `make cover` |
| TST-4 | E2E lintas service + validasi OpenAPI + `tools/e2e.sh` + `make test-e2e` | 8 | 362 dtk, lulus |
| TKT-1 | audit QR/tiket | 3 | AUDIT-FINAL §4 |
| NTF-1 | audit notifikasi | 2 | AUDIT-FINAL §4 |
| INF-1 | Terraform staging (HLD §4.5) | 8 | `fmt`/`validate`/Trivy |
| AUD-1 | traceability matrix SRS | 13 | AUDIT-FINAL §9 |
| AUD-2 | audit keamanan/keandalan/CI/privasi | 13 | AUDIT-FINAL §2, §5–§7 |
| IMG-1 | API unggah gambar event (`POST /internal/v1/admin/events/{id}/images`; local git-ignored / S3 + bucket Terraform; validasi isi file, ≤ 5 MB) | 8 | unit + integration + E2E + OpenAPI; URL belum ada di kontrak katalog FE (perlu keputusan kontrak mobile) |
| RL-1 | Rate limit per IP configurable, dalam-memori (`httpx.RateLimit`; Booking, Payment, webhook terpisah) | 5 | unit + handler |
| JIRA-1 | Sinkron Jira SCRUM (epic SCRUM-5..13 dan SCRUM-41 fitur inti; task SCRUM-14..40, 42, 43; story SCRUM-44..50 per kelompok area SRS; story point + status) | 2 | Jira |
| R5/R6 | `currentAttempt` hanya attempt ber-redirectUrl; klaim `nik_masked` di token User → Queue → Booking | 2 | unit |
| API-3 | `imageUrl`/`bannerUrl`/`thumbnailUrl` pada `EventSummary` (katalog, detail) sesuai FE NEED THIS | 2 | unit + E2E + OpenAPI |
| R1 | Proyektor ketersediaan SSE (`availability-projector`, `SetAvailability`, `OnSaleAvailability`) | 5 | unit + integration |
| DOC-1 | sinkron dokumen root dengan salinan mobile | 2 | – |
| DOC-2 | ADR-024/025, README, tests/README, OpenAPI | (termasuk di atas) | – |

## Backlog — tersisa (PDP-1b 3, INF-3 2 blocked, INF-2 8, PERF-1 13, R8 2 = 28 SP)

| ID | Task | Prio | SP | Status | Catatan / desain singkat |
|---|---|---|---|---|---|
| PDP-1 | **Enkripsi `buyerEmail` di Booking** (UU PDP Pasal 39) | P1 | 8 | Backlog | AES-GCM (kunci `PII_ENC_KEY`, AAD per kolom) di `admissions`/`bookings`, kolom hash untuk pencarian, migrasi backfill + hapus kolom plaintext. Payload event (`booking.paid/review/expired`) dan `outbox_events` berisi email plaintext: kirim ciphertext, Notification membuka dengan kunci bersama (pola `otp.requested`). Dekripsi hanya di jalur kirim notifikasi |
| INF-3 | `terraform plan/apply` ke AWS | P2 | 2 | **Blocked** | butuh akses AWS; ditunda sesuai keputusan pemilik |
| INF-2 | CloudFront+WAF, Route 53, ACM, web Next.js, Prometheus/Grafana, scheduled scaling | P2 | 8 | Backlog | digabung dengan penyiapan Terraform nanti; butuh domain |
| PERF-1 | Load test (k6), EXPLAIN, pen-test | P2 | 13 | Backlog | dikerjakan bersama penyiapan infra/Terraform nanti |
| R1 | ~~Penulis snapshot ketersediaan SSE~~ | – | 5 | **Done** | job `availability-projector` (lihat bagian selesai) |
| R2 | Webhook verifikasi ulang Status API | – | 0 | **Dibatalkan** | keputusan pemilik: keamanan env tanggung jawab pemilik |
| R8 | Jalankan pipeline GitHub Actions; merge Dependabot | P2 | 2 | Backlog | butuh runner/repo remote |

## Human Confirmation Required

1. **Akses AWS untuk `terraform plan`.** Login Google di browser tidak memberi akses CLI. Opsi (rekomendasi urut): (a) IAM Identity Center/SSO — jalankan sendiri `aws configure sso` lalu `aws sso login --profile rusuh` di terminal Anda, dan beri tahu nama profile; saya hanya memakai profile itu, kredensial tidak lewat percakapan; (b) role terbatas (read-only + izin plan) lewat `AWS_PROFILE`; (c) access key IAM user — paling tidak disarankan, jangan ditempel di chat. `apply` tetap hanya dengan persetujuan eksplisit Anda. Memblokir INF-3 saja.
2. **Push dan PR** branch ini (menunggu persetujuan; sesuai CLAUDE.md saya tidak push/PR tanpa diminta).
3. **Domain/ACM** untuk INF-2 (CloudFront, HTTPS wajib di ALB).
4. ~~RL-1 limiter Payment~~: diputuskan dalam-memori; bila perlu batas global antar instance, pindah ke Valkey.
5. **Breaking operasional**: `ADMIN_EMAILS`, `TICKET_LINK_KEY`, `PII_*` Booking, `TICKET_LINK_BASE_URL` harus diisi di environment nyata (README).

## Perintah verifikasi

```bash
make infra && make test-integration && make cover      # unit + integration + ambang 80%
make test-e2e                                          # E2E lintas service (E2E_SLOW=1 untuk expiry)
make contracts && make isolation
bash tools/verify-reports.sh quality <dir-laporan>     # gerbang kualitas CI (laporan dari scanner versi CI)
cd services/payment && go test -tags sandbox -run TestSandbox ./internal/payment/infrastructure/midtrans/
cd deployments/terraform && terraform init -backend=false && terraform validate
```
