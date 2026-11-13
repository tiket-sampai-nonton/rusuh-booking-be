# Laporan Restrukturisasi README

Tanggal: 13 November 2026. Basis kode: `main` `99fd2b2`. Pelaksana: sesi AI (Claude Code) atas permintaan pemilik platform.

## 1. Yang diubah

| Berkas | Perubahan |
|---|---|
| `README.md` | Ditulis ulang sebagai dokumen produk dan solusi untuk penilai tender, bagian A sampai P. Konten operasional lama (stage CI/CD, konfigurasi keamanan, struktur) dipertahankan dalam blok `<details>` |
| `docs/product/PRODUCTION-ARCHITECTURE.md` | Baru: HLD topologi produksi nyata dari Terraform, routing ALB, arsitektur target, LLD, 5 diagram sekuens |
| `docs/product/CAPABILITIES-AND-EVIDENCE.md` | Baru: 10 kapabilitas bisnis dengan status dan bukti, pemetaan tantangan ke kapabilitas, matriks bukti kualitas |
| `docs/product/INFRASTRUCTURE-PRICING.md` | Baru: tiga skenario infrastruktur dan estimasi biaya dari harga resmi |

Dokumen yang disarankan tetapi **tidak dibuat** karena isinya sudah tercakup tanpa duplikasi: `PRODUCT-OVERVIEW.md` (README A–B), `TECHNOLOGY-STACK.md` (README F), `DEPLOYMENT-GUIDE.md` (README K–L, `deployments/terraform/README.md`, `deployments/dev/README.md`).

## 2. Temuan dari kode dan infrastruktur

- Produksi berjalan di `ap-southeast-2` (bawaan `variables.tf`), bukan Jakarta. Semua instance Graviton `t4g.small`: ASG queue 1..3, ASG core 1..2, satu data host berisi PostgreSQL, Valkey, RabbitMQ, dan Notification.
- Ada **dua ALB** (publik dan internal), dua subnet publik tanpa NAT, instance mendapat IP publik (`map_public_ip_on_launch = true`), akses admin tanpa SSH lewat SSM.
- **Tidak ada CloudFront** di Terraform; edge adalah Cloudflare (ADR-034 mengamandemen ADR-012). README lama menyebut `IMAGE_PUBLIC_BASE_URL` menunjuk CloudFront; dikoreksi.
- **Tidak ada Tempo**; tracing terdistribusi belum ada. Observabilitas: Prometheus, Loki, Alloy, Grafana (lokal, dev) dan CloudWatch EMF (AWS).
- Alokasi stok terjadi sebelum promosi antrean (`promote.lua` menerima `allocationId` per entry; Queue memanggil `/internal/v1/allocations:batch`), sehingga klaim "Jangan PHP" didukung kode.
- Payment memanggil Booking (`payable`, `payment-lock`); Booking memanggil Payment (`resolve`). Webhook Midtrans diverifikasi SHA-512.
- Rollback prod tersedia lewat `deploy-aws.sh` dengan tag sebelumnya.

## 3. Klaim yang dikoreksi

| Klaim lama | Koreksi |
|---|---|
| "Boilerplate siap Sprint 1", peran BE-1/BE-2 | Dihapus; sistem sudah berjalan di tiga lingkungan |
| `deployment-prod` masih placeholder | Deploy nyata lewat `deploy-aws.sh` dengan persetujuan reviewer |
| Kredensial AWS via OIDC (`AWS_ROLE_ARN_PROD`) | IAM user statis (SEC-005), OIDC belum dipakai |
| Observabilitas termasuk Tempo | Tempo tidak ada di repo |
| Gambar event lewat CloudFront | Tidak ada CloudFront di Terraform |
| (draf awal hari ini) "UI dev 8/9 dan 9/10" | TC-REPORT terbaru: dev 9/9 dan 10/10 |

## 4. Bukti yang belum ada atau belum diverifikasi

