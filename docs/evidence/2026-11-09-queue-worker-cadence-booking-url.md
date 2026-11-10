# Queue worker: jadwal per-job dan URL halaman booking publik (2026-11-09)

## Diskusi
Pemilik menanyakan apakah `bookingUrl` ikut membawa token dan apakah URL publik bermasalah. Keputusan: URL halaman publik `{BOOKING_PAGE_BASE_URL}/{eventId}` tanpa token (akses tetap lewat `admissionToken` ke `POST /api/v1/booking-sessions`, NFR-07); tidak ada route baru. Pemilik juga menyetujui butir 1 dan 2 (fallback interval = `WORKER_INTERVAL`; ADR berupa amandemen ADR-022). Temuan awal: `QUEUE_PROMOTOR_INTERVAL_MS`, `QUEUE_SWEEPER_INTERVAL_MS`, `QUEUE_STATUS_INTERVAL_MS` dimuat tetapi tidak pernah dibaca; semua job berjalan pada satu `WORKER_INTERVAL` (5 s), sehingga promotor tidak pernah ~100 ms.

## Perubahan
- `delivery/worker.go`: `runJobs` (satu goroutine + ticker per job, panic-safe, tick terlewat dibuang, menunggu semua job berhenti). `loop` dihapus.
- `delivery/queue_module.go`: `jobEvery`, `buildJobs`, `statusTTL` (TTL status dari `QUEUE_STATUS_INTERVAL_MS`: 4 x 1 s = 4 s, sebelumnya 4 x 5 s = 20 s).
- `config`: validasi tiga interval `> 0`; pesan `WORKER_INTERVAL`/`HEALTH_TIMEOUT`/`HTTP_SHUTDOWN_TIMEOUT` dipisah; `BOOKING_PAGE_BASE_URL` wajib dan divalidasi (`validatePageBaseURL`).
- `usecase/claim.go`: `bookingUrl = base + "/" + url.PathEscape(eventId)`.
- `worker/liveness.go`: Warn "liveness lease held, sweep skipped" tiap 6 skip beruntun per event (tanpa perubahan perilaku).
- Env: `services/queue/.env.example`, `deployments/dev/env/queue.env.example`, `docker-compose.yaml`; `services/queue/.env` lokal (gitignored, di checkout utama) diberi `BOOKING_PAGE_BASE_URL=http://localhost:3000/book`.

## Perubahan kontrak (diketahui pemilik)
- `bookingUrl` pada respons claim kini `{BOOKING_PAGE_BASE_URL}/{eventId}` (sebelumnya `{BOOKING_SERVICE_URL}/book/{entryId}`).
- Env baru wajib `BOOKING_PAGE_BASE_URL`; service gagal start bila kosong (D-24).

## Hasil
- Admit pada event 1 slot, promotor 100 ms, Valkey nyata `qa-vk` :56380 (`TestPromoterCadenceAdmitsNextWaiterQuickly`): pelepasan slot sampai waiter kedua ADMITTED = **83.5 ms, 83.4 ms, 83.6 ms** (3 run), ambang <= 1.5 s.
- Liveness vs promotor pada lease yang sama (`TestPromoterCadenceAndLivenessContendForLease`, `-race`): liveness lolos minimal sekali, `ZCARD active <= kapasitas`.
- Startup smoke biner `queue worker`: tanpa `BOOKING_PAGE_BASE_URL` keluar rc=1 dengan `BOOKING_PAGE_BASE_URL wajib diisi`; `http://` dengan `APP_ENV=prod` ditolak; dengan nilai valid lolos validasi config dan baru gagal di koneksi Postgres (port palsu).
- Circuit breaker Booking (R-1): tidak diukur live (tidak ada Booking nyata pada uji ini); mitigasi tetap breaker yang ada.
- Layanan lokal dari `.agents/tasks/local/run/start.sh` tidak dihentikan; Postgres 5432 dan Valkey 7000-7005 tidak disentuh. Catatan: `make ci` penuh memakai default `TEST_POSTGRES_DSN` ke `localhost:5432` (milik proyek lain, kredensial `rbt` ditolak) sehingga gagal di `pkg/*`; modul queue dijalankan ulang terhadap `qa-pg`/`qa-vk`.

## Dampak frontend (tidak diubah di repo ini)
`rusuh-booking-web` harus membuka `bookingUrl` sebagai rute per event dan mengirim `admissionToken` dari respons claim ke `POST /api/v1/booking-sessions` (body, bukan `Authorization: Bearer`). Drift lain di `08a` (mis. `expiresAt` pada claim, error code claim) hanya dikoreksi pada bagian claim/create-session.

## Di luar cakupan
Cache `ListEventIDs`/`settings.Get` pada 100 ms (R-4), timeout per tick (R-3), perubahan pengali TTL status, perubahan Booking/Payment/Notification/User.
