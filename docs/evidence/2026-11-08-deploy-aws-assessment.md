# Evidence: keputusan deploy AWS untuk assessment

Tanggal: 2026-11-08. Pemilik platform: arnu12rahman. Melengkapi HLD §4.5 tanpa mengubah dokumen analisa.

| Keputusan | Alasan |
|---|---|
| AWS dipakai kurang dari 1 bulan, dinyalakan seperlunya, lalu teardown | PRD CON-04: kredit US$200; assessor tetap bisa jalan lokal (US-082) |
| Tetap profil PoC: satu host stateful, tanpa HA penuh (FCA-15 diterima) | PRD "Klaim yang tidak dibuat"; profil RDS/ElastiCache/Amazon MQ tetap referensi Tier S/M/L di HLD |
| Cloudflare menggantikan CloudFront + WAF | DNS domain sudah di Cloudflare, tidak menambah biaya; ALB hanya menerima IP Cloudflare |
| ALB internal Queue → Booking ditambahkan | Keduanya di ASG berbeda dan IP instance berubah-ubah |
| Next.js belum dideploy | Web di luar cakupan deployment saat ini |
| Observability AWS: CloudWatch (log, ALB, alarm); Prometheus/Grafana tidak dipasang | Cukup untuk demo; `trace_id` di log mengorelasikan request |
| X-Ray ditunda | ADR-023 menunda tracing penuh; aplikasi belum memakai OpenTelemetry/X-Ray SDK |
| Midtrans tetap Sandbox | SRS-C-03 |
| Region `ap-southeast-2` (Sydney), menyimpang dari SRS-C-04 (`ap-southeast-3`) | Region akun kredit tidak bisa diubah; hanya untuk assessment |
| Tetap AWS Free plan | Kredit US$200; akun tutup saat kredit habis atau setelah 6 bulan |
| Host data `t4g.small`, bukan `t4g.medium` (HLD) | Tipe t4g tertinggi yang eligible Free plan; load test hanya skala kecil |
| Snapshot harian DLM (7) ditambah snapshot tiap `aws-power.sh off` (3) | SCP Free plan mengizinkan `dlm:*`; API DLM terbaca dari akun ini |
| CI merilis lewat IAM user khusus (`create_ci_user`) dengan access key di GitHub Secrets environment `prod`, bukan OIDC | SCP Free plan menolak `iam:*Provider*`; kunci statis diterima untuk assessment dengan policy sempit, Required reviewers, dan penghapusan kunci saat teardown. Rilis dari laptop tetap jadi cadangan |
| Login `aws login` dengan role `AccountFullAccessRole` (2026-11-08) | Akun anggota AWS Organizations terkelola; SCP menolak sebagian aksi IAM tetapi mengizinkan role/instance profile/policy bernama biasa, SSM, Budgets, ACM, ECR; RegionFloor hanya mengizinkan us-east-1, us-west-2, dan region terpilih |
| IAM user, bukan IAM Identity Center | Identity Center tidak didukung Free plan |
| `PAYMENT_*_RETURN_URL` dan `TICKET_LINK_BASE_URL` di AWS mengarah ke FE dev `https://rusuh-booking.pages.dev` | FE belum punya alamat prod; ini assessment. Kode FE web belum punya rute `/payment/success`, `/payment/failed`, `/tickets`, jadi tautan tiket di email dan return Snap web bisa mendarat di halaman tak ditemukan (mobile memakai WebView) |
| Kunci Midtrans diperiksa langsung ke Sandbox (404 = sah), bukan dari awalan | Awalan `SB-Mid-server-` bukan jaminan format; `seed-ssm.sh` memakai uji 401 |
