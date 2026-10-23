# Requirements Document — Platform (lintas service)

| Field | Value |
|-------|-------|
| **Spec** | platform |
| **Cakupan** | `pkg/` (shared utilities tanpa logika bisnis), kontrak `docs/contracts/`, CI (`.github/workflows/ci.yml`), lingkungan lokal (`docker-compose.yaml`, `deployments/compose`, `deployments/edge`), observability (`deployments/observability`), cloud (`deployments/terraform`), test lintas service (`tests/`), tooling (`tools/`) |
| **Komponen HLD** | CMP-PLATFORM, CMP-EDGE |
| **Owner** | BE-1 + BE-2 |
| **Sumber** | SRS-RBT-001 §3.1.4, §3.2.11, §3.2.19, §3.2.22, §3.3, §3.5, §3.7 · SDD-RBT-001 §4.1.3, §4.5, §4.6.2, §4.9 · HLD-AD-RBT-001 (Deployment & Security view, tabel Token) · ADR-010, ADR-011, ADR-012, ADR-016, ADR-019, ADR-022 · evidence `docs/evidence/2026-10-19-boilerplate-v1.5.md` |

## Introduction

Platform menyediakan fondasi yang dipakai kelima service: utilitas `pkg/` (error envelope, HTTP
server/middleware, token EdDSA, kripto PII, client PostgreSQL/Valkey/RabbitMQ, migrasi tertanam,
event store, circuit breaker), kontrak API/event yang menjadi sumber kebenaran antar tim, gerbang CI,
dan lingkungan lokal/cloud. Spec ini **tidak** memuat aturan bisnis (stok, antrean, pembayaran) —
aturan itu milik spec service.

**Status kode (audit 2026-10-21) [kode]:** `pkg/{apperror,clock,crypto,dotenv,eventstore,httpx,logger,
migrate,postgres,rabbitmq,resilience,token,valkey}` ada dan ber-test (evidence v1.5: rata-rata
coverage 82,9%). Yang **direncanakan tetapi belum ada** (SDD-RBT §4.1.3): `pkg/nik`, `pkg/outbox`,
`pkg/telemetry`, middleware auth/rate-limit/CORS/client-IP di `pkg/httpx`, redaksi PII di
`pkg/logger`, `deployments/edge/nginx.conf`, `deployments/observability/*`, `deployments/terraform/*`,
isi `tests/{features,k6,apidog,contract}`, `tools/{migrate,seed}`. CI sudah punya stage
`code-quality → secret-detection → verify-code-quality → case-coverage → code-rank → build → package →
scan-package → publish → deployment-*`, tetapi belum memvalidasi OpenAPI/JSON Schema/Mermaid.

## Glossary

- **Visitor token** — JWT EdDSA terbitan Queue berisi `visitorId`, `buyerNikHash`, email; diverifikasi Queue, Booking, Payment (HLD tabel Token).
- **Admission token** — JWT sekali pakai (`jti`, TTL 60 s) terbitan Queue, diverifikasi Booking.
- **Service token** — JWT EdDSA berumur 5 menit (`iss`, `aud`, `exp`) yang ditandatangani service pemanggil untuk `/internal/v1/*` (ADR-011).
- **User access token** — JWT EdDSA terbitan User Service (`sub`, `email`, `nik_hash`, `has_nik`).
- **Envelope** — pembungkus event (`envelope.schema.json`): `messageId`, `eventType`, `schemaVersion`, `occurredAt`, `producer`, `aggregateId`, `correlationId?`, `payload`.
- **Outbox relay** — worker yang menerbitkan baris `outbox_events` ke RabbitMQ dengan publisher confirm.
- **Breaker / Bulkhead** — `pkg/resilience.Breaker` (CLOSED/OPEN/HALF_OPEN) dan pembatas konkurensi.
- **PII** — NIK, email, token, signature, secret (GR-15).

---

## Requirement 1 — Validator struktur NIK bersama

**User Story:** Sebagai developer Queue, Booking, dan User, saya ingin satu validator struktur NIK yang sama, agar NIK yang diterima di join, cek pemegang, hold, dan profil dinilai dengan aturan identik.

