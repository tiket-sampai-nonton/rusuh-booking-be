# Analisis kapasitas: dari yang terukur menuju 300.000 pengantre

Tanggal: 2026-11-13. Dasar: uji k6 lokal hari ini di kode `main` (PR #31 sudah masuk), dokumen desain (HLD §4.7 dan §17, SRS-Q-PERF-01..06,
PRD GR-11), dan kode konfigurasi queue. Laporan mentah per jenis ada di `reports/local/<jenis>/`.

## 1. Jawaban singkat

**Apakah sistem sanggup 300.000 pengantre aktif bersamaan? Belum terbukti, dan dengan klien yang ada sekarang jawaban jujurnya
tidak untuk topologi PoC.** Yang ada adalah ukuran nyata pada skala kecil, model yang transparan, dan daftar yang belum dibuktikan.

| Pertanyaan | Jawaban | Dasar |
|---|---|---|
| Terukur: berapa pengantre serentak yang dilayani tanpa masalah di laptop? | **3.000 pengantre (profil klien sekarang) dengan p95 3 ms, p99 22 ms, dan 0% error**, rata-rata 1.792 request per detik (puncak 2.261), CPU queue-api rata-rata 40% satu core | `waiting` hari ini, bagian 3 |
| Terukur: titik jenuh? | **Rentang S5 sampai S6 di laptop**: kapasitas berkelanjutan S5 = 1.300 pengantre + 130 katalog/dtk + 13 beli/dtk (**1.524 RPS**, p95 3 ms, 0% error). S6 (1.800 pengantre, 1.657 RPS) melewati SLO p95 2 detik (2.088 ms), error tetap 0%. Titik ini tidak stabil antar-run (lihat bagian 3.1). Batas utama: Postgres dan kontensi, bukan CPU host | `stress` pass 2026-11-13, bagian 3.1 |
| Model: beban origin untuk 300.000 pengantre dengan **klien sekarang** | **±112.000 request per detik tak-bisa-di-cache** (96.000 `/queue-entries/me`, 10.000 heartbeat, 6.000 join puncak) | laju terukur per pengantre, bagian 4 |
| Model: dengan **klien sesuai desain HLD** (profil C) dan heartbeat 120 detik | **±10.600 request per detik** | bagian 4 |
| Terbukti di infra target (AWS)? | **Tidak.** Tidak ada uji beban di AWS; prod PoC adalah `t4g.small` | bagian 7 |

Temuan terpenting: **beban origin ditentukan oleh perilaku polling klien, bukan oleh jumlah pengantre.** HLD §17.2 menyatakan
hal itu, tetapi FE yang ada (lihat bagian 4.1) belum memakai desain yang membuat pernyataan itu benar, dan model HLD tidak
memuat beban heartbeat per pengantre.

## 2. Rujukan persyaratan

| Rujukan | Isi | Status bukti |
|---|---|---|
| PRD GR-11 (MUST) | Proposal menawarkan kapasitas 100 rb sampai 1 juta pengantre beserta justifikasi dan harga | Model dan Tier S/M/L ada di HLD §17; harga belum difinalkan (AWS Pricing Calculator) |
| SRS-Q-PERF-06 | Tier M penawaran utama; Tier L hanya diklaim setelah ekstrapolasi dari bukti PoC | Bagian ini menyediakan ekstrapolasi, dengan batasnya |
| SRS-Q-PERF-02 / TC-PERF-01 | Origin RPS untuk `queue-status` konstan pada 5.000 vs 50.000 pengantre | Hanya benar bila klien memakai cache edge; belum diukur di edge (bagian 7) |
| SRS-Q-PERF-03 / TC-PERF-02 | Laju join per instance Queue sampai jenuh | Diukur lokal dengan skenario `joinrate` (bagian 5) |
| NFR-06 / SRS-Q-PERF-01 | p95 di bawah 1 detik untuk halaman utama dan posisi antrean di bawah beban tinggi | Lokal: p95 di bawah 65 ms di semua profil sampai 2.217 VU. **Infra target: belum diukur (PERF-002)** |

