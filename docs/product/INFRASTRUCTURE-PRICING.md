# Infrastruktur, Strategi Peningkatan, dan Estimasi Biaya

> Bagian dari dokumentasi produk Rusuh Booking Tiket. Kembali ke [README](../../README.md).
> Harga diambil **13 November 2026** dari AWS Price List publik (region `ap-southeast-2` Sydney, on-demand, USD, tanpa pajak) dan halaman harga resmi vendor. Semua angka bulanan memakai **730 jam**.

## 1. Ringkasan

| Skenario | Untuk apa | Estimasi bulanan (list price) | Status |
|---|---|---:|---|
| **1. PoC / demo (saat ini)** | Demo, penilaian, uji fungsional | **sekitar US$133** (terukur lebih rendah, lihat 3.2) | DEPLOYED |
| **2. Produksi standar** | Penjualan berbayar nyata, volume menengah, data tier HA | **sekitar US$750** | RECOMMENDED |
| **3. Enterprise / trafik tinggi** | Penjualan besar dengan lonjakan terjadwal | **sekitar US$3.500** (anggaran indikatif) | RECOMMENDED, ukuran wajib divalidasi uji beban |

Biaya infrastruktur di atas **terpisah** dari biaya transaksi Midtrans (per transaksi, ditanggung per penjualan) dan dari biaya pengembangan, pemeliharaan, serta dukungan aplikasi (lihat bagian 7).

## 2. Harga satuan yang dipakai

| Komponen | Harga | Sumber |
|---|---:|---|
| EC2 `t4g.small` (2 vCPU, 2 GiB) | US$0,0212/jam | AWS Price List EC2 Sydney, Linux on-demand |
| EC2 `t4g.medium` (2 vCPU, 4 GiB) | US$0,0424/jam | idem |
| EC2 `c7g.large` / `c7g.xlarge` | US$0,0944 / US$0,1887 per jam | idem |
| EC2 `m7g.large` (2 vCPU, 8 GiB) | US$0,1020/jam | idem |
| Application Load Balancer | US$0,0252/jam + US$0,008 per LCU-jam | AWS Price List `AWSELB` ap-southeast-2 (publikasi 2026-11-11) |
| IPv4 publik | US$0,005/jam per alamat | AWS Price List `AmazonVPC` (2026-11-05) |
| EBS gp3 | US$0,096 per GB-bulan; snapshot US$0,055 per GB-bulan | AWS price map EBS Sydney |
| RDS PostgreSQL `db.t4g.medium` | US$0,102/jam Single-AZ; US$0,203/jam Multi-AZ | AWS Price List `AmazonRDS` (2026-11-12) |
| RDS PostgreSQL `db.r7g.large` Multi-AZ | US$0,574/jam | idem |
| RDS gp3 | US$0,138 per GB-bulan Single-AZ; US$0,276 Multi-AZ | idem |
| ElastiCache Valkey `cache.t4g.medium` / `cache.m7g.large` | US$0,076 / US$0,1568 per node-jam | AWS Price List `AmazonElastiCache` (2026-11-13) |
| Amazon MQ RabbitMQ `mq.m7g.medium` single / cluster 3 node | US$0,1709 / US$0,5126 per jam | AWS Price List `AmazonMQ` (2026-09-11) |
| Amazon MQ RabbitMQ `mq.m7g.large` cluster 3 node | US$1,0251/jam | idem |
| CloudWatch Logs | US$0,67 per GB ingest; US$0,033 per GB-bulan simpan; metrik kustom US$0,30 per metrik-bulan (10.000 pertama) | AWS Price List `AmazonCloudWatch` (2026-11-04) |
| Transfer data keluar internet | US$0,114 per GB (10 TB pertama, setelah 100 GB gratis global) | AWS Price List `AWSDataTransfer` (2026-11-11) |
| Cloudflare | Free US$0; Pro US$25/bulan (US$20 bila tahunan); Business US$250/bulan (US$200 tahunan) | https://www.cloudflare.com/plans/network-cdn |
| Resend | Free US$0 (3.000/bulan, **maks 100/hari**); Pro US$20 (50.000/bulan, tanpa batas harian, overage US$0,90 per 1.000); Scale mulai US$90 (100.000) sampai US$650 (1.000.000) | https://resend.com/pricing |
| Midtrans | VA/transfer bank Rp4.000; QRIS 0,7%; GoPay 2%; kartu kredit 2,9% + Rp2.000 per transaksi sukses; tanpa biaya setup atau bulanan | https://midtrans.com/pricing |

Sumber AWS: berkas Price List publik `https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/<layanan>/current/ap-southeast-2/index.json` dan kalkulator resmi https://calculator.aws/. Harga Midtrans di halaman publik tidak termasuk PPN untuk VA dan kartu kredit; tarif kontrak bisa berbeda dan harus dikonfirmasi ke Midtrans.

