# Keputusan GET /me mengembalikan email dan NIK asli (P2-23)

Tanggal: 2026-11-13. Keputusan pemilik akun atas temuan P2-23 audit anomali 2026-11-12. Dicatat di ADR-033.

## Keputusan
- `GET /api/v1/me` tetap mengembalikan `email` dan `nik` dalam bentuk asli, hanya kepada pemilik akun.
- Respons tetap `Cache-Control: no-store`. Kontrak `docs/contracts/openapi/user.yaml` tidak diubah untuk P2-23 (perubahan 429 pada PATCH /me berasal dari U4).
- Endpoint lain tetap hanya mengirim bentuk tersamar.

## Alasan
- Perubahan ini disengaja sejak commit `d04cf0d`. Yang kurang adalah pencatatan di steering dan SRS.
- Pemilik akun perlu melihat datanya sendiri. Risiko (token dicuri, XSS, perangkat bersama, proxy yang merekam body)
  diterima dengan mitigasi di ADR-033.
- Tidak ada body logging: `pkg/httpx/accesslog.go` tidak mencatat body, header, atau query string.

## Berkas yang berubah
- `docs/analisa/05-ADR-RBT.md`: ADR-033 dan baris indeks.
- `.kiro/steering/security-privacy.md`: satu baris pengecualian.
- `docs/evidence/2026-11-12-me-plaintext-pii.md`: berkas ini.
- `docs/ai-log/README.md`: satu baris catatan sesi.
