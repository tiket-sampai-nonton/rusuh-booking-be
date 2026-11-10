# rusuh-booking-be

Backend pemesanan tiket dengan waiting room: lima service Go (`queue`, `booking`, `payment`, `notification`, `user`) dan
pustaka bersama `pkg/`. Dokumen acuan: `docs/analisa/` (PRD, SRS, HLD, SDD, ADR), spec per service di `specs/`, kontrak di
`docs/contracts/`. Cara tim memakai AI dijelaskan di `docs/ai/README.md`.

Aturan proyek yang sama dipakai Kiro, jadi tidak disalin ulang di sini:

@.kiro/steering/product.md
@.kiro/steering/tech.md
@.kiro/steering/structure.md
@.kiro/steering/code-principles.md
@.kiro/steering/invariants.md
@.kiro/steering/security-privacy.md
@.kiro/steering/testing-review.md

## Cara kerja
- Task, requirement, atau fitur baru: pakai workflow **`/feature <deskripsi>`** (`.claude/skills/feature/`): planning dengan
  persetujuan, TDD, review keamanan, quality gate, auto-fix maksimal 3 putaran.
- Jangan commit, push, merge, atau membuat PR tanpa persetujuan eksplisit pemilik.
- Ikuti konvensi yang sudah ada (struktur folder, gaya tes, format error `{"meta":{"code","message"}}`) daripada pola baru.
- Kode harus bisa dijelaskan pemiliknya saat assessment: hindari over-engineering, DTO sama dengan kontrak, ambil kolom yang
  dibutuhkan saja, dokumen ringkas.

## Kepemilikan kode (lihat juga `CODEOWNERS`)
- `arnu12rahman`: notification, payment, dan platform (`pkg/`, `deployments/`, `tools/`, CI).
- `nuzirwan`: user, queue, booking, dan `tests/`. Kerjakan bagian yang hanya menyentuh milik sendiri lebih dulu; file milik
  pemilik lain disentuh terakhir dan dengan koordinasi.

## Perintah
- Go dijalankan dengan `GOWORK=off` (CI juga), atau lewat `make`.
- Perintah resmi quality gate (`/feature` memakai ini):
  - test: `cd pkg && go test -race -count=1 ./...`, lalu `go test -race -count=1 ./...` di tiap `services/<s>`
  - lint: `gofmt -l` pada file Go yang berubah terhadap `origin/main`
  - typecheck: `cd pkg && go vet ./...` lalu `make isolation`
- `make up` (stack lokal Docker), `make test`, `make cover`, `make lint`, `make contracts`, `make ci`.
- Uji beban: `tests/k6/` (`npm run test:smoke`, `test:load`, dan seterusnya; lihat `tests/k6/README.md`).

## Gotcha lokal
- RabbitMQ dan Valkey kadang hidup tanpa port terpublikasi: `docker compose up -d --force-recreate --no-deps rabbitmq`
  (atau `valkey`); RabbitMQ butuh sekitar 60 detik sebelum menerima koneksi.
- API dan worker satu service memakai `APP_PORT` yang sama; jalankan worker lokal dengan port lain (mis. 8093).
- Webhook Midtrans ke lokal: `cloudflared` quick tunnel hanya jalan dengan `--protocol http2`; arahkan per transaksi lewat
  `MIDTRANS_NOTIFICATION_URL`. Sandbox: `custom_expiry` minimal 30 detik.
- Skema database lokal kadang diubah manual oleh pemilik: cek `migrate status` dan tanyakan sebelum menganggap skema hanya
  berasal dari migrasi.

## Keamanan
- Jangan membaca atau menyalin isi `.env`, kunci di `keys/`, atau kredensial, kecuali task memerlukannya, dan jangan pernah
  menampilkan nilai secret.
- Jangan menulis data pribadi asli (NIK, email, nama, data pembayaran) ke log, tes, fixture, atau output. Data uji memakai NIK
  fiktif dan domain email `example.test`.

## Komunikasi dan commit
- Balas dalam Bahasa Indonesia, kecuali pemilik menulis dalam bahasa lain.
- Commit memakai conventional commit, tanpa trailer `Co-Authored-By` dan tanpa atribusi atau tautan sesi Claude; identitas
  author dan committer asli dipertahankan.
