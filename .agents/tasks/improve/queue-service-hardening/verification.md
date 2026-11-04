# Verification — Queue Service Hardening (iterasi 1, software-engineer)

Worktree: `.worktrees/improve/queue-service-hardening`, branch `improve/queue-service-hardening`, base `30cae0e`. Semua angka di bawah berasal dari run nyata pada sesi ini; log mentahnya ada di `EV/`.

## 1. Infrastruktur

- **Valkey nyata TERSEDIA untuk proyek ini**: kontainer `qsh-valkey` (`valkey/valkey:8.1-alpine`) pada `localhost:6390`; `INFO server`: `valkey_version:8.1.10`, `server_mode:standalone` (`EV/valkey-info.txt`). `TEST_VALKEY_SERVER='[{"host":"localhost","port":6390}]'` dipakai untuk **semua** run integrasi.
- `localhost:6379` adalah Redis 7 milik proyek lain (`telkomsel_redis`) dan klaster `7000-7005` milik proyek lain: **tidak dipakai** (sesuai rencana R9-4).
- **Tidak ada Postgres proyek ini yang dapat dijangkau** dari host (kontainer `rusuh-booking-be-postgres-1` tidak mem-publish port; `5432` dipegang Postgres proyek lain dengan kredensial berbeda). Akibatnya 2 test Postgres Queue (`TestEventSettings_GetEnsureAndList`, `TestEntryAudit_AppendsForGuestAndAccount`) **SKIP** (`TEST_POSTGRES_DSN` tidak diset) dan tidak terverifikasi di sesi ini. Kode Postgres Queue tidak diubah.

## 2. Gerbang (definition of green) dan hasilnya

`make ci` = `build lint cover isolation contracts` untuk semua modul.

| Perintah | Hasil nyata |
|---|---|
| `make ci` (root, penuh) | **exit 2** pada tahap `cover`, modul `pkg/*` (urutan pertama): `pkg/outbox`, `pkg/postgres`, `pkg/migrate`, `pkg/eventstore`, `pkg/rabbitmq` gagal karena butuh Postgres/RabbitMQ yang tidak tersedia/berkredensial lain (`FATAL: password authentication failed for user "rbt"`). `git diff 30cae0e -- pkg` kosong: modul `pkg/` **tidak disentuh** perubahan ini, kegagalan ini ada di lingkungan, bukan di perubahan (`EV/make-ci-final.txt`). Tahap `build` (6 modul) dan `lint` (6 modul, `0 issues.`) lulus. Karena `make` berhenti di modul pertama yang gagal, `cover`/`isolation`/`contracts` untuk modul lain tidak dijalankan oleh `make ci`; saya menjalankannya terpisah untuk Queue di bawah. |
| `cd services/queue && gofmt -l .` | kosong |
| `go build ./...` | exit 0 |
| `go vet ./...` dan `go vet -tags integration ./...` | exit 0 / exit 0 |
| `go test -race -count=1 ./...` | **exit 0**, 19 paket `ok`, 0 FAIL (`EV/final-unit.txt`) |
| `go test -tags integration -race -count=1 -v ./...` (Valkey 8.1.10 nyata) | **exit 0**, `--- PASS` 406, `--- FAIL` 0, `--- SKIP` 2 (hanya dua test Postgres di atas; **0 SKIP pada paket Valkey/worker**) (`EV/final-integration.txt`) |
| `golangci-lint run --config .golangci.yml ./...` (services/queue; juga semua modul) | `0 issues.` |
| `GO_TEST_TAGS=integration bash tools/check-coverage.sh services/queue 80` | `queue: coverage logika 86.1% (ambang 80%)`, exit 0 |
| `cd services/queue && GOWORK=off go build ./...` (isolation) | exit 0 |
| `make contracts` | exit 0 |
| `grep -rn "MarkMissed\|FIFO_SKIPPED" services/queue --include=*.go` (di luar `_mock`) | kosong |
| `grep -rnE 'slog\.(Info\|Warn\|Error\|Debug).*"(nik\|email\|token)"' services/queue/internal` | kosong |

## 3. Bukti test-first (merah dulu) per tugas

