# Architecture Decision Records — Rusuh Booking Tiket

| Field | Value |
|-------|-------|
| **Document ID** | ADR-RBT |
| **Version** | 1.2 |
| **Date** | 2026-10-19 |
| **Deciders** | Tim PT. Tiket Sampai Nonton Sentosa (BE-1, BE-2, WEB-1, WEB-2, MOB-1) |
| **Source** | PRD v4.6 §1.4, §5.25, §7, §9.7, §9.8, §13.4, Lampiran L; keputusan tim 14–16 Oktober 2026; keputusan bersama BE-2 19 Oktober 2026 |

> **Reference basis.** Format mengikuti Nygard ADR / MADR dan mencatat elemen *architecture decision* dan *rationale* pada ISO/IEC/IEEE 42010:2022. Satu keputusan per ADR. ADR yang sudah Accepted tidak diubah; perubahan dibuat dengan ADR baru yang menggantikannya.
>
> Semua ADR di bawah berstatus **Accepted** sebagai baseline PoC. Perubahan scope, data model, urutan lock, batas service, atau topologi deployment wajib melalui ADR baru (v4.6 Final Canonical Release Statement).

## Index

| ADR | Title | Date | Concerns (HLD) | Related requirements |
|-----|-------|------|----------------|----------------------|
| ADR-001 | Empat service sesuai aktor requirement, schema per service, monorepo | 2026-10-15 | CN-02, CN-03 | SRS-C-01, NFR-04 |
| ADR-002 | Reservasi stok tingkat event saat admission | 2026-10-15 | CN-03, CN-04 | GR-01, SRS §3.2.9 |
| ADR-003 | Waiting room via polling Status Document di CDN; SSE hanya di halaman detail | 2026-10-15 | CN-07 | FR-06–FR-08, NFR-01, NFR-06, SRS §3.2.25 |
| ADR-004 | Guest checkout tanpa OTP email | 2026-10-15 | CN-06 | GR-05, AS-04 |
| ADR-005 | Aturan NIK: validasi struktur, kuota pembeli dan pemegang terpisah, cek langsung tanpa kunci | 2026-10-15 | CN-06 | GR-03, GR-14, BR-01, BR-02, SRS §3.2.11 |
| ADR-006 | Ubah pesanan hanya sebelum "Bayar", penggantian atomik, sisa unit dipegang | 2026-10-15 | CN-04 | AS-21, AS-22, SRS §3.2.23 |
| ADR-007 | Rekonsiliasi pembayaran sebelum melepas stok; refund manual | 2026-10-15 | CN-04 | GR-09, FR-23, AS-09 |
| ADR-008 | PostgreSQL sebagai otoritas stok dan uang; Valkey hanya antrean, rate limit, cache | 2026-10-16 | CN-03 | NFR-04, SRS-Q-REL-02..03 |
| ADR-009 | Sikap CAP/PACELC per komponen | 2026-10-16 | CN-03 | SRS-Q-REL-02..04 |
| ADR-010 | Outbox hanya di Booking dan Payment; consumer idempoten tanpa tabel inbox; RabbitMQ + JSON | 2026-10-16 | CN-03, CN-04 | NFR-10, FR-25 |
| ADR-011 | REST internal dan JWT EdDSA | 2026-10-15 | CN-02, CN-06 | NFR-07, NFR-08 |
| ADR-012 | Edge CloudFront + WAF + ALB dan compute EC2 Auto Scaling Group | 2026-10-14 | CN-05, CN-06 | NFR-01, NFR-03 |
| ADR-013 | Snapshot konfigurasi per sesi; aturan jaminan dikunci saat penjualan dibuka | 2026-10-15 | CN-04 | GR-12, AS-26, SRS §3.2.26 |
| ADR-014 | Notifikasi giliran: peringatan + push + jendela klaim 3 menit, tanpa prioritas | 2026-10-15 | CN-04 | GR-08, AS-06, AS-24, AS-25 |
| ADR-015 | Tech stack sesuai tech radar dan alasan best practice | 2026-10-16 | CN-09 | SRS-C-02 |
| ADR-016 | Struktur proyek mengikuti standar proyek organisasi | 2026-10-17 | CN-02, CN-09 | SRS-C-08 |
| ADR-017 | Tiga repository terpisah (backend, web, mobile) dalam satu organisasi GitHub | 2026-10-17 | CN-02, CN-05 | CON-06, SRS-C-08 |
| ADR-018 | Konsistensi lintas service dengan saga koreografi dan kompensasi | 2026-10-18 | CN-03, CN-04 | NFR-10, SRS-Q-REL-04, BR-06 |
| ADR-019 | Event sourcing untuk agregat Booking (Payment: tabel state, amandemen 2026-10-23); stok tetap relasional | 2026-10-19 | CN-03, CN-04, CN-09 | SRS-C-09, SRS-D-10, SRS-D-11, SRS-Q-REL-09 |
| ADR-020 | User Service untuk akun opsional dengan login OTP email; guest checkout tetap utama | 2026-10-19 | CN-02, CN-06 | SRS §3.2.27, SRS-F-01-08, SRS-Q-SEC-10..11 |
| ADR-021 | Usulan BE-2 yang dicatat sebagai alternatif (gRPC internal, EKS + Helm, API Gateway/Kong) | 2026-10-19 | CN-02, CN-05, CN-09 | NFR-01, NFR-03 |
| ADR-022 | Runtime dan tooling boilerplate: Go 1.26, verb Cobra, client standar, migrasi tertanam, `/health` | 2026-10-19 | CN-09 | SRS-C-02, SRS-C-08, SRS-F-19-03 |
| ADR-023 | Observability: log JSON ke stdout, metrik Prometheus, Grafana + Loki (Alloy), korelasi `trace_id` | 2026-10-21 | CN-09 | SRS-Q-MNT-02, SRS-Q-MNT-03, SRS-Q-SEC-03, SRS-F-90-06 |

---

## ADR-001: Empat service sesuai aktor requirement, schema per service, monorepo

| Field | Value |
|-------|-------|
| **Status** | Accepted (menggantikan modular monolith v4.1); **jumlah service diperbarui ADR-020** (User Service ditambahkan) |
| **Date** | 2026-10-15 |
| **Concerns** | CN-02, CN-03 |
| **Affected components** | CMP-QUEUE, CMP-BOOKING, CMP-PAYMENT, CMP-NOTIF |

**Context.** Requirement menyebut aktor Queue Service, Booking Service, Payment Service, dan Notification Service. Beban join antrean berbeda jauh dari beban transaksi. Oversell harus mustahil walaupun sistem terdistribusi. Tim hanya 2 BE.

**Decision drivers.** Keterlacakan ke aktor requirement; tidak ada oversell; skala waiting room terpisah; kolaborasi 2 BE; operasional sederhana.

**Considered options.** (A) 4 service + schema per service; (B) modular monolith; (C) microservices granular (stok, kuota, tiket terpisah).

**Decision.** Pilih **A**: 4 service dalam satu monorepo (satu proyek dan `go.mod` per service sesuai standar proyek, disatukan `go.work`; satu binary per service dengan mode api/worker; lihat ADR-016), satu cluster PostgreSQL dengan schema dan user DB terpisah per service, tanpa FK/join lintas schema. Semua yang menyentuh stok berada di Booking Service dan berubah dalam satu transaksi. Komunikasi sinkron hanya Queue→Booking dan Booking↔Payment; sisanya asinkron via RabbitMQ.

**Consequences.**
- Positif: oversell dicegah oleh satu transaksi; Queue di-scale terpisah; pembagian kerja jelas (BE-1 Queue + Notification, BE-2 Booking + Payment); schema dapat dipindah ke instance sendiri tanpa ubah kode.
- Negatif: 4 image dan kontrak antar-service; koordinasi kontrak (dibekukan D2).

**Confirmation.** Kode service di `internal/` (tidak dapat diimpor service lain); `depguard` di CI; contract test; TC-BKG-07.

---

## ADR-002: Reservasi stok tingkat event saat admission

| Field | Value |
|-------|-------|
| **Status** | Accepted |
| **Date** | 2026-10-15 (v4.2, disempurnakan v4.3) |
| **Concerns** | CN-03, CN-04 |
| **Affected components** | CMP-BOOKING, CMP-QUEUE |

**Context.** GR-01: pembeli yang masuk halaman beli pasti dapat tiket. Notulen bertentangan (D-2 vs KP-3/KP-4). Stok puluhan ribu tidak dapat di-hold untuk 1 juta pengantre. Pembeli belum memilih kategori saat giliran tiba.

**Considered options.** Hold saat masuk antrean; reservasi per kategori saat admission; reservasi 1 unit; **reservasi tingkat event N = min(3, sisa kuota, stok bebas) saat admission**.

**Decision.** Giliran hanya diberikan bila Booking berhasil mereservasi N unit tingkat event. Unit dikonversi menjadi hold kategori saat "Lanjut ke Pembayaran". Invariant dijaga DB: `allocated + held + sold ≤ total_capacity`, `total_capacity = Σ kapasitas kategori`, hold ≤ reservasi sendiri.

**Consequences.**
- Positif: jaminan "hingga N tiket" selalu benar (dibuktikan harness); sisa stok 1–2 tetap terjual; kategori favorit tidak terkunci.
- Negatif: kategori favorit tidak dijamin (dikomunikasikan, AS-10); admission ditahan saat stok bebas 0 tetapi masih ada yang mungkin kembali.

