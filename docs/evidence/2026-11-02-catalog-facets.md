# Katalog Event — Endpoint Facet (`GET /api/v1/events/facets`)

Tanggal: 2026-11-02 · Alat: Kiro CLI · Service: Booking · Task: 2.R3

> **Revisi 2026-11-02 (drill-down).** Desain awal (di bawah) menjadikan facet endpoint tanpa parameter —
> mengembalikan seluruh kosakata katalog. Atas masukan UX (daftar event hanya menampilkan 2 kota tetapi
> facet menampilkan semua kota → pilihan filter tidak cocok dengan daftar), facet diubah menjadi
> **drill-down**: menerima parameter filter yang sama dengan `GET /api/v1/events` dan mengikuti event list
> saat ini. Setiap facet menerapkan semua filter aktif **kecuali dirinya sendiri** (`cities` mengabaikan
> `city`, `genres` mengabaikan `genres`) sehingga nilai cocok dengan daftar tanpa menutup pilihan lain pada
> dimensi yang sedang dipilih (perilaku faceted-search standar). Perubahan kunci vs desain awal:
> - Predikat filter `ListEvents` diekstrak menjadi `eventFilterWhere` + `filterArgs` (satu sumber
>   kebenaran; `ListEvents` dan `ListFacets` memakainya — bukan duplikasi SQL).
> - `IEventRepo.ListFacets(ctx, q, dim)` mengembalikan satu dimensi (`outbound.FacetDimension`:
>   `FacetCities`/`FacetGenres`); usecase memanggilnya dua kali, masing-masing dengan filter dimensi itu
>   dikosongkan.
> - `IEventFacetsUsecase.GetFacets(ctx, q)` menormalkan query dengan `normalizeEventQuery` yang sama →
>   **paritas validasi** dengan daftar (enum fail-soft; tanggal cacat/rentang terbalik/cap genre → `400`).
> - Handler mem-parse filter yang sama seperti `listEvents` (tanpa `sort`; facet selalu urut alfabet).
> - Kontrak `booking.yaml` menambah query params `timeframe,city,genres,startDate,endDate,status` +
>   respons `400` pada path `/events/facets`.
> Sisa keputusan (satu endpoint, tanpa hitungan per-nilai, lantai published+non-cancelled, always-array,
> rute statis menang atas `{eventId}`, tanpa migrasi) tetap berlaku.
>
> **Revisi 2026-11-02 (option c — alignment facet↔daftar).** Masukan lanjutan: pada filter gabungan yang
> tidak cocok satu event pun (mis. `?city=Bandung&genres=ROCK`, Bandung tak punya event ROCK), drill-down
> tetap menawarkan opsi (cities/genres non-kosong) padahal daftar event kosong — facet "tidak selaras"
> dengan daftar. Diperbaiki dengan **guard**: usecase menghitung event di bawah **query penuh** (semua
> filter, tanpa dimensi dibuang) lewat `IEventRepo.CountEvents(ctx, q)` (predikat `eventFilterWhere` yang
> sama). Bila hitungan = 0, kembalikan `{cities:[], genres:[]}` tanpa menjalankan query drill-down. Jadi:
> daftar kosong ⇒ facet kosong; daftar non-kosong ⇒ drill-down (tiap facet buang filter sendiri). Properti
> yang dijamin: *tidak ada nilai facet yang, bila dipilih, mengosongkan hasil mengingat filter lain yang
> aktif*. Catatan: ini **hanya** di endpoint facet; endpoint daftar `GET /api/v1/events` tetap `AND` semua
> filter seperti biasa (tidak membuang dimensi apa pun). Diverifikasi live via curl (lihat Verifikasi).
>
> **Revisi 2026-11-02 (performa — satu round-trip).** `GetFacets` semula melakukan **tiga** round-trip
> PostgreSQL berurutan (`CountEvents` guard + `ListFacets(cities)` + `ListFacets(genres)`). Karena satu
> request menahan koneksi pool selama tiga query, ini melipatgandakan waktu-tahan koneksi di bawah beban
> (yang relevan saat lonjakan katalog). Direfaktor menjadi **satu round-trip**: satu query dengan tiga CTE
> (`cnt`, `cities`, `genres`), masing-masing memakai predikat bersama `eventFilterWhere` yang **dinomori
> ulang** per blok parameter (`$1–6` filter penuh untuk count, `$7–12` drop `city` untuk cities, `$13–18`
> drop `genres` untuk genres) via helper `filterWhereAt(base)`, dan tiap dimensi di-`array_agg(... ORDER
> BY)`. Perubahan API internal: `IEventRepo.ListFacets`+`CountEvents`+`FacetDimension` diganti satu metode
> `Facets(ctx, q) (EventFacets, int, error)`; drill-down kini intrinsik di SQL (bukan tugas usecase memanggil
> dua kali). Perilaku/kontrak eksternal **tidak berubah** (drill-down, guard option c, always-array, lantai
> visibilitas identik). Catatan: implementasi awal `filterWhereAt` memakai string-replace naif yang merusak
> placeholder multi-digit (`$10`→`$70`); diganti regex token utuh `\$\d+` dan **diverifikasi terhadap
> PostgreSQL nyata** — integration test `TestEventRepo_Facets_*` (4) + seluruh suite postgres Booking lulus.