Trace: SRS-F-11-01, SRS-F-01-01 · TC-BKG-08, TC-GST-02 · SDD-RBT §4.9 "Validasi Struktur NIK (Go)" · Alloc CMP-PLATFORM

#### Acceptance Criteria

1. THE platform SHALL menyediakan paket `pkg/nik` dengan `Validate(s string, now time.Time) error` yang memeriksa: tepat 16 digit numerik; kode provinsi ada di daftar Kemendagri; kode kab/kota (`[2:4]`) dan kecamatan (`[4:6]`) bukan `00`; tanggal lahir `DDMMYY` valid secara kalender (DD+40 untuk perempuan) dan tidak di masa depan; nomor urut (`[12:16]`) bukan `0000`.
2. WHEN validasi gagal, THE validator SHALL mengembalikan error sentinel yang dapat dibedakan (`ErrLength`, `ErrDigits`, `ErrProvince`, `ErrBirth`, `ErrSerial`); service memetakan semuanya ke `422 NIK_INVALID`.
3. THE daftar kode provinsi SHALL dikelola sebagai data (map), bukan logika bercabang.
4. THE validator SHALL tidak pernah menulis NIK ke log atau pesan error.
5. THE validator SHALL lulus kasus uji SDD §4.9: laki-laki, perempuan (+40), 30 Februari, nomor urut 0000, provinsi tidak dikenal, kab/kota 00, panjang salah, karakter non-digit, tahun dua digit abad sebelumnya.

## Requirement 2 — Autentikasi & proteksi HTTP bersama

**User Story:** Sebagai developer service, saya ingin middleware autentikasi dan proteksi yang sama untuk visitor, user, admission, service, dan admin token, agar web dan mobile diverifikasi identik dan endpoint internal tertutup.

Trace: SRS-F-22-02, SRS-IF-33, SRS-IF-34, SRS-Q-SEC-01, SRS-Q-SEC-05, SRS-Q-SEC-07, SRS-F-04-09 · TC-API-01, TC-SEC-01, TC-SEC-02 · ADR-011 · HLD tabel Token

#### Acceptance Criteria

1. THE `pkg/httpx` SHALL menyediakan middleware yang mengekstrak visitor token dari cookie `rb_visitor` (web) **atau** header `X-Visitor-Token` (mobile), memverifikasinya dengan `pkg/token.Verifier`, dan menaruh `visitorId`, `buyerNikHash` ke context. *(Header mobile: lihat OQ-8.)*
2. THE middleware SHALL memverifikasi user access token `Authorization: Bearer` (kunci publik User Service, `kid`) dan menaruh `accountId`, `has_nik`, `nik_hash` ke context; token tidak valid → `401 UNAUTHORIZED` + `WWW-Authenticate: Bearer`.
3. THE middleware internal SHALL menolak request `/internal/v1/*` tanpa service token valid (`iss` = service pemanggil yang dikenal, `aud` = service penerima, `exp` ≤ 5 menit) dengan `401 UNAUTHORIZED`.
4. THE platform SHALL menyediakan penerbit service token (`pkg/token.Signer`) yang di-cache sampai mendekati `exp` sehingga pemanggil tidak menandatangani per request.
5. THE `pkg/httpx` SHALL menyediakan rate limiter berbasis Valkey (konsisten lintas instance) yang menjawab `429 RATE_LIMITED` dengan header `Retry-After`.
6. THE `pkg/httpx` SHALL menyediakan penentu IP klien yang hanya mempercayai `X-Forwarded-For` bila konfigurasi `trustProxy` aktif (di belakang ALB).
7. THE decoder JSON SHALL tetap strict (`DisallowUnknownFields`, `MaxBytesReader`) seperti `pkg/httpx/request.go` saat ini. *(Sudah ada [kode].)*
8. THE error response SHALL hanya berisi `code` dan `message`: `{"meta":{"code":409,"message":"BOOKING_LOCKED: Pesanan sudah terkunci"},"data":null}`. `message` boleh diawali kode error (`KODE: pesan`) agar FE bisa membaca jenis error, atau berupa pesan umum untuk error internal (`"message":"internal error"`). Tidak ada `requestId`, `errorCode`, atau `details` di body; `requestId` dikirim lewat header `X-Request-ID`. Detail lengkap (kode error, detail validasi, error asli, dan payload request yang sudah diredaksi) ditulis ke log dengan `requestId` yang sama. Tidak pernah memuat SQL, stack trace, secret, atau payload provider. *(Keputusan tim 2026-10-21. Kompatibel dengan kontrak karena hanya `meta.code` yang wajib; FE web/mobile perlu diberi tahu karena SRS §3.1.4 dan kontrak menyebut field `errorCode`.)*
9. THE response sukses SHALL hanya memuat field yang didefinisikan kontrak untuk layar itu (tanpa over-fetching).
10. THE middleware CORS SHALL hanya aktif di `APP_ENV=local`; di cloud CORS ditangani edge. *(Sudah ada `httpx.CORS()` dari DEV-2 [kode].)*

