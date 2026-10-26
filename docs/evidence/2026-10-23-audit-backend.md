# Evidence — Audit Backend dan Rencana Perbaikan (23 Oktober 2026)

Peserta: arnu12rahman (BE-1), dibantu Claude Code (Opus 5.5). Temuan Booking/Queue/User perlu dikonfirmasi nuzirwan (BE-2).

Audit dilakukan read-only terhadap `main` (`ec57805`) dengan baseline skeleton `c0d0c58`. Acuan: `Tender Rusuh Booking Ticket.pdf` (FR/NFR/BR) dan `Kompetensi_Backend_Developer_Level_3_4.md` (Level 3 wajib, Level 4 nilai tambah).

## 1. Ringkasan

- Fondasi sudah kuat: hexagonal per service, urutan lock terdokumentasi, CHECK constraint anti-oversell, event sourcing Booking, outbox, retry + DLQ, dan CI lengkap (gosec, govulncheck, gitleaks, Trivy + SBOM, coverage gate).
- Journey utama Antrian → Booking → Bayar → E-tiket **belum bisa selesai**:
  1. Payment Service baru schema dan `/health`.
  2. Ada empat bug P0 di Booking yang lolos karena adapter Postgres Booking tidak punya integration test.
  3. Endpoint internal/admin Booking menerima token user dan admission token.
- Keputusan audit: **MAJOR GAPS**. Perbaikan Booking kecil (puluhan baris); pekerjaan besar hanya Payment.

## 2. Keputusan baru: Payment wajib Midtrans

Requirement tambahan dari BE-1 (23 Okt): pembayaran **wajib memakai Midtrans**. Akun Sandbox sudah didaftarkan. Rencana di bagian 7 mengikuti keputusan ini:

- Adapter `midtrans` (Core API charge/status/cancel + verifikasi `signature_key` SHA-512) dibangun sesuai `specs/payment-service/design.md` §5. Desain itu tetap diverifikasi ke dokumentasi resmi Midtrans saat implementasi (OI-05).
- Simulator **tidak** dibangun sebagai provider. Test memakai fake `IPaymentGateway` (unit) dan `httptest` server yang meniru Midtrans (integration), jadi CI tidak bergantung pada jaringan.
- Kunci Midtrans hanya di `.env` lokal dan secret deploy, tidak pernah di-commit (`services/payment/.env.example` sudah menyiapkan variabelnya).
- Webhook butuh URL publik. Untuk demo lokal dipakai tunnel (mis. ngrok); `GET status` ke Midtrans menjadi fallback bila webhook terlambat.

## 3. Hasil pemeriksaan

| Pemeriksaan | Hasil |
|---|---|
| Unit test semua modul (`go test ./...`) | Lulus, setelah `go.mod` disinkronkan di salinan sementara |
| `GOWORK=off go test` di 5 service | **Gagal**: `go.mod` tidak sinkron (`amqp091-go` 1.15.0 vs 1.16.0 di `pkg`) |
| `make build` / `make test` dengan `go.work` | **Gagal**: `go.work` memuat `./tests` yang tidak punya `go.mod` (commit `f91dea3`) |
| Integration test (`-tags integration`) | Tidak dijalankan (butuh `make infra`) |
| Status CI GitHub | Tidak diperiksa (`gh` tidak tersedia); kemungkinan merah karena drift `go.mod` |
| Ukuran perubahan sejak skeleton | 706 file, +59.852 / −3.147 baris |

Coverage adapter Postgres: Booking, Notification, Queue, dan User 0% pada unit test. Integration test hanya ada di `pkg/*`, Queue (Valkey), dan User.

## 4. Temuan

### P0 — Critical

**F1. Payment Service belum dibangun.** Hanya migrasi dan `/health`; tidak ada route `/payments` dan semua task di `specs/payment-service/tasks.md` masih `[ ]`. Melanggar FR-20, FR-21, FR-24, FR-25, NFR-08. Owner: BE-1.

**F2. Endpoint internal/admin Booking bisa diakses pembeli.**
- Bukti: `services/booking/internal/delivery/booking_module.go:153-154` memakai `MultiBearerVerifier(userVerifier, queueVerifier)` untuk `/internal/v1/*` dan `/internal/v1/admin/*`.
- Token user, atau admission token yang ditandatangani kunci Queue, lolos ke `allocations:batch`, `payment-lock`, `deadline-extension`, dan `admin/events`. Claim `scope` di `pkg/token` tidak dicek.
- Guard fail-open bila verifier nil (`internal_handler.go:81-86`, `admin_handler.go:35-40`), dan `loadVerifier` menelan error baca/parse kunci.
- Dampak: antrian bisa dilewati, deadline sendiri bisa diperpanjang (NFR-05, NFR-07; OWASP A01).
- Minimal fix: verifier khusus service token yang mewajibkan claim service; service gagal start bila kunci tidak ada.
- Owner: BE-2.