Merah runtime terhadap kode lama (output di `EV/`): T02 (`T02-red.txt`: 9 test gagal), T03/T04 (`T03-T04-red.txt`), T06 (`T06-red.txt`: pelepasan lease basi menghapus lease baru), T07 (`T07-red.txt`: `panic: integer divide by zero`), T10/T11 (`T10-T11-red.txt`: 8 test gagal, termasuk `StrangerCausesNoBookingCall`), T12 handler (`T12-handler-red.txt`: 3 test), T14/T15 (`T14-T15-red.txt`), `MarkNotifiedSoon` (galat `valkey nil message`).
Merah karena API belum ada (gagal kompilasi): T08 (`T08-red.txt`), T09 (`T09-red.txt`), T17 (`T17-red.txt`), T24 (`T24-red.txt`), T12 usecase (`Sleep` tidak ada di `JoinDeps`), T05 (`mapPromote` tidak ada).
Tidak ada merah runtime terhadap kode lama untuk perilaku promoter (T13/T22/T25/T26) karena test baru memakai API baru; sebagai gantinya dipakai **mutasi**: mengembalikan release jumper lama membuat 3 test gagal (`EV/T22-mutant-release.txt`); menghapus fence `promote.lua` membuat `TestPromote_FenceRejects...` gagal (`EV/T05-mutation-nofence.txt`).

## 4. Bukti mutasi hunk-level (worktree sekali pakai, sudah dihapus)

- T20 (`EV/T20-mutations.txt`): 6 mutan, `vet` exit 0 pada semuanya, semuanya `killed=True`, setelah dikembalikan test lulus. M-b semula lolos -> sub-test `b_mixed_page_clears` diperkuat (orphan > limit) lalu terbunuh.
- T27 (`EV/T27-mutations.txt`): M27-a `fail_count=5` (4 sub-test + induk), M27-b `fail_count=3`, M27-c `fail_count=1`, `skip_count=0`, setelah dikembalikan lulus.
- Prosedur dijalankan dengan skrip yang menerapkan dan mengembalikan satu hunk (bukan file patch terpisah seperti di rencana).

## 5. Checklist gerbang penulis

- [x] Test per kriteria penerimaan ada lebih dulu (lihat §3 untuk jenis merahnya).
- [x] Gerbang tingkat Queue dijalankan; exit code nyata di §2. **Tidak dapat diklaim**: `make ci` penuh hijau (exit 2 di `pkg/*`, lingkungan).
- [x] Uji invarian: INV-1/2 (`TestAdmissionRace/Det/Stale/Diff`, `TestPromote_*`), INV-3 (`TestJoinHandler_Forged*`), INV-4 (deadline lampau tidak dipromosikan), INV-5 (PAID pada WAITING IGNORED; `end_entry` idempoten), INV-6 (kegagalan notify tidak mengubah hasil). Duplicate delivery consumer: `TestConsumer_JourneyEnded_DoubleDeliveryIdempotent`.
- [x] Diff = `services/queue/**` + `_mock/` (hasil `go generate`) + dokumen yang dideklarasikan; tidak ada migrasi diterapkan, tidak ada dependensi baru (`go.mod` tidak berubah).
- [x] Tidak ada rahasia/PII di log atau payload event (grep di §2 kosong; test memeriksa log tanpa email/NIK).
- [x] Bookkeeping: `docs/ai-log/README.md` ditambah baris; `docs/evidence/2026-11-04-queue-hardening-implementation.md` dibuat.

## 6. Penyimpangan dari rencana dan yang tidak dikerjakan (jujur)

1. **`compensation` bukan paket `application/compensation`**: `golangci-lint` (depguard `queue-infrastructure`) melarang adapter promoter mengimpor `application`. Keputusan lepas dipindah ke `domain/valueobject.ShouldRelease` (murni) dan `outbound.ReleaseTerminal` (di sebelah port, dipakai join dan promoter). Fungsi dan test tetap sama.
2. **TC-QUE-19c cepat berbasis fake (`admission_race_test.go`, tanpa tag) tidak ditulis.** Bukti konkurensi nyata ada di `TestAdmissionRace` (Valkey nyata, `-race`, 6 iterasi x 8 joiner + loop promoter + sampler `ZCARD`).
3. Bukti per tugas disimpan sebagai berkas teks di `EV/` + `findings.md`, bukan `EV/Tnn.md` terpisah per tugas.
4. T00 (baseline `make ci` pada pohon tak berubah) hanya mencatat build/vet/unit hijau (`EV/T00-baseline.txt`) dan integrasi Valkey hijau; `make ci` penuh tidak dijalankan pada pohon dasar karena kegagalan `pkg/*` bersifat lingkungan dan `pkg/` tidak berubah.
5. Dua test Postgres Queue SKIP (lihat §1).
6. Ukuran nyata `claim_window_seconds`/`WORKER_INTERVAL` Booking di lingkungan deploy tidak diukur.
7. Perilaku terlihat pengguna yang berubah dan masih provisional (QSH-OQ-3, QSH-OQ-4) tercantum di `findings.md` §2; semuanya sudah ditetapkan di desain rev 8 §2a / rencana rev 9 dan ditandai di SRS/OpenAPI; tidak ada temuan baru di luar desain yang mengubah perilaku pengguna, sehingga tidak ada `warning` yang dikirim.
