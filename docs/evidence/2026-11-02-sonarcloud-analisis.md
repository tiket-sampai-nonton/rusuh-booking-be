# Analisis SonarCloud dan perbaikannya (2 Nov 2026)

Data dibaca dari API publik SonarCloud (project public, tanpa token) untuk branch `main`. Memindai ulang butuh `SONAR_TOKEN` di CI, jadi hasil setelah perbaikan baru terbukti setelah run berikutnya.

## Kondisi awal
Quality Gate ERROR karena tiga kondisi pada kode baru: security rating E (ambang A), coverage 46,0% (ambang 80%), duplikasi 5,7% (ambang 3%). Enam vulnerability: 1 blocker, 1 critical, 1 major, 3 minor.

## Temuan dan keputusan
| Temuan | Lokasi | Keputusan |
|---|---|---|
| `secrets:S6698` (blocker) dan `go:S2068` (major): password Postgres di DSN bawaan | `tests/e2e/helpers/env.go` | **Diperbaiki di sumber.** DSN wajib dari `TEST_POSTGRES_DSN` tanpa nilai bawaan; `make test-e2e` mengisinya. Tidak dikecualikan dari analisis supaya masalah serupa di folder itu tetap terlihat. Ini penyebab security rating E. |
| `go:S5542` (critical) | `fcm/push_sender.go` | **False positive.** Itu tanda tangan JWT RS256 (PKCS#1 v1.5) yang diwajibkan OAuth service account Google, bukan enkripsi; PSS ditolak. Diberi `// NOSONAR` dengan alasan. |
| `go:S2092` (minor, 2 tempat) | `payment_handler.go`, `queue join_handler.go` | Flag `Secure` ditentukan per environment (false hanya untuk http lokal). Diberi `// NOSONAR` dengan alasan. |
| `go:S5332` (minor) | `smtp/email_sender.go` | STARTTLS dipakai bila ditawarkan; tanpa TLS hanya Mailpit lokal, dan `smtp.PlainAuth` menolak mengirim kredensial tanpa TLS ke host non-lokal. Diberi `// NOSONAR` dengan alasan. |

Bila pemilik lebih suka menutup temuan di UI SonarCloud (Resolve as False Positive / Accepted) daripada komentar di kode, hapus `NOSONAR` dan lakukan di UI.

## Coverage: penyebabnya bukan kurang tes
Coverage lokal tiap modul 82-92% (pkg 90,9, user 92,5, queue 84,0, booking 82,7), tetapi SonarCloud melaporkan 46%. Banyak file bernilai 0% padahal punya tes (mis. `user/.../auth.go` 0 dari 120 baris, `pkg/migrate/migrate.go` 0 dari 104). Sebabnya job `case-coverage` hanya menjalankan modul yang berubah pada push itu (`tools/detect-changes.sh`), sehingga laporan coverage modul lain tidak ada di analisis dan terbaca 0%. Perbaikan: push ke main menjalankan semua modul (`PUSH_BEFORE` diabaikan). PR tetap hanya modul yang berubah (Sonar menilai kode PR).

Folder `tools/`, `tests/e2e/`, dan `pkg/` dikecualikan dari coverage sesuai permintaan (`sonar.coverage.exclusions`), tetap dianalisis.

## Duplikasi: sebagian besar kerangka per service
2.801 baris duplikat dari 28.234 baris baru (9,9%): `cmd/` 877, `internal/delivery/` 579, `infrastructure/` 595, `domain/` 320, lain-lain 430. `cmd/` (root, migrate) dan `delivery/` (clients, worker) adalah kerangka yang sengaja disalin tiap service agar bisa berdiri sendiri (`make isolation`); keduanya dikecualikan dari hitungan duplikasi lewat `sonar.cpd.exclusions`. Sisanya sekitar 4,8%, di atas ambang 3%. Menurunkannya butuh refactor ke `pkg` (adapter Postgres, Valkey, RabbitMQ yang sama di banyak service), yang menyentuh kode semua service dan belum dikerjakan.

## Belum terbukti
Seluruh angka "sesudah" menunggu scan setelah merge/push. Tidak ada yang dinyatakan lulus sebelum itu.