**Confirmation.** TC-ADM-01..05, TC-BKG-12; harness Test Plan Annex B.

---

## ADR-003: Waiting room via polling Status Document di CDN; SSE hanya di halaman detail

| Field | Value |
|-------|-------|
| **Status** | Accepted |
| **Date** | 2026-10-15 (v4.4) |
| **Concerns** | CN-07 |
| **Affected components** | CMP-QUEUE, CMP-EDGE, CMP-WEB, CMP-MOBILE, CMP-BOOKING |

**Context.** Requirement meminta posisi "real time" tanpa angka latensi. Inti yang diminta: tidak PHP dan adil. Pengantre hingga 1 juta. Di mobile, koneksi persisten putus saat jaringan berpindah, dan aplikasi di background tidak menerima polling, SSE, maupun WebSocket; yang benar-benar menolong adalah push notification (ADR-014).

**Considered options.** Polling ke origin; WebSocket/SSE di waiting room; **polling satu dokumen status per event yang di-cache CDN**.

**Decision.** Waiting room membaca Status Document dari CDN (cache 1 s); posisi dihitung di klien dari nomor antrean bertanda tangan; interval polling dikendalikan server. "Real time" = near-real-time dengan batas keterlambatan terukur (SRS-F-25-01). SSE hanya di halaman detail (jumlah koneksi dibatasi slot aktif) dengan fallback polling otomatis.

**Consequences.**
- Positif: beban origin konstan terhadap jumlah pengantre; tanpa 1 juta koneksi persisten; keadilan tidak dipengaruhi latensi karena urutan dari seq server; interval dapat dituning tanpa rilis aplikasi.
- Negatif: keterlambatan beberapa detik; bila panitia meminta < 1 s di waiting room, biaya naik (AS-20, OI-07).

**Confirmation.** TC-PERF-01, TC-QUE-14, TC-QUE-15, TC-SSE-01..03.

---

## ADR-004: Guest checkout tanpa OTP email

| Field | Value |
|-------|-------|
| **Status** | Accepted (menggantikan modul Identity & Auth v4.1); **diubah sebagian oleh ADR-020**: guest tetap tanpa OTP, login OTP tersedia sebagai pilihan |
| **Date** | 2026-10-15 |
| **Concerns** | CN-06 |
| **Affected components** | CMP-QUEUE, CMP-WEB, CMP-MOBILE |

**Context.** Grooming KP-7/D-6: login tidak wajib, email wajib. OTP untuk 1 juta pengantre mahal dan menambah friksi.

**Considered options.** Login/akun; OTP email; **guest + email diketik dua kali tanpa OTP**.

**Decision.** Identitas customer = visitor token bertanda tangan + NIK pembeli (HMAC) + email. Email diketik dua kali; tiket juga tampil di layar dan via tautan.

**Consequences.** Positif: friksi rendah, biaya rendah. Negatif: risiko salah ketik email (dimitigasi konfirmasi ganda dan tiket di layar).

**Confirmation.** TC-GST-01..05, TC-TKT-01.

---

## ADR-005: Aturan NIK — validasi struktur, kuota pembeli dan pemegang terpisah, cek langsung tanpa kunci

| Field | Value |
|-------|-------|
| **Status** | Accepted |
| **Date** | 2026-10-15 |
| **Concerns** | CN-06 |
| **Affected components** | CMP-BOOKING, CMP-QUEUE, CMP-PLATFORM |

**Context.** BR-01 (maks 3 tiket per NIK) bertentangan dengan arahan CTO KP-13 (setiap tiket NIK berbeda). Validasi NIK harus internal (GR-14). Pembeli ingin tahu lebih awal bila NIK pemegang tidak bisa dipakai.

**Considered options.** Satu counter gabungan vs dua hitungan terpisah; Dukcapil vs validasi struktur; mengunci NIK selama sesi vs cek langsung tanpa kunci.

**Decision.** NIK pemegang maks 1 tiket per event (unique index); NIK pembeli maks 3 tiket per event; dua hitungan terpisah per event. Validasi struktur NIK internal. Cek NIK pemegang langsung saat diketik, dibatasi 10 NIK berbeda per sesi, tanpa mengunci; cek final atomik saat hold.

**Consequences.** Positif: sesuai dua aturan sumber; perlindungan calo dari aturan pemegang; tidak ada celah memblokir NIK orang lain. Negatif: NIK palsu berformat benar lolos (Dukcapil sebagai opsi lanjutan); bentrok bersamaan ditangkap saat hold.

**Confirmation.** TC-BKG-01..03, TC-BKG-08..11.

---

## ADR-006: Ubah pesanan hanya sebelum "Bayar", penggantian atomik, sisa unit dipegang

| Field | Value |
|-------|-------|
| **Status** | Accepted |
| **Date** | 2026-10-15 (v4.4) |
| **Concerns** | CN-04 |
| **Affected components** | CMP-BOOKING, CMP-PAYMENT, CMP-WEB, CMP-MOBILE |

**Context.** Pembeli perlu mengoreksi pesanan setelah "Lanjut ke Pembayaran". Mengubah setelah transaksi gateway dibuat berisiko transfer ke VA lama.

**Considered options.** Tidak bisa ubah; bebas ubah selama TTL Booking (batalkan transaksi gateway); **ubah hanya sebelum "Bayar"**. Lepas hold lalu pilih ulang vs **penggantian atomik**. Sisa unit dilepas saat hold vs **dipegang sampai terkunci**.

**Decision.** Ubah pesanan diizinkan sampai menekan "Bayar Sekarang" dan selama TTL Booking; maks 3 kali; penggantian atomik (`PUT /bookings/{id}/items`); sisa unit dipegang sampai terkunci; TTL Pembayaran tidak di-reset; Payment memanggil `payment-lock` sebelum membuat transaksi.

**Consequences.** Positif: jaminan N tetap berlaku saat kembali ke detail; tidak ada risiko VA ganda; kategori yang dipegang tidak hilang di tengah perubahan. Negatif: state `EDITABLE` menambah kompleksitas (dimitigasi harness dan flag per event).

**Confirmation.** TC-EDT-01..06, TC-NAV-01..03.

---

## ADR-007: Rekonsiliasi pembayaran sebelum melepas stok; refund manual

| Field | Value |
|-------|-------|
| **Status** | Accepted |
| **Date** | 2026-10-15 |
| **Concerns** | CN-04 |
| **Affected components** | CMP-BOOKING, CMP-PAYMENT |

**Context.** GR-09: gateway yang lambat tidak boleh membuat pembeli di-PHP. Webhook dapat terlambat. Dukungan refund API berbeda per metode.

**Considered options.** Release langsung saat TTL; **resolve ke gateway sebelum release**. Refund otomatis vs **refund manual**.

**Decision.** Saat TTL Pembayaran habis, booking `VERIFYING` (stok ditahan) dan Payment melakukan `resolve(CHECK_AND_CANCEL)`. Tepat waktu dinilai dari `settlement_time`. Pembayaran setelah batas → `REVIEW` dan refund manual oleh Ops.

**Consequences.** Positif: pembayaran sah tidak dibatalkan; stok orang lain tidak diambil. Negatif: stok tertahan hingga masa tenggang 3 menit; proses Ops manual.

**Confirmation.** TC-PAY-04, TC-PAY-06..08.

---

## ADR-008: PostgreSQL sebagai otoritas stok dan uang; Valkey hanya antrean, rate limit, cache

| Field | Value |
|-------|-------|
| **Status** | Accepted; **diperluas ADR-020**: Valkey juga menyimpan challenge OTP sementara (hash kode + hitungan percobaan, ber-TTL). Sesi login tetap di PostgreSQL |
| **Date** | 2026-10-16 (v4.5) |
| **Concerns** | CN-03 |
| **Affected components** | CMP-BOOKING, CMP-PAYMENT, CMP-QUEUE, DS-PG, DS-VK |

**Context.** Antrean butuh operasi atomik berlatensi rendah (INCR, sorted set, Lua). Stok dan uang butuh jaminan transaksi terkuat. Replikasi in-memory bersifat async.

**Considered options.** Valkey juga untuk stok dan sesi; **Valkey hanya antrean/rate limit/cache, stok di PostgreSQL**. Client `valkey-go` (pure Go) vs client berbasis core native (CGO). Redis OSS vs Valkey.

**Decision.** PostgreSQL 16 untuk seluruh stok, alokasi, hold, sesi, kuota, NIK pemegang, booking, pembayaran. Valkey 8.1 + `valkey-go` hanya untuk antrean, rate limit, dan cache; semua data Valkey dapat dipulihkan atau dihitung ulang.

**Consequences.** Positif: failover Valkey tidak pernah menyebabkan oversell atau hilangnya tiket yang dijanjikan; tulisan stok dibatasi slot aktif (< 250/detik di Tier L) sehingga PostgreSQL bukan bottleneck; lisensi BSD; ElastiCache for Valkey lebih murah. Negatif: dua data store untuk dioperasikan.

**Confirmation.** TC-CAP-01, TC-CAP-02; benchmark Lua (SDD §4.9).

---

## ADR-009: Sikap CAP/PACELC per komponen