## 3. Yang terukur hari ini (lokal)

Mesin: Apple M4, 10 core, 16 GiB; Docker VM 8 CPU dan 7,75 GiB. Stack: 5 service, PostgreSQL 16, Valkey, RabbitMQ, Mailpit dalam
satu mesin yang sama dengan load generator (k6 0.57). Override `tests/k6/env/local.compose.yaml`: batas per IP dilonggarkan
(join 100.000 per menit, OTP, booking, payment). Heartbeat di compose lokal 30 detik, stale 90, end 300.
**Angka ini bukan kapasitas sistem produksi**: semua komponen berbagi CPU dan disk laptop, tanpa jaringan, tanpa CDN.

| Jenis | VU maks | Request | RPS rata-rata | RPS puncak | p95 | p99 | Error | Status |
|---|---:|---:|---:|---:|---:|---:|---:|---|
| load | 420 | 67.907 | 193,7 | 952 | 2 ms | 4 ms | 0% | PASS |
| stress (run 2) | 1.942 | 413.955 | 984,4 | 2.821 | 11 ms | 85 ms | 0% | PASS |
| stress (run 1) | 2.217 | 401.763 | 954,4 | 3.930 | 64 ms | 546 ms | 0% | WARNING |
| spike | 1.128 | 103.467 | 470,0 | 1.602 | 46 ms | 164 ms | 0% | PASS |
| soak 5 menit | 270 | 65.708 | 182,5 | 747 | 2 ms | 5 ms | 0% | PASS |
| waiting, profil A (klien sekarang) | 3.000 | 305.918 | 1.792 | 2.261 | 3 ms | 22 ms | 0% | PASS |

### 3.1 Pass ulang 2026-11-13 (berurutan, kode 99fd2b2ae9, 08:47 sampai 09:20 WIB)

Semua profil lokal dijalankan ulang dalam satu sesi dengan kunci stack. Rincian per profil dan per level ada di
[reports/local/RINGKASAN.md](reports/local/RINGKASAN.md), bagian "Analisis pass 2026-11-13".

| Jenis | VU maks | RPS rata-rata | Puncak 1 dtk | p95 | Error | Status |
|---|---:|---:|---:|---:|---:|---|
| joinrate (sampai 1.500 join/dtk) | 200 | 648 | 1.513 | 2 ms | 0% | PASS, belum jenuh |
| load | 420 | 194 | 808 | 2 ms | 0% | PASS |
| spike | 1.120 | 471 | 1.498 | 2 ms | 0% | PASS |
| waiting A, 1.000 VU | 1.000 | 470 | 918 | 1 ms | 0% | PASS |
| waiting B, 1.000 VU | 1.000 | 251 | 521 | 7 ms | 0% | PASS |
| waiting C, 1.000 VU | 1.000 | 224 | 426 | 2 ms | 0% | PASS |
| waiting C, 3.000 VU | 3.000 | 587 | 1.083 | 2 ms | 0% | PASS |
| readonly, 1.000 VU | 1.000 | 181 | 522 | 1 ms | 0% | PASS |
| stress | 2.594 | 936 | 2.874 | 123 ms (S6: 2.088 ms) | 0% | WARNING (breaking S6) |

Catatan:
- Pada 1.000 pengantre, profil C mengirim sekitar 2,1 kali lebih sedikit request daripada profil A. Itu sesuai desain
  (zona jauh tidak memanggil `/me`), tetapi konfigurasi slot dan ramp run ini berbeda dari uji laju per pengantre di bagian
  4.2, sehingga angka 4.2 belum divalidasi ulang.