## Requirement 3 — Transactional outbox relay bersama

**User Story:** Sebagai developer Booking dan Payment, saya ingin relay outbox yang sama, agar event perubahan stok/uang terkirim at-least-once tanpa menulis ulang logika publish.

Trace: SRS-Q-REL-04, SRS-IF-30 · TC-CAP-03 · ADR-010 · SDD-RBT §4.5.2, §4.6.2 "Retry Ownership"

#### Acceptance Criteria

1. THE platform SHALL menyediakan `pkg/outbox` dengan fungsi `Write(ctx, tx, routingKey, aggregateID, schemaVersion, payload)` yang menyisipkan baris `outbox_events` **di dalam transaksi pemanggil**.
2. THE relay SHALL memilih baris `published_at IS NULL AND available_at <= now()` urut `available_at` dengan `FOR UPDATE SKIP LOCKED LIMIT n`, membungkusnya dalam envelope, dan menerbitkan lewat `pkg/rabbitmq.Publisher` (publisher confirm).
3. WHEN publish dikonfirmasi broker, THE relay SHALL mengisi `published_at`.
4. IF publish gagal atau tidak dikonfirmasi dalam `RABBITMQ_CONFIRM_TIMEOUT`, THEN THE relay SHALL menaikkan `publish_attempts`, menyimpan `last_error` (tanpa PII), dan menunda `available_at` dengan backoff + jitter; baris tetap pending sampai terkirim (tidak ada event hilang).
5. THE relay SHALL aman dijalankan di beberapa instance worker sekaligus (tidak ada publish ganda dari dua instance untuk baris yang sama dalam satu siklus).
6. THE relay SHALL mengekspos lag (umur baris pending tertua) sebagai metrik untuk alert `*OutboxLag` (lihat R4).

## Requirement 4 — Telemetry (metrik, log, health)

**User Story:** Sebagai operator, saya ingin metrik real-time dan log terstruktur tanpa PII, agar antrean, konversi, expiry, dan kesehatan dependency terpantau.

Trace: SRS-Q-MNT-02, SRS-Q-MNT-03, SRS-Q-SEC-03, SRS-F-90-06 · TC-OBS-01, TC-OBS-02, TC-SEC-03 · SDD-RBT §4.1.3 (`pkg/telemetry`), D-23

#### Acceptance Criteria

