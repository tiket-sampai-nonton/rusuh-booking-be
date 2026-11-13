# Risk acceptance topologi PoC dan edge Cloudflare — 2026-11-13

Mencatat keputusan ADR-034 (aturan repo: diskusi yang mengubah dokumen planning dicatat sebagai evidence).

## Keputusan dan sumbernya
- 2026-11-12: pemilik mengonfirmasi dev dan prod adalah **PoC/demo** dengan kredit cloud terbatas, dan bahwa assessor menerima hal itu (tercatat di `docs/audit/backend-comprehensive-audit/00-executive-summary.md`). OPS-001 (satu host data) dan OPS-002 (email gratis) berstatus *Risk accepted (PoC)*.
- 2026-11-13: ADR-034 menuliskan risiko yang diterima, pemicu migrasi, topologi produksi target (cloud dan on-prem), dan mengamandemen ADR-012: edge adalah Cloudflare, bukan CloudFront + WAF.

## Data biaya yang dipakai (terukur)
Cost Explorer, akun PoC, 4 sampai 13 November 2026, **biaya kotor sebelum kredit** (filter `RECORD_TYPE` bukan Credit atau Refund): US$6,69 (Elastic Load Balancing 4,79; VPC 1,00; EC2 - Other 0,88; CloudWatch 0,02; lainnya mendekati nol). Biaya harian saat menyala sekitar US$1,7; biaya dasar saat aplikasi dimatikan (dua ALB dan IP publik) sekitar US$1,1 per hari. Selisih saldo kredit (US$200 menjadi US$193) sesuai dengan angka ini. Komputasi EC2 tidak tercatat sebagai biaya kotor; penyebabnya belum dipastikan, sehingga angka ini **tidak** boleh diekstrapolasi ke produksi.

## Yang belum ada
- **Harga topologi produksi.** Belum dihitung. Isi dari AWS Pricing Calculator (region `ap-southeast-3` atau `ap-southeast-1`), per komponen di tabel ADR-034, lalu lampirkan tautan atau ekspor kalkulasinya di bagian ini.
- **Perbandingan TCO 12 bulan cloud vs on-prem.** Belum dihitung; komponen non-server (tenaga operasi, listrik atau colocation, lisensi, waktu pengadaan) disebut di ADR-034.
- **Bukti kapasitas 100 ribu, 300 ribu, 1 juta pengantre.** Tidak ada pada PoC; hanya yang terukur yang boleh diklaim.
- Pengujian akses langsung ke ALB dan keberadaan WAF managed rules di edge belum diperiksa ulang untuk dokumen ini.

## Tautan
- ADR-034 dan ADR-012: `docs/analisa/05-ADR-RBT.md`
- Audit: `docs/audit/backend-comprehensive-audit/` (OPS-001, OPS-002, OPS-003, DOC-001)