---

Menambah endpoint publik `GET /api/v1/events/facets` yang mengembalikan nilai filter yang tersedia
(`{cities, genres}`) agar frontend dapat mengisi pilihan filter kota dan chip genre tanpa menebak atau
mengeruk dari halaman `GET /api/v1/events`. Memperluas Requirement 1 (kriteria 12–14), bukan requirement
baru. Tanpa migrasi — kolom `events.city` (indeks `idx_events_city`, migrasi 0003) dan `events.genre
text[]` (GIN `idx_events_genre`, migrasi 0006) sudah ada.

## Keputusan desain

### 1. Satu endpoint facet, bukan digabung ke katalog maupun per-facet
- Satu endpoint `GET /api/v1/events/facets` mengembalikan `{cities, genres}` sekaligus — satu round
  trip, cache TTL tersendiri, terpisah dari respons daftar `GET /api/v1/events`.
- **Bukan** digabung ke `meta` daftar event (facet berubah jauh lebih jarang daripada daftar; cache 2s
  daftar tidak cocok untuk facet) dan **bukan** dua endpoint terpisah `/cities` + `/genres` (keputusan
  pemilik 1.b).

### 2. Daftar polos, tanpa hitungan
- `cities` dan `genres` hanya daftar nilai distinct terurut, tanpa `count` per nilai (keputusan 2.a;
  YAGNI). Penambahan count adalah pengerasan masa depan bila UI memerlukannya.

### 3. Visibilitas sama dengan katalog
- Facet hanya diambil dari event yang terlihat di katalog publik: `published_at IS NOT NULL AND status
  <> 'CANCELLED'` — predikat yang sama dengan `ListEvents` (R1.4). Event draf dan dibatalkan tidak
  pernah menyumbang nilai facet (keputusan 3.a).

### 4. SQL — satu query (tiga CTE) yang memakai indeks
- Satu round-trip mengembalikan count filter-penuh + dua daftar DISTINCT, lewat tiga CTE yang berbagi
  predikat `eventFilterWhere` (dinomori ulang per blok parameter oleh `filterWhereAt`):
  - `cnt`: `SELECT count(*) ... <filter penuh $1–6>` — sinyal guard option c.
  - `cities`: `SELECT DISTINCT city ... <filter kecuali city, $7–12> AND city <> ''` (btree `idx_events_city`).
  - `genres`: `SELECT DISTINCT g FROM events e, unnest(e.genre) g ... <filter kecuali genres, $13–18>`
    (GIN `idx_events_genre`) — `unnest` meratakan `text[]`, DISTINCT men-dedupe lintas event.
- Hasil akhir: `SELECT (SELECT n FROM cnt), array_agg(cities ORDER BY city), array_agg(genres ORDER BY g)` —
  terurut server (output deterministik), `COALESCE(..., '{}')` sehingga kosong = array kosong, bukan NULL.

### 5. Kontrak always-array
- `EventFacets.cities`/`genres` **selalu** array (`[]` bila kosong), bukan `null`. Dijamin oleh
  `mapper.ToEventFacets` (nil slice → `[]string{}`), sama seperti `EventSummary.genres`. Normalisasi ada
  di usecase/mapper (single source of truth), bukan di handler.

### 6. Rute statis menang atas wildcard
- `GET /api/v1/events/facets` didaftarkan bersama `GET /api/v1/events/{eventId}`. Go 1.22+ `ServeMux`
  memilih pola yang lebih spesifik (statis) sehingga `/facets` tidak pernah tertangkap sebagai
  `eventId = "facets"`. Dibuktikan oleh test yang membangun mux dan melayani kedua rute.

