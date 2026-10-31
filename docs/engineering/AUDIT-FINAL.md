# Audit Final — Rusuh Booking BE

Tanggal: 2026-10-31. Branch: `feature/snap-payment-ci-hardening` (satu PR, 33 commit, 149 file, +5.721/−985).
Lingkup: repo BE. Repo mobile hanya referensi kontrak (prioritas kontrak = repo mobile; tidak diubah).
Cara membaca: setiap klaim "terverifikasi" menyebut buktinya. Yang hanya dibaca tetapi tidak dijalankan ditandai **tidak terverifikasi**.

## 1. Ringkasan

Audit menemukan **21 cacat nyata** di luar 3 temuan CI awal; semuanya sudah diperbaiki dan diuji, kecuali yang tercantum di §8. Kebanyakan baru terlihat setelah alur lintas service dijalankan sungguhan (E2E), bukan dari unit test.

| Kelompok | Jumlah | Contoh terpenting |
|---|---|---|
| Keamanan | 6 | endpoint admin/internal terbuka untuk token visitor publik; identitas visitor tidak diverifikasi (IDOR); tiket dapat dibaca hanya dengan bookingId |
| Fungsi yang mati total | 7 | e-tiket tidak pernah terkirim; `queue.event_settings` tidak pernah terisi (antrean tidak jalan); sweeper EDITABLE gagal terus; `ExtendDeadline` selalu gagal; status pembayaran 503 setelah PAID |
| Integritas data | 4 | versi read model ≠ event stream; payment-lock tidak idempoten; promotor membuang alokasi GRANTED |
| Kontrak/dokumen | 4 | status event di luar enum; filter katalog tidak sesuai SRS; OpenAPI admin berbeda dari kode |

Bukti akhir (semua dijalankan hari ini pada kode final):

| Pemeriksaan | Hasil |
|---|---|
| `make test-integration` (6 modul, `-race`, infra Docker) | Lulus |
| `make cover` (ambang 80%, tanpa pengecualian) | pkg 90,6 · queue 83,6 · booking 81,0 · payment 90,0 · notification 82,7 · user 92,4 |
| E2E lintas service `E2E_SLOW=1 tools/e2e.sh` (5 service nyata + Postgres/Valkey/RabbitMQ/Mailpit + token Snap Sandbox nyata; setiap respons divalidasi ke OpenAPI) | Lulus, 362 dtk |
| E2E cepat melawan stack Docker Compose (container) | Lulus, **sebelum** perbaikan tiket/notifikasi; tidak diulang setelahnya |
| Tes Sandbox Midtrans (`-tags sandbox`) | Lulus |
| `tools/verify-reports.sh quality` (gosec 2.22.0 HIGH, govulncheck 1.1.4, hadolint 2.12.0, gitleaks 8.18.4) | Lulus, exit 0 |
| golangci-lint 6 modul · `make contracts` · `make isolation` | 0 isu · lulus · lulus |
| Terraform `fmt -check`, `validate` (1.17.1), Trivy IaC HIGH/CRITICAL | Lulus |
| Pipeline GitHub Actions | **Tidak dijalankan** (tidak ada akses ke runner) |
| `terraform plan/apply` | **Tidak dijalankan** (tanpa akses AWS) |

## 2. Temuan dan perbaikan

Severity: **K** kritis, **T** tinggi, **S** sedang, **R** rendah. "Bukti" = tes yang gagal sebelum perbaikan dan lulus sesudahnya.

### Keamanan