- Angka `waiting A, 3.000 VU` (1.792 RPS) berasal dari run 08:37. Folder `waiting` ditimpa run C 3.000 VU, jadi
  analysis.html untuk run A itu tidak lagi ada di folder; angkanya hanya di riwayat RINGKASAN.
- Titik stress tidak stabil antar-run (07:51 gagal di S5, 08:26 PASS penuh, 08:57 dan 2026-11-10 gagal di S6).
- Total CPU seluruh kontainer pada stress (core setara M4): S5 **1,9**, S6 **4,4** untuk RPS hanya naik 9%. Pada S6 lock
  wait Postgres muncul (maks 7) dan Postgres CPU rata-rata 147%. Pola ini mengarah ke kontensi dan kerja per request yang
  membengkak; penyebab baris stok belum dibuktikan (perlu `pg_stat_statements`).

**Stress, level S5 (1.300 pengantre).** Run 1: S5 gagal (p99 5,2 detik, maks 8,9 detik) karena **stall sekitar 10 detik** pada
detik ke-285 sampai 290; sampler sumber daya sendiri berhenti 17 detik, operasi Valkey jatuh lalu pulih, lock wait PostgreSQL 0,
CPU container tertinggi 54%. Run 2 dengan kode dan stack yang sama: S5 lulus (p95 38 ms, p99 138 ms) dan seluruh level PASS. Jadi
gangguan itu tidak terreproduksi dan polanya mengarah ke stall host atau Docker VM, bukan jenuh sistem. Namun S5 tampak paling
"panas" di **kedua** run; penyebabnya belum teridentifikasi, jadi bukan angka yang boleh diklaim sebagai kapasitas berkelanjutan
tanpa pengukuran di mesin terpisah.

**Catatan metodologi dari hari ini.**
- Disk laptop sempat penuh (2 GB bebas dari 228 GB); penulisan `metrics.json.gz` gagal dan container Docker crash. Seluruh run
  yang terpengaruh dibuang dan diulang setelah disk dibersihkan. Run tersebut tidak dipakai di dokumen ini.
- Mailpit lokal menampung maksimal 500 pesan. Setelah tes beban (6.107 email terkirim dalam 40 menit), smoke dan e2e gagal pada
  satu check ("email e-tiket terkirim"); setelah Mailpit dikosongkan keduanya PASS. Jalankan E2E sebelum tes beban atau kosongkan
  Mailpit (`DELETE /api/v1/messages`). Dua baris itu tetap ada di riwayat `reports/local/RINGKASAN.md`.

## 4. Model beban origin

### 4.1 Parameter dan sumbernya

| Parameter | Nilai | Sumber |
|---|---|---|
| Heartbeat | 30 detik (jitter ±20%); dekat kepala antrean 10 detik; stale 90, end 300 | kode `services/queue/internal/config/load.go:112-115` (nilai bawaan; prod tidak menimpa). Dev memakai 120/10/300/600 (`deployments/dev/env/queue.env.example`) |
| Interval polling dari server | jauh (posisi di atas 500) 30 dtk, menengah (101 sampai 500) 10 dtk, dekat (sampai 100) 3 dtk | `event_settings` (kolom `poll_*_ms`, `poll_far_above`, `poll_mid_above`) |
| Cache `queue-status` | `Cache-Control: public, max-age=1`; Cloudflare Cache Rule `cache-queue-status` aktif untuk dev dan api | `status_handler.go:32`, panduan 4.1 |
| Batas join per IP | 200 per menit (lunak) | `RATE_LIMIT_JOIN_PER_IP` |
| Puncak join, slot aktif | S 2.000/3.000, M 6.000/3.000, L 20.000/5.000 (per detik / slot) | HLD §17.1 |
| Perilaku klien | **Profil A**: tiap pengantre memanggil `/queue-entries/me` dan `queue-status` tiap `nextPollAfterMs` (di server ±3 detik) | `waiting-room.js` (komentar) dan FE `http-queue.repository.ts`: memakai `nextPollAfterMs` dan `exactPosition`, **tidak** memakai `poll.farMs/midMs/nearMs` |