- Kapasitas prod belum terukur (uji 500 VU terhenti oleh batas join per IP). 100 ribu, 300 ribu, dan 1 juta pengantre hanya model.
- `tests/k6/ANALISIS-PERBANDINGAN.md` masih menyebut dev "belum diukur", padahal run dev 3.000 VU sudah ada (`tests/k6/reports/dev/waiting/summary.json`: 18.237 request, p95 181 ms, p99 253 ms, 0% gagal). Dokumen itu perlu diperbarui pemilik `tests/`.
- Coverage berasal dari AUDIT-FINAL 31 Oktober 2026; tidak diukur ulang di sesi ini.
- Push notifikasi (FCM): adapter ada, pemakaian produksi tidak diverifikasi.
- Appium belum dijalankan; 4 skenario BDD gagal karena harness.
- Skor kepatuhan tender (35 PASS / 7 PARTIAL / 1 belum diverifikasi) berasal dari audit di luar repo; tidak dikutip di README.
- FE prod `https://rusuhbooking.my.id` tidak bisa diverifikasi dari mesin penyusun: jaringan lokal menyadap TLS domain itu (sertifikat dari "Telekomunikasi Selular Pt Forward Trust CA") dan menjawab 503. API prod dan dev menjawab 200 (prod memuat 10 event). Perlu dicek dari jaringan lain.

## 5. Asumsi harga

- Sumber: berkas AWS Price List publik per layanan untuk `ap-southeast-2` (tanggal publikasi 2026-09-11 sampai 2026-11-13), halaman harga Resend, Cloudflare, dan Midtrans, dibaca 13 November 2026.
- 730 jam per bulan, on-demand, USD, tanpa pajak, tanpa Savings Plans, tanpa free tier.
- Asumsi beban per skenario (LCU ALB, GB log, transfer data, jumlah email) tertulis per baris di `INFRASTRUCTURE-PRICING.md`; semuanya perkiraan dan harus dikonfirmasi dengan AWS Pricing Calculator sebelum penawaran final.
- Biaya terukur PoC (US$1,7 per hari) lebih rendah dari list price karena jam EC2 tidak muncul sebagai biaya pada periode itu (penyebab belum dipastikan). Angka ini disalin dari ADR-034, bukan diambil ulang dari Cost Explorer.
- Harga region Jakarta dan biaya domain belum dihitung.

## 6. Tautan

- ADR-034 dan pembaruan ADR-012 baru ada di branch audit (`fix/audit-platform-hardening`), belum di `main`. README merujuk berkas `docs/analisa/05-ADR-RBT.md` yang ada; bagian ADR-034 di dalamnya baru tersedia setelah PR audit merge.
- Header keamanan dan penolakan start tanpa CORS allowlist (SEC-003/004) juga baru ada di branch audit; README menandainya "di branch audit".
- Semua tautan relatif di README dan `docs/product/` diperiksa ke berkas yang ada (lihat bagian 7).

## 7. Pemeriksaan kualitas yang dijalankan

- Setiap tautan relatif di README dan `docs/product/*.md` dicek keberadaannya.
- Total biaya per skenario dihitung ulang dengan skrip.
- Tidak ada nilai rahasia, token, atau data pribadi di dokumen.
- URL produksi dan dev dicek dengan permintaan `GET` baca saja.

## 8. Rekomendasi berikutnya

1. Merge PR audit agar ADR-034 dan SEC-003/004 tersedia di `main`.
2. Perbarui `ANALISIS-PERBANDINGAN.md` dengan hasil dev.
3. Hitung harga dengan AWS Pricing Calculator (Sydney dan Jakarta) dan lampirkan tautannya di `INFRASTRUCTURE-PRICING.md`.
4. Publikasikan Swagger UI (misalnya GitHub Pages) agar penilai bisa membuka API tanpa menjalankan lokal.
5. Jalankan uji beban terdistribusi di infrastruktur Skenario 2 sebelum menawarkan angka kapasitas.