| # | Sev | Temuan | Dampak | Perbaikan | Bukti |
|---|---|---|---|---|---|
| S1 | K | Token visitor (publik: diberikan ke siapa pun yang masuk antrean dengan NIK/email sembarang) diterima sebagai bearer di `/internal/v1/*` dan `/internal/v1/admin/*` | Anonim dapat membuat event, mengubah konfigurasi, memanggil alokasi/pelepasan stok | Token dibatasi per scope: internal wajib `service`, admin wajib token pembeli `buyer` dengan email di `ADMIN_EMAILS`, admission wajib `admission`; `MultiBearerVerifier` dihapus (60a2a34) | `TestAdmin_OnlyAllowListedAccountsCanCreateEvents` (E2E), `TestBearerVerifier_AcceptsOnlyItsScope` |
| S2 | K | Guard admin/internal **terbuka** bila verifier tidak terkonfigurasi (`return next`) | Salah konfigurasi = API admin tanpa autentikasi | Fail closed (`denyAll`) | `TestAdminHandler_FailsClosedWithoutAVerifier`, `…Supply_RefusesWithoutAServiceToken` |
| S3 | K | Booking memakai nilai mentah `rb_visitor` / `X-Visitor-ID` / `X-Visitor-Token` sebagai identitas tanpa verifikasi | Menyamar sebagai visitor lain (sesi, hold, lihat/batalkan booking); JWT asli dari FE bahkan ditolak (cocok-string dengan UUID) | `ResolveVisitor` memverifikasi JWT (kunci Queue, scope `visitor`); `X-Visitor-ID` dihapus (ae7f537) | `TestResolveVisitor_OnlyAVerifiedVisitorTokenIdentifiesTheCaller`, E2E akses tidak sah |
| S4 | K | `GET /bookings/{id}/tickets` tanpa autentikasi/kepemilikan; `ticketLinkToken` tidak pernah diterbitkan | Siapa pun yang tahu bookingId membaca nama pemegang dan kode QR (pencurian tiket) | Akses = visitor pemilik, akun pembeli, atau tautan email bertoken HMAC; selain itu 404 (ee210f5) | `TestGetTickets_OnlyTheOwnerOrTheEmailLinkCanRead`, E2E (orang asing, token palsu, tautan email) |
| S5 | T | Kode tiket = payload QR, dibuat 3 cara berbeda, satu hanya 32 bit (`RB-XXXXXXXX`) | Dapat ditebak/bertabrakan di puluhan ribu tiket | `TK-` + 20 hex acak (80 bit), satu generator (ee210f5) | `TestNewTicketCode_IsRandomAndLongEnough` (20.000 unik) |
| S6 | S | Booking jatuh ke kunci PII nol bila env kosong di semua environment | Hash NIK tidak cocok dengan Queue/User, enkripsi lemah, diam-diam | Wajib diisi di luar `APP_ENV=local` (7129bfc) | `TestBuildPII_DevKeyOnlyForLocalRuns` |

### Fungsi yang mati total

| # | Sev | Temuan | Perbaikan | Bukti |
|---|---|---|---|---|
| F1 | T | `queue.event_settings` tidak pernah diisi → promotor tidak pernah menjalankan event baru; join menerima UUID sembarang | Join pertama memverifikasi event ke Booking (`/supply`, 404 bukan kegagalan breaker) lalu membuat pengaturan default (4c609b0) | `TestJoin_FirstJoinForKnownEvent…`, `TestJoin_UnknownEventIsRefused…`, E2E |
| F2 | T | Queue tidak mengirim `buyerEmail`/`buyerNikMasked`/`accountId` (kontrak internal mewajibkan) → `booking.paid` ditolak Notification, **e-tiket tidak pernah terkirim** | Disimpan di entri Valkey, dikirim saat alokasi (f19cc42) | E2E (email e-tiket diterima), `TestQueueStore_JoinKeepsMaskedNIKAndEmailOnTheEntry` |
| F3 | T | `GET /payments/{id}/status` → 503 setelah PAID/EXPIRED (Booking menjawab 409 di `/payable`) | `/payable` mengembalikan status sebenarnya (20fe48d) | E2E (verifikasi setelah bayar), `TestGetPayable_ReturnsTheRealStatusEvenWhenNotPayable` |
| F4 | T | Session sweeper gagal selamanya untuk admission EDITABLE (`admissions_check2`) | `lock_reason` dalam satu statement (9cadbbe) | `TestExpireSession_EditableAdmissionBecomesConverted` (gagal tanpa perbaikan) |
| F5 | S | `ExtendDeadline` selalu gagal (int → `text`) | `make_interval(secs => $2)` (2683c3c) | `TestExtendDeadline_OncePerBooking` |
| F6 | S | `MarkNotifiedSoon`: `.Ex(600)` = 600 ns → "invalid expire time" | `600 * time.Second` (6d536ef) | `TestQueueStore_SaleStatusDocLeaseAndNotifications` |
| F7 | S | `booking.paid` tanpa objek `event` (nama/venue/waktu) padahal ada di kontrak dan dipakai e-tiket | Dilengkapi (b933371) | `TestSettle_PublishesATicketPayload…` |