| Field | Value |
|-------|-------|
| **Status** | Accepted |
| **Date** | 2026-10-16 (v4.6) |
| **Concerns** | CN-03 |

**Context.** Sistem terdistribusi harus memilih antara konsistensi dan ketersediaan saat partisi.

**Considered options.** Satu sikap untuk seluruh sistem; **sikap per komponen menurut akibat bila data salah**.

**Decision.** CP untuk stok dan uang (PostgreSQL, satu primary); fail closed untuk penerbitan nomor antrean; AP untuk cache, Status Document, dan notifikasi. Tanpa transaksi terdistribusi (2PC); alur pemesanan lintas service memakai saga koreografi dengan kompensasi (ADR-018). Rincian tabel ada di HLD §4.3.

**Consequences.** Positif: konsistensi kuat hanya di tempat yang membutuhkan; tidak ada oversell saat partisi. Negatif: hold gagal sementara saat primary tidak terjangkau; join ditolak beberapa detik saat Valkey gagal.

**Confirmation.** TC-CAP-01..03, TC-MSG-01..03.

---

## ADR-010: Outbox hanya di Booking dan Payment; consumer idempoten tanpa tabel inbox; RabbitMQ + JSON

| Field | Value |
|-------|-------|
| **Status** | Accepted (menggantikan `consumer_inbox` di v4.5) |
| **Date** | 2026-10-16 (v4.6) |
| **Concerns** | CN-03, CN-04 |
| **Affected components** | CMP-BOOKING, CMP-PAYMENT, CMP-QUEUE, CMP-NOTIF, DS-MQ |

**Context.** Dual write berbahaya hanya untuk event yang membawa akibat uang atau tiket. RabbitMQ mengirim at-least-once.

**Considered options.** Outbox di semua producer / publish langsung di semua / **outbox hanya Booking dan Payment**. Tabel `consumer_inbox` per service vs **idempotensi dari data bisnis**. Protobuf vs **JSON + JSON Schema**. Amazon SQS vs **RabbitMQ**.

**Decision.** Transactional outbox di Booking dan Payment; Queue mem-publish `queue.turn.*` langsung dengan publisher confirm; Notification hanya consumer. Idempotensi consumer lewat guard state, Lua idempoten, dan unique key bisnis. RabbitMQ dengan retry berjenjang dan DLQ; payload JSON.

**Consequences.** Positif: customer tidak mungkin sudah bayar tanpa tiket karena crash; tabel pendukung turun 13 → 8; mudah diinspeksi dan dijalankan lokal. Negatif: email/push at-least-once (dinyatakan jujur).

**Confirmation.** TC-MSG-01..03, TC-CAP-03.

---

## ADR-011: REST internal dan JWT EdDSA

| Field | Value |
|-------|-------|
| **Status** | Accepted |
| **Date** | 2026-10-15 |
| **Concerns** | CN-02, CN-06 |

**Context.** Hanya dua titik komunikasi sinkron. Autoscaling tanpa sticky session. Swagger/OpenAPI ADOPT di radar; gRPC belum.

**Considered options.** gRPC vs **REST/JSON + OpenAPI 3.1**. Session store terpusat vs **JWT EdDSA**. Perbandingan lengkap dengan usulan gRPC BE-2: ADR-021.

**Decision.** API internal REST/JSON dengan service token JWT berumur pendek di jaringan privat. Token visitor, queue, admission, payment page, ticket link, dan service memakai JWT EdDSA (Ed25519); private key hanya di service penerbit; `jti` untuk token sekali pakai; kunci di SSM dengan rotasi `kid`.

**Consequences.** Positif: tidak ada session store; aman untuk autoscaling; signature kecil dan cepat. Negatif: revokasi token bergantung pada TTL pendek dan `jti`.

**Confirmation.** TC-SEC-01, TC-API-02.

---

## ADR-012: Edge CloudFront + WAF + ALB dan compute EC2 Auto Scaling Group

| Field | Value |
|-------|-------|
| **Status** | Accepted |
| **Date** | 2026-10-14 (v4.1, tanpa Kong) |
| **Concerns** | CN-05, CN-06 |
| **Affected components** | CMP-EDGE, semua service |

**Context.** Polling status hingga 1 juta klien, bot saat jam buka, kredit AWS US$200, assessor harus bisa menjalankan lokal.

**Considered options.** Kong / API Gateway vs **CloudFront + WAF + ALB**; container orchestrator vs **EC2 ASG (Graviton)**. Perbandingan lengkap dengan usulan EKS + Helm + Kong BE-2: ADR-021.

**Decision.** CloudFront menyerap polling Status Document dan event list; WAF managed rules, rate-based rule, dan challenge saat jam buka; ALB path routing per service, origin hanya dari CloudFront. Compute EC2 ASG terpisah untuk Queue dan Core, scheduled scale-out sebelum jam buka. Lokal: nginx edge di Docker Compose meniru routing dan cache.

**Consequences.** Positif: proteksi bot dan DDoS managed; biaya PoC dalam kredit. Negatif: status radar CloudFront/WAF/ALB perlu konfirmasi (OI-02).

**Confirmation.** TC-SEC-02, TC-ASG-01, TC-PERF-01.

---

## ADR-013: Snapshot konfigurasi per sesi; aturan jaminan dikunci saat penjualan dibuka

| Field | Value |
|-------|-------|
| **Status** | Accepted |
| **Date** | 2026-10-15 (v4.4) |
| **Concerns** | CN-04 |
| **Affected components** | CMP-BOOKING, CMP-QUEUE |

**Context.** GR-12: TTL configurable. Mengubah aturan di tengah penjualan membuat aturan berbeda antara pengantre sebelum dan sesudah perubahan.

**Considered options.** Konfigurasi global dibaca setiap request vs **snapshot per sesi + aturan jaminan dikunci**.

**Decision.** Nilai TTL dan fitur disalin ke admission saat giliran diberikan; aturan pembentuk jaminan dikunci pada `rules_locked_at`; validasi silang gagal sejak awal; setiap perubahan diaudit.

**Consequences.** Positif: perubahan tidak pernah memendekkan waktu orang yang sedang berjalan. Negatif: kesalahan konfigurasi aturan harus diputuskan sebelum jam buka.

**Confirmation.** TC-CFG-01..04.

---

## ADR-014: Notifikasi giliran — peringatan + push + jendela klaim 3 menit, tanpa prioritas

| Field | Value |
|-------|-------|
| **Status** | Accepted (claim window 2 → 3 menit) |
| **Date** | 2026-10-15 (v4.4) |
| **Concerns** | CN-04 |
| **Affected components** | CMP-QUEUE, CMP-NOTIF, CMP-MOBILE |

**Context.** Aplikasi mobile di background tidak menerima polling. Giliran hangus merugikan pembeli; kesempatan kedua merugikan pengantre lain.

**Considered options.** Jendela panjang (10 menit); kesempatan kedua dengan prioritas; **peringatan sebelum giliran + push + jendela 3 menit, tanpa prioritas**.

**Decision.** Banner "giliran sebentar lagi" dan push `TURN_SOON`; push `TURN_ARRIVED`; claim window 3 menit (PROPOSED, configurable); giliran hangus tanpa prioritas; missed turn rate dipantau (> 5% → naikkan).

**Consequences.** Positif: mengurangi giliran hangus tanpa menahan stok lama; urutan tidak pernah diubah. Negatif: push best effort; pembeli yang lalai antre ulang dari belakang.

**Confirmation.** TC-QUE-09, TC-QUE-13.

---

## ADR-015: Tech stack sesuai tech radar dan alasan best practice

| Field | Value |
|-------|-------|
| **Status** | Accepted (PostgreSQL, Valkey, RabbitMQ dikonfirmasi ADOPT 16 Okt; komponen "perlu cek radar" dikonfirmasi saat kickoff); **versi Go dan library boilerplate diperbarui ADR-022** |
| **Date** | 2026-10-16 (v4.5) |
| **Concerns** | CN-09 |

**Context.** Proposal tender harus sesuai tech radar klien dan dapat dipertahankan saat technical defense. Daftar stack ada di HLD §4.8.

**Decision.** Stack mengikuti tabel di HLD §4.8. Prinsip pemilihan dan alasan per komponen:

*Sumber: PRD v4.6 §7.3*

Prinsip pemilihan: **(1)** sesuai tech radar klien, **(2)** setiap komponen punya satu tugas yang jelas, **(3)** yang menyentuh stok dan uang memakai teknologi dengan jaminan transaksi terkuat, **(4)** semuanya dapat dijalankan assessor secara lokal tanpa AWS, **(5)** biaya dikendalikan oleh desain, bukan oleh diskon.