**F3. `payment-lock` selalu gagal.**
- Bukti: `services/booking/internal/booking/infrastructure/postgres/unit_of_work.go:540-547` men-set `status='VERIFYING'` tanpa `verification_until`.
- Migrasi punya `CHECK ((status <> 'VERIFYING') OR verification_until IS NOT NULL)`, jadi setiap lock gagal dengan 500 dan pembayaran tidak pernah bisa dimulai.
- Belum tercatat di tasks.
- Minimal fix: isi `verification_until` (mis. `payment_expires_at`).
- Owner: BE-2.

**F4. Stok dialokasikan ganda setelah hold.**
- Bukti: tidak ada kode yang mengubah admission ke `EDITABLE`/`CONVERTED` setelah hold.
- `hold()` memindahkan unit dari `allocated_units` ke `held_units`. Saat TTL seleksi habis, `ExpireSession` (`admission_repo.go:260`) mengurangi `allocated_units` lagi untuk admission yang masih `ACTIVE`.
- Akibatnya CHECK `allocated_units >= 0` gagal terus (sweeper error tiap tick dan admission macet), atau muncul supply hantu: orang masuk tanpa stok ("PHP").
- Oversell tetap dicegah CHECK `category_inventories`.
- Akar masalah: task 10.R1 sudah `[x]`, tetapi 6.R3 (admission → `EDITABLE` setelah hold) masih `[ ]`.
- Melanggar NFR-02, FR-18, BR-03/04.
- Owner: BE-2.

**F5. Payload `booking.paid` melanggar kontrak.**
- Bukti: `unit_of_work.go:351` hanya mengirim `{bookingId, publicRef, buyerEmail}`. `docs/contracts/events/booking.paid.v1.schema.json` mewajibkan juga `tickets` dan `ticketLinkToken`.
- Notification menolaknya sebagai poison → DLQ (`booking_paid_payload.go:48`), jadi email e-tiket (FR-22) tidak pernah terkirim.
- Minimal fix: lengkapi payload dan tambah test kontrak di sisi producer.
- Keputusan terbuka: siapa yang membuat `ticketLinkToken`.
- Owner: BE-2.

### P1 — Required

**F6. Settlement terlambat menandai booking `EXPIRED` sebagai `PAID`.**
- Bukti: guard di `unit_of_work.go:270` hanya `status == PAID`. Booking `EXPIRED`/`CANCELLED` yang dibayar terlambat menjadi `PAID` tanpa tiket.
- Melanggar BR-08 dan integritas data.
- Minimal fix: bila `!IsPayable()`, jangan settle dan tandai `REVIEW`.
- Owner: BE-2.

**F7. Tidak ada integration test adapter Postgres Booking.**
- Inilah sebab F3, F4, F6 lolos.
- Kompetensi Level 3 "sample code concurrent data access" dan NFR-04 belum terbukti.
- Minimal fix: satu file integration test untuk (a) N goroutine berebut unit terakhir, (b) hold → sweep → settle menjaga invarian `allocated+held+sold`.
- Owner: BE-2 / bersama.

**F8. Build rusak.** Drift `go.mod` di 5 service dan `./tests` di `go.work`. Minimal fix: `make tidy` dan hapus `./tests` dari `go.work`. Owner: BE-1 (platform).

**F11. Kunci PII jatuh ke kunci serba-nol.** `booking_module.go:126-131` memakai `devKeyHex` bila `PII_HASH_KEY`/`PII_ENC_KEY` kosong. Minimal fix: fail-fast kecuali lingkungan lokal. Owner: BE-2.

### P2 — Valuable

- **F9.** Record idempotency hold tertinggal `IN_PROGRESS` saat hold gagal (`hold.go:59`), sehingga retry dengan key yang sama ditolak selama 24 jam. Fix: hapus record saat gagal.
- **F10.** `replace()` mengunci booking → category → supply, terbalik dari `hold()`. Bisa deadlock 40P01 yang tidak di-retry (user mendapat 500). Fix: kunci `event_supply` lebih dulu.
- **F12.** NFR-06 (p95 < 1 detik) tanpa bukti karena `tests/k6` kosong. Fix: satu skrip k6 untuk `queue-status` + join, hasilnya dicatat.
- **F13.** NFR-09 hanya punya metrik HTTP. Fix: metrik panjang antrian, konversi, dan rate expired lewat `pkg/telemetry`.

