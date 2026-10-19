# Kontrak OpenAPI (D-19)

Satu file per service (format usulan BE-2), isi mengikuti SDD v1.5 §4.6.1 dan SRS §3.1.4. Semua respons sukses
berbentuk `{"meta": {...}, "data": ...}` dan error berbentuk `{"meta": {"code", "message", "requestId", "errorCode", "details"}, "data": null}`.

| File | Service | Konsumen |
|---|---|---|
| `queue.yaml` | Queue (publik) | Web, mobile |
| `booking.yaml` | Booking (publik) | Web, mobile |
| `payment.yaml` | Payment (publik + webhook) | Web, mobile (WebView), Midtrans |
| `notification.yaml` | Notification (publik) | Mobile |
| `user.yaml` | User (publik, login opsional) | Web, mobile |
| `internal-booking.yaml` | Booking (internal) | Queue, Payment |
| `internal-payment.yaml` | Payment (internal) | Booking |

Klien web dan mobile merujuk file ini dengan ref yang dikunci (ADR-017). `user.yaml` sudah sesuai implementasi `services/user`;
file lain menjadi acuan implementasi Sprint 1.
