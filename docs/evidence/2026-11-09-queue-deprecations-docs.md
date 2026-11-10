# Evidence 2026-11-09: queue deprecations dan perbaikan drift dokumen

Task `fix/queue-deprecations-docs` (Batch 3b). Hanya dokumen, anotasi OpenAPI, dan komentar Go. Tanpa perubahan perilaku.

## Keputusan pemilik
- Q4 (ii): `bookingUrl` deprecated, dipertahankan satu rilis; klien menavigasi dari `admissionToken`.
- Q5: `ENTRY_MOVED` / SRS-F-01-10 / spec Req 7.4 / task 14.2 / TC-QUE-16 dipertahankan sebagai desain masa depan; kode dicadangkan.
- Q6: `queueToken` deprecated.
- D1: addendum ADR-030 (tanpa ADR baru) + penunjuk satu kalimat di amandemen ADR-022. Indeks ADR tidak diubah.
- D2: `BOOKING_PAGE_BASE_URL` tetap wajib (`bookingUrl` masih dibangun darinya); penghapusan ditunda.

## Temuan
- TC-QUE-35 meng-assert `bookingUrl`: dianotasi, status tetap PASS. Tidak ada TC yang meng-assert `queueToken` (grep).
- Entri CORS `X-Queue-Token` (`pkg/httpx`) tidak diubah; hanya didokumentasikan (deskripsi claim di `queue.yaml`).
- Penanda Go memakai `// DEPRECATED (...)` / `// RESERVED (...)`, bukan `// Deprecated:`, agar tidak memicu SA1019. `make lint` exit 0.
- `gofmt` menyelaraskan ulang spasi kolom pada struct/var block yang disisipi komentar (3 file); diff non-spasi hanya komentar (dicek dengan `git diff -w`).
- Perbaikan drift tambahan: SRS baris claim (auth) dan glosarium; SRS Change History 1.6 dan TP 1.9 (maks sebelumnya 1.5 dan 1.8, dihitung dari tabel).
- Sapuan konsistensi menemukan sebutan tanpa kualifier di berkas di luar touch-set awal; diberi kualifier `(deprecated)` satu frasa: `04-SDD-RBT-001.md` (join contoh, claim), `03-HLD-AD-RBT-001.md:524`, `04g-E2E-FLOW-DIAGRAMS-MERMAID.md:701`.
- Hasil sapuan: sebutan `queueToken|bookingUrl|ENTRY_MOVED` semuanya berkualifier atau penjelasan deprecation/reserved; `Bearer <queueToken>` hanya tersisa di `specs/queue-service/requirements.md:71` (tabel delta historis, diberi label). `Bearer` lain adalah access/service token yang sah. `X-Queue-Token` hanya di `queue.yaml` deskripsi claim.

## Pertanyaan terbuka
- OQ-A: frasa FR-10 "entry server-side per NIK pembeli" usang terhadap ADR-030 (NIK tidak lagi di Queue); tidak diubah.
- OQ-B: rilis penghapusan `queueToken`, `bookingUrl`, CORS `X-Queue-Token`, `BOOKING_PAGE_BASE_URL` belum ditanggalkan ("satu rilis").
- OQ-C (ditutup iterasi 2): contoh respons claim di SDD 04a §3.4 diperbaiki ke `{admissionToken, bookingUrl}` dan daftar error claim ditulis di bagian itu; HLD 03 (baris 480, 485, 567, 1107) tidak lagi menyebut queue token sebagai kredensial claim.

## Exit code
Lihat `.agents/tasks/fix/queue-deprecations-docs/verification.md` (perintah, exit code, commit hash).