### P3 — Optional

- **F14.** `dump.rdb` ter-commit walau ada di `.gitignore`.
- **F15.** Dokumentasi ±24.500 baris; DDL disalin ke 6 dokumen (55 `CREATE TABLE`), dan desain ada di tiga lapis (`04-SDD-RBT-001`, `04a–e`, `specs/*/design.md`). Fix: catatan sumber kebenaran di `docs/README-RBT.md`.
- `ReleasePromoterLease` (Queue) menjalankan `DEL` tanpa cek pemilik lease.

## 5. Status requirement

| Status | Requirement |
|---|---|
| PASS (kode) | FR-01..05, FR-11..15, FR-17, FR-19, BR-01, BR-02, BR-07, NFR-10 |
| PARTIAL | FR-06, FR-07, FR-10, FR-16, FR-23, BR-05, BR-06, BR-08, NFR-01, NFR-03, NFR-04, NFR-09 |
| FAIL | FR-18, FR-20, FR-21, FR-22, FR-24, FR-25, BR-03, BR-04, NFR-02, NFR-05, NFR-07, NFR-08 |
| NOT VERIFIED | FR-08, FR-09, NFR-06 |

Ambiguitas di PDF: FR-13 ("tanpa NIK tidak dapat lanjut") bertentangan dengan BR-01 ("bila NIK tidak digunakan, kuota per sesi"). Implementasi memilih NIK wajib dan menambah aturan NIK attendee unik per event (`uq_attendee_nik_per_event`). Keduanya perlu tercatat sebagai asumsi.

## 6. Kompetensi

- **Level 3 sudah terbukti:** rancangan DB/aplikasi, microservices, event sourcing (Booking), messaging, kontrak API tervalidasi CI, circuit breaker (Queue → Booking), rate limiting (OTP, join), error handling dan log dengan redaksi PII, CI/CD, notifikasi email + push.
- **Level 3 belum terbukti:** demo concurrent data access (F7), integration test jalur kritis (F7), callback pembayaran idempoten (F1), access control (F2, F11).
- **Level 4 sudah ada:** ADR trade-off, security testing di CI.
- **Level 4 yang layak ditambah:** k6 (F12) dan analisis integritas data lewat test konkuren (F7).

## 7. Rencana perbaikan (urutan)

Temuan Booking ditulis sebagai sub-task R di `specs/booking-service/tasks.md` untuk BE-2, kecuali diputuskan lain.

| Batch | Isi | File (perkiraan) | Acceptance |
|---|---|---|---|
| B0 | Catat F2–F6, F9–F11 sebagai task BE-2 | `specs/booking-service/tasks.md` | Tiap temuan punya ID, bukti, acceptance |
| B1 | F8, F14 | `go.work`, 5× `go.mod`/`go.sum`, `dump.rdb` | `make build test isolation` lulus; `GOWORK=off go test ./...` lulus |
| B2 | F3, F4, F6 + integration test F7 | `unit_of_work.go`, 1 test baru (±300 baris) | Lock → `VERIFYING` sukses; admission `EDITABLE` setelah hold; invarian supply konsisten setelah sweep; settle atas `EXPIRED` tidak menjadi `PAID` |
| B3 | F2, F11 | 4 file Booking (±80 baris + test) | Token user/admission ke `/internal/*` mendapat 401; service gagal start tanpa kunci |
| B4 | F5 | `unit_of_work.go` + test kontrak | Payload lolos `booking.paid.v1.schema.json` |
| B5 | Payment task 0.1–0.4, 9, 3: config Midtrans, domain attempt, klien Booking (`payable`, `payment-lock`), adapter Midtrans Core API + breaker, `POST` attempt | ±7 file, ±700 baris | Charge Sandbox menghasilkan instruksi VA/QRIS; dua charge paralel menghasilkan satu attempt terbuka |
| B6 | Platform 3.1/3.2/3.4 (`pkg/outbox`) + Payment task 4, 5: webhook SHA-512 constant-time, dedup `UNIQUE(provider, fingerprint)`, outbox `payment.succeeded`/`payment.failed` | ±6 file, ±600 baris | Callback ganda paralel menghasilkan satu perubahan state; signature salah mendapat 401 |
| B7 | Payment task 7.1, 6, 1, 2, 11, 13: resolve, status, payment page, page token, isolasi `/internal` | ±6 file, ±500 baris | E2E Sandbox: hold → bayar → booking `PAID` → email di Mailpit |
| B9 | Notification task 0a.R1 + 0a.2 | 2 file, ±120 baris | Email yang gagal terkirim ulang lewat retry queue; `SENT` tidak dikirim dua kali |
| B8 | F12, F13 | ±3 file, ±200 baris | Laporan p95 k6; metrik bisnis tampil di Grafana |