## 3. Skenario 1: PoC / demo (saat ini, DEPLOYED)

### 3.1 Konfigurasi nyata

Dari `deployments/terraform/variables.tf` dan modulnya: ASG queue dan core masing-masing satu `t4g.small` (maks 3 dan 2), satu data host `t4g.small` dengan EBS 50 GB, dua ALB (publik dan internal), dua subnet publik tanpa NAT Gateway, snapshot harian simpan 7, S3 backup dan gambar, CloudWatch, AWS Budgets 50/100/150 USD. Edge Cloudflare Free, email Resend Free, Midtrans Sandbox.

### 3.2 Biaya

| Komponen | Asumsi | US$/bulan (list price) |
|---|---|---:|
| EC2 3× `t4g.small` | menyala 24×7 | 46,43 |
| ALB 2 unit | jam | 36,79 |
| ALB LCU | rata-rata 1 LCU per ALB | 11,68 |
| IPv4 publik | 5 alamat (2 ALB publik, 3 instance) | 18,25 |
| EBS gp3 | 110 GB (3 root 20 GB + data 50 GB) | 10,56 |
| Snapshot EBS | ±30 GB-bulan (inkremental) | 1,65 |
| CloudWatch | 2 GB log, 20 metrik kustom | 7,34 |
| Transfer data keluar | 50 GB, masih dalam 100 GB gratis | 0,00 |
| Cloudflare, Resend | paket Free | 0,00 |
| **Total** | | **±132,70** |

**Biaya terukur** (Cost Explorer, 4 sampai 13 November 2026, kotor sebelum kredit, ADR-034): US$6,69 total, sekitar **US$1,7 per hari** saat menyala dan **US$1,1 per hari** saat aplikasi dimatikan (`aws-power.sh off` tidak menghentikan dua ALB dan IP publik). Jam EC2 tidak muncul sebagai biaya pada periode itu, penyebabnya belum dipastikan (dugaan: manfaat free tier atau paket akun), sehingga angka terukur lebih rendah dari list price dan **tidak boleh diekstrapolasi ke produksi**. Kredit akun PoC tersisa US$193 per 13 November 2026.

### 3.3 Batas kuota gratis yang perlu diketahui

| Layanan | Batas | Yang terjadi bila terlampaui |
|---|---|---|
| Resend Free | 100 email per hari, 3.000 per bulan | Email e-tiket dan OTP berikutnya ditolak penyedia; Notification mencoba ulang lalu memindahkan pesan ke DLQ. Transaksi tidak batal, tetapi pembeli tidak menerima email |
| Cloudflare Free | 70 rule total; tanpa WAF rate limiting tingkat lanjut | Pembatasan join saat ini mengandalkan limiter Valkey di aplikasi (200 per menit per IP) |
| Midtrans Sandbox | Bukan uang nyata | Wajib akun production sebelum penjualan |
| Data host tunggal | Tanpa replika | Kegagalan host menghentikan semua service; pemulihan dari snapshot harian (RPO hingga 24 jam) |

## 4. Skenario 2: Produksi standar (RECOMMENDED)

### 4.1 Perubahan arsitektur

- PostgreSQL pindah ke **RDS Multi-AZ** dengan point-in-time recovery (menghapus SPOF data dan RPO 24 jam).
- Valkey pindah ke **ElastiCache** primary + replika.
- RabbitMQ ke **Amazon MQ single instance**. Pilihan ini sengaja hemat: event stok dan uang ditahan di outbox PostgreSQL, sehingga gangguan broker menunda event tanpa menghilangkannya.
- Queue dan core masing-masing **2× `t4g.medium`** di dua AZ.
- **Cloudflare Pro** (WAF managed rules) dan **Resend Pro** (50.000 email per bulan tanpa batas harian).
- Midtrans production; deploy lewat OIDC (menutup SEC-005).

### 4.2 Biaya

| Komponen | US$/bulan |
|---|---:|
| ASG queue 2× `t4g.medium` | 61,90 |
| ASG core 2× `t4g.medium` | 61,90 |
| RDS PostgreSQL `db.t4g.medium` Multi-AZ + gp3 100 GB | 175,79 |
| ElastiCache Valkey 2× `cache.t4g.medium` | 110,96 |
| Amazon MQ RabbitMQ `mq.m7g.medium` single | 124,76 |
| ALB 2 unit + rata-rata 6 LCU | 71,83 |
| IPv4 publik 6 alamat | 21,90 |
| EBS root 80 GB | 7,68 |
| CloudWatch (10 GB log, 40 metrik) | 18,70 |
| Transfer data keluar 500 GB | 45,60 |
| Cloudflare Pro | 25,00 |
| Resend Pro | 20,00 |
| **Total** | **±746** |

