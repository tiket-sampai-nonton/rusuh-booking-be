# Evidence: User Service hardening (HRD-001 … HRD-016, ADR-027)

Tanggal: 2026-11-04. Cabang `improve/user-service-hardening`. Keputusan desain: ADR-027. Test case: TC-USR-13…22 (`docs/analisa/06-TP-RBT-001.md`, ID diusulkan; pemilik dokumen menetapkan final).

## Hasil per temuan

| ID | Status | Catatan |
|---|---|---|
| C1 (HRD-001) | HARDENED, premis tidak terverifikasi; CloudFront = CONDITIONAL | `ClientIPHops` (gabung semua baris `X-Forwarded-For`, ambil entri ke-N dari kanan) + `APP_TRUSTED_PROXY_HOPS` (default 1, rentang 1–5). `ClientIP` bersama tidak diubah (Queue/Booking/Payment). Tidak ada capture ALB yang menunjukkan dua baris XFF; ALB biasanya menggabung satu baris. Topologi saat ini ALB saja → N=1 benar. CloudFront tidak diprovisioning (INF-2); N=2 hanya setelah smoke test staging (lihat "Tindak lanjut"). |
| C2 (HRD-002) | FIXED | `Verify` satu skrip Lua (GET, INCR, bandingkan, DEL); 20 goroutine → tepat satu `Matched` (Valkey 8.1 nyata). |
| C3 (HRD-003) | FIXED | Token akses dan id sesi (UUIDv4 dari `crypto/rand`) disiapkan sebelum commit; kegagalan sebelum commit tidak menyentuh sesi. Kegagalan login pertama setelah kode terkonsumsi: ACCEPTED (OQ-04), dikunci oleh test. |
| C4 (HRD-004) | FIXED | Logout ulang menulis denylist dengan sisa TTL dari `revoked_at`; token tak dikenal tidak menyentuh denylist. |
| C5 (HRD-005) | FIXED | Cek cooldown sebelum kuota; `Release(ticket)` pada keluar dini; publish gagal → `Cancel` (challenge + cooldown), kuota tidak dikembalikan (ack publish bisa saja terkirim). |
| C6 (HRD-006) | FIXED | `sanitizeUA` (UTF-8 valid, tanpa karakter kontrol, ≤256 rune); IP rusak → kosong → NULL. |
| C7 (HRD-007) | FIXED | `RevokeAllForAccount` satu pernyataan SQL mengembalikan id; semua `sid` masuk denylist; audit setelah semua penulisan berhasil. Migrasi `0002` hanya indeks. |
| S1 (HRD-008) | FIXED | Limiter per IP: verify (gagal tertutup), refresh/logout (gagal terbuka). Verify tanpa challenge tidak menulis baris audit (keputusan desain; `OTP_EXPIRED` yang sah tidak lagi terlihat di `login_audit`). |
| S2 | PARTIAL / DEFER | Dimitigasi HRD-005 + HRD-008; sisa kuncian pihak ketiga = OQ-05. |
| S3 (HRD-009) | FIXED | Batas atas `OTP_MAX_PER_EMAIL_HOUR` ≤10, `OTP_MAX_PER_IP_HOUR` ≤1000, anggaran tebakan ≤100, env baru bergerbang. |
| S4 (HRD-010) | FIXED | `Start` atomik (cooldown NX + kode + reset counter). |
| S5 (HRD-011) | FIXED (User) | `Allow` selalu ber-TTL dan menyembuhkan key tanpa TTL. Salinan `RateLimiter` Queue: tindak lanjut. |
| S6, S7 | DEFER (OQ-03) | Perlu keputusan SRS/ADR. |
| S8 | DEFER | Batas produk (tanpa bukti identitas NIK). |
| S9, S10 | DEFER (OQ-06) | Kontrak klaim lintas service / perilaku `/me`. |
| S11 (HRD-014) | DEFER | AAD terikat id akun butuh id lebih awal + dual-read; syarat (d) tidak terpenuhi tanpa restrukturisasi `Create`. |
| S12 (HRD-012) | FIXED | Tiga pasangan kunci dicek tanpa syarat; `ParseKey` error dikembalikan; pesan tanpa isi kunci (`ParseKey` tidak lagi meneruskan error hex yang menyebut byte salah). |
| S13 (HRD-013) | FIXED shutdown; lazy dial DEFER | `Clients.AddCloser/Close` (urutan terbalik, error digabung), `RunServer` menunda `Close`. |
| S14 (HRD-015) | FIXED | `MaskEmail` per rune; `NewEmail` batas local 64 byte, total 254 byte, tanpa karakter kontrol. |
| S15 | REJECT | Berfungsi; baris no-op benign. |
| S16 | DEFER | Metrik audit = peningkatan observability. |
| H1 | FIXED | Bucket IPv6 /64, IPv4-mapped → IPv4. |
| H2 | DEFER (OQ-06) | Akun nonaktif tetap punya sesi sampai exp. |
| H3 | REJECTED | `NewEmail` sudah lowercase+trim; test regresi ditambah. |
| H4 | REJECTED | `DecodeJSON` membatasi body dan menolak field tak dikenal; test regresi pada empat route auth. |
| H5 | FIXED | Counter di-PEXPIRE ke PTTL kode; diuji dengan `PEXPIRETIME`. |
| H6 | CONFIRMED-BUT-DEFERRED | UPDATE dijaga `revoked_at IS NULL`; jendela kedaluwarsa milidetik. |
| H7 | REJECTED (by design) | Sesi baru mendapat `sid` baru; access token lama hidup ≤ `AccessTTL`. |
| H8 | FIXED | `Allow` menolak `limit<1` / `window<1ms`. |
| H9 | VERIFIED | Semua panggilan `slog` di `services/user` hanya memuat `error`/outcome/angka; `AccessLog` memuat pola route, bukan path. Test `TestLogSafety_NewFailureBranches` (7 cabang). |
| H10 | FIXED | `normalizeClient` untuk IP dan UA. |
| H11 | HARDEN, premis tidak terverifikasi | Lihat C1. |