B5–B7 melebihi budget 5 file / 300 baris per batch karena Payment adalah fitur MUST yang belum ada sama sekali. Setiap batch tetap bisa diuji sendiri.

Ketergantungan: B5 butuh F3 (B2) agar `payment-lock` berhasil, dan email di B7 butuh F5 (B4).

**Keputusan terbuka:** Payment tanpa event sourcing (`payment_attempts` sebagai state + `payment_callbacks` UNIQUE + outbox). Alasannya: event sourcing sudah dibuktikan di Booking dan state machine attempt pendek. Tabel `payment_events` dibiarkan tidak dipakai (migrasi tidak diubah). Bila disetujui, ADR-019 perlu diamandemen.

## 8. Non-goals

- Simulator sebagai provider produksi, page token WebView terpisah, refund case otomatis, event sourcing Payment.
- Menghapus atau merefaktor fitur di luar PDF yang sudah ada (edit pesanan, perpanjangan deadline, `REVIEW`, FCM, admin event). Fitur ini dibekukan, tidak dikembangkan.
- Menggabungkan atau menulis ulang SDD/HLD.
- Terraform/IaC, deploy nyata, BDD godog, job rekonsiliasi.
- Refactor gaya atau abstraksi baru di `pkg/`.

## 9. Batas verifikasi

- Integration test, lint, dan status CI tidak dijalankan.
- Lua Queue, User service, serta repo web dan mobile tidak diaudit mendalam.
- Dokumen dibaca sebagai sampel.
- F3–F6 diverifikasi dengan membaca kode dan schema, belum dijalankan ke PostgreSQL.

## 10. Rekonsiliasi spec Payment, Notification, dan platform terkait (23 Oktober 2026)

Sumber yang dibandingkan: `specs/{payment-service,notification-service,platform}/{requirements,design,tasks}.md`, kontrak `docs/contracts/`, kode `services/{payment,notification,booking}`, dan laporan ini (§1–9). Hasilnya diterapkan ke `specs/payment-service/tasks.md` dan `specs/notification-service/tasks.md`. `requirements.md` dan `design.md` sengaja tidak diubah; konflik di bawah perlu diselaraskan terpisah.

### 10.1 Temuan baru

- **F16 (P0, BE-2). Klien resolve Booking melanggar kontrak.** `services/booking/internal/booking/infrastructure/paymentclient/client.go` memanggil `POST /internal/v1/payments/{id}/resolve` tanpa body dan membaca `{"data":{"paid":bool}}`. Kontrak `docs/contracts/openapi/internal-payment.yaml` mewajibkan `mode` (`CHECK`/`CHECK_AND_CANCEL`) dan membalas `outcome` (`SUCCEEDED`/`NOT_PAID`/`NO_ATTEMPT`/`UNKNOWN`). Dampak: `UNKNOWN` akan dibaca sebagai "belum bayar", sehingga stok dilepas walaupun pembayaran mungkin settle. Payment mengikuti kontrak; Booking yang diperbaiki.
- **F17 (konflik spec, tanpa perubahan kode).** Spec Payment R5.1/Δ7 dan spec Notification memakai routing key `*.v1`, sedangkan semua kode (producer Booking, consumer Booking dan Notification) memakai routing key tanpa versi. Yang berlaku: kode, karena producer dan consumer sudah konsisten; Payment menerbitkan `payment.succeeded`/`payment.failed`. Penggantian ke `.v1` hanya lewat platform 12.1 (tidak direncanakan).
- **F18 (konflik dengan PDF).** Payment R3.9 meminta `deadline-extension` saat breaker charge OPEN. PDF BR-05 menetapkan TTL pembayaran 10 menit sejak booking dibuat; perpanjangan mengubah aturan bisnis. Task 3 tidak memanggilnya; Payment cukup membalas `503` (breaker OPEN).
- **F19 (P1, BE-1). Notification 0a.R1 terkonfirmasi.** `ClaimDelivery` memakai `ON CONFLICT DO NOTHING` (`services/notification/internal/notification/infrastructure/postgres/delivery_repository.go:30`). Pesan dari retry queue dianggap duplikat, jadi email yang gagal tidak pernah dikirim ulang (NFR-10, FR-22, FR-23).
- **F20 (P3, belum diverifikasi runtime).** `last_error` menyimpan `err.Error()` mentah; balasan SMTP saat `RCPT` gagal bisa memuat alamat email. Hanya berdampak di Mailpit lokal karena adapter Resend sudah membersihkan error.
- **F21 (konflik spec).** `specs/payment-service/requirements.md` menulis Owner BE-2, sedangkan `tasks.md` dan keputusan 21 Okt menetapkan arnu12rahman. Requirement R10 (simulator) bertentangan dengan keputusan "Payment wajib Midtrans".
- **Dependensi yang belum ada.** Payment Task 5 memakai `pkg/outbox`, tetapi paket itu belum ada (platform 3 belum dikerjakan). Payment Task 0.4/11 memakai `ServiceAuth` dan `ServiceTokenSource` (platform 2.3), dan Task 1 memakai `VisitorAuth` (platform 2.1); keduanya belum dikerjakan.

