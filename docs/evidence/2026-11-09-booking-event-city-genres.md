# Booking: city/genres pada admin create/PATCH dan `minPriceMinor` detail (2026-11-09)

Workflow `booking-event-city-genres`. Perubahan hanya di Booking Service (+ kontrak/dokumen). Tanpa migrasi, tanpa dependensi baru, tanpa rute baru.

## Keputusan
- D1 (ASUMSI, menunggu konfirmasi pemilik): `city`/`genres` boleh diubah setelah penjualan dibuka. `guard_event_rules` hanya mengunci `max_tickets_per_order`, `max_tickets_per_buyer_nik`, `allow_mixed_categories`, `rules_locked_at`; invarian 1–6 tidak tersentuh (diuji TC-CAT-15: `event_supply`, `category_inventories`, `rules_locked_at` identik). Jika pemilik menolak: migrasi baru `0NNN` (file baru saja) memperluas guard, lalu TC-CAT-15 dibalik.
- D2: city di-trim, huruf asli dipertahankan, maks 100 rune; hanya spasi → `''`.
- D3: genres ditolak (400 `VALIDATION_ERROR`) bila melebihi batas filter yang sama (20 token, 64 byte); token kosong dibuang. `normalizeGenres` dipindah ke `genres.go` tanpa perubahan perilaku (dipakai filter dan jalur tulis admin).
- D4: PATCH memakai pointer: tanpa/`null` = tidak berubah, `""`/`[]` = kosongkan. Berbeda dengan `description` (null = hapus); dicatat di OpenAPI.
- D5: `GetDetail` menghitung `MinPriceMinor` dari kategori yang sama yang dimuatnya (kategori habis dihitung; 0 bila tanpa kategori) = nilai `ListEvents`.

## Temuan / batasan diketahui
- Facet tidak melipat huruf besar-kecil city: `Jakarta` dan `jakarta` muncul sebagai dua entri (`SELECT DISTINCT e.city`), sedangkan filter city case-insensitive. Diamati oleh `TestCityGenres_FacetsDoNotFoldCityCase`; di luar cakupan.
- OQ-8: genres/facets belum punya ID SRS sebelumnya (drift SRS/TP); hanya dicatat, tidak diselesaikan. ID baru SRS-F-28-07/08 dan TC-CAT-12..15, TC-EVT-06 adalah ID DIUSULKAN.
- R5.3 (campuran kolom terkunci + city/genre dalam satu request) tidak dapat dibentuk lewat API (`DisallowUnknownFields` → 400), jadi diverifikasi di tingkat trigger DB.
- Urutan rollout: deploy server dahulu sebelum klien admin mengirim field baru (server lama menjawab 400 untuk field tak dikenal).
- Status TC di TP tetap `NOT_RUN` mengikuti konvensi dokumen (belum ada eksekusi formal siklus uji).

## Lingkungan
Hanya infrastruktur QA yang berjalan: `qa-pg` (localhost:55433, DB sementara `rbt_it_booking` dibuat/dihapus oleh `setup(t)`), `qa-vk` (56380), RabbitMQ (5672). `telkomsel_db_postgresql` tidak disentuh. Hasil gate lengkap ada di `.agents/tasks/fix/booking-event-city-genres/verification.md`.
