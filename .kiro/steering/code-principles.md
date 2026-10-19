---
inclusion: always
---

# Prinsip Kode

- SOLID sesuai SDD §4.5.1: satu usecase per file; perilaku baru lewat adapter baru; interface kecil; usecase bergantung pada port.
- Accept interfaces, return structs. Konstruktor mengembalikan tipe konkret.
- Transaksi stok memakai unit of work (`IUnitOfWork`/`IBookingTx`) dengan urutan lock tetap.
- Logika Lua hanya untuk atomisitas antrean; aturan bisnis lain di Go.
- Consumer idempoten tanpa tabel inbox (guard state, `ON CONFLICT DO NOTHING`), dengan test kirim ganda.
- Outbox hanya untuk perubahan stok atau uang, di dalam transaksi yang sama.
- Event sourcing (Booking, Payment): append event + proyeksi read model + perubahan stok + outbox dalam satu transaksi; `ErrConcurrency` berarti baca ulang lalu ulangi; payload event tanpa NIK/email/token plaintext (ditolak `pkg/eventstore`).
- Consumer: retry berjenjang (`RABBITMQ_RETRY_DELAYS`), DLQ setelah `RABBITMQ_MAX_RETRIES`; kembalikan `rabbitmq.Permanent(err)` untuk payload rusak.
- Error domain bertipe dipetakan ke `errorCode` di `infrastructure/http` memakai `pkg/apperror`.
- Handler tidak pernah mengembalikan entity; selalu DTO lewat `domain/mapper`.