| Komponen | Pilihan | Alasan best practice | Nilai untuk tender |
|---|---|---|---|
| Bahasa backend | **Go 1.26** + `net/http` standard library | Goroutine murah untuk ribuan koneksi bersamaan (join, SSE); binary tunggal tanpa runtime; memori kecil per instance; race detector bawaan untuk menguji konkurensi; dependency minimal mengurangi CVE | Instance kecil (t4g) cukup, biaya compute rendah; ADOPT di radar |
| Database transaksi | **PostgreSQL 16** | Transaksi ACID, row lock, partial unique index, dan constraint trigger menegakkan invariant stok di database itu sendiri, bukan hanya di kode; `SKIP LOCKED` untuk worker expiry | Jawaban paling kuat untuk "bagaimana menjamin tidak oversell": dijaga constraint, dibuktikan harness (Lampiran C.7 [v4.6 → SDD §4.7.1]) |
| In-memory store | **Valkey 8.1** + **`valkey-go`** | Struktur data yang tepat untuk antrean (INCR, sorted set, TTL) dan Lua atomik untuk join/admit; lisensi open source BSD di bawah Linux Foundation; kompatibel penuh dengan perintah dan Lua Redis 7.2; `valkey-go` pure Go (tanpa CGO), mendukung cluster, auto-pipelining, dan `EVALSHA` | ElastiCache for Valkey dipatok lebih murah dibanding engine Redis OSS; tidak ada risiko lisensi bagi klien |
| Broker | **RabbitMQ** + transactional outbox (Booking, Payment) | Retry berjenjang, DLQ, dan publisher confirm bawaan; outbox menulis state dan event dalam satu commit sehingga event yang membawa akibat uang/tiket tidak hilang walau proses crash atau broker mati; consumer idempoten lewat guard state dan unique key; manual ack setelah efek durable | Kegagalan email/notifikasi tidak pernah membatalkan transaksi (NFR-10); dapat dijalankan lokal |
| Komunikasi | **REST/JSON + OpenAPI 3.1**, satu kontrak untuk web dan mobile | Kontrak-first: tipe FE di-generate dari kontrak, mock Apidog untuk FE sejak hari pertama; tidak ada logika per klien di backend | Memenuhi FR-24/NFR-08; tim FE tidak menunggu BE |
| Real-time | **Polling ke CDN** di waiting room, **SSE** di halaman detail | Beban origin konstan terhadap jumlah pengantre; SSE hanya untuk pembeli yang sudah lolos antrean (jumlah dibatasi slot aktif); fallback polling otomatis | 1 juta pengantre tanpa 1 juta koneksi terbuka; biaya per penjualan didominasi request CDN yang murah |
| Token | **JWT EdDSA** (Ed25519) | Signature kecil dan verifikasi cepat; private key hanya di service penerbit, service lain cukup public key; `jti` untuk token sekali pakai | Tidak ada session store terpusat; aman untuk autoscaling tanpa sticky session |
| Web | **Next.js (App Router) + TypeScript + Tailwind** | Satu codebase untuk halaman customer dan payment page; payment page satu origin dengan API (cookie HttpOnly, tanpa CORS); TypeScript strict dari kontrak | Payment page sama dipakai di WebView mobile (SCP-01): satu implementasi, dua platform |
| Mobile | **React Native + TypeScript** | Satu developer mobile dapat mengirim Android dan iOS; logika posisi antrean (`queue-position.ts`) dipakai bersama web | Mengurangi risiko jadwal dengan 1 FE mobile |
| Edge | **CloudFront + AWS WAF + ALB** | CDN menyerap polling status; WAF challenge dan rate-based rule menahan bot di tepi; ALB path routing per service; origin hanya menerima dari CloudFront | Perlindungan bot dan DDoS managed tanpa operasi tambahan |
| Compute | **EC2 Auto Scaling Group** (Graviton t4g/c7g) | ASG Queue terpisah karena beban join berbeda dari beban transaksi; scheduled scale-out sebelum jam buka; Graviton memberi harga per vCPU lebih rendah | Biaya PoC dalam kredit; skala production terukur per tier (§17 [v4.6 → HLD §4.7]) |
| IaC & CI/CD | **Terraform** + **GitHub Actions dengan OIDC** | Infrastruktur dapat direview dan diulang; OIDC menghapus access key jangka panjang; image dibangun sekali dan di-deploy per digest | Assessor dapat mengaudit seluruh infra dari repo |
| Lokal | **Docker Compose** + nginx edge | Seluruh sistem (5 service, PostgreSQL, Valkey, RabbitMQ, Mailpit, observability) jalan dengan satu perintah; nginx meniru routing ALB dan cache CloudFront | Clean-room assessor tanpa AWS dan tanpa private registry |
| Payment | **Midtrans Sandbox (Snap)** | Backend membuat token Snap; webhook bersignature, status API untuk rekonsiliasi, expiry Snap diselaraskan dengan TTL (lihat ADR-024); data kartu tidak pernah menyentuh sistem | Integrasi aktual, bukan mock; sesuai grooming (GR-13) |
| Observability | **OpenTelemetry → Prometheus + Grafana**, Jaeger lokal | Standar vendor-neutral; trace lintas service dan event; dashboard disimpan di repo | Metrik antrean, konversi, dan expiry real-time (NFR-09) dengan biaya rendah |
| Testing | Go `testing` (race), **godog**, **Apidog**, **Katalon**, **Appium**, **k6**, **Zephyr Scale** | Piramida test lengkap; acceptance criteria ditulis Gherkin; k6 membuktikan klaim kapasitas | Setiap klaim proposal ditautkan ke test ID dan evidence |
| Keamanan | gosec, govulncheck, SonarCloud, Trivy, ZAP, secret scanning | Shift-left: dicek di setiap PR, bukan di akhir | Repo public tetap aman; temuan high/critical menjadi release blocker |


**Confirmation.** Review tech radar pada kickoff; komponen HOLD diganti lewat ADR baru.

---

## ADR-016: Struktur proyek mengikuti standar proyek organisasi

| Field | Value |
|-------|-------|
| **Status** | Accepted (menggantikan struktur monorepo Lampiran F PRD v4.6); **tabel deviasi diperbarui ADR-022** (entry point Cobra, 5 service) |
| **Date** | 2026-10-17 |
| **Concerns** | CN-02, CN-09 |
| **Related requirements** | SRS-C-08 |
| **Affected components** | CMP-QUEUE, CMP-BOOKING, CMP-PAYMENT, CMP-NOTIF, CMP-PLATFORM |

**Context.** Organisasi memiliki standar struktur proyek Go yang wajib diikuti: hexagon per domain di `internal/<domain>/` dengan `port/{inbound,outbound}`, `domain/{entity,valueobject,dto,mapper}`, `application/usecase`, `infrastructure/*`; `internal/shared/entity`, `internal/config`, `internal/delivery/server.go` sebagai composition root; `pkg/` untuk utilitas bersama; `_mock/` hasil generate; `assets/`, `deployments/`, `docs/`; `.gitlab-ci.yml`; interface berawalan `I`. Struktur di PRD v4.6 (Lampiran F) memakai `domain/application/adapter` dengan port dalam satu file dan wiring di `cmd`. RBT berbeda dari contoh standar karena repository backend memuat 4 service dan artefak wajib assessment; web dan mobile berada di repository terpisah (ADR-017).

**Decision drivers.** Kepatuhan pada standar organisasi (wajib); batas antar-service yang ditegakkan compiler; kode bersama tidak disalin 4 kali; aturan assessment (GitHub public, `.kiro/`, `specs/`, Docker Compose untuk assessor).

**Considered options.** (A) Setiap service menjadi proyek penuh sesuai standar di `services/<svc>/`, `pkg/` bersama di root via `go.work`; (B) satu proyek dengan 4 hexagon di `internal/{queue,booking,payment,notification}` dan `cmd/<svc>/main.go`; (C) tetap struktur v4.6.

**Decision.** Pilih **A**. Setiap `services/<svc>/` mengikuti standar sepenuhnya (SDD-RBT-001 §4.1). Penyesuaian yang diperlukan dicatat sebagai deviasi:

| Elemen | Penyesuaian | Alasan |
|---|---|---|
| `pkg/` | Di root sebagai module bersama, bukan per service | Dipakai 5 service; menghindari 5 salinan |
| Worker | `cmd/main.go` dengan flag `--mode=api` atau `--mode=worker` + `internal/delivery/worker.go` (sejak ADR-022: verb Cobra `api`/`worker`) | Mempertahankan satu entry point sesuai standar |
| Migration, Dockerfile | `services/<svc>/deployments/{Dockerfile,migrations}` | Standar tidak punya folder migration; `deployments/` adalah tempat konfigurasi deployment |
| `.gitlab-ci.yml` | Diganti GitHub Actions (`.github/workflows/`) | Assessment mewajibkan repository GitHub public; stage pipeline sama dengan standar, satu `ci.yml` bertahap |
| Root monorepo backend | Tambahan `services/`, `tests/`, `tools/`, `.kiro/`, `specs/`, `docker-compose.yaml`, `Makefile`, `go.work` | Standar mendefinisikan satu service; RBT memuat beberapa proyek dan artefak assessment |
| Kontrak | `docs/contracts/{openapi,events}` | Dokumentasi berada di `docs/` |

**Consequences.**
- Positif: konsisten dengan proyek organisasi lain; reviewer dan tim maintenance langsung familiar; port inbound/outbound eksplisit memudahkan mock dan contract test; `internal/` mencegah import lintas service tanpa bergantung linter.
- Negatif: lebih banyak folder per service; `go.work` perlu dikelola; CI berbeda dari standar sampai repository dipindah ke GitLab.

**Confirmation.** Review struktur di setiap PR (CODEOWNERS); `depguard` untuk aturan lapisan; `make mock` menghasilkan `_mock/` tanpa diff; checklist `.kiro/steering/structure.md`.