1. THE platform SHALL menyediakan `pkg/telemetry` berbasis Prometheus client (`client_golang`; keputusan OQ-5, ADR-023) untuk mendaftarkan counter/gauge/histogram dan mengeksposnya di `GET /metrics`, terpisah dari `/health`. Endpoint ini tidak dirutekan edge (R7.2, R11.2).
2. THE platform SHALL menyediakan middleware metrik HTTP: `rbt_http_requests_total{service,method,route,status}` dan histogram `rbt_http_request_duration_seconds{service,method,route}`, ditambah metrik runtime Go dan proses (goroutine, memori) bawaan Prometheus client. `route` adalah template (`/bookings/{bookingId}`), bukan path asli.
3. THE metrik SHALL tidak memakai identifier berkardinalitas tinggi (NIK, booking ID, entry ID, email) sebagai label; label yang diizinkan: `service`, `route`, `status`, `event_id`, `reason`, `name`.
4. THE `pkg/logger` SHALL meredaksi atribut ber-kunci sensitif (`nik`, `buyerNik`, `email`, `emailConfirmation`, `token`, `authorization`, `signature`, `signature_key`, `password`, `code`, `pushToken`, `secret`) sebelum ditulis, termasuk di dalam payload request yang dilog saat error (R2.8).
5. THE `GET /health` SHALL tetap satu endpoint per service (200 UP / 503 DOWN, `Cache-Control: no-store`) untuk ALB dan HEALTHCHECK container. *(Sudah ada di kelima service [kode].)*
6. THE breaker SHALL melaporkan perubahan state sebagai metrik `breaker_state{name}` lewat `OnStateChange`.
7. THE platform SHALL menyediakan middleware **access log**: satu baris INFO per request berisi `method`, `route` (template), `status`, `duration_ms`, `bytes`, `requestId`, `trace_id`. `/health` dan `/metrics` tidak dilog. Status ≥ 500 dilog `ERROR`, 4xx `WARN`. Saat ini tidak ada access log; hanya error 5xx dan panic yang dilog [kode: `pkg/httpx`].
8. THE setiap baris log SHALL memuat field baku `time`, `level`, `msg`, `service`, `verb`, `env`, dan — dalam konteks request — `requestId` serta `trace_id`. `trace_id` diambil dari header `X-Amzn-Trace-Id` (ditambahkan ALB; dipakai AWS X-Ray), atau `traceparent` (W3C), atau dibuat baru; `requestId` dikembalikan di header `X-Request-ID`. Antar-service meneruskan header yang sama sehingga satu permintaan dapat ditelusuri lintas service.
9. THE `LOG_FORMAT` SHALL mendukung `json` (default; langsung terbaca CloudWatch Logs / Logs Insights dan Loki), `gcp` (JSON dengan `severity`, `message`, `timestamp`, `logging.googleapis.com/trace` agar terbaca Google Cloud Logging), dan `text` (dev). Log selalu ditulis ke stdout; pengumpulan ke backend dilakukan agen di luar aplikasi (Alloy lokal, CloudWatch agent / FireLens di AWS, Ops Agent di GCP) — aplikasi tidak bergantung pada vendor tertentu.
10. THE stack observability lokal (Prometheus, Loki, Alloy, Grafana) SHALL tersedia lewat `make obs` (R7.6) dengan datasource dan dashboard ter-provision, tanpa konfigurasi manual.

## Requirement 5 — Kontrak API & event sebagai sumber kebenaran

**User Story:** Sebagai tim web, mobile, dan backend, saya ingin kontrak OpenAPI dan JSON Schema yang tervalidasi otomatis, agar perubahan kontrak tidak memecah klien.

Trace: SRS-IF-30, SRS-IF-31, SRS-F-22-01, SRS-F-22-03, SRS-C-07, SRS-Q-COM-01 · TC-API-01, TC-API-02 · ADR-017, D-19

#### Acceptance Criteria

1. THE CI SHALL memvalidasi ketujuh file `docs/contracts/openapi/*.yaml` (OpenAPI 3.1) dan seluruh `docs/contracts/events/*.schema.json` (JSON Schema 2020-12) pada setiap PR yang menyentuh `docs/contracts/`.
2. THE CI SHALL menolak perubahan kontrak `v1` yang tidak additive (penghapusan path/field, perubahan tipe/enum yang mempersempit) kecuali di bawah `v2`.
3. THE kontrak event SHALL memuat schema untuk setiap routing key yang diterbitkan, termasuk `otp.requested.v1`, dan `envelope.schema.json` `producer` SHALL mencakup setiap service penerbit (termasuk `user-service`). *(Belum: OQ-4.)*
4. THE `pkg/rabbitmq.Publisher` atau pemanggilnya SHALL memvalidasi payload terhadap schema event sebelum publish (SDD-RBT §4.6.2 "divalidasi di CI dan saat publish").
5. THE contract test (`tests/contract/`) SHALL memverifikasi respons nyata setiap service terhadap kontraknya (TC-API-01/02), termasuk error envelope.

## Requirement 6 — Manajemen kunci & secret

**User Story:** Sebagai operator, saya ingin kunci token dan PII dikelola konsisten, agar token terverifikasi lintas service dan rotasi kunci tidak memutus sesi.