### Integritas data

| # | Sev | Temuan | Perbaikan | Bukti |
|---|---|---|---|---|
| D1 | T | Promotor membuang semua alokasi `GRANTED` bila ada satu `NO_SUPPLY_NOW` → begitu antrean lebih panjang dari sisa stok, **tidak ada yang diadmit** dan stok tertahan RESERVED | Prefix FIFO sebelum `NO_SUPPLY_NOW` dipromosikan, grant di luar urutan dilepas (a2db216) | `TestPromoter_PromotesTheGrantedPrefixWhenSupplyRunsOut`, E2E konkurensi |
| D2 | T | Hold yang langsung terkunci: event v2 tetapi `bookings.version` = 1 → payment-lock/settle/expire bentrok versi | Versi 2 (b933371) | `TestHold_LockedAtOnceKeepsReadModelInStepWithTheStream` |
| D3 | S | `PaymentLock` hanya idempoten untuk `VERIFYING` (status tetap PENDING_PAYMENT) → lock kedua menambah event/versi | Cek `payment_started_at` (b933371) | `TestPaymentLock_MovesToVerifyingAndIsIdempotent` |
| D4 | S | `ReserveBatch` ulang untuk entri yang sudah RESERVED dijawab `NO_SUPPLY_NOW` bila stok habis (stok itu miliknya) | Grant yang sama dikembalikan (7921b7f) | `TestAdmissionRepo_ReserveBatchGrantsInOrderThenRunsOutOfSupply` |

### Kontrak dan dokumen

| # | Sev | Temuan | Perbaikan |
|---|---|---|---|
| K1 | S | Status event di API tidak pernah keluar dari `DRAFT` (di luar enum FE) | Diturunkan dari jendela jual dan stok (10db357) |
| K2 | S | Filter katalog: `timeframe` tidak diterapkan; `city` substring; `status` memakai nilai tersimpan (SRS-F-28) | Diperbaiki sesuai SRS (7921b7f) |
| K3 | S | OpenAPI admin memakai nama field yang tidak ada di kode + endpoint upload gambar yang tidak ada; `claim` tanpa parameter `eventId`; respons 401/403/404 tidak terdokumentasi | Diselaraskan; setiap respons E2E kini divalidasi ke OpenAPI (66857a4, 2d6714d) |
| K4 | R | Config event di luar batas → 500 | 400 `VALIDATION_ERROR` (e7c4551) |

Perubahan kontrak ke FE (mengikuti salinan mobile): `Attempt` memakai `redirectUrl/successReturnUrl/failedReturnUrl` (Snap); `PaymentStatus` memuat `orderId`, `paymentExpiresAt`; `Booking`/`TicketList` memuat `orderId`, `ticketQuantity`, `buyerNikMasked`, `publicRef`, `amountMinor`. Selisih sengaja yang tersisa: `JoinRequest.buyerNik` opsional untuk user login (BE benar); `Attempt.redirectUrl` hanya ada saat `PENDING`; endpoint admin dan `publicRef` internal hanya di BE.

## 3. Audit Payment (Midtrans Snap)

| Persyaratan | Status | Bukti |
|---|---|---|
| Snap saja; token dibuat backend; jumlah dan `order_id` dari Booking, bukan klien | Terverifikasi | `chargeSnap`; E2E; Sandbox: token, semua bucket (`enabled_payments`), `expiry.unit=minute` diterima |
| Siklus status; backend otoritatif | Terverifikasi | E2E: pending → deny → FAILED tidak menghidupkan lagi; settlement → PAID; FE tidak pernah melihat PAID sebelum backend memverifikasi |
| Signature webhook (SHA512), nominal, idempotensi, duplikat | Terverifikasi | E2E: signature palsu 401; nominal salah tidak mengubah apa pun; duplikat 200 tanpa e-tiket kedua |
| Kedaluwarsa, settlement terlambat | Terverifikasi | E2E lambat: sweeper → EXPIRED; settlement setelah batas → REVIEW tanpa tiket |
| Order Snap baru ada di Core API setelah pembayar memilih channel (404 sebelumnya = belum dibayar) | Terverifikasi | Tes Sandbox |
| Secret | Server key hanya dari env; `.env` di-ignore git dan `.dockerignore`; tidak muncul di log/respons | Pemeriksaan log container |
| **Belum dilakukan** | Pembayaran selesai sungguhan di Sandbox (VA/GoPay perlu halaman simulator manual). Webhook uji ditandatangani dengan server key yang sama seperti Midtrans, bukan dikirim Midtrans. Webhook tidak memverifikasi ulang ke Status API (rekomendasi pertahanan berlapis, §8) | |
| Risiko diterima | Token Snap tidak bisa dibatalkan; pembayaran lewat token lama setelah attempt diganti masuk `SETTLED_AFTER_CANCEL` (refund manual) — ADR-024 | |