---

## ADR-017: Tiga repository terpisah (backend, web, mobile) dalam satu organisasi GitHub

| Field | Value |
|-------|-------|
| **Status** | Accepted (mengubah ruang lingkup root monorepo di ADR-016) |
| **Date** | 2026-10-17 |
| **Concerns** | CN-02, CN-05 |
| **Related requirements** | CON-06, SRS-C-08 |
| **Affected components** | CMP-QUEUE, CMP-BOOKING, CMP-PAYMENT, CMP-NOTIF, CMP-PLATFORM, CMP-WEB, CMP-MOBILE |

**Context.** PRD v4.6 Lampiran F dan ADR-016 menaruh 4 service, web, dan mobile dalam satu repository. Penilaian assessment dilakukan per orang, dan tim ingin pipeline CI/CD serta kepemilikan setiap domain terlihat terpisah. Toolchain backend (Go) dan frontend (Node, Expo) juga berbeda.

**Considered options.** (A) Satu monorepo untuk backend, web, dan mobile; (B) tiga repository dalam satu organisasi: backend (4 service), web, mobile; (C) satu repository per service ditambah web dan mobile.

**Decision.** Pilih **B**: organisasi `tiket-sampai-nonton` dengan `rusuh-booking-be`, `rusuh-booking-web`, dan `rusuh-booking-mobile`.

| Aspek | Keputusan |
|---|---|
| Dokumen sistem | Hanya di `rusuh-booking-be/docs/`; web dan mobile menautkannya |
| Kontrak API dan event | Sumber kebenaran `rusuh-booking-be/docs/contracts/`; web dan mobile mengunci ref di `docs/contracts/REF`, menyalin kontrak, dan membangkitkan tipe |
| Pipeline | Satu pipeline per repository (Engineering Handbook §2) |
| Rilis mobile | APK dari GitHub Actions ke GitHub Releases saat tag `v*` |
| Test lintas service | `rusuh-booking-be/tests/`; E2E web dan mobile di repository masing-masing |

**Consequences.**
- Positif: pipeline, CODEOWNERS, dan riwayat commit per domain jelas; toolchain tidak bercampur; repository web dan mobile tetap bisa dikembangkan dengan mock kontrak sebelum backend siap.
- Negatif: perubahan kontrak perlu koordinasi lintas repository (ref dikunci dan diperbarui lewat PR); tiga set ruleset dan CODEOWNERS dikelola; E2E penuh hanya di lingkungan yang menjalankan ketiganya.

**Confirmation.** PR yang mengubah `docs/contracts/` ditinjau BE-1, BE-2, dan satu FE (CODEOWNERS); web dan mobile memperbarui `docs/contracts/REF` lewat PR.

---

## ADR-018: Konsistensi lintas service dengan saga koreografi dan kompensasi

| Field | Value |
|-------|-------|
| **Status** | Accepted (memperjelas ADR-009 dan ADR-010; desain tidak berubah) |
| **Date** | 2026-10-18 |
| **Concerns** | CN-03, CN-04 |
| **Related requirements** | NFR-10, SRS-Q-REL-04, BR-06 |
| **Affected components** | CMP-QUEUE, CMP-BOOKING, CMP-PAYMENT, CMP-NOTIF |

**Context.** Stok dan uang hanya diubah Booking Service dalam satu transaksi PostgreSQL, sehingga stok tidak memerlukan transaksi terdistribusi. Namun satu pemesanan tetap melewati beberapa service (Queue → Booking → Payment → Booking → Notification), dan kegagalan di tengah alur harus dipulihkan. Frasa "tanpa 2PC/saga" di PRD v4.6 dapat dibaca seolah alur ini tidak memiliki kompensasi.

**Considered options.** (A) Transaksi terdistribusi (2PC); (B) saga orkestrasi dengan orchestrator atau workflow engine; (C) saga koreografi: setiap langkah adalah transaksi lokal, service bereaksi lewat event (outbox) dan panggilan internal, kegagalan dikompensasi secara lokal.

**Decision.** Pilih **C**. Tanpa 2PC dan tanpa orchestrator.

| Langkah | Service | Kompensasi |
|---|---|---|
| Giliran antrean diklaim, alokasi dibuat | Queue → Booking | `POST /internal/v1/allocations/{id}:release` (`QUEUE_ENTRY_GONE`); rekonsiliasi alokasi yatim |
| Booking dibuat, stok dan kuota di-hold | Booking | TTL habis: `EXPIRED`, hold dan kuota dilepas, NIK bebas dipakai lagi (setelah resolve pembayaran) |
| Pembayaran dimulai (`payment-lock`, transaksi gateway) | Payment | `payment.failed` hanya informasi; hold tetap sampai TTL |
| Settlement diterima | Payment → Booking | Tepat waktu: `PAID`. Terlambat: `REVIEW`, hold dan kuota dilepas, `booking.review` → Payment membuat `refund_cases` |
| Notifikasi | Notification | Tidak ada kompensasi: efek samping dengan retry dan DLQ |

**Consequences.**
- Positif: invariant stok tetap dijaga satu transaksi ACID; tidak ada orchestrator yang menjadi titik gagal tunggal; setiap langkah dapat diuji terpisah.
- Negatif: alur tersebar di beberapa consumer sehingga butuh correlation ID dan tracing; konsistensi antar service eventual; rekonsiliasi terjadwal wajib berjalan.

**Confirmation.** TC-PAY-03, TC-PAY-04, TC-PAY-09, TC-CAP-03; outbox lag kembali 0 setelah broker pulih; rekonsiliasi tidak menemukan selisih.

---

## ADR-019: Event sourcing untuk agregat Booking dan Payment; stok tetap relasional

| Field | Value |
|-------|-------|
| **Status** | Accepted (keputusan tim bersama BE-2, D-04); **diamandemen 2026-10-23** untuk Payment |
| **Date** | 2026-10-19 |
| **Concerns** | CN-03, CN-04, CN-09 |
| **Related requirements** | SRS-C-09, SRS-D-10, SRS-D-11, SRS-Q-REL-09; rubrik kompetensi Level 3 "aplikasi event sourcing" |
| **Affected components** | CMP-BOOKING, CMP-PAYMENT, CMP-PLATFORM (`pkg/eventstore`) |

**Context.** PRD v4.6 menyimpan booking dan attempt pembayaran sebagai state di tabel (`bookings.status`, `payment_attempts.status`); event RabbitMQ hanya pesan integrasi. Rubrik kompetensi Level 3 (wajib) meminta aplikasi dengan arsitektur event sourcing, dan BE-2 mengusulkan event sourcing untuk Booking (V2 ADR-0003). Invariant "tidak oversell" tidak boleh bergantung pada replay, dan waktu pengembangan hanya 20 Oktober sampai 8 November.

**Decision drivers.** Riwayat yang bernilai bisnis dan audit (siklus tiket dan uang); invariant stok tetap ACID; biaya implementasi terbatas; pertanyaan "kenapa hanya itu?" harus punya jawaban berprinsip.

**Considered options.** (A) Tanpa event sourcing; (B) Booking saja; (C) **Booking dan Payment**; (D) semua service, termasuk stok, Queue, dan Notification.

**Decision.** Pilih **C** dengan kriteria tertulis: *event sourcing dipakai pada agregat yang riwayatnya bernilai bisnis dan audit. Tidak dipakai pada stok (butuh konsistensi kuat), Queue (data sementara di Valkey), dan Notification (tidak ada invarian bisnis).*

| Aspek | Keputusan |
|---|---|
| Sumber kebenaran | `booking.booking_events` (stream = `bookings.id`) dan `payment.payment_events` (stream = `payment_attempts.id`), append-only, kunci `(stream_id, version)` |
| Read model | `bookings` dan `payment_attempts` diproyeksikan **sinkron** di transaksi yang sama dengan append; `bookings.version` dan `payment_attempts.stream_version` = versi event terakhir |
| Stok | `category_inventories` dan `event_supply` tetap relasional, diubah di transaksi yang sama dengan append event dan outbox |
| Concurrency | Optimistic: append dengan `expectedVersion` yang sudah terlewati ditolak primary key (23505) → `ErrConcurrency` → baca ulang lalu ulangi atau kembalikan `BOOKING_VERSION_CONFLICT` |
| Log append-only | Trigger menolak `UPDATE`, `DELETE`, `TRUNCATE` pada tabel `*_events` |
| PII | Payload event tidak memuat NIK, email, atau token plaintext; `pkg/eventstore` menolak field terlarang. Yang boleh: hash dan nilai tersamar |
| Integrasi | Outbox tetap ada; event integrasi (`booking.paid.v1` dan lainnya) ditulis bersama event domain |
| Replay | Dipakai untuk audit, test, dan membangun ulang read model; jalur produksi membaca read model |
| Versi event | `schema_version` per event; perubahan bentuk payload = versi baru, handler lama tetap ada sampai migrasi data |

**Consequences.**
- Positif: riwayat lengkap setiap booking dan attempt tanpa tabel audit terpisah; konflik ubah pesanan terdeteksi di database; memenuhi butir rubrik dengan cakupan yang bisa dibela; stok tetap dijaga satu transaksi ACID.
- Negatif: dua tabel event dan proyeksi yang harus diuji; skema event perlu versioning; ukuran tabel event bertambah (diarsipkan setelah event selesai, sejalan dengan `PII_RETENTION_DAYS_AFTER_EVENT`).

