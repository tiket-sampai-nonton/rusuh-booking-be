## Ringkasan

<!-- Apa yang berubah dan kenapa. -->

## Requirement / Test

- Requirement (SRS ID):
- Test case (TC ID):

## Checklist

- [ ] Struktur mengikuti standar proyek (Handbook §0): port/domain/application/infrastructure/delivery
- [ ] Interface baru berawalan `I` dan ada di `port/`; `make mock` sudah dijalankan
- [ ] Unit test usecase memakai mock dari `_mock/`; integration test adapter memakai dependency nyata
- [ ] `make ci` lolos lokal
- [ ] Perubahan kontrak API/event sudah diperbarui di `docs/contracts/`
- [ ] Penggunaan AI dicatat di `docs/ai-log/` (jangan hapus steering/spec)