### 10.2 Verdict task Payment

| Task | Verdict | Alasan |
|---|---|---|
| 0.1 | KEEP WITH ADJUSTMENT | `PAYMENT_PROVIDER` dihapus karena hanya Midtrans |
| 0.2 | KEEP | State machine attempt dibutuhkan FR-25 |
| 0.3 | NEEDS CLARIFICATION | Event sourcing vs tabel state (keputusan terbuka); refund store dikeluarkan |
| 0.4 | KEEP WITH ADJUSTMENT | Tanpa `deadline-extension` (F18); terblokir F3 |
| 1 | KEEP | FR-20, FR-21 |
| 2 | KEEP | PDF meminta payment page web dipakai ulang di WebView (§2, sequence 8.4) |
| 3 | KEEP WITH ADJUSTMENT | Tanpa R3.9; bergantung Task 9, bukan Task 12; menyerap test Task 10 |
| 4 | KEEP WITH ADJUSTMENT | Tambah integration test callback paralel (FR-25, kompetensi L3 concurrent access) |
| 5 | KEEP WITH ADJUSTMENT | Routing key tanpa `.v1` (F17); bergantung platform 3 |
| 6 | KEEP | Halaman hasil bayar perlu status (FR-22) |
| 7 | SPLIT | 7.1 resolve wajib (FR-23, dipanggil sweeper Booking); 7.2 sweeper attempt prioritas rendah |
| 8 | REMOVE FROM PLAN | Refund tidak diminta PDF; REVIEW dan email review sudah ada |
| 9 | KEEP (dinaikkan) | Midtrans wajib; dikerjakan sebelum Task 3; Core API saja, tanpa fallback Snap |
| 10 | MERGE ke 3 dan 7.1 | Task 10 sendiri menyatakan isinya hanya test |
| 11 | KEEP WITH ADJUSTMENT | Hanya service token Booking yang diterima (pelajaran F2) |
| 12 | REMOVE FROM PLAN | Diganti Sandbox + fake gateway di test |
| 13 | KEEP WITH ADJUSTMENT | Fake gateway + E2E Sandbox manual; metrik counter saja (platform 4.2 selesai) |
| 14.1 | REMOVE FROM PLAN | Retensi bukan requirement PDF |

### 10.3 Verdict task Notification

| Task | Verdict | Alasan |
|---|---|---|
| 0a.R1 | KEEP (P1) | F19 terkonfirmasi |
| 0a.2 | KEEP WITH ADJUSTMENT | Jadikan integration test PostgreSQL dengan klaim paralel |
| 0b.R1 | REMOVE FROM PLAN | Push bukan requirement PDF |
| 0c, 1–5, 10 | ALREADY SATISFIED | Kode dan unit test ada; email e-tiket terblokir F5 di sisi Booking |
| 1.1 | KEEP WITH ADJUSTMENT | Fixture dari schema kontrak |
| 6 / 6.R1 | REMOVE FROM PLAN | API push di luar PDF |
| 7 / 7.1 / 7.2 | REMOVE FROM PLAN | OTP opsional; poison → DLQ sudah diuji di `pkg/rabbitmq` |
| 8.R1 | KEEP (P2) | Retry queue sudah memenuhi NFR-10; breaker sebagai pelindung |
| 8.2 | REMOVE FROM PLAN | Bergantung platform 9.3 (prioritas rendah) |
| 9.1 | KEEP WITH ADJUSTMENT (P3) | Dipersempit ke `last_error` (F20); redaksi log sudah di platform 4.4 |
| 11.1 | REMOVE FROM PLAN | Bukan metrik NFR-09 |
| 12.1 | REMOVE FROM PLAN | Retensi bukan requirement PDF |

### 10.4 Task platform yang relevan (tidak diubah di `specs/platform/tasks.md`)

