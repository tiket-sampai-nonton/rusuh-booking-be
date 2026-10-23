# Kontrak OpenAPI (D-19)

Satu file per service (format usulan BE-2), isi mengikuti SDD v1.5 §4.6.1 dan SRS §3.1.4. Semua respons sukses
berbentuk `{"meta": {...}, "data": ...}` dan error berbentuk `{"meta": {"code", "message"}, "data": null}` dengan `message` berformat `KODE: pesan`; `requestId` ada di header `X-Request-ID`, sedangkan detail hanya di log (platform R2.8; `errorCode` dan `details` pada schema `Meta` ditandai deprecated).

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

Validasi lokal dan di CI: `make contracts` (modul `tools/contracts`: OpenAPI wajib 3.1.x, schema event wajib JSON Schema 2020-12). Job CI `contracts` jalan bila `docs/contracts/` berubah.
