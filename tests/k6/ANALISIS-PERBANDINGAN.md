# Perbandingan lokal, dev, dan prod: apa yang diukur dan apa yang belum

Tanggal: 2026-11-13. Dasar: pass lokal hari ini (`reports/local/RINGKASAN.md`, bagian "Analisis pass 2026-11-13"),
`ANALISIS-KAPASITAS.md`, `README.md` k6, dan konfigurasi infra (`deployments/terraform/variables.tf`).

## 1. Ringkasan

| Lingkungan | Status | Yang diukur | Batas utama |
|---|---|---|---|
| **Lokal** (laptop M4, stack satu mesin) | **Diukur**, 11 profil, 13 Nov 2026 | Alur bisnis, stress sampai S6, waiting A/B/C, joinrate sampai 1.500/dtk | k6 dan stack berbagi satu mesin; angka hanya batas bawah dan bukan kapasitas produksi |
| **Dev** (VPS, Docker Compose, satu host) | **Belum diukur**: token admin dev tidak tersedia | Belum ada | Menunggu `AUTH_TOKEN` dari pemilik dan tunnel dev (queue-admin port 18081) |
| **Prod** (PoC AWS, `t4g.small`, data tier satu host, di belakang Cloudflare) | **Belum diukur**: menunggu persetujuan pemilik | Belum ada (rencana ada di bagian 5) | Akan dicari: batas origin PoC dan perilaku cache edge |

Kesimpulan yang boleh diambil hari ini: **kapasitas terukur ada di laptop, bukan di infra target.** Tidak ada angka
yang membuktikan 300.000 pengantre aktif. Ukuran terbesar yang dibangkitkan di laptop adalah 3.000 pengantre (profil C)
dan 1.800 pengantre bersama beban katalog dan beli (S6), dan di sana latensi melewati SLO.

## 2. Tabel ringkas

| Ukuran | Lokal (terukur) | Dev | Prod (PoC) |
|---|---|---|---|
| CPU/memori node | Apple M4, 10 core, 16 GiB; Docker 8 vCPU, 7,75 GiB | Tidak diketahui dari repo (VPS satu host) | `t4g.small` (2 vCPU burstable, Graviton2): data host 1 instance; Queue ASG 1–2; service lain ASG 1–3 (default Terraform) |
| Topologi | Satu mesin, PostgreSQL, Valkey, RabbitMQ, Mailpit, 5 service, k6 di mesin yang sama | Satu VPS, Compose | Edge Cloudflare, ALB, ASG, data tier satu host |
| Pengantre serentak tertinggi (SLO p95 < 2 dtk) | 1.300 sebagai kapasitas berkelanjutan (S5); 1.800 melewati SLO (S6) | Belum diukur | Belum diukur |
| RPS berkelanjutan | 1.524 (S5, p95 3 ms, 0% error) | Belum diukur | Belum diukur |
| Join per detik | 1.500 tanpa jenuh (laju per IP membatasi di sekitar 1.667/dtk) | Belum diukur | Belum diukur |
| Titik jenuh | S5 sampai S6, tidak stabil antar-run; bottleneck Postgres (kontensi) | Belum diukur | Belum diukur |
| Error rate di atas SLO | 0% sampai S6 (degradasi berupa latensi) | Belum diukur | Belum diukur |

## 3. Perbedaan sizing dan implikasinya

- **Laptop vs t4g.small.** Satu laptop M4 (10 core) jauh lebih kuat daripada `t4g.small` (2 vCPU burstable, Graviton2).
  Perbedaan per-core belum diukur, jadi mengalikan angka laptop dengan faktor tertentu adalah dugaan. Pada S5, seluruh
  kontainer lokal menjumlah sekitar 1,9 core setara M4. Pada 2 vCPU `t4g.small` dengan CPU credit yang terbatas, beban
  seperti itu bisa jadi melebihi kapasitas, tetapi belum diverifikasi.
