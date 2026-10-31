# Autonomous Execution Status

Terakhir diperbarui: 2026-10-31. Dipakai untuk melanjutkan pekerjaan tanpa konteks percakapan.
Branch kerja: `feature/snap-payment-ci-hardening` (satu PR, commit per task). Fokus: repo BE; repo mobile hanya referensi kontrak (tidak diubah).
Status: Backlog / Ready / In Progress / Blocked / In Review / Done. Story point Fibonacci.

## Baseline

- Stack: Go monorepo (pkg + queue, booking, payment, notification, user), PostgreSQL, Valkey, RabbitMQ. OpenAPI di `docs/contracts/openapi/*.yaml`, divalidasi `make contracts`.
- Sebelumnya payment memakai Midtrans Core API; kontrak mobile (`rusuh-booking-mobile/.genesis/docs/api-contract`) sudah memakai Snap. Salinan root `FE-BE-align-contract.md` dan `FE-Flow-Diagrams.md` (di luar repo git) lebih lama daripada salinan mobile dan **belum disinkronkan**.

## Backlog

| ID | Epic | Judul | Prio | SP | Status | Bukti / catatan |
|---|---|---|---|---|---|---|
| CI-1 | CI/CD | govulncheck: stdlib Go 1.26.x rentan | P0 | 2 | **Done** | `go 1.26.10` di go.work + go.mod; govulncheck 6 modul bersih |
| CI-2 | CI/CD | gosec HIGH (G115 x2 pkg, G101 x2 notification) | P0 | 2 | **Done** | `#nosec` beralasan; gosec HIGH = 0 di 6 modul |
| CI-3 | CI/CD | gitleaks 7 temuan | P0 | 2 | **Done** | fixture Idempotency-Key; `.gitleaksignore` per fingerprint; tidak ada rahasia nyata, rotasi tidak perlu |
| CI-4 | CI/CD | `verify-reports.sh quality` lulus | P0 | 1 | **Done (lokal)** | exit 0 dengan scanner versi CI. Belum dibuktikan di runner GitHub |
| PAY-1 | Payment | Midtrans Snap saja (hapus charge Core API) | P0 | 5 | **Done** | `Attempt` = `redirectUrl`/`successReturnUrl`/`failedReturnUrl`; bucket metode → `enabled_payments`; migrasi 0004; unit + integration payment lulus (journey memakai fake Snap) |
| PAY-2 | Payment | Verifikasi Snap di Sandbox nyata | P1 | 3 | **Blocked** | butuh `MIDTRANS_SERVER_KEY` sandbox. Belum terverifikasi: `expiry.unit=minute`, `X-Override-Notification` pada Snap, payment_type di webhook |
| PAY-3 | Payment | Status `GET /payments/{id}/status`: `orderId`, `paymentExpiresAt` | P1 | 1 | **Done** | sesuai kontrak mobile; tes journey |
| PAY-4 | Payment | Dokumentasi Snap (ADR-024, SDD 04c, 08b, OpenAPI) | P1 | 3 | **Done** | |
| API-1 | Kontrak | Selaraskan Booking/TicketList dengan kontrak mobile (`orderId`, `ticketQuantity`, `buyerNikMasked`, `publicRef`, `amountMinor`) | P1 | 3 | **Done** | mapper + repo + OpenAPI; `make contracts` ok |
| API-2 | Kontrak | Perbedaan sengaja yang tersisa | P2 | 1 | **Done (dicatat)** | Queue `JoinRequest.buyerNik` opsional untuk user login (BE benar, kontrak mobile menandai wajib). `Attempt.redirectUrl` hanya ada saat PENDING (mobile menandai wajib). Internal booking: `publicRef`, endpoint admin hanya di BE |
| BKG-1 | Booking | Bug: hold terkunci langsung menambah event v2 tetapi `bookings.version` tetap 1 → payment-lock/settle/expire bentrok versi | P0 | 3 | **Done** | fix + tes regresi |
| BKG-2 | Booking | Bug: `PaymentLock` tidak idempoten (cek `status==VERIFYING`, padahal status tetap PENDING_PAYMENT) → event/versi bertambah tiap lock (mis. ganti metode) | P0 | 2 | **Done** | cek `payment_started_at` |
| BKG-3 | Booking/Notif | Bug: payload `booking.paid` tanpa objek `event` (nama, venue, waktu) padahal dipakai e-ticket | P1 | 2 | **Done** | |
| BKG-4 | Booking | `Settle` mengembalikan true untuk jalur REVIEW | P2 | 1 | **Done** | |
| TST-1 | Testing | Perbaiki integration test booking (UUID non-hex, search_path, supply, NIK, public_ref) | P1 | 3 | **Done** | suite booking lulus 3× berturut dengan `-race` |
| INF-1 | AWS | Terraform staging sesuai HLD §4.5 | P2 | 8 | **Done (tervalidasi, belum di-plan)** | `fmt -check`, `validate` (TF 1.17.1), Trivy HIGH/CRITICAL bersih. `plan`/`apply` belum (tanpa akses AWS) |
| INF-2 | AWS | CloudFront+WAF, Route53, web Next.js, Prometheus/Grafana, scheduled scaling | P2 | 8 | Backlog | di luar INF-1; butuh domain |
| TKT-1 | Ticketing | Audit QR | P1 | 3 | **Done (audit)** | lihat bawah |
| NTF-1 | Notification | Audit pemicu notifikasi | P1 | 2 | **Done (audit)** | lihat bawah |
| **TST-2** | Testing | **Coverage logika booking 50,8% (ambang 80%)** | P0 | 13 | **Backlog** | terendah: infrastructure/postgres 19%, valkey 0%, http 56%, rabbitmq 59%. Akan menggagalkan `case-coverage` di CI |
| **TST-3** | Testing | **Coverage logika queue 64,5% (ambang 80%)** | P0 | 8 | **Backlog** | adapter valkey 11,5%, postgres 0%, bookingclient 78% |
| AUD-1 | Audit | Matriks keterlacakan requirement (PRD/SRS → kode → tes) | P1 | 13 | Backlog | belum dibuat |
| AUD-2 | Audit | Audit keamanan/performa menyeluruh (IDOR, rate limit, N+1, indeks) | P1 | 13 | Backlog | belum dilakukan |
| TST-4 | Testing | E2E lintas service (booking→bayar→tiket→email) | P1 | 8 | Backlog | hanya journey payment dengan fake Midtrans yang ada |
| DOC-1 | Docs | Sinkronkan `FE-BE-align-contract.md` dan `FE-Flow-Diagrams.md` di root dengan salinan mobile | P2 | 2 | Backlog | file di luar git; perlu keputusan pemilik |