**Confirmation.** `pkg/eventstore` integration test (20 penulis paralel, tepat satu berhasil); trigger append-only diuji di DDL; TC-ES-01..TC-ES-05 (Test Plan Annex A); replay booking menghasilkan state yang sama dengan read model.

**Amandemen 2026-10-23 (Payment → tabel state).** Diputuskan BE-1 dari audit `docs/evidence/2026-10-23-audit-backend.md` §11.
- **Keputusan:** Payment memakai opsi A (tanpa event sourcing). `payment_attempts` menjadi sumber kebenaran. Setiap transisi berupa `UPDATE … WHERE status = ANY(<status asal sah>)` dalam satu transaksi dengan outbox. `payment_events` tetap ada (migrasi tidak diubah) tetapi tidak dipakai. Booking tetap memakai event sourcing.
- **Alasan:**
  - Kode Payment belum ada, dan Midtrans menjadi wajib sehingga effort integrasi bertambah.
  - Event sourcing di Payment menambah 8 tipe event, proyeksi, dan retry `ErrConcurrency` tanpa kebutuhan baru dari PDF.
  - Jejak audit pembayaran sudah tersedia di `payment_callbacks` (append, `UNIQUE (provider, fingerprint)`) dan log Midtrans.
  - Rubrik Level 3 "aplikasi event sourcing" sudah dipenuhi Booking.
- **Konsekuensi:**
  - Riwayat transisi attempt tidak dapat di-replay; cukup dari `payment_callbacks` + `updated_at`.
  - Kriteria "agregat bernilai audit" di atas tidak lagi berlaku untuk Payment.
  - Perlu diinformasikan ke BE-2 karena keputusan awal dibuat bersama.

---

## ADR-020: User Service untuk akun opsional dengan login OTP email; guest checkout tetap utama

| Field | Value |
|-------|-------|
| **Status** | Accepted (keputusan tim bersama BE-2, D-03 dan D-21; mengubah sebagian ADR-001, ADR-004, ADR-008) |
| **Date** | 2026-10-19 |
| **Concerns** | CN-02, CN-06 |
| **Related requirements** | SRS-F-27-01 … SRS-F-27-12, SRS-F-01-08 (direvisi), NFR-07 |
| **Affected components** | CMP-USER (baru), CMP-QUEUE, CMP-BOOKING, CMP-WEB, CMP-MOBILE |

**Context.** ADR-004 menetapkan guest checkout tanpa OTP. BE-2 mengusulkan User Service (register, login, NIK tersimpan). Tim sepakat mendukung dua jalur: pembeli bisa tetap guest, atau login agar NIK, email, dan nama tidak perlu diketik ulang dan riwayat pesanan tersedia. Login tidak boleh menjadi syarat membeli, dan waktu war ticket tidak boleh bertambah karena login.

**Considered options.** (A) Tetap guest saja; (B) akun wajib dengan password; (C) **akun opsional, login OTP email tanpa password**; (D) akun opsional dengan password + OTP.

**Decision.** Pilih **C** sebagai service kelima (`services/user`, schema `users`, role `svc_user`).

| Aspek | Keputusan |
|---|---|
| Login | `POST /auth/otp/request` mengirim kode 6 digit; `POST /auth/otp/verify` menukar kode dengan token. Akun dibuat otomatis saat login pertama. Tanpa password, tanpa reset password |
| Batas OTP | TTL 5 menit, maks 5 percobaan per kode, cooldown kirim ulang 60 detik, maks 5 permintaan per email dan 20 per IP per jam; respons sama untuk email terdaftar atau belum (anti enumerasi) |
| Penyimpanan OTP | Valkey dengan TTL: hash kode (HMAC terikat email) dan hitungan percobaan. Hilang saat Valkey restart hanya berarti minta kode baru (ADR-008 diperbarui) |
| Token | Access token JWT EdDSA 15 menit (`aud=rbt`, `scope=buyer`); refresh token acak 32 byte, disimpan sebagai hash di `users.sessions`, berlaku 30 hari, **dirotasi** setiap refresh; token lama yang dipakai ulang mencabut semua sesi akun |
| PII | Email dan NIK disimpan AES-256-GCM + hash HMAC; satu NIK hanya boleh tertaut ke satu akun (`uq_accounts_nik`) agar kuota NIK pembeli tidak diakali lewat banyak akun |
| Pengiriman OTP | Lewat RabbitMQ ke Notification Service (event `otp.requested`): kode OTP dienkripsi AES-256-GCM dalam payload; Notification Service mendekripsi dan mengirim email; konsisten dengan prinsip Notification Service memiliki semua pengiriman email |
| Integrasi | Klien mengisi form join dari `GET /me`; join tetap mengirim NIK dan email seperti guest. Bila `Authorization` dikirim, Queue memverifikasi token dengan public key User Service, mencocokkan `nik_hash`, dan meneruskan `accountId` ke alokasi → `admissions.account_id` → `bookings.account_id`; `GET /api/v1/me/bookings` di Booking menampilkan riwayat |
| Guest | Tidak berubah: NIK + email diketik dua kali, tanpa OTP (SRS-F-01-08 direvisi menjadi "guest checkout tidak memerlukan OTP") |

**Consequences.**
- Positif: kompetensi autentikasi dan pengelolaan sesi terlihat; pembeli berulang lebih cepat; tidak ada password yang harus dilindungi.
- Negatif: service, schema, dan kontrak tambahan (`docs/contracts/openapi/user.yaml`); SMTP menjadi dependency jalur login; permukaan serangan bertambah (OTP brute force, credential stuffing) yang dimitigasi rate limit, lock percobaan, dan audit `users.login_audit`.

**Confirmation.** Unit dan integration test `services/user` (coverage logika 94%); uji end-to-end OTP → token → profil → rotasi → reuse detection (`docs/evidence/2026-10-19-boilerplate-v1.5.md`); TC-USR-01..TC-USR-12.

---

## ADR-021: Usulan BE-2 yang dicatat sebagai alternatif (gRPC internal, EKS + Helm, API Gateway/Kong)

| Field | Value |
|-------|-------|
| **Status** | Accepted (keputusan tim bersama BE-2, D-05, D-07, D-08: usulan dicatat sebagai trade-off, tidak diadopsi untuk PoC) |
| **Date** | 2026-10-19 |
| **Concerns** | CN-02, CN-05, CN-09 |
| **Related requirements** | NFR-01, NFR-03, rubrik Level 4 "mempresentasikan berbagai alternatif desain" |
| **Affected components** | CMP-EDGE, semua service |

**Context.** BE-2 (dokumen V2) mengusulkan gRPC untuk komunikasi internal, EKS + Helm + ingress Kong untuk deployment, dan API Gateway sebagai service sendiri. ADR-011 dan ADR-012 sudah memilih REST internal, CloudFront + WAF + ALB, dan EC2 ASG. Tim sepakat menyimpan usulan ini sebagai alternatif dengan alasan dan kondisi kapan layak dipakai.

**Decision.** Tetap ADR-011 dan ADR-012 untuk PoC. Perbandingannya:

| Topik | Pilihan PoC | Usulan BE-2 | Kenapa PoC tidak memakainya sekarang | Kapan layak diadopsi |
|---|---|---|---|---|
| Komunikasi internal | REST/JSON + OpenAPI + JWT EdDSA | gRPC + `.proto` + service token; event HMAC | Hanya 3 jalur sinkron; tooling sama dengan API publik; status gRPC di tech radar belum ADOPT | Jalur sinkron bertambah, butuh streaming internal, atau latensi internal menjadi bottleneck terukur |
| Deployment | EC2 ASG Graviton + Terraform | EKS + Helm, k3d/Tilt lokal | Biaya control plane EKS dari kredit US$200; satu AZ PoC; tim kecil | Lebih dari ±10 deployment, butuh autoscaling per pod, atau tim ops tersedia |
| Ingress / gateway | CloudFront + WAF + ALB rule per path; validasi token di middleware `pkg/httpx` | API Gateway service sendiri / ingress Kong | Komponen tambahan di jalur kritis; fungsi utama (TLS, rate limit, bot) sudah oleh komponen managed | Butuh plugin gateway (quota per klien API, monetisasi) atau banyak klien pihak ketiga |

Pola yang diadopsi dari usulan BE-2: **satu image banyak verb** (`api`, `worker`, `migrate`) dengan satu verb per deployment, dipakai di ASG dan Compose (ADR-022).

**Consequences.** Positif: keputusan dapat dipertahankan saat technical defense dengan alternatif yang jelas. Negatif: bila kebutuhan berubah, migrasi ke gRPC/EKS butuh ADR baru dan perubahan pipeline.

**Confirmation.** Review ulang saat kickoff produksi; biaya EKS dihitung terhadap model biaya HLD §4.7.

---

## ADR-022: Runtime dan tooling boilerplate: Go 1.26, verb Cobra, client standar, migrasi tertanam, `/health`

| Field | Value |
|-------|-------|
| **Status** | Accepted (keputusan tim bersama BE-2, D-13 … D-16, D-22 … D-25, D-27; memperbarui ADR-015 dan tabel deviasi ADR-016) |
| **Date** | 2026-10-19 |
| **Concerns** | CN-09 |
| **Related requirements** | SRS-C-02, SRS-C-08, SRS-Q-MNT-01 |
| **Affected components** | Semua service, CMP-PLATFORM |