## Tidak terverifikasi / catatan

- Perilaku CloudFront (apakah menambah entri XFF) tidak diverifikasi; N=2 bergantung pada smoke test staging.
- RabbitMQ lokal tidak tersedia: publisher dan `Clients.Close` rabbitmq hanya diuji unit (fake closer), tidak terhadap broker nyata. Log `trusted proxy hops` ada di `newUserModule` (butuh PG/VK nyata) dan hanya diperiksa lewat tinjauan kode, bukan test otomatis.
- `deployments/dev/env/user.env.example` memakai `OTP_MAX_PER_IP_HOUR=100000`, yang sekarang gagal validasi (>1000) dan akan mencegah service dev start. Diubah menjadi `1000` plus `OTP_MAX_VERIFY_PER_IP_MIN=6000` dan `TOKEN_MAX_REFRESH_PER_IP_MIN=6000`. Operator dev harus menyelaraskan `user.env` di SSM/VPS sebelum rilis. Batas per IP 1000/jam tetap bisa terasa ketat di dev karena semua pengunjung berbagi satu IP.

## Tindak lanjut

- INF-2 (pelaksana INF-2): saat CloudFront dipasang, set `APP_TRUSTED_PROXY_HOPS=2` hanya setelah smoke test staging: panggil API lewat CloudFront dengan `X-Forwarded-For: 1.2.3.4` palsu, bandingkan `client_ip` di baris `login_audit` terbaru (DB staging) dengan IP penelepon. `hops=1` di belakang CloudFront salah (terbaca alamat edge).
- Queue punya salinan `RateLimiter` dengan celah INCR+PEXPIRE yang sama; perbaikan terpisah.
- Queue/Booking/Payment masih memakai `ClientIP` baris pertama (OQ-10).
- Rollout: `PII_ENC_KEY`, `PII_HASH_KEY`, `OTP_HASH_KEY` yang salah satunya sama dengan yang lain kini gagal start (sebelumnya pasangan enc==otp lolos).