Trace: SRS-Q-SEC-05, SRS-Q-SEC-06, SRS-Q-SEC-10, SRS-C-06 · ADR-011 · HLD tabel Token · `security-privacy.md`

#### Acceptance Criteria

1. THE `make keys` SHALL membuat pasangan kunci Ed25519 lokal per penerbit token (User; Queue untuk visitor/queue/admission; Payment untuk payment page; Booking untuk ticket link; setiap service pemanggil internal untuk service token) di luar git.
2. THE verifier SHALL menerima beberapa kunci publik per `kid` sehingga rotasi kunci tidak memutus token yang masih berlaku. *(`pkg/token.NewVerifier(keys map[string]ed25519.PublicKey, …)` sudah mendukung [kode].)*
3. THE kunci HMAC NIK (`PII_HASH_KEY`) SHALL identik di Queue, Booking, dan User agar `nik_hash` cocok lintas service.
4. THE secret (kunci privat, `MIDTRANS_SERVER_KEY`, password DB/broker) SHALL dibaca dari env/SSM; `.env` dan state infrastruktur tidak di-commit; CI stage `secret-detection` tetap aktif.

## Requirement 7 — Lingkungan lokal clean-room

**User Story:** Sebagai assessor, saya ingin menjalankan seluruh alur secara lokal dengan Docker Compose tanpa AWS, agar sistem dapat diverifikasi end-to-end.

Trace: SRS-Q-FLX-03, SRS-C-05, SRS-IF-24, SRS-F-90-04 · SDD-RBT §4.1.3

#### Acceptance Criteria

1. WHEN `make up` dijalankan, THE compose SHALL menjalankan PostgreSQL, Valkey, RabbitMQ, Mailpit, migrasi kelima service, serta `api` dan `worker` setiap service. *(Sudah ada [kode: `docker-compose.yaml`].)*
2. THE compose SHALL menyertakan edge lokal (`deployments/edge/nginx.conf`) yang meniru CloudFront/ALB: routing per service berdasarkan path, cache 1 s untuk Status Document dan 2 s untuk katalog, tanpa cache untuk SSE, dan **tidak** merutekan `/internal/v1/*`.
3. THE `tools/seed` SHALL membuat data event sintetis (event, kategori, kapasitas, konfigurasi) lewat endpoint admin internal Booking atau SQL seed; data pribadi hanya sintetis.
4. THE simulator pembayaran SHALL hanya aktif saat `APP_ENV ∈ {ci, local-offline}` dan hasilnya berlabel `SIMULATED`. *(Implementasi di payment spec; platform menyediakan variabel lingkungan dan profil compose.)*
5. THE `make smoke` SHALL menjalankan alur bahagia singkat (join → claim → hold → bayar simulasi → tiket) terhadap compose.
6. THE observability lokal SHALL dijalankan lewat `make obs` (compose profile `observability`, tidak ikut `make up`): Prometheus :9090, Loki :3100, Alloy :12345, Grafana :3000 (tanpa login, khusus lokal). Alloy (pengganti Promtail yang end-of-life sejak Maret 2026) membaca log container compose dan file `logs/*.log` untuk service yang dijalankan dengan `go run`; retensi log 30 hari (R13). *(Sudah ada — task 7.5.)*

## Requirement 8 — Gerbang CI & kualitas

**User Story:** Sebagai reviewer, saya ingin gerbang CI otomatis, agar setiap PR memenuhi standar sebelum digabung.

Trace: SRS-Q-MNT-01, SRS-Q-MNT-04, SRS-Q-MNT-05, SRS-Q-SEC-08, SRS-Q-SEC-12, SRS-C-08 · ADR-016, ADR-022 · `testing-review.md`

#### Acceptance Criteria

