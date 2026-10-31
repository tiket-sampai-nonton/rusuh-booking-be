# Katalog Event — Filter Lanjutan (genres, rentang tanggal, DATE_DESC)

Tanggal: 2026-11-01 · Alat: Kiro CLI · Service: Booking · Task: 2.R2

Ekstensi `GET /api/v1/events` dengan filter `genres`, rentang tanggal `startDate`/`endDate`, dan sort
`DATE_DESC`. Mempertahankan filter lama (`timeframe`, `city`, `status`, `sort`) dan semantik irisan
AND (SRS-F-28-04). `province` **di luar scope** (kolom `city` sudah ada sejak migrasi 0003 dan sudah
difilter).

## Keputusan desain

### 1. Pemodelan genre — array, bukan tabel join
- Event dapat punya **beberapa** genre → kolom `events.genre text[] NOT NULL DEFAULT '{}'` (migrasi 0006),
  dicocokkan dengan operator overlap `&&`; `genres` kosong = tanpa filter (semua).
- Kosakata genre dipelihara **di frontend** (PRD US-015: Pop, Rock, K-Pop, Jazz, …). Backend
  **accept-and-match** tanpa allowlist: token tak dikenal diterima dan hanya tidak cocok (harmless,
  self-correcting). Menghindari kopling rilis backend ke daftar genre UI.
- Token dinormalisasi: trim, uppercase, `-`/spasi → `_` (K-Pop → `K_POP`), dedupe, dan **urut**
  (bentuk kanonik → set setara menghasilkan satu cache key stabil).

### 2. Rentang tanggal — sargable, bukan cast per baris
- `startDate`/`endDate` (`YYYY-MM-DD`) diinterpretasi sebagai hari kalender **WIB**, inklusif, tiap batas
  opsional. Usecase mengubahnya ke jendela UTC **half-open** `[startUTC, endUTC)` dengan
  `endUTC = WIB-midnight(endDate + 1 hari)` sehingga seluruh hari akhir ikut.
- Predikat repo `e.starts_at >= $start AND e.starts_at < $end` **sargable** (dapat memakai btree
  `idx_events_starts_at`), berbeda dari blok `timeframe` lama yang membungkus kolom dengan
  `(starts_at AT TIME ZONE 'Asia/Jakarta')::date` (tidak sargable). Filter baru sengaja lebih efisien.
- **Independen dari `timeframe`** dan di-AND bila keduanya diberikan — pasangan bertentangan → hasil
  kosong (didokumentasikan sebagai sharp edge).

### 3. Sort DATE_DESC + tiebreaker
- `DATE_DESC` = `e.starts_at DESC`; semua cabang `ORDER BY` memperoleh tiebreaker `, e.id` agar
  deterministik untuk tes dan paginasi masa depan.

### 4. Pemisahan validasi (fail-soft vs fail-hard)
- **Enum** (`timeframe`, `status`, `sort`): *fail-soft* — nilai tak dikenal jatuh ke default (kosmetik;
  tidak merusak halaman).
- **Input terstruktur**: *fail-hard* `400 VALIDATION_ERROR` via `pkg/apperror` —
  format tanggal bukan `YYYY-MM-DD`, rentang terbalik, jumlah genre > 20, panjang token > 64.
- Seluruh validasi di **usecase**; handler hanya parse; handler mengembalikan DTO lewat mapper.

### 5. Pengerasan attack-query
- Semua nilai user lewat parameter posisi pgx (tidak pernah di-interpolasi); `ORDER BY` tetap `switch`
  atas enum ternormalisasi. `genres[]` → `$4::text[]`, tanggal → `*time.Time` (`NULL` bila kosong).
- Cap jumlah (20) & panjang token (64) menolak array raksasa / cache-busting **sebelum** query DB.
- Cache key CDN memakai parameter **terkanonikalisasi** (genre uppercase+dedupe+urut, tanggal UTC) →
  set setara berbagi satu key. WAF + rate-limit edge (CloudFront) adalah backstop volumetrik.
  **Koreksi (2026-11-01):** kanonikalisasi ada di **usecase**, berjalan *setelah* lapisan cache. Edge
  meng-cache pada **query string mentah**, jadi param setara dengan urutan/kapitalisasi berbeda saat ini
  MISS terpisah; menyatukan key memerlukan dukungan edge (redirect kanonik / normalisasi edge) dan belum
  diimplementasikan (edge `nginx.conf` pun belum ada). Baris ini future hardening, bukan perilaku saat ini.

### 6. Ditunda (future, bukan scope)
- Materialisasi `events.min_price_minor` (dipelihara di transaksi `CreateEvent`) agar query list bisa
  membuang `LEFT JOIN ticket_categories` + `GROUP BY`. Belum perlu selama katalog kecil; menambah
  kewajiban pemeliharaan (risiko harga basi) sehingga baru layak saat data membesar.

## JSON kontrak
- `EventSummary.genres` **selalu** array (`[]` bila kosong), bukan `omitempty` — koleksi yang selalu
  di-iterate klien. Berbeda dari `imageUrl`/`bannerUrl` (skalar opsional, tetap `omitempty`).

## Verifikasi
- `go build ./...` bersih; `go test -race -count=1 ./...` hijau (usecase, http, mapper, migrations, delivery).
- `embed_test.go` jumlah migrasi 5 → 6.
- Integrasi PostgreSQL nyata (Docker `rusuh-booking-be-postgres-1`, build tag `integration`):
  `TestEventRepo_ListFilterSortAndDetail` lulus (overlap genre, rentang half-open, urutan DATE_DESC) dan
  seluruh suite postgres hijau (~55s), mengonfirmasi migrasi 0006 diterapkan oleh runner.
- Test case: TC-CAT-06 diperluas (DATE_DESC), TC-CAT-07..10 baru di `docs/analisa/06-TP-RBT-001.md`.

## Berkas utama
- Migrasi: `services/booking/deployments/migrations/0006_event_genre.{up,down}.sql`
- DTO/entity/mapper: `domain/dto/event_catalog.go`, `domain/entity/event.go`, `domain/mapper/booking_mapper.go`
- Usecase: `application/usecase/list_events.go` (`normalizeGenres`, `resolveDateRange`, `wib`)
- Handler: `infrastructure/http/catalog_handler.go` (`parseGenres`)
- Repo: `infrastructure/postgres/event_repo.go` (predikat `&&`, rentang `$5/$6`, `DATE_DESC`)
