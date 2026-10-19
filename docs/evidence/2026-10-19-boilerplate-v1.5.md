# Evidence — Boilerplate v1.5 (19 Oktober 2026)

Keputusan sumber: `Perbedaan-Desain-BE-Kita-vs-BE2.xlsx` (D-01..D-48). Lingkungan: Go 1.26, PostgreSQL 16, Valkey/Redis lokal. RabbitMQ dan Docker tidak tersedia di lingkungan ini, jadi bagian itu **belum** diverifikasi (lihat akhir dokumen).

## 1. Build, vet, unit test

Setiap modul (`pkg`, `services/{queue,booking,payment,notification,user}`) dijalankan tanpa `go.work` (`GOWORK=off`, setara `make isolation`):

| Pemeriksaan | Hasil |
|---|---|
| `go vet ./...` | Lulus semua modul |
| `go test -race -count=1 ./...` | Lulus semua modul |
| `gofmt -l` | Tidak ada file |

## 2. Integration test (`-tags integration`)

| Modul | Coverage | Catatan |
|---|---|---|
| `pkg` (rata-rata) | 82,9% | `pkg/rabbitmq` 58,2%: test integration broker dilewati karena RabbitMQ tidak tersedia |
| `pkg/eventstore` | 94,4% | Append, optimistic concurrency (`ErrConcurrency`), trigger append-only, tolak field PII |
| `pkg/migrate` | 87,3% | Up, Down, Status, advisory lock, deteksi checksum berubah |
| `services/user` (logika) | 94,4% | Usecase 98,8%; postgres 88,6%; valkey 83,8% |
| Queue, Booking, Payment, Notification (logika) | 100% | Boilerplate health + config |

## 3. Migrasi

Kelima schema (`queue`, `booking`, `payment`, `notification`, `users`) diterapkan dengan binary `<svc> migrate up` ke PostgreSQL 16: 28 tabel. Menjalankan ulang menerapkan 0 migrasi. `migrate down` lalu `up` berhasil.

## 4. End-to-end User Service (16 langkah)

API `user` dijalankan terhadap PostgreSQL + Valkey lokal dan SMTP palsu:

| # | Langkah | Hasil |
|---|---|---|
| 1 | `GET /health` | `UP` (postgres, valkey) |
| 2 | Minta OTP | 202, `expiresInSeconds` 300, `resendAfterSeconds` 60 |
| 3 | Minta ulang terlalu cepat | `OTP_RESEND_TOO_SOON` |
| 4 | Kode diterima di email | 6 digit |
| 5 | Kode salah | 401 `OTP_INVALID`, `attemptsLeft` 4 |
| 6 | Verifikasi benar | 200, token Bearer, email tersamar |
| 7 | `/me` tanpa token | 401 `UNAUTHORIZED` |
| 8 | Ubah profil (nama, NIK) | NIK tersamar `3174********0001` |
| 9 | NIK tidak valid | 400 `VALIDATION_ERROR` |
| 10 | Refresh | 200, token dirotasi |
| 11 | Pakai refresh lama | `SESSION_INVALID` |
| 12 | Token baru ikut dicabut (deteksi reuse) | `SESSION_INVALID` |
| 13 | Logout | 204 |
| 14 | Body dengan field asing | 400 |
| 15 | Audit login | `LOGIN_OK`, `OTP_INVALID`, `OTP_SENT`, `REFRESH_OK`, `REFRESH_REUSED` tercatat |
| 16 | Data tersimpan | Email dan NIK terenkripsi; NIK asli tidak ada di ciphertext |

Respons verifikasi divalidasi terhadap skema `TokenResponse` di `user.yaml`.

## 5. Kontrak

7 file OpenAPI (`queue`, `booking`, `payment`, `notification`, `user`, `internal-booking`, `internal-payment`) valid menurut kin-openapi. 10 JSON Schema event + envelope ada di `docs/contracts/events/`.

## 6. Belum diverifikasi

| Item | Cara verifikasi |
|---|---|
| Integration `pkg/rabbitmq` (topologi, retry, DLQ) | `make infra && make test-integration` |
| `docker compose up` dan build image | `make keys && make up` |
| golangci-lint (depguard) | `make lint` |
| Run pertama CI GitHub Actions | Push branch, lihat job `ci-ok` |
| `go.sum` | `make tidy`, lalu commit `go.mod` dan `go.sum` |