1. THE CI SHALL menjalankan build, lint (`golangci-lint` + `depguard` lapisan hexagonal dan larangan import lintas `services/*`), unit + integration test `-race`, ambang coverage logika ≥ 80%, dan build isolation `GOWORK=off`. *(Sudah ada [kode: `ci.yml`, `.golangci.yml`, `tools/check-coverage.sh`].)*
2. THE CI SHALL merender setiap diagram Mermaid di `docs/` dan `specs/` dan gagal bila ada yang tidak valid (SRS-Q-MNT-05).
3. THE CI SHALL memastikan `_mock/` sesuai port (`make mock` tidak menghasilkan diff) dan `go.mod`/`go.sum` rapi.
4. THE CI SHALL menjalankan pemindaian dependency/image dan menolak temuan critical/high tanpa risk acceptance (SRS-Q-SEC-08). *(Stage `scan-package` ada [kode]; kebijakan penolakan perlu diverifikasi.)*

## Requirement 9 — Tooling operasional

**User Story:** Sebagai Ops, saya ingin rekonsiliasi terjadwal dan redrive DLQ yang teraudit, agar inkonsistensi terdeteksi dan pesan gagal dapat diproses ulang dengan aman.

Trace: SRS-F-90-01, SRS-F-90-02, SRS-F-90-03 · TC-OPS-01, TC-MSG-02

#### Acceptance Criteria

1. THE platform SHALL menyediakan kerangka job rekonsiliasi yang menjalankan pemeriksaan milik setiap service (didefinisikan di spec service) dan mengirim hasil ke metrik/alert; pemeriksaan lintas service (PAID tanpa attempt SUCCEEDED, SUCCEEDED tanpa PAID/REVIEW, slot aktif Queue vs admission aktif Booking) dijalankan dengan membaca lewat API/akses read-only, bukan join lintas schema.
2. THE perbaikan hasil rekonsiliasi SHALL hanya lewat tool dry-run → apply yang mencatat aktor, waktu, dan perubahan.
3. THE redrive DLQ SHALL hanya lewat tool internal yang mencatat aktor, pesan, dan jumlah, serta memindahkan pesan kembali ke queue utama (bukan publish ulang manual).

## Requirement 10 — Test lintas service & evidence

**User Story:** Sebagai QA, saya ingin test end-to-end, beban, dan kegagalan yang dapat diulang, agar invarian terbukti di level sistem.

Trace: SRS-Q-PERF-01..06, SRS-Q-REL-02..06, SRS-Q-FUN-01..06 · TC-PERF-01..05, TC-CAP-01..03, TC-ASG-01, TC-SES-*, TC-NAV-* · `testing-review.md`

#### Acceptance Criteria

1. THE `tests/features/` SHALL memuat skenario godog untuk alur lintas service (join → admission → hold → edit → bayar → tiket; expiry; cancel; sold out).
2. THE `tests/k6/` SHALL memuat skrip TC-PERF-01..05 (origin load Status Document konstan 5.000 vs 50.000 waiter, throughput join, p95 home/status < 1 s, 500 slot aktif tanpa pelanggaran invarian, delay informasi sold out).
3. THE failure-injection test SHALL membuktikan TC-CAP-01 (DB primary tak terjangkau → 503 tanpa hold setengah jadi), TC-CAP-02 (Valkey down → join/claim fail closed), TC-CAP-03 (broker down → booking tetap commit, outbox lag kembali 0).
4. THE hasil load test dan uji kegagalan SHALL disimpan sebagai evidence bertanggal di `docs/evidence/`.

## Requirement 11 — Deployment cloud & edge

**User Story:** Sebagai operator, saya ingin infrastruktur demo di AWS yang dapat dibuat ulang dengan Terraform, agar demo berjalan dalam batas kredit dan origin terlindungi.

Trace: SRS-C-04, SRS-Q-SEC-04, SRS-Q-SEC-06, SRS-IF-32, SRS-IF-34, SRS-Q-FLX-01, SRS-Q-REL-06, SRS-Q-REL-07 · TC-SEC-02, TC-ASG-01 · ADR-012

#### Acceptance Criteria

1. THE `deployments/terraform` SHALL menyediakan modul `network`, `edge` (CloudFront + WAF), `alb`, `asg`, `stateful`, `iam-oidc`, `budget` dan environment `staging` di `ap-southeast-3`.
2. THE origin (ALB) SHALL menolak request yang tidak berasal dari CloudFront dengan `403` (TC-SEC-02); `/internal/v1/*` tidak dirutekan ALB publik.
3. THE database, Valkey, broker, dan dashboard SHALL tidak dapat diakses publik; akses admin hanya lewat SSM.
4. THE ASG Queue Service SHALL mendukung scale-out terjadwal sebelum penjualan dibuka; scale-out/in tidak boleh kehilangan sesi (tanpa sticky session).
5. THE modul `budget` SHALL memasang alarm di bawah kredit US$200.
6. THE backup dan restore PostgreSQL SHALL didemonstrasikan sebelum rilis.