## Lapisan (hexagonal)
- Port: `outbound.IEventRepo.Facets(ctx, q) (EventFacets, int, error)`, `inbound.IEventFacetsUsecase.GetFacets(ctx, q)`.
- Usecase: `application/usecase/list_facets.go` — normalisasi (paritas validasi daftar), satu panggilan
  `Facets`, guard option c (count = 0 → `{[],[]}`), lalu `mapper.ToEventFacets`.
- Repo: `infrastructure/postgres/event_repo.go` — `Facets` (satu query tiga CTE di atas; drill-down intrinsik).
- Handler: `infrastructure/http/catalog_handler.go` — `getFacets` (cache 2s, `security: []`), route
  didaftarkan di `CatalogHandler.Register`; di-wire di composition root `internal/delivery/booking_module.go`.
- DTO/entity: `dto.EventFacets`, `entity.EventFacets`.

## JSON kontrak
- `docs/contracts/openapi/booking.yaml`: path `/api/v1/events/facets` (operationId `listEventFacets`,
  tag `catalog`, `security: []`, `200` membungkus `EventFacets`) + schema `EventFacets` (`cities`,
  `genres` array string, keduanya `required`).

## Verifikasi
- `go build ./...` bersih; unit test (mapper nil→[], usecase pass-through + normalisasi + empty-guard +
  paritas validasi + error propagation, handler 200 + forward filter + route-precedence) hijau.
- Integrasi PostgreSQL nyata (build tag `integration`): `TestEventRepo_Facets_VisibilityAndDistinct`,
  `TestEventRepo_Facets_DrillDown`, `TestEventRepo_Facets_EmptyWhenNoVisibleEvents`, dan
  `TestEventRepo_Facets_CountSharedPredicateVsDrillDown` lulus — distinct/terurut, mengecualikan
  `CANCELLED`/draf, dedupe `POP` lintas event, drill-down (filter satu dimensi tak mempersempit facet-nya
  sendiri) dalam satu query, dan count filter-penuh = 0 pada filter gabungan bertentangan meski facet
  drill-down masing-masing non-kosong. Seluruh suite postgres Booking hijau (dijalankan via
  `TEST_POSTGRES_DSN` ke PostgreSQL nyata; harness membuat & men-drop database throwaway-nya sendiri).
- **Verifikasi live (service berjalan + seed demo):** `go run ./cmd api` di :8082 terhadap PostgreSQL
  `berebut` nyata; curl membandingkan `/events` vs `/events/facets` pada beberapa query. Hasil kunci:
  `?city=Surabaya` (hanya ROCK) → genres facet `[ROCK]` (JAZZ/POP tidak muncul); `?city=Bandung&genres=ROCK`
  (daftar kosong) → facet `{[],[]}` (guard option c); `?city=Jakarta&genres=JAZZ` (daftar 1 event) →
  drill-down penuh. Memastikan facet selalu selaras dengan daftar; daftar event sendiri tetap `AND` ketat.
- `make contracts` hijau (OpenAPI 3.1 valid).
- Test case: TC-CAT-11 (`docs/analisa/06-TP-RBT-001.md`).

## Out of scope (YAGNI)
Tanpa hitungan per-nilai di respons (count filter-penuh dari `Facets` hanya dipakai internal untuk guard,
bukan count yang dikembalikan ke klien), tanpa caching tambahan selain `cache2s`, tanpa facet provinsi,
tanpa endpoint per-facet, tanpa perubahan pada filter enum (`timeframe`/`status`/`sort`).

## Berkas utama
- DTO/entity/mapper: `domain/dto/event_catalog.go`, `domain/entity/event.go`, `domain/mapper/booking_mapper.go` (`ToEventFacets`)
- Port: `port/outbound/ports.go` (`IEventRepo.Facets`), `port/inbound/usecases.go` (`IEventFacetsUsecase`)
- Usecase: `application/usecase/list_facets.go` (satu panggilan `Facets` + guard option c)
- Repo: `infrastructure/postgres/event_repo.go` (`eventFilterWhere`/`filterArgs`/`filterWhereAt` bersama, `Facets` satu query tiga CTE) + `facets_integration_test.go`
- Handler: `infrastructure/http/catalog_handler.go` (`getFacets`) + `handlers_test.go`
- Composition root: `internal/delivery/booking_module.go`
- Kontrak: `docs/contracts/openapi/booking.yaml`
- Spec: `specs/booking-service/{requirements,design,tasks}.md`