## 4. Audit Ticketing dan Notifikasi

- QR = `RBT:` + kode tiket 80 bit acak, tanpa PII; diterbitkan sekali per item (`ON CONFLICT DO NOTHING`) hanya saat PAID. Tidak ada endpoint check-in/validasi QR di PRD/SRS/kontrak, jadi tidak ada yang dibuat; bila check-in ditambahkan, payload perlu ditandatangani dan status redeemed disimpan server-side.
- E-tiket dikirim dari `booking.paid`, yang hanya diterbitkan setelah `Settle` commit (setelah signature + nominal terverifikasi): notifikasi tidak mendahului verifikasi. Email memuat URL `TICKET_LINK_BASE_URL/{bookingId}?t={token}` (terverifikasi E2E, dibuka tanpa login, token tidak berlaku untuk booking lain).
- Terverifikasi E2E: OTP login (User → RabbitMQ → Notification → Mailpit), e-tiket sekali walau webhook diulang. Tidak terverifikasi E2E: email `expired`/`review`, push FCM, `queue.turn.*`.
- Tidak ada notifikasi "payment pending/failed" karena tidak ada di requirement yang dibaca.

## 5. Audit keamanan lain, performa, keandalan

- **Autorisasi per endpoint** (E2E): booking/pembayaran/tiket pemilik saja; token palsu/visitor lain/`X-Visitor-ID` palsu/tanpa token ditolak (401/403/404).
- **Log**: pada run Compose, tidak ditemukan email, NIK 16 digit, bearer/JWT di log container user/notification/queue/booking/payment (pencocokan 16 digit adalah ID request/trace heksadesimal).
- **Injeksi**: SQL yang dibaca berparameter; satu-satunya penyusunan string adalah nama kolom/urutan dari daftar tetap. gosec HIGH bersih; aturan MEDIUM tidak dijalankan di gerbang.
- **Rate limiting**: ada untuk join (per email/IP), OTP (per email/IP), `me` (per visitor). **Tidak ada** pada endpoint booking/pembayaran/webhook publik (dibatasi WAF di desain HLD, belum ada di Terraform).
- **Konkurensi/oversell**: tes 100 hold bersamaan atas 50 stok (tepat 50), E2E 6 pembeli atas 2 stok (tepat 2, stok 0), idempotency-key hold dan attempt, advisory lock migrasi, lease promotor.
- **Indeks**: query sweeper memakai indeks parsial (`idx_bookings_due`, `idx_admissions_due`, `idx_booking_outbox_pending`). Tinjauan N+1/EXPLAIN menyeluruh dan load test **tidak dilakukan** (butuh data realistis).
- **Reliabilitas**: outbox + relay dengan backoff (terverifikasi ke broker nyata), retry berjenjang + DLQ (terlihat saat debugging: pesan racun masuk DLQ, bukan hilang), breaker + bulkhead pada Midtrans/Booking, shutdown bertahap.
- **Dependensi**: govulncheck bersih pada Go 1.26.10. Cabang Dependabot (`prometheus`, `amqp091-go`, GitHub Actions) belum dimerge.
- **Privasi**: NIK hanya sebagai hash + ciphertext + masked; email dienkripsi di Queue/User; `buyerEmail` dikirim ke Booking melalui kanal internal ber-JWT (sesuai kontrak) dan disimpan sebagai plaintext di `admissions`/`bookings` — keputusan desain yang perlu ditinjau terhadap UU PDP (retensi dan akses). Tidak ada klaim kepatuhan hukum; yang ada hanya kontrol teknis di atas.

## 6. CI/CD