## Requirement 12 — Penyelarasan event lintas service

**User Story:** Sebagai tim, saya ingin nama routing key dan queue konsumen sama di semua service, agar tidak ada event yang hilang karena salah nama.

Trace: SDD-RBT §4.6.2, `pkg/rabbitmq.Subscription` · K8 · TC-API-02

#### Acceptance Criteria

1. THE routing key SHALL berversi (`<event>.v1`) di semua producer dan consumer, diganti dalam satu perubahan terkoordinasi. *(Saat ini semua service konsisten memakai nama tanpa `.v1` [kode], jadi alur tetap berjalan.)*
2. THE nama routing key dan queue SHALL didefinisikan sekali sebagai konstanta bersama (mis. `pkg/rabbitmq/topics.go`), bukan string tersebar per service.
3. THE payload setiap event SHALL lolos test kontrak terhadap `docs/contracts/events/*.schema.json` di CI; ketidakcocokan payload antar service (seperti `booking.journey.ended`) tertangkap sebelum merge.

## Requirement 13 — Retensi data pribadi (UU PDP)

**User Story:** Sebagai penyelenggara, saya ingin data pribadi hanya disimpan selama dibutuhkan dan selalu terenkripsi saat disimpan, agar sesuai prinsip UU PDP (UU 27/2022): minimal, terbatas waktu, dan aman.

Trace: SRS §3.4 "Data Pribadi" (GR-15), SRS-D-02..07, SRS-F-11-10, SRS-Q-SEC-03/10 · SDD-RBT §4.10 `PII_RETENTION_DAYS_AFTER_EVENT` · OQ-11 (diputuskan 2026-10-21)

#### Acceptance Criteria

1. THE kebijakan retensi SHALL: data pribadi booking (email pembeli, nama & NIK terenkripsi pemegang) dibersihkan `PII_RETENTION_DAYS_AFTER_EVENT` hari (default 30) setelah event selesai; token push dihapus saat penjualan berakhir; baris outbox yang sudah terkirim dihapus setelah 7 hari; state antrean di Valkey kedaluwarsa setelah penjualan berakhir; log disimpan maksimal 30 hari.
2. WHEN data dibersihkan, THE sistem SHALL menghapus ciphertext/plaintext dan mengganti nama dengan versi tersamar, tetapi mempertahankan hash HMAC dan data transaksi (nominal, status, waktu) untuk audit.
3. THE data pribadi yang masih disimpan SHALL terenkripsi AES-256-GCM (+ hash HMAC untuk pencarian), kecuali nama pemegang yang ditampilkan di tiket dan dibersihkan sesuai AC 1.
4. THE setiap service SHALL menjalankan pembersihan untuk schema miliknya sendiri sebagai job worker idempoten (aman dijalankan ulang), dengan metrik jumlah baris yang dibersihkan dan tanpa menulis data pribadi ke log.
5. THE nilai retensi SHALL dikonfigurasi lewat env dan divalidasi saat start. *(Angka di atas disetujui tim; konfirmasi akhir ke klien tetap dicatat di OQ-11.)*

## Invarian platform

| # | Invarian | Penegak |
|---|----------|---------|
| P-1 | `pkg/` tanpa logika bisnis; tidak ada import antar `services/*` | `depguard`, review (SRS-Q-MNT-04) |
| P-2 | PII tidak pernah di log, metrik, fixture, payload event | `pkg/logger` redaksi (R4.4), `pkg/eventstore` penolak field PII [kode], R4.3 |
| P-3 | Event tidak hilang saat broker down | `pkg/outbox` (R3.4) |
| P-4 | Internal API tidak dapat diakses tanpa service token / dari jaringan publik | R2.3, R11.2 |
| P-5 | Kontrak `v1` hanya berubah additive | R5.2 |