| Task | Verdict | Alasan masuk scope |
|---|---|---|
| 2.1 `VisitorAuth` | KEEP | Payment page dan attempt mengautentikasi pembeli |
| 2.3 `ServiceAuth` + `ServiceTokenSource` | KEEP | Payment ↔ Booking internal; juga pola perbaikan F2 |
| 3.1, 3.2, 3.4 `pkg/outbox` | KEEP | Dipakai Payment Task 5 (NFR-10, tidak kehilangan event) |
| 3.3 metrik lag outbox | DITUNDA | Tidak diminta NFR-09 |
| 5.4 validasi payload sebelum publish | MERGE ke 12.2 | Test kontrak per producer lebih murah dan cukup menangkap F5 |
| 12.2 test kontrak payload di CI | KEEP | Kegagalan kontrak lintas service (F5, F16) adalah risiko terbesar Payment–Booking–Notification |
| 12.1 routing key ke `.v1` | REMOVE FROM PLAN | Refactor 5 service tanpa requirement (F17) |
| 7.3 profil `local-offline` simulator | REMOVE FROM PLAN | Simulator tidak dibangun |
| 8.2 cek `go mod tidy` di CI | KEEP | Akan mencegah drift seperti F8 |
| 9.3 redrive, 13.1 retensi | DITUNDA | Tidak dibutuhkan PDF |

### 10.5 Kompetensi yang dibuktikan lewat scope ini

- **Level 3:**
  - Kontrak dan dokumentasi API: `payment.yaml`, `internal-payment.yaml`.
  - Circuit breaker: Midtrans charge/status.
  - Concurrent data access: test charge paralel dan callback paralel.
  - Integrasi pihak ketiga dengan idempotency key.
  - Integration test otomatis di CI (fake gateway).
  - Secret management: server key hanya di env.
- **Level 4:**
  - Integrasi antar aplikasi lewat messaging (outbox → Booking → Notification).
  - Analisis integritas data pada settlement terlambat dan timeout ambigu (Property 6).
- Belum terbukti sampai B5–B9 selesai. Tidak ada task baru yang dibuat hanya untuk Level 4.

### 10.6 Urutan eksekusi

1. B1 (build).
2. Perbaikan Booking milik DEV-2: F3, F5, F16.
3. B9 (Notification 0a.R1).
4. Platform 2.1, 2.3, 3.
5. Payment B5 → B6 → B7.
6. Platform 12.2.

B5–B7 melebihi budget per batch karena Payment belum ada sama sekali; setiap batch tetap bisa diuji sendiri dengan fake gateway.

**Keputusan rekonsiliasi:** REVISE PLAN. Ada gap material (F16, F19, dependensi platform yang belum ada) dan pekerjaan tanpa dasar PDF (simulator, refund case, `deadline-extension`, push/OTP lanjutan, redrive, retensi).

## 11. Keputusan lanjutan BE-1 (23 Oktober 2026)

1. **Payment memakai tabel state, bukan event sourcing.**
   - Alasan: lihat amandemen ADR-019 di `docs/analisa/05-ADR-RBT.md` dan `specs/payment-service/design.md` §4.
   - Pola transisi: `UPDATE … WHERE status = ANY(<asal sah>)` + outbox dalam satu transaksi. Pola ini cukup menjamin idempotensi dan status yang tidak mundur.
2. **Perbaikan Booking yang dibutuhkan Payment dikerjakan BE-1** dan diinfokan ke nuzirwan, dengan batas file yang tertulis di `specs/payment-service/tasks.md` Task 15:
   - 15.1 = F3 (`verification_until`)
   - 15.2 = F16 (kontrak resolve)
   - 15.3 = F5 (payload `booking.paid`; `ticketLinkToken` opsional)
   - 15.4 = F6 (settle atas booking non-payable)
   - 15.5 = F2 khusus endpoint `payable`/`payment-lock`

   Tetap milik nuzirwan: F2 untuk endpoint internal lain dan admin, F4, F7 (test Booking umum), F9, F10, F11.
3. **Spec diselaraskan:**
   - `specs/payment-service/{requirements,design,tasks}.md`
   - `specs/platform/tasks.md` (prioritas + backlog)
   - `specs/notification-service/requirements.md` (`ticketLinkToken` opsional)
   - ADR-019 (amandemen)
4. **Nice-to-have masuk backlog** di `specs/platform/tasks.md` § "Backlog nice-to-have": endpoint admin, edit pesanan, `deadline-extension`, refund case, push, OTP lanjutan, retensi, Terraform, routing key `.v1`. Dikerjakan hanya setelah seluruh scope PDF terdeliver.