Manfaat: tidak ada SPOF di data tier, backup otomatis dengan PITR, email cukup untuk ±50.000 e-tiket dan OTP per bulan. Batas: broker belum HA; kapasitas puncak belum terukur di infrastruktur ini. Implikasi operasional: biaya tetap naik sekitar 5,6 kali dari PoC, beban operasi turun karena patching dan backup database dikelola AWS.

## 5. Skenario 3: Enterprise / trafik tinggi (RECOMMENDED, indikatif)

### 5.1 Perubahan arsitektur

- ASG queue `c7g` dengan **scheduled scale-out** sebelum jam buka penjualan (desain HLD), baseline 2× `c7g.large` dan lonjakan hingga 10× `c7g.xlarge`.
- RDS `db.r7g.large` Multi-AZ, ditambah read replica bila katalog membebani primary.
- ElastiCache Valkey 3 node `cache.m7g.large`; Amazon MQ **cluster 3 node** `mq.m7g.large`.
- **Cloudflare Business** (atau Enterprise) dengan WAF dan rate limiting di edge; Resend Scale.
- Observabilitas tingkat lanjut (tracing terdistribusi) dan rencana DR lintas region bila ada SLA yang memintanya.

### 5.2 Biaya

| Komponen | Asumsi | US$/bulan |
|---|---|---:|
| ASG queue baseline 2× `c7g.large` | 24×7 | 137,82 |
| ASG queue lonjakan 10× `c7g.xlarge` | 4 hari penjualan × 24 jam | 181,15 |
| ASG core 3× `m7g.large` | 24×7 | 223,38 |
| RDS `db.r7g.large` Multi-AZ + gp3 500 GB | | 557,02 |
| ElastiCache Valkey 3× `cache.m7g.large` | | 343,39 |
| Amazon MQ cluster 3 node `mq.m7g.large` | | 748,32 |
| ALB 2 unit + rata-rata 30 LCU | | 211,99 |
| IPv4 publik, EBS root 300 GB | | 50,70 |
| CloudWatch (50 GB log, 150 metrik) | | 78,50 |
| Transfer data keluar 3 TB | setelah cache Cloudflare | 342,00 |
| Cloudflare Business | bulanan | 250,00 |
| Resend Scale 500.000 email | | 350,00 |
| **Total** | | **±3.474** |

**Penting:** angka ini anggaran indikatif. Kapasitas 100 ribu, 300 ribu, atau 1 juta pengantre **belum dibuktikan**. Model di [`tests/k6/ANALISIS-KAPASITAS.md`](../../tests/k6/ANALISIS-KAPASITAS.md) menunjukkan beban origin ditentukan perilaku polling klien: untuk 300.000 pengantre sekitar 112.000 request per detik dengan klien saat ini, atau sekitar 10.600 dengan klien sesuai desain HLD. Ukuran final wajib ditetapkan dari uji beban terdistribusi di infrastruktur ini.

## 6. Biaya variabel per transaksi (Midtrans)

| Contoh tiket Rp500.000 | Biaya per transaksi |
|---|---:|
| Virtual account / transfer bank | Rp4.000 (belum PPN) |
| QRIS | Rp3.500 (0,7%) |
| GoPay | Rp10.000 (2%) |
| Kartu kredit | Rp16.500 (2,9% + Rp2.000, belum PPN) |

## 7. Di luar cakupan biaya infrastruktur

- Pengembangan fitur, pemeliharaan, dukungan (on-call), dan pengujian beban terdistribusi.
- Domain `rusuhbooking.my.id` (biaya registrar tidak tercatat di repo; konfirmasi ke pemilik).
- Harga region Jakarta (`ap-southeast-3`) belum dihitung. ADR-034 menyebutnya sebagai opsi untuk latensi pengguna Indonesia; hitung ulang dengan AWS Pricing Calculator sebelum penawaran final.
- Diskon Savings Plans atau Reserved Instances (menurunkan biaya compute dan RDS untuk komitmen 1 atau 3 tahun; besarannya belum dihitung).
- TCO on-premise (topologi alternatif ada di ADR-034).

## 8. Kapan naik tingkat

Pindah dari Skenario 1 ke 2 bila salah satu pemicu ADR-034 terjadi: penjualan berbayar nyata (Midtrans production), lebih dari 100 transaksi per hari, ada SLA tertulis kepada klien, atau lebih dari 100 email per hari. Pindah ke Skenario 3 bila satu penjualan diperkirakan melampaui kapasitas yang terbukti di Skenario 2 lewat uji beban.