Profil B memakai interval menurut zona; profil C seperti B, tetapi zona jauh **tidak** memanggil `/me`: posisi dihitung di klien dari
`seq - headSeq` pada Status Document yang di-cache (inilah desain HLD §17, QS-01).

### 4.2 Laju terukur per pengantre

3.000 pengantre, 50 slot aktif (supaya mayoritas menunggu di zona jauh), ramp 45 detik lalu hold 120 detik; laju dihitung dari
sampel mentah pada jendela hold:

| Profil klien | `/queue-entries/me` per detik | `queue-status` per detik (cache-able) | heartbeat per detik |
|---|---:|---:|---:|
| A (FE sekarang) | **962,3** (0,321 per pengantre, tiap ±3 dtk) | 961,9 | 103,7 (tiap ±29 dtk) |
| B | 201,7 | 201,1 | 104,1 |
| C | **80,9** (datar: bergantung zona dekat, bukan jumlah pengantre) | 201,1 | 104,0 |

Kecocokan dengan model teoretis dari konfigurasi: `/me` profil C terukur 80,9 vs model 73; heartbeat terukur 103,7 vs
3.000/30 = 100. Model teoretis untuk profil B meleset sekitar 29% (terukur 201,7 vs 156): jadi B hanya dipakai sebagai batas
tengah, bukan dasar perhitungan.

### 4.3 Ekstrapolasi ke 100 ribu, 300 ribu, dan 1 juta

Linear dari laju terukur di atas. Keabsahan: A linear terhadap jumlah pengantre (semua zona polling ±3 detik); C hanya heartbeat
yang linear (`/me` C mengikuti slot aktif: HLD §17.2, 2 × slot ÷ 3 detik).

| Tier | `/me` profil A (FE sekarang) | `queue-status` A (di edge) | heartbeat @30 dtk | heartbeat @120 dtk | `/me` profil C (HLD) | join puncak (HLD) |
|---|---:|---:|---:|---:|---:|---:|
| S: 100 rb | 32.077 | 32.063 | 3.457 | 864 | 1.333 | 2.000 |
| **M: 300 rb** | **96.232** | 96.188 | 10.370 | 2.592 | 2.000 | 6.000 |
| L: 1 juta | 320.773 | 320.627 | 34.567 | 8.642 | 3.333 | 20.000 |

Tiga bacaan:
1. **Klien sekarang tidak skala.** Pada 300 ribu, `/me` saja 96 ribu request per detik tak-bisa-di-cache, 100 kali lipat
   dari 962 per detik yang terukur pada 3.000 pengantre (laju per pengantre sama). Desain HLD (profil C) menurunkannya menjadi ±2.000.
2. **Heartbeat adalah beban besar yang tidak ada di model HLD §17.2.** Pada 30 detik, 300 ribu pengantre menghasilkan 10.370
   tulisan per detik ke Valkey. Memperpanjang interval menjadi 120 detik memotongnya empat kali; kode mewajibkan stale lebih dari 2 kali
   interval dan end lebih dari stale, jadi pakai set seperti dev: 120/10/300/600 (interval, dekat, stale, end).
3. **`queue-status` bukan beban origin hanya jika cache edge bekerja** (HLD: sekitar 1 request per detik per titik cache). Volume
   di edge tetap 96 ribu request per detik pada profil A; itu perlu dipastikan batas dan biayanya di penyedia edge.

### 4.4 Kebutuhan sumber daya (indikatif)

Biaya marginal terukur (regresi linear CPU terhadap RPS dari tiga profil di atas, n = 3, setara core Apple M4 di Docker):
**queue-api 0,162 core-detik per 1.000 request** (1 core ≈ 6.180 request/dtk), **Valkey 0,044** (1 core ≈ 22.900), PostgreSQL
0,008 (campuran `queue.*` hampir tidak menyentuh DB). Beban dasar tetap: queue-worker sekitar 0,3 core, Valkey 0,18 core.

