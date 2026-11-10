# Pengecualian SonarCloud dan perapian skrip shell — 2026-11-10

Konteks: quality gate SonarCloud lolos setelah `tests/k6/**` dikecualikan dan dua perbaikan `report.mjs`. Sisa 198 issue
terbuka (177 maintainability, 21 reliability); jendela "new code" 24 hari mencakup hampir seluruh repo (dimulai 2026-10-17),
sehingga angka new code dan overall hampir sama.

## Pengecualian yang disengaja (`sonar-project.properties`, `sonar.issue.ignore.multicriteria`)

| Butir | Aturan | Lokasi | Jumlah saat dicatat | Alasan |
|---|---|---|---:|---|
| e1 | godre:S8196 | `services/**/port/**` | 61 | Antarmuka port memakai awalan `I` (konvensi hexagonal repo), bukan akhiran "-er" |
| e2 | Web:S1827 | `services/notification/assets/templates/**` | 49 | Atribut `width` dibutuhkan email client (Outlook mengabaikan CSS width pada tabel/gambar) |
| e3 | godre:S8239 | `pkg/httpx/server.go` | 1 | Shutdown berjalan setelah ctx dibatalkan; wajib context baru |
| e4 | godre:S8239 | `pkg/rabbitmq/consumer.go` | 1 | Republish harus selesai walau ctx konsumen dibatalkan agar pesan tidak hilang |

Total 112 issue. Tidak diabaikan: `css:S7924` (7, kontras warna template email) karena itu masalah aksesibilitas nyata,
dan semua aturan lain (kompleksitas fungsi, shadowing `min`/`max`, dan sebagainya) tetap menjadi backlog terbuka.

## Skrip shell (`tools/*.sh`): `[` menjadi `[[` (shell:S7688, 19 issue pada 18 baris)

Perubahan mekanis tanpa perubahan perilaku. Semua tes berupa operasi sederhana dengan operand ter-quote (tanpa `-a`/`-o`,
glob tak ter-quote, atau variabel kosong tak ter-quote), dan semua skrip berawalan `#!/usr/bin/env bash` serta dipanggil
dengan `bash` (Makefile dan CI).

Bukti: skrip lama (dari `HEAD`) dan baru dijalankan dengan masukan yang sama lalu stdout, stderr, dan exit code dibandingkan.
- `detect-changes.sh`: 18 kasus (push, pull_request, workflow_dispatch, basis kosong/ZERO/tidak valid/di luar riwayat, dan
  repo sintetis untuk satu service, dua service, kontrak, dokumen saja, bagian bersama, `tools/`, diff kosong): identik.
- `verify-reports.sh`: mode quality dan image, laporan bersih, ada temuan, rusak, kosong, folder tidak ada, mode salah,
  tanpa argumen, `IMAGE_FAIL_SEVERITY` ganda: identik.
- `check-coverage.sh`: modul `pkg` dengan ambang lulus dan gagal, dengan `GITHUB_STEP_SUMMARY`: identik.
- `e2e.sh`: skrip utuh butuh Docker dan DSN sehingga tidak dijalankan; dua ekspresi penjaganya diuji di empat kombinasi
  kunci/`.env` ada atau hilang: identik.

Tidak diubah: `godre:S8239` di dua kode Go di atas (disengaja, lihat e3 dan e4).
