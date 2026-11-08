# Evidence: E2E pembelian di AWS (Midtrans Sandbox)

Tanggal: 2026-11-08. Lingkungan: `https://api.rusuhbooking.my.id` (Cloudflare -> ALB -> ASG, Sydney), commit `3025e4d`. Skrip: `deployments/terraform/aws-e2e.sh`.
Data uji fiktif (NIK dan nama); email penerima milik tim. Tidak ada uang nyata.

| Tahap | Hasil |
|---|---|
| Join antrean guest (tanpa data, ADR-030) | 201, langsung `ADMITTED` |
| Claim, sesi booking, hold (blok `buyer`), attempt VA_BCA | Semua 2xx; NIK tersimpan tersamar |
| Bayar di simulator Midtrans | Status booking `PAID` sekitar 45 detik setelah bayar |
| Webhook | `settlement` valid, jumlah 250000, `PROCESSED`; `pending` dicatat `DUPLICATE` (idempoten); signature sah |
| E-tiket | 1 tiket terbit; `notification.deliveries` `BOOKING_PAID`/`EMAIL`/`SENT` (diterimanya di inbox dikonfirmasi manual oleh pemilik) |
| Stok | Festival 35 -> 34 |
| Log | Tanpa level ERROR di payment, booking, user selama 2 jam |
| IP klien | `login_audit` mencatat IP publik asli, bukan IP Cloudflare |

Temuan:
- `PANDUAN-E2E-TESTING-DEV.md` bagian 5 dan 6 tertinggal dari ADR-030: join harus tanpa isi, `buyer` wajib di hold. Hold tanpa `buyer` menjawab `NIK_INVALID` (pesan menyesatkan).
- FE web belum punya rute `/payment/success`, `/payment/failed`, `/tickets/{id}`; return Snap web dan tautan email akan mendarat di halaman tak ditemukan.
- Cloudflare tidak men-cache `queue-status` (`DYNAMIC`); aturan cache 1 detik HLD belum dipasang.
- Belum diuji: skenario gagal/expired, webhook palsu dengan order sah, tautan tiket `?t=`, beban.

## Rilis lewat CI (GitHub Actions, run 42499429525, commit `6df50ef`)

| Tahap | Hasil |
|---|---|
| Persetujuan environment `prod` (Required reviewers) | Disetujui manual oleh arnu12rahman |
| Kredensial AWS (IAM user CI, kunci statis; OIDC diblokir SCP) | Berhasil |
| Salin 5 image GHCR -> ECR (`git-6df50ef...`) | Berhasil |
| `deploy-aws.sh`: `image_tag`, Notification di host data, instance refresh kedua ASG, smoke test | Berhasil; kedua refresh `Successful 100%` |
| Setelah rilis | 6 target group healthy; semua container memakai tag baru; API 200; booking E2E tetap `PAID` dengan 1 tiket terbaca; stok tetap 34 |
| `deployment-dev` | Gagal di langkah "Kirim konfigurasi": `DEV_HOST` memakai nama domain yang kini Proxied di Cloudflare (SSH tidak diteruskan). Diperbaiki ke IP `103.119.237.53`; belum dijalankan ulang |

Catatan: policy IAM user CI diverifikasi dengan `simulate-principal-policy` (harus diberi konteks `aws:RequestedRegion`, kalau tidak SCP Free plan menolak semuanya). Cloudflare memblokir User-Agent bawaan Python (403); klien uji harus mengirim User-Agent sendiri.

## Siklus mati-nyala (`aws-power.sh`), 8 November 2026

| Tahap | Hasil |
|---|---|
| `off` | 139 detik; ASG 0, host data `stopped`, volume data tetap, snapshot `rusuh-prod-power-off` terbentuk; API HTTP 503 |
| `on` | 135 detik; instance aplikasi baru (ID berubah), host data menyala; API 200 |
| Setelah `on` | 6 target group healthy; `/data` ter-mount dari volume yang sama (442 MB terpakai); 5 schema database utuh (booking 15, notification 3, payment 8, queue 3, users 4 tabel); event 1, stok Festival 34, booking E2E `PAID`, 1 tiket terbaca |
| Snapshot | `completed 100%` setelah `on` selesai (dibuat saat host berhenti, selesai di latar belakang) |

Biaya saat mati dan nyala masih perkiraan dari daftar harga; belum dicocokkan dengan tagihan.