**Context.** Boilerplate kita (skeleton 17 Okt) dan boilerplate BE-2 berbeda di entry point, client infrastruktur, migrasi, nama env, health endpoint, versi Go, dan gaya test. Perbedaan ini paling cepat menimbulkan bentrok karena development dimulai 20 Oktober.

**Decision.**

| Topik | Keputusan | Asal |
|---|---|---|
| Versi Go | **1.26** (`go 1.26` di semua `go.mod`, image `golang:1.26-alpine`) | BE-2 mengusulkan 1.25; dinaikkan ke 1.26 karena sudah tersedia (D-25) |
| Entry point | `cmd/main.go` tetap (standar) + Cobra: `api`, `worker`, `migrate up|down|status`, `healthcheck` | BE-2 (D-14), lokasi file mengikuti standar |
| Client | `pkg/postgres` (pgx pool terbatas, `search_path` per service), `pkg/valkey` (valkey-go, standalone/cluster), `pkg/rabbitmq` (amqp091, publisher confirm, consumer manual ack, retry berjenjang, DLQ); setiap verb hanya membuka client yang dipakai; ping saat start | BE-2 (D-15) + DLQ (D-12) |
| Migrasi | Tertanam di binary (`deployments/migrations/*.up.sql` + `.down.sql`, `go:embed`), satu transaksi per langkah, `pg_advisory_lock` per schema, tabel `<schema>.schema_migrations` dengan checksum (migrasi lama yang diubah ditolak) | BE-2 + tambahan kita (D-16) |
| Env | `APP_*`, `LOG_*`, `POSTGRES_HOST/PORT/USER/PASSWORD/DATABASE/SSLMODE/SCHEMA/MAX_CONNS`, `VALKEY_SERVER` (JSON), `RABBITMQ_*`; semua divalidasi saat start dan dilaporkan sekaligus | BE-2 (D-24) + `POSTGRES_SCHEMA` |
| Health | Satu endpoint `GET /health`: 200 `UP` bila semua dependency ping, 503 `DOWN` bila tidak | Kompromi (D-23) |
| Test | testify (`assert`/`require`), table-driven, mock `go.uber.org/mock` lewat `go tool mockgen`; integration test dengan build tag `integration` | BE-2 (D-27) |
| Struktur | Standar organisasi + aturan STEERING BE-2 yang tidak bertentangan: route registrar per domain, error kontrak di `port/inbound`, `ReadHeaderTimeout`, `cmd.Context()` | Kompromi (D-13) |
| Resilience | `pkg/resilience`: circuit breaker + bulkhead untuk setiap panggilan sinkron (PAY-06 menjadi MUST, D-40) | Rubrik Level 3 |

**Consequences.** Positif: satu konvensi untuk kedua BE; boilerplate sudah terbukti jalan (migrasi lima schema, `/health`, alur OTP end-to-end). Negatif: tiga dependency pihak ketiga baru (Cobra, testify, mock) yang perlu dicatat di tech radar; `go.sum` wajib di-commit (`make tidy`).

**Confirmation.** CI stage `code-quality` memeriksa `go.mod`/`go.sum` rapi dan `_mock/` sesuai port; `case-coverage` menjalankan integration test; evidence `docs/evidence/2026-10-19-boilerplate-v1.5.md`.

---

## ADR-023: Observability — log JSON ke stdout, metrik Prometheus, Grafana + Loki, korelasi `trace_id`

| Field | Value |
|-------|-------|
| **Status** | Accepted (keputusan tim, 21 Oktober 2026; menutup OQ-5) |
| **Date** | 2026-10-21 |
| **Concerns** | CN-09 |
| **Related requirements** | SRS-Q-MNT-02, SRS-Q-MNT-03, SRS-Q-SEC-03, SRS-F-90-06 |
| **Affected components** | Semua service, CMP-PLATFORM |

**Context.** Metrik real-time dan log terstruktur diwajibkan (SRS-Q-MNT-02), tetapi library metrik belum dipilih dan service belum punya access log. Tim ingin log dan metrik langsung terlihat di lokal dan formatnya terbaca layanan cloud (CloudWatch, X-Ray, Google Cloud Logging).

**Considered options.** Prometheus client + Loki vs OpenTelemetry penuh (metrik, log, trace lewat collector) vs hanya log. Pengumpul log: Promtail vs Grafana Alloy.

**Decision.**
- Log JSON (`slog`) ke **stdout** dengan field baku `time, level, msg, service, verb, env, requestId, trace_id`; access log satu baris per request. `LOG_FORMAT=gcp` untuk Cloud Logging. Aplikasi tidak bergantung pada vendor; agen pengumpul yang berbeda per lingkungan.
- Metrik dengan **Prometheus client** (`client_golang`) di `GET /metrics`; Grafana sebagai dashboard.
- Lokal: Prometheus, Loki, **Grafana Alloy**, Grafana (`make obs`). Alloy dipilih menggantikan Promtail yang end-of-life sejak Maret 2026; fungsinya sama.
- Korelasi lintas service lewat `requestId` dan `trace_id` (`X-Amzn-Trace-Id` / `traceparent`). Tracing penuh (OpenTelemetry/X-Ray SDK) ditunda.
- Label Loki hanya `container`, `source`, `level`; ID bisnis dan NIK/email tidak pernah menjadi label maupun isi log (redaksi, platform R4.4).

**Consequences.** Positif: sederhana dijelaskan, satu format log untuk semua cloud, dapat dijalankan penuh di laptop. Negatif: tanpa trace span (hanya korelasi ID); satu dependency baru (`client_golang`) perlu dicatat di tech radar.

**Confirmation.** Platform task 4.2, 4.5, 4.6; stack lokal terverifikasi 2026-10-21 (`docs/evidence/2026-10-21-keputusan-spec-dan-docs.md`).

---

## Appendix A — Architecture Trade-Off Matrix

*Sumber: PRD v4.6 Lampiran L*

### Gaya Arsitektur

| Kriteria | 4 service + schema per service (dipilih) | Modular monolith (v4.1) | Microservices granular (stok, kuota, tiket terpisah) |
|---|---|---|---|
| Kesesuaian dengan aktor requirement | Tinggi | Sedang (logical service) | Tinggi tetapi berlebihan |
| Risiko oversell | Rendah: stok di satu service | Rendah | Tinggi: perlu saga/kompensasi |
| Skala waiting room | Queue di-scale terpisah | Perlu mode khusus | Baik |
| Kolaborasi 2 BE | Jelas: BE-1 Queue+Notification, BE-2 Booking+Payment | Jelas per modul | Terlalu banyak kontrak |
| Operasional | Menengah (4 image, 1 cluster data) | Rendah | Tinggi |
| Evolusi | Schema → instance terpisah tanpa ubah kode | Butuh pemisahan data dulu | Sudah terpisah |

### Keputusan Desain Kunci