| Tier / skenario | RPS origin tak-cache | core queue-api (setara M4, 100%) | core Valkey (marginal) |
|---|---:|---:|---:|
| S 100 rb / A, heartbeat 30 dtk | 37.534 | 6,1 | 1,6 |
| S 100 rb / C, heartbeat 30 dtk | 6.790 | 1,1 | 0,3 |
| S 100 rb / C, heartbeat 120 dtk | 4.198 | 0,7 | 0,2 |
| **M 300 rb / A, heartbeat 30 dtk** | **112.602** | **18,2** | **4,9** |
| **M 300 rb / C, heartbeat 30 dtk** | **18.370** | **3,0** | **0,8** |
| **M 300 rb / C, heartbeat 120 dtk** | **10.592** | **1,7** | **0,5** |
| L 1 jt / A, heartbeat 30 dtk | 375.340 | 60,8 | 16,4 |
| L 1 jt / C, heartbeat 30 dtk | 57.900 | 9,4 | 2,5 |
| L 1 jt / C, heartbeat 120 dtk | 31.975 | 5,2 | 1,4 |

Kolom RPS menjumlahkan `/me`, heartbeat, dan join puncak; seluruhnya diberi biaya campuran `queue.*`. **Join lebih mahal dari rata-rata
campuran** (bagian 5), jadi tabel ini batas bawah untuk skenario join-dominan.

**Cara membacanya, dengan batas yang jujur.**
- Angka core adalah setara core Apple M4 pada 100% CPU. **Tidak diukur di `t4g.small`** (Graviton2 burstable: 2 vCPU, CPU credit
  terbatas). Mengalikan dengan faktor pelambatan tanpa pengukuran adalah dugaan; untuk perencanaan awal, uji dulu satu instance
  `c7g.large` (SRS-Q-PERF-03 memproyeksikan 3.000 join/dtk per instance) dan ukur nilai sebenarnya.
- Skenario **A pada 300 ribu** membutuhkan puluhan core Queue dan Valkey di atas batas satu thread untuk eksekusi perintah:
  perlu sharding Valkey. Skenario C jauh lebih ringan, tetapi tetap melewati kapasitas ASG Queue PoC (maksimum 2 instance
  `t4g.small`).
- Postgres tidak menjadi bottleneck jalur antrean yang diukur; jalur hold dan booking tidak dibebani di tes `waiting`.

## 5. Laju join (TC-PERF-02)

<!-- JOINRATE -->
Diukur lokal 2026-11-13 (`joinrate`, RATES 100 sampai 1.500, ramp 10 dtk, hold 20 dtk per laju, satu event uji):

| Laju join target | Selesai | Terputus | p50 | p95 | p99 | Error |
|---:|---:|---:|---:|---:|---:|---:|
| 100/dtk | 2.000 | 0 | 0,8 ms | 2,1 ms | 21 ms | 0% |
| 300/dtk | 5.999 | 0 | 0,7 ms | 1,9 ms | 9,5 ms | 0% |
| 600/dtk | 12.000 | 0 | 0,7 ms | 2,5 ms | 13,8 ms | 0% |
| 1.000/dtk | 20.000 | 0 | 0,6 ms | 1,8 ms | 7,5 ms | 0% |
| 1.500/dtk | 29.999 | 0 | 0,8 ms | 2,5 ms | 9,4 ms | 0% |

- **Belum jenuh sampai 1.500 join/dtk** di laptop ini (0 iterasi terputus dari 97.500, dan log k6 tidak memuat
  `dropped_iterations`). Titik
  jenuh join tidak diukur, jadi angka join per instance Queue (SRS-Q-PERF-03) **belum terbukti**.
