# Keputusan masa tenggang verifikasi (ADR-007 amandemen) — 2026-11-12

## Keputusan

Pemilik memilih mempertahankan `VERIFY_GRACE_SECONDS = 180` (default), dengan alasan: memberi jeda bila TTL pembayaran habis sementara pembayaran masih diproses. Deviasi dari FR-23 dan BR-06 (tender: batal tepat setelah 10 menit) diterima untuk cakupan PoC/demo. Amandemen dicatat di `docs/analisa/05-ADR-RBT.md` pada bagian ADR-007.

## Apa yang dilindungi masa tenggang

Masa tenggang melindungi pembayaran yang **dimulai sebelum 10 menit** tetapi konfirmasinya (webhook atau resolve) baru datang setelah 10 menit. Sistem tidak langsung membatalkan booking dan stok tidak dilepas sebelum gateway ditanya.

## Apa yang tidak dilindungi

- Pembayaran dengan `settlement_time` setelah 10 menit tetap masuk `REVIEW` (stok dilepas, refund manual). Ini berlaku walau masih dalam masa tenggang.
- Tidak ada attempt baru setelah 10 menit (`isPayable`).

## Bukti dari kode (dibaca 2026-11-12)

| Perilaku | Lokasi |
|---|---|
| Masa tenggang dimulai saat TTL habis (`VerificationUntil`) | `services/booking/internal/booking/application/usecase/expire.go:95-103` |
| Pembayaran setelah batas → REVIEW | `services/booking/internal/booking/infrastructure/postgres/unit_of_work.go:478-507` |
| Attempt baru ditolak setelah batas | `services/payment/internal/payment/application/usecase/common.go:56-58` |
| Rentang nilai grace 60–600 detik | `services/booking/internal/config/config.go:86-87` |

## Dampak yang perlu diketahui

- Stok ditahan sekitar 13 menit pada kasus tidak dibayar (bukan 10 menit).
- Frontend perlu status "memverifikasi" pada rentang 10–13 menit. Belum saya periksa implementasinya di FE.
- Status audit: FR-23 dan BR-06 tetap PARTIAL dengan catatan deviasi diterima. Naik ke PASS hanya bila assessor menerima deviasi secara tertulis.

## Batasan

Tidak ada uji waktu nyata untuk kasus "bayar di menit ke-9 dengan webhook terlambat". Itu perlu skenario uji di sandbox Midtrans (`custom_expiry` minimal 30 detik, lihat CLAUDE.md) bila ingin dibuktikan.