| Keputusan | Alternatif | Alasan dipilih |
|---|---|---|
| Reservasi saat admission | Hold saat masuk antrean | Stok puluhan ribu tidak dapat di-hold untuk 1 juta pengantre; jaminan diberikan di titik yang mungkin dijamin |
| Reservasi tingkat event (bukan kategori) | Reservasi per kategori | Customer belum memilih kategori saat admission; tingkat event menjamin ada tiket tanpa mengunci kategori favorit |
| Reservasi `min(3, sisa kuota, stok bebas)` unit | 1 unit | Menjamin order penuh hingga 3 tiket; sisa stok 1–2 tetap terjual; unit sisa kembali saat pesanan terkunci |
| Kuota pembeli dan pemegang terpisah | Satu counter gabungan | Sesuai dua aturan sumber (BR-01 dan arahan CTO); perlindungan calo datang dari aturan pemegang; lebih mudah dijelaskan ke customer |
| Cek NIK pemegang langsung tanpa kunci | Mengunci NIK selama sesi | Sederhana; bentrok bersamaan sangat jarang dan tidak menghilangkan reservasi; mengunci NIK membuka celah memblokir NIK orang lain |
| Status antrean dari CDN, near-real-time | Polling ke origin; WebSocket/SSE | Kebutuhan sebenarnya "jujur dan adil", bukan latensi sub-detik; urutan dari seq server sehingga latensi tidak memengaruhi keadilan; beban origin konstan; tanpa 1 juta koneksi persisten; sederhana untuk mobile |
| Interval polling dikirim server | Hardcode di aplikasi | Dapat dituning saat penjualan tanpa rilis aplikasi mobile |
| Ubah pesanan hanya sebelum "Bayar" | Bebas ubah selama TTL Booking (batalkan transaksi gateway); tidak bisa ubah | Tanpa pembatalan transaksi di gateway dan tanpa risiko transfer ke VA lama; tetap memberi ruang koreksi |
| Sisa unit dipegang sampai pesanan terkunci | Dilepas saat hold | Menjaga jaminan N saat customer kembali menambah tiket; ongkos throughput kecil (≤ TTL Booking) |
| Penggantian atomik saat ubah | Lepas hold lalu pilih ulang | Kategori yang sedang dipegang tidak bisa diambil orang lain di tengah perubahan |
| Push + peringatan sebelum giliran + jendela 3 menit | Jendela panjang (10 menit); kesempatan kedua dengan prioritas | Mengurangi giliran hangus tanpa menahan stok lama; urutan antrean tidak pernah diubah |
| Snapshot konfigurasi per sesi + aturan jaminan dikunci | Konfigurasi global dibaca setiap request | Perubahan tidak pernah memendekkan waktu orang yang sedang berjalan atau mengubah aturan di tengah penjualan |
| Zona peluang | Hanya posisi | Menjawab "jangan PHP" di antrean secara terukur |
| Guest + email tanpa OTP | OTP email | OTP untuk 1 juta pengantre mahal dan menambah friksi; tiket juga tampil di layar |
| Validasi struktur NIK | Dukcapil | Grooming: validasi internal; Dukcapil opsi lanjutan |
| Resolve sebelum release | Release langsung saat TTL | Mencegah pembayaran sah dibatalkan karena webhook telat |
| Refund manual | Refund otomatis | Dukungan refund API berbeda per metode (VA); risiko refund ganda; kasus jarang |
| REST internal | gRPC | Swagger ADOPT; dua titik sinkron saja |
| Valkey hanya untuk antrean, rate limit, cache | Valkey juga untuk stok dan sesi | Replikasi in-memory bersifat async sehingga failover dapat menghilangkan hold yang sudah dijanjikan; PostgreSQL sudah cukup karena tulisan stok dibatasi slot aktif (< 250/detik di Tier L) |
| `valkey-go` | Client berbasis core native (CGO) | Pure Go: build arm64 dan image Alpine sederhana, `go run` di Compose tanpa toolchain tambahan |
| CP untuk stok/uang, AP untuk tampilan dan notifikasi (§9.7 [v4.6 → HLD §4.3]) | Satu sikap untuk seluruh sistem | Sikap dipilih menurut akibat bila data salah; konsistensi kuat hanya di tempat yang membutuhkannya |
| Outbox hanya di Booking dan Payment | Outbox di semua producer; publish langsung di semua producer | Dual write hanya berbahaya untuk event uang/tiket; push antrean memang best effort |
| Consumer idempoten lewat data bisnis | Tabel `consumer_inbox` per service | Idempotensi sudah dijamin guard state, Lua, dan unique key; 4 tabel dan satu library lebih sedikit |
| RabbitMQ + JSON | Protobuf | Mudah diinspeksi di UI dan DLQ; JSON Schema cukup untuk evolusi |

### Evaluasi Teknologi

| Lapisan | Pilihan | Alternatif | Trade-off |
|---|---|---|---|
| Bahasa BE | Go | Java/Kotlin | ADOPT; concurrency dan deployment ringkas |
| Web | Next.js + TS + Tailwind | React SPA | ADOPT; payment page same-origin |
| Mobile | React Native TS | Kotlin/Compose, SwiftUI | ADOPT; satu codebase untuk 1 developer |
| DB | PostgreSQL | MySQL | Row lock, partial unique index, constraint |
| In-memory store | Valkey 8.1 + `valkey-go` | Redis OSS, PostgreSQL | Lua atomik, sorted set, TTL; lisensi BSD; lebih murah di ElastiCache; bukan source of truth stok |
| Broker | RabbitMQ | Amazon SQS | Retry/DLQ dan dapat dijalankan lokal |
| Edge | CloudFront + WAF + ALB | Kong, API Gateway | Managed, cache Status Document, challenge |
| Observability | OTel → Prometheus/Grafana | CloudWatch penuh | Biaya rendah, dapat dijalankan assessor |

---

---

## ADR-024: Midtrans Snap menggantikan Core API charge

| Field | Isi |
|---|---|
| **Status** | Accepted (2026-10-31; menggantikan keputusan Core API 26 Oktober 2026) |
| **Konteks** | Persyaratan dan kontrak FE mobile memakai alur Snap: backend mengembalikan `redirectUrl`, FE membukanya dan memverifikasi status ke backend. Core API (`/v2/charge`) memaksa backend dan FE menangani instruksi VA/QR/deeplink per metode, dua jalur integrasi yang berlebihan. |
| **Keputusan** | Hanya Snap (`POST /snap/v1/transactions`). Bucket metode (`VA_BCA`, `QRIS`, …) tetap di API sebagai pembatas `enabled_payments`; `SNAP` = semua channel. Status, cancel, dan webhook tetap memakai format Core API. Kode charge Core API dihapus. |
| **Konsekuensi** | (1) Order Midtrans baru ada setelah pembayar memilih channel; status 404 sebelum itu = belum dibayar. (2) Token Snap tidak bisa dibatalkan; pembayaran lewat token lama setelah attempt diganti masuk jalur `SETTLED_AFTER_CANCEL` (refund manual). (3) Expiry Snap dalam menit, dibulatkan ke bawah; sisa waktu < 1 menit tidak dapat memulai attempt. (4) Instruksi VA/QR/deeplink hilang dari kontrak; `Attempt.instruction` dihapus. |
| **Terverifikasi di Sandbox (2026-10-31)** | Token Snap dibuat dengan `expiry.unit=minute`; semua bucket metode diterima sebagai `enabled_payments`; memilih channel lewat `POST /snap/v1/transactions/{token}/pay` membuat order Core API berstatus `PENDING`; sebelum itu `GET /v2/{id}/status` menjawab 404. Tes: `go test -tags sandbox -run TestSandbox`. |
| **Belum terverifikasi** | `X-Override-Notification` pada Snap; pembayaran selesai (VA/GoPay butuh halaman simulator manual). |

---

## ADR-025: Token dibatasi per scope; admin lewat allow-list; tiket hanya untuk pemilik

| Field | Isi |
|---|---|
| **Status** | Accepted (2026-10-31) |
| **Konteks** | Kunci Queue menandatangani token visitor, queue, admission, dan service. Token visitor diberikan ke siapa pun yang masuk antrean, jadi tidak boleh dianggap kredensial internal. Booking sebelumnya menerima tiap token dari kunci itu di endpoint internal/admin, memercayai nilai mentah header visitor sebagai identitas, dan membuka tiket hanya dengan `bookingId`. |
| **Keputusan** | (1) Setiap guard menyebut satu scope: `service` untuk `/internal/v1/*` (kunci Queue), `admission` untuk membuka sesi, `visitor` untuk identitas pembeli (JWT diverifikasi di middleware; `X-Visitor-ID` dihapus), `buyer` untuk token User. (2) Admin = token `buyer` dengan email di `ADMIN_EMAILS` (entri `@domain` untuk domain yang dikuasai, mis. E2E lokal); kosong = tidak ada yang boleh. (3) Guard tanpa verifier menolak semua permintaan (fail closed). (4) Tiket dibuka oleh visitor pemilik, akun pembeli pemilik, atau tautan email bertoken HMAC-SHA256(bookingId) dengan `TICKET_LINK_KEY`; selain itu 404. (5) Kode tiket = QR payload = 80 bit acak (`TK-` + 20 hex). (6) Kunci PII dan `TICKET_LINK_KEY` wajib diisi di luar `APP_ENV=local`. |
| **Konsekuensi** | Operator wajib mengisi `ADMIN_EMAILS`, `TICKET_LINK_KEY`, `PII_*`. Klien yang memakai `X-Visitor-ID` atau mengirim raw UUID sebagai visitor token berhenti bekerja (harus memakai token dari Queue). Email e-tiket memuat URL bila `TICKET_LINK_BASE_URL` diisi. Check-in/validasi QR belum ada; bila ditambahkan, payload perlu ditandatangani. |

---

## ADR-026: Email pembeli terenkripsi sejak Queue; hanya Notification yang membuka (PDP-1)

| Field | Isi |
|---|---|
| **Status** | Accepted (2026-10-31) |
| **Konteks** | UU PDP Pasal 39 mewajibkan menjaga kerahasiaan data pribadi dan mencegah akses tidak sah. Email pembeli sebelumnya plaintext di `booking.admissions`, `booking.bookings`, payload event `booking.paid/review/expired`, dan `booking.outbox_events` (tabel yang bisa dibaca banyak peran), serta dikirim plaintext dari Queue ke Booking. |
| **Keputusan** | Queue mengenkripsi email sekali (AES-256-GCM, kunci `PII_ENC_KEY`, AAD `booking.buyer_email`, base64) dan mengirim `buyerEmailEnc`. Booking menyimpan ciphertext di `buyer_email_enc` dan meneruskannya apa adanya ke event; Booking tidak pernah membuka (data minimization). Notification adalah satu-satunya yang mendekripsi, tepat saat mengirim email; hash penerima (HMAC) dihitung dari plaintext hasil dekripsi. Ciphertext yang tidak terbuka = pesan racun → DLQ. Kolom plaintext tidak lagi ditulis dan dihapus oleh migrasi Booking 0005 (PDP-1b); migrasi itu menolak berjalan bila masih ada baris plaintext. |
| **Konsekuensi** | `PII_ENC_KEY` harus **sama** di Queue, Booking, Notification, dan User; rotasi kunci memerlukan re-enkripsi. Dekripsi untuk pengecekan manual hanya lewat Notification (atau perintah operasional yang memakai kunci yang sama). Payload event lama yang masih berisi `buyerEmail` tetap diterima Notification. |