- Batas yang berlaku: batas join per IP lokal 100.000 per menit (sekitar 1.667 per detik). Laju di atas itu terkena batas,
  bukan kapasitas service. Uji 2.000/dtk ke atas perlu IP sumber yang berbeda atau batas yang lebih longgar.
- Tiap join menulis entri antrean ke Valkey dan jejak audit. Sepanjang run, Valkey mencapai sekitar 51.800 operasi per detik
  dan memori sekitar 235 MB pada puncak; CPU rata-rata selama run: queue-worker 39%, Valkey 31%, queue-api 22%.
- Ini tidak menguji kapasitas Redis/Valkey di t4g atau c7g: angka ini hanya bukti bahwa jalur join di laptop melewati
  1.500/dtk tanpa kegagalan.

## 6. Kaitan dengan topologi produksi target (ADR-033)

ADR-033 (Fase 0 rencana remediasi audit, belum ditulis saat dokumen ini dibuat) memuat tabel topologi produksi target.
Dokumen ini memberi tiga masukan ke tabel itu:
1. Ukuran node Queue dan Valkey tidak boleh ditetapkan dari jumlah pengantre, tetapi dari tabel bagian 4.4 untuk **profil klien
   yang akan dirilis** (A atau C) dan interval heartbeat.
2. Tanpa perubahan FE ke profil C, Tier M tidak tercapai pada topologi mana pun yang wajar; dengan profil C, Tier M tampak
   dalam jangkauan beberapa instance Queue dan satu Valkey, **tetapi itu proyeksi, bukan hasil uji**.
3. Harga **tidak** dihitung di sini; gunakan AWS Pricing Calculator (`ap-southeast-3`) dengan komponen di atas.

## 7. Yang BELUM terbukti

1. Kapasitas pada infra target (AWS, EC2 `t4g.small`, data tier satu host): **tidak ada uji beban di AWS**. Uji prod yang dirancang
   (`ANALISIS-PERBANDINGAN.md`, bagian 5) hanya baca dan menunggu persetujuan pemilik.
2. 300.000 atau 1.000.000 pengantre sesungguhnya. Load generator satu laptop tidak mampu; membuktikannya butuh generator
   terdistribusi (beberapa instance k6, atau k6 cloud) dan infra target yang diperbesar.
3. Perilaku cache edge di bawah beban nyata: rasio hit, biaya request edge, dan batas penyedia (SRS-Q-PERF-02).
4. Interaksi dengan jalur hold, pembayaran, dan notifikasi pada puncak; `waiting` hanya mengukur antrean.
5. Perilaku `t4g` (CPU credit) dan skalabilitas ASG Queue di bawah lonjakan join (QS-10).
6. Penyebab "panas" pada S5 di stress lokal (bagian 3).
7. FE: bahwa klien benar-benar berjalan di profil A pada rilis produksi dibaca dari kode `rusuh-booking-mobile` (checkout hari ini);
   belum diverifikasi terhadap build yang dipakai pengguna.

## 8. Rekomendasi, berurutan

1. **Pindahkan FE ke profil C** (posisi dihitung dari `seq - headSeq` di Status Document yang di-cache; `/me` hanya untuk zona
   dekat). Itu perubahan terbesar pada beban origin (96.000 menjadi ±2.000 pada 300 ribu).
2. **Naikkan interval heartbeat zona jauh** (mis. 120 detik) lewat `QUEUE_HEARTBEAT_INTERVAL_SECONDS` dan `QUEUE_STALE_AFTER_SECONDS`.
3. Tambahkan heartbeat ke model HLD §17.2 dan ke tes TC-PERF-01.
4. Ukur satu instance `c7g.large` dengan `joinrate` dan `waiting` profil C untuk mengganti "setara M4" dengan angka nyata.
5. Bila klaim Tier M tetap dibutuhkan untuk tender: uji terdistribusi di infra target sebelum klaim, dan nyatakan di proposal
   bahwa angka saat ini adalah proyeksi.