- **Data tier.** Lokal, Postgres berbagi CPU dan disk dengan service lain. Di prod, data tier satu host, dan Postgres
  adalah bottleneck teramati di lokal (lihat bagian 4). Pemisahan host tidak dicoba.
- **Queue.** Queue ASG PoC maksimum 2 instance. Model di `ANALISIS-KAPASITAS.md` bagian 4.4 menunjukkan bahwa skenario
  profil A untuk 300.000 pengantre melewati kapasitas itu jauh.
- **Cache edge.** Prod punya Cloudflare di depan, lokal tidak. Hasil `readonly` lokal adalah origin murni; hasil prod tanpa
  `CACHE_BUST` mengukur edge, dengan `CACHE_BUST=1` mengukur origin. Keduanya harus dilaporkan terpisah.

## 4. Bottleneck yang teramati (lokal)

| Gejala | Lokasi | Angka | Tafsir |
|---|---|---|---|
| Latensi naik di S6, error tetap 0% | Seluruh stack | p95 2.088 ms, p99 4.916 ms | Sistem melambat, tidak gagal |
| CPU Postgres naik tajam | Postgres | 41% (S5) ke 147% rata-rata, puncak 308% (S6) | Jalur tulis hold dan booking membebani DB |
| Lock wait | Postgres | 0 sampai S5, maks 7 di S6 | Kontensi baris, hipotesis: baris stok `event_supply` yang sama untuk semua pembeli satu event (belum terbukti) |
| Total CPU naik tidak sebanding RPS | Seluruh kontainer | S5 1,9 ke S6 4,4 core setara, RPS naik 9% | Kerja per request membengkak, bukan batas CPU host (8 vCPU) |
| CPU queue-api dan booking-api di S6 | queue-api, booking-api | rata-rata 92% dan 87%, maks 129% dan 159% | Ikut naik, tetapi bukan bottleneck tunggal |
| Join 1.500/dtk | Valkey, queue-worker | queue-worker 39%, Valkey 31%, puncak 51.800 op/dtk | Jalur join belum jenuh di laptop |
| Lonjakan 1.000 pengantre | Queue | spike PASS, p95 1,5 ms | Queue menyerap lonjakan singkat tanpa degradasi |

Titik stress tidak stabil: 07:51 gagal di S5, 08:26 PASS penuh (1.942 VU maks), 08:57 gagal di S6, 2026-11-10 gagal di S6.
Kapasitas yang sah diklaim adalah rentang S5 sampai S6, bukan satu angka.

## 5. Rencana uji prod (dirancang, menunggu persetujuan pemilik)

**Status: MENUNGGU PERSETUJUAN PEMILIK.** Tidak ada beban yang dijalankan ke prod. Profil yang dipakai: `readonly` (hanya
`GET` katalog, facet, detail, dan `queue-status`; tidak membuat event, tidak join, tidak memakai token admin, tidak
menulis DB).

Dua seri, dijalankan terpisah:

1. **Edge (yang dialami pengantre), tanpa `CACHE_BUST`:**
   `TEST_ENV=prod CONFIRM_PROD=yes LEVELS=50,200,500,1000 HOLD=60 bash tests/k6/scripts/run.sh readonly`
2. **Origin PoC, dengan `CACHE_BUST=1`:**
   `TEST_ENV=prod CONFIRM_PROD=yes CACHE_BUST=1 LEVELS=20,50,100,200 HOLD=60 bash tests/k6/scripts/run.sh readonly`

Syarat dan kriteria berhenti:
- Persetujuan eksplisit pemilik; AWS menyala dan sehat; di luar jendela demo; tim diberi tahu sebelum run.
- Naik satu level per kali. Jangan lompat ke 1.000 VU sebelum level di bawahnya bersih.
- **Berhenti otomatis** bila error di atas 5% atau p95 di atas 2 detik (setelah 20 detik ramp).
- Cloudflare: jangan mengaktifkan Bot Fight Mode (menantang webhook Midtrans). Bila WAF atau rate limit memblokir
  load generator, pakai `CF_BYPASS_HEADER` dan `CF_BYPASS_VALUE` (rahasia, tidak dicetak) atau allowlist IP penguji.
  Jangan mematikan WAF. Respons 403 atau 503 HTML dihitung `edge_blocked`, bukan kapasitas.