Penyebab `verify-code-quality` gagal ditemukan dari laporan nyata: stdlib Go 1.26.x (govulncheck), 4 temuan gosec (2 G115 diperbaiki/dijustifikasi, 2 G101 false positive), 7 temuan gitleaks (fixture `Idempotency-Key`, tanpa rahasia nyata → `.gitleaksignore` per fingerprint, rule tetap aktif). Dijalankan ulang dengan versi scanner CI: lulus. Tidak ada scanner yang dimatikan atau ambang yang diubah. Gerbang `case-coverage` kini lulus di semua modul (sebelumnya booking 50,8% dan queue 64,5%).

## 7. Infrastruktur AWS

Terraform di `deployments/terraform` (modul network/ecr/iam/storage/data_host/compute/observability) mengikuti HLD §4.5; `fmt`, `validate`, Trivy bersih; README memuat diagram, bootstrap state, deploy, migrasi, rollback, validasi, troubleshooting. Tidak ada resource dibuat. **Belum:** `plan`/`apply`; CloudFront+WAF, Route 53, ACM (ARN dipasok manual), web Next.js, Prometheus/Grafana, scheduled scaling.

## 8. Risiko dan pekerjaan yang tersisa

| # | Sev | Item | Catatan |
|---|---|---|---|
| R1 | S | SSE ketersediaan kategori: `booking:avail:*` hanya dibaca, tidak ada penulis → stream selalu kosong | FE punya fallback polling `GET /booking-sessions/{id}`; perlu proyeksi stok ke Valkey |
| R2 | S | Webhook tidak memverifikasi ulang ke Status API | Signature + nominal sudah kuat; tambahan ini pertahanan berlapis |
| R3 | S | Tidak ada rate limit pada endpoint booking/pembayaran/webhook | Ditutup WAF di produksi (belum ada di Terraform) |
| R4 | S | Endpoint upload gambar event (ada di rencana awal) tidak diimplementasikan; dihapus dari OpenAPI | Perlu keputusan produk |
| R5 | R | `Attempt.redirectUrl` hanya ada saat PENDING (kontrak mobile menandainya wajib) | FE perlu menangani `CREATED/UNKNOWN` |
| R6 | R | `buyerNikMasked` kosong untuk user login yang NIK-nya hanya berupa hash di token | Perlu klaim `nik_masked` di token User |
| R7 | R | Pembayaran selesai sungguhan di Sandbox, load test, EXPLAIN, pen-test tidak dilakukan | |
| R8 | R | Pipeline GitHub Actions belum dijalankan; Dependabot belum dimerge | |

Perubahan yang **breaking** bagi operator: `ADMIN_EMAILS` (admin), `TICKET_LINK_KEY` (wajib di luar `APP_ENV=local`), `PII_*` Booking wajib di luar lokal, `TICKET_LINK_BASE_URL` (tautan di email), `PAYMENT_SUCCESS_RETURN_URL`/`PAYMENT_FAILED_RETURN_URL`, `MIDTRANS_SNAP_BASE_URL`. Semua ada di `.env.example`/compose/README.

## 9. Traceability matrix (SRS §3.2, 193 requirement fungsional)

Dikelompokkan per area SRS. "Verifikasi": **E2E** = lulus di E2E lintas service; **Int** = tes integrasi dengan infra nyata; **Unit** = unit test saja; **Tinjauan** = hanya dibaca, belum dijalankan. Per-ID di tingkat requirement tidak dibuat; cakupannya diberikan per area.