Urutan kerja BE-1 (diperbarui: pekerjaan yang menyentuh kode nuzirwan di akhir):
1. B1: `make tidy` sudah dijalankan 2026-10-23 (`amqp091-go` 1.16.0 di 5 service; build + test semua modul lulus dengan `GOWORK=off`). Penghapusan `./tests` dari `go.work` ditunda karena milik nuzirwan. Lalu platform 8.2.
2. Notification 0a.R1/0a.2.
3. Platform 2.1, 2.3, 3.
4. Payment 0 → 9 → 3 → 4 → 5 → 7.1 → 6 → 1 → 2 → 11, dengan Booking palsu di test.
5. Platform 12.2 (producer Payment, consumer Notification).
6. Terakhir, setelah koordinasi dengan nuzirwan: Payment Task 15 (sisi Booking) → Task 13 (E2E Midtrans Sandbox).

Kontrak yang berubah di Task 15.3 (`booking.paid.v1`: `ticketLinkToken` tidak lagi wajib) bersifat melonggarkan, bukan breaking: producer lama yang mengirim field tersebut tetap valid.

## 12. Progres eksekusi (26 Oktober 2026)

Dikerjakan menurut urutan §11 untuk semua pekerjaan yang tidak menyentuh kode nuzirwan. Setiap batch lolos gate: golangci-lint 0 isu, `go vet`, gosec (tanpa HIGH baru), govulncheck, unit + integration test `-race`, `go mod tidy` dan `_mock` tanpa diff, coverage ≥ 80% (`pkg` 90,6%, Notification 82,7%, Payment 90,1%).

| Batch | Hasil | Bukti |
|---|---|---|
| B1 | `make tidy` (amqp091 1.16.0); platform 8.2 sudah ada di CI | build + test semua modul lulus dengan `GOWORK=off` |
| B9 Notification | F19 selesai: klaim delivery kondisional + lease; test terbukti gagal di kode lama. Juga `ticketLinkToken` opsional, breaker + bulkhead email, alamat email tidak lagi ada di `last_error`/log SMTP | `delivery_repository_integration_test.go`, `emailguard/`, `smtp/redact_test.go` |
| Platform | 2.1 `VisitorAuth`, 2.3 `ServiceAuth` (fail-closed, token > 5 menit ditolak) + `ServiceTokenSource`, 3 `pkg/outbox`, 12.2 `pkg/contract` (validator JSON Schema), `telemetry.NewCounterVec` | `pkg/httpx/auth_test.go`, `pkg/outbox/outbox_integration_test.go` (termasuk RabbitMQ asli), `pkg/contract/schema_test.go` |
| B5–B7 Payment | Tasks 0, 9, 3, 4, 5, 6, 1, 2, 7.1, 11 selesai; Task 13 sebagian (perjalanan otomatis + metrik). Tabel state, bukan event sourcing | test perjalanan lengkap `internal/delivery/payment_journey_integration_test.go` |

### Temuan baru dari pengerjaan

- **F22 (diperbaiki).** Relay outbox Booking membuat `messageId` baru pada setiap percobaan publish, padahal Notification memakai `messageId` sebagai kunci dedup. `pkg/outbox` memakai id baris outbox yang stabil. Relay Booking belum dipindah (milik nuzirwan).
- **F23 (diperbaiki).** Fingerprint callback webhook tidak memuat hasil verifikasi signature dan nominal, sehingga webhook palsu bisa menempati baris audit callback asli. Ditemukan oleh test perjalanan; test unitnya ada di `handle_webhook_test.go`.
- **F24 (diperbaiki).** Status transaksi kedaluwarsa Midtrans dijawab dengan `status_code` 407 di body; adapter menganggapnya penolakan. Ditemukan oleh test adapter. Lihat `docs/evidence/2026-10-26-midtrans-verifikasi.md`.
- **F25 (diperbaiki 2026-10-26).** Satuan `amountMinor`: contoh SDD (`priceMinor: 1500000` untuk VIP Rp 1.500.000) dan simulator Midtrans ("IDR 150000.00" untuk 150000) menunjukkan 1 minor unit = 1 rupiah. `formatIDR` Notification yang membagi 100 keliru dan sudah diperbaiki beserta testnya.
- **F26 (belum).** Token service Queue → Booking berumur panjang (dibuat sekali saat start). Begitu Booking memasang `ServiceAuth` (Task 15.5), token itu ditolak karena umurnya > 5 menit; Queue perlu memakai `httpx.ServiceTokenSource`.
- Gate menegakkan aturan arsitektur: paket `domain` hanya boleh stdlib, jadi test kontrak ditaruh di lapisan `application`.
- Lint dan gosec lama yang bukan dari perubahan ini: 2 isu `eventstore_integration_test.go`, 2 gosec G115 di `pkg`, 2 gosec G101 (false positive) di Notification.

### Yang tersisa

