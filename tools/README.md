# tools

Skrip yang dipakai CI dan bisa dijalankan lokal:

- `detect-changes.sh` menentukan modul/service yang berubah (dipakai job `changes`).
- `check-coverage.sh <modul> [ambang]` unit test + ambang coverage logika (`make cover`).
- `verify-reports.sh quality|image <dir>` gerbang hasil scan (stage `verify-code-quality` dan `scan-package-verify`).

Alat lain (migrate, seed) ditambahkan saat dibutuhkan.