| Area (jumlah req) | Modul → API/fungsi | Implementasi | Tes | Celah / temuan | Task | Verifikasi |
|---|---|---|---|---|---|---|
| GST-01 Guest session (10) | Queue `POST /events/{id}/queue-entries`, token visitor; Booking `ResolveVisitor` | Ada | E2E (header mobile), Unit | Jalur cookie web belum di E2E; S3 | S3 | Sebagian E2E |
| EVT-01 Daftar event (4) | Booking `GET /events` | Ada | Int, E2E (kontrak) | K1 status | K1 | E2E + Int |
| EVT-02 Detail event (5) | `GET /events/{id}` | Ada | Int, E2E | SOLD_OUT diturunkan dari stok | K1 | E2E |
| QUE-01 Join & direct admission (10) | Queue `Join`, `tryDirectAdmission` | Ada | E2E, Unit, Int | F1, F2; validasi NIK | F1,F2 | E2E |
| QUE-02 Status Document (11) | Queue status publisher | Ada | Unit | Tidak dijalankan E2E | – | Unit |
| QUE-03 Admission & claim (8) | Queue `claim`; Booking `POST /booking-sessions` | Ada | E2E | Parameter `eventId` claim | K3 | E2E |
| SES-01 Sesi pemilihan & TTL (6) | Booking `Session` | Ada | E2E, Unit | S3 | S3 | E2E |
| SES-02 Timeout pemilihan (3) | Booking `SweepSessions` | Ada | Int | F4 (sweeper EDITABLE) | F4 | Int |
| ADM-01 Jaminan reservasi (8) | Booking `ReserveBatch`, Queue Promotor | Ada | Int, E2E konkurensi | D1, D4 | D1,D4 | E2E + Int |
| QUE-04 Sold-out (3) | Booking → `booking.sold_out`, Queue consumer | Ada | Unit | Tidak E2E | – | Unit |
| BKG-01 NIK, kuota, identitas pemegang (10) | Booking `CheckAttendee`, `Hold` | Ada | Int, Unit, E2E | – | – | E2E + Int |
| BKG-02 Hold atomik (7) | Booking `UnitOfWork.Hold` | Ada | Int (100×50), E2E | D2 | D2 | E2E + Int |
| BKG-03 Batal (3, SHOULD) | Booking `Cancel` | Ada | Unit, E2E (otorisasi) | Alur batal sukses tidak E2E | – | Unit |
| PAY-01 Pembuatan pembayaran (7) | Payment `attempts` (Snap) | Ada | E2E, Sandbox, Unit | Pembayaran selesai Sandbox manual | PAY-1,2 | E2E + Sandbox |
| PAY-02 Webhook (3) | Payment `webhooks/midtrans` | Ada | E2E, Unit | R2 | – | E2E |
| PAY-03 Sukses tepat waktu (3) | Payment → `payment.succeeded` → Booking `Settle` | Ada | E2E | F7, F3 | F3,F7 | E2E |
| PAY-04 Kedaluwarsa + rekonsiliasi (5) | Booking `Expire`, Payment `resolve` | Ada | E2E lambat, Int | – | – | E2E |
| PAY-05 Halaman bayar web/WebView (4) | Payment `page`, `page-tokens`, `payment-page-sessions` | Ada | Unit/journey | Perilaku WebView FE tidak diuji | – | Unit |
| PAY-06 Breaker (3) | `pkg/resilience`, gateway | Ada | Unit | – | – | Unit |
| TKT-01 Tiket (3) | Booking `GetTickets`, QR | Ada | E2E, Unit | S4, S5; tanpa check-in (bukan requirement) | S4,S5 | E2E |
| MSG-01 Notifikasi (3) | Notification consumer + template | Ada | E2E (OTP, e-tiket), Unit | F2; push/expired/review tidak E2E | F2 | Sebagian E2E |
| API-01 API bersama (3) | OpenAPI + envelope `{meta,data}` | Ada | E2E (validator OpenAPI) | K3 | K3 | E2E |
| BKG-04 Ubah pesanan (11) | Booking `ReplaceItems` | Ada | Int, Unit | Tidak E2E | – | Int |
| SES-03 Refresh/koneksi/back (13) | Booking `GetCurrentSession` | Ada | Unit | Tidak E2E | – | Unit |
| QUE-05 Near-real-time & notif giliran (13) | Queue status, SSE, FCM | Sebagian | Unit, Int (dedupe) | F6; **R1 SSE tanpa penulis**; FCM tidak diuji | F6,R1 | Unit |
| CFG-01 Konfigurasi (8) | Booking admin `PATCH` config | Ada | Int | K4; admin dibatasi (S1) | S1,K4 | Int |
| USR-01 Akun opsional & OTP (13) | User service | Ada | E2E (login OTP), Unit | Profil/NIK tidak E2E | – | Sebagian E2E |
| CAT-01 Filter katalog (12) | Booking `ListEvents` | Ada | Int | K2 | K2 | Int |

Kebutuhan non-fungsional (SRS-Q, 54 item) dan antarmuka/constraint (IF 17, C 12, D 12): keamanan, ketahanan, observabilitas, kontrak diperiksa pada §3–§7; item performa kuantitatif (latensi p99, throughput join) **tidak diukur**.