1. **Task 15 (sisi Booking, milik nuzirwan, perlu koordinasi):** F3 `payment-lock`, F5 payload `booking.paid` (+ schema `ticketLinkToken` opsional), F6, F16 klien resolve, F2 `ServiceAuth` untuk `payable`/`payment-lock`. Tanpa F3 dan F16, E2E tidak bisa jalan; tanpa F5 email e-tiket tidak terkirim.
2. **Task 13:** E2E Midtrans Sandbox nyata (server key Anda + tunnel untuk webhook) dan koreksi asumsi zona waktu `settlement_time`.
3. Hapus `./tests` dari `go.work` (milik Irwan).

## 13. E2E Midtrans Sandbox nyata (26 Oktober 2026)

Dijalankan atas izin pemilik: API dan worker Payment asli dengan server key Sandbox di `.env`, Booking digantikan program uji sementara, webhook lewat tunnel `cloudflared`. Hasil lengkap di `docs/evidence/2026-10-26-midtrans-verifikasi.md`. Ringkasan:

- Semua metode aktif berhasil membuat transaksi Sandbox nyata; webhook nyata terverifikasi, idempoten, dan menghasilkan event `payment.succeeded` di RabbitMQ.
- **Zona `settlement_time` terbukti WIB**, `amountMinor` = rupiah, `X-Override-Notification` berfungsi, status/cancel/resolve nyata benar.
- Perubahan kode karena pengujian ini: opsi `MIDTRANS_NOTIFICATION_URL` (kosong secara default; hanya untuk tes lokal lewat tunnel https), `formatIDR` Notification.
- Temuan lingkungan: kolom `payment.payment_attempts.booking_version` sudah ada di database lokal sebelum migrasi 0003 dijalankan, tanpa tercatat di `schema_migrations`. Pemilik mengonfirmasi bahwa ia sendiri yang menambahkannya secara manual (log PostgreSQL mencatat `ALTER TABLE payment.payment_attempts DROP COLUMN booking_version` pukul 08:35 WIB dari sesi itu). Tabel kosong; kolom dihapus lalu migrasi diterapkan lewat runner agar status migrasi konsisten dengan kode.
- Verifikasi lanjutan sore hari: cancel pada order settle (412), order tidak ada (404), kedaluwarsa nyata (status 407, webhook `expire`), dan batas minimum `custom_expiry` (20 detik ditolak, 30 detik diterima). Test opt-in `-tags sandbox` mengulang ini kapan saja.
- Yang masih menunggu: Task 15 (Booking nyata). `VA_PERMATA` tidak diperlukan.

## 14. Task 15 (sisi Booking) dan E2E dengan Booking nyata (26 Oktober 2026)

Dikerjakan atas izin pemilik; Irwan (nuzirwan) diinfokan oleh pemilik. Perubahan Booking: F3 `payment-lock` mengisi `verification_until`; F16 klien resolve (`CHECK`/`CHECK_AND_CANCEL`, `outcome`); F5 payload `booking.paid` sesuai kontrak (`ticketLinkToken` opsional); F6 pembayaran terlambat → status `REVIEW` + event `booking.review`; F2 `ServiceAuth` untuk `payable`/`payment-lock` (token Payment saja); 4.R3 panic rute `:release` diperbaiki (+ test mux). Test integrasi PostgreSQL pertama di Booking (terbukti gagal di kode lama).

**E2E nyata** (Booking, Payment, Notification asli; Midtrans Sandbox; webhook via tunnel): hold → attempt GoPay → bayar di simulator → webhook → Booking `PAID`, 1 tiket, stok terjual 1, email e-tiket di Mailpit. Booking kedua: attempt lalu dibiarkan kedaluwarsa → `EXPIRED`, stok kembali, email expired terkirim. Cancel oleh pembeli setelah pembayaran dimulai ditolak `409 BOOKING_LOCKED` (aturan lama, disengaja).

Temuan baru:
- **F27 (diperbaiki).** `ticketCodeFor` hanya 32 bit dan deterministik; kini 48 bit. Jalur `settle` dari event memakai `NewPublicRef` acak berformat `RB-…` (format berbeda, aman untuk retry).
- **F28 (belum).** Booking memercayai `X-Visitor-ID`/cookie `rb_visitor` mentah tanpa verifikasi JWT; identitas pengunjung bisa dipalsukan.
- **Cakupan Booking 51,7%** (< 80% ambang CI) — masalah lama, di luar Task 15.

Sisa: F26 (token Queue → Booking berumur panjang), F28, kunci token Booking di compose (platform 7.x), fase VERIFYING/grace, `settlementTime` terlambat dari event `payment.succeeded`, hapus `./tests` dari `go.work`.
