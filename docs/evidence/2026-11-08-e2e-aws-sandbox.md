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