- Sumber daya server prod tidak diukur oleh k6; pakai CloudWatch saat run dan catat jamnya.

Dev (menunggu token dari pemilik): `AUTH_TOKEN` untuk akun admin dev, `ADMIN_URL` lewat tunnel
`deployments/dev/dev-tunnel.sh`, dan `QUEUE_ADMIN_URL` (port 18081, dibuka otomatis oleh tunnel) karena `createEvent` memanggil
PUT queue-settings. Profil yang disarankan untuk dev: `smoke`, `e2e` (berhenti di attempt PENDING karena tidak ada
webhook bertanda tangan di dev), lalu `waiting` kecil. Email dev terkirim sungguhan: jangan jalankan E2E dengan domain
email nyata.

## 6. Rekomendasi

1. **Ukur Postgres dulu** di lokal (`pg_stat_statements`, query lock) untuk membuktikan atau menolak hipotesis kontensi
   baris stok. Itu satu-satunya bottleneck yang sudah terlihat.
2. **FE ke profil C** (dari `ANALISIS-KAPASITAS.md`, bagian 8). Beban origin untuk antrean besar bergantung pada klien, bukan
   jumlah pengantre. Ukur ulang `waiting` profil C dengan lebih banyak VU setelah perubahan.
3. **Setujui dan jalankan seri `readonly` prod** (bagian 5) untuk mendapat angka PoC nyata, edge dan origin terpisah.
4. **Berikan token dev** agar smoke, e2e, dan waiting kecil bisa dijalankan di lingkungan yang bukan lokal.
5. **Uji terdistribusi** (beberapa instance k6 atau k6 cloud) sebelum mengklaim lebih dari 1.000 pengantre aktif; laptop
   tidak bisa menjadi generator sumber tunggal untuk angka besar.
6. Tambahkan skenario waiting yang menaikkan jumlah pengantre serentak di atas 3.000 setelah perbaikan FE, supaya kurva
   skala terlihat.

## 7. Batasan metodologi

- **Laptop sebagai load generator.** k6 dan stack berbagi CPU, RAM, dan Docker VM yang sama. Di S6, k6 juga memakai CPU
  yang dibutuhkan service. Angka lokal adalah batas bawah, dan bisa juga salah arah karena kontensi antar-proses.
- **Jaringan dan ISP.** Lokal memakai loopback. Prod dan dev diukur lewat internet dari laptop; latensi dan bandwidth
  ISP ikut terhitung dan tidak mewakili pengguna sungguhan.
- **Cloudflare.** Cache edge mengubah yang diukur (edge vs origin). WAF dan rate limit bisa memblokir load generator.
  Hasil prod tanpa `CACHE_BUST` tidak boleh diklaim sebagai kapasitas origin.
- **Docker Desktop.** Lokal dibatasi 8 vCPU dan sampler `docker stats` per 4 detik (perkiraan, bukan metrik host).
- **Batas per IP.** Lokal memakai override yang melonggarkan rate limit. Dev dan prod memakai batas bawaan, jadi perilaku
  rate limiter tidak sama.
- **Data uji.** Mailpit lokal menampung maksimal 500 pesan. Setelah beban besar, check email E2E bisa gagal; kosongkan
  Mailpit sebelum E2E (pengalaman 2026-11-10).
- **Hasil tidak stabil antar-run.** Titik jenuh bergeser antar-run (lihat bagian 4). Satu run tidak membuktikan kapasitas.
- **Kode.** Pass hari ini berjalan di commit `99fd2b2ae9` dengan working tree yang belum di-commit (99 berkas). Angka
  harus dibaca bersama `run.json` setiap jenis.