Total 25 task, 112 SP. Done: 44 SP. Blocked: 3 SP. Backlog: 65 SP.

## Temuan audit

**Ticketing/QR (TKT-1).** `qrPayload = "RBT:" + ticketCode`, `ticketCode = "TK-" + 12 hex dari UUID acak item` (48 bit), unik per item (`UNIQUE`), diterbitkan sekali (`ON CONFLICT DO NOTHING`) hanya saat PAID, tanpa PII. Tidak ada endpoint check-in/validasi di PRD/SRS/kontrak mobile, jadi tidak diimplementasikan. Bila check-in ditambahkan: payload perlu ditandatangani (HMAC/EdDSA) dan validasi server-side beserta status redeemed.
**Notifikasi (NTF-1).** E-ticket dikirim dari `booking.paid`, yang hanya diterbitkan outbox Booking setelah `Settle` commit (setelah webhook Midtrans berverifikasi signature + nominal). Notifikasi sukses tidak mendahului verifikasi backend. `booking.expired`/`booking.review`/`queue.turn.*`/`otp.requested` ada. Tidak ada notifikasi "payment pending/failed" (tidak ada di requirement yang dibaca).
**Risiko Snap.** Token Snap tidak bisa dibatalkan; pembayaran lewat token lama setelah attempt diganti masuk `SETTLED_AFTER_CANCEL` (refund manual) — didokumentasikan di ADR-024.

## Hasil yang benar-benar dijalankan

| Pemeriksaan | Hasil |
|---|---|
| `tools/verify-reports.sh quality` (gosec 2.22.0, govulncheck 1.1.4, hadolint 2.12.0, gitleaks 8.18.4, toolchain 1.26.10) | **Lulus**, exit 0 (lokal) |
| golangci-lint pkg + 5 service | 0 issues |
| `go build`, `make isolation`, `make contracts` | lulus |
| Unit + integration test `-race` (infra docker): pkg, payment, booking, queue, notification, user | **Lulus** |
| Coverage logika (`tools/check-coverage.sh`, ambang 80%): pkg 90,6 · payment 90,0 · notification 82,6 · user 92,4 · **queue 64,5 · booking 50,8** | queue dan booking **Gagal** (sudah rendah sebelum perubahan) |
| Terraform `fmt -check`, `validate`, Trivy config HIGH/CRITICAL | Lulus. `plan` tidak dijalankan |
| Tes Sandbox Midtrans (`-tags sandbox`) | Tidak dijalankan (tanpa server key) |
| Frontend (tests, E2E, mockup) | Tidak dijalankan: repo mobile hanya dipakai sebagai referensi kontrak |
| Pipeline GitHub Actions | Tidak dijalankan |

## Human Confirmation Required

1. **Server key Sandbox Midtrans** untuk PAY-2 (verifikasi Snap nyata). Memblokir keyakinan produksi, bukan task lain.
2. **Coverage booking/queue (TST-2/TST-3, 21 SP):** menaikkan ke 80% berarti menulis banyak tes adapter. Opsi: (a) tulis tes (direkomendasikan), (b) kecualikan adapter infrastruktur dari hitungan seperti `cmd/`/`delivery/` — melonggarkan gerbang, tidak saya lakukan tanpa persetujuan. Memblokir `case-coverage` CI.
3. **Akses AWS + domain/ACM** untuk `terraform plan/apply` dan INF-2. Apply tidak akan dijalankan tanpa persetujuan eksplisit.
4. **Salinan root** `FE-BE-align-contract.md`/`FE-Flow-Diagrams.md` lebih lama dari salinan mobile: timpa atau biarkan?
5. **Memerlukan merge/PR**: semua perubahan ada di branch lokal; belum di-push.
