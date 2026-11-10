# Booking + Payment contract gaps (T7, T8, T9) — 2026-11-09

Diskusi dan keputusan yang mengubah planning/spec, dicatat sesuai aturan `testing-review`.

> **Pembaruan 2026-11-10:** T8 (simulator lokal) di-revert (commit 84b5c1c, e03a52c, 75e1888) dan endpoint `local-simulator` dihapus dari `payment.yaml`, SRS, TP, ADR (ADR-032 dicabut). DEV-1..DEV-4 di bawah tidak berlaku lagi. T7 dan T9 tetap.

## Keputusan pemilik (dari sesi workflow)
- T7: Booking saja, `keepFromItemId` didukung di `PUT /bookings/{id}/items`; `POST /bookings` menolaknya (400).
- T8: simulator lokal untuk `APP_ENV=local` dan `ci` (bukan `local-offline`); web dan mobile sama; tamu dan login sama.
- T9: kontrak OpenAPI disamakan dengan perilaku kode, tiap baris dibuktikan oleh uji.

## Deviasi dari requirements.md (diterima di tinjauan desain, perlu pengakuan orchestrator)
- DEV-1: kriteria 2.9 "EXPIRE lalu SETTLE_LATE → PAID" berlaku untuk attempt dan untuk booking yang belum EXPIRED. Booking yang sudah EXPIRED tetap ke REVIEW (stok sudah dilepas; memaksa PAID berarti oversell, invarian 1). Dibuktikan di sisi Payment (`TestSimulator_ExpireThenSettleLate`) dan sisi Booking (`TestSettle_VerifyingBookingJudgedBySettlementTime`, `TestSettle_AfterExpiryGoesToReviewWithoutTicketsOrStockChange`).
- DEV-2: di lingkungan nonaktif rute tidak terdaftar; jawabannya 404 bawaan server (tanpa envelope), identik dengan path tak dikenal.
- DEV-3: body 202 memakai envelope `data{simulated,scenario}`.
- DEV-4: simulator bukan provider alternatif; konsisten dengan Handbook.

## Temuan di luar cakupan (tidak diubah)
- `notification.yaml`: 401 dari `POST /push-subscriptions` membawa errorCode `VALIDATION_ERROR` (`inbound.ErrUnauthorized`), bukan `UNAUTHORIZED` seperti service lain. Didokumentasikan apa adanya; mengubahnya mengubah kontrak respons dan menunggu keputusan pemilik.

Hasil gate dan perintah: `.agents/tasks/fix/booking-payment-contract/verification.md`.
