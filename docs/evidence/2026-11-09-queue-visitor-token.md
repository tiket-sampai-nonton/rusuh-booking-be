# Evidence 2026-11-09: penanganan visitor token di Queue (Batch 2: F1–F4)

Cabang `fix/queue-visitor-token` (basis `feature/rework@3f0c489`). Temuan asal: `docs/happy-path-endpoints.md` T3, T4 dan tiga temuan terkait
(F1 token di body join, F3 `me` never-joined, F4 `X-Device-Id` di CORS). Keputusan pemilik: q1.a, q2 BE saja, q3 keep, q4 app + web, q5 guest + login,
q6 deprecate, q1+q2.i, q4.ii, oq1.b, OQ-5 ya (catatan generasi `vtok` diperbarui pada join EXISTING).

## Perubahan perilaku yang terlihat klien (ditandai agar ditinjau)

1. **Join:** cookie `rb_visitor` selalu di-set bila token terbit (termasuk untuk native). `visitorToken` di body hanya untuk `X-Client-Type: mobile` tanpa cookie
   valid. Pemegang cookie valid tidak pernah menerima token di body walau header mobile dikirim. Cookie tidak kosong yang tidak valid → visitor baru.
   App native tanpa `X-Client-Type: mobile` tidak menerima token di body (OQ dicatat di ADR-030 addendum).
2. **Claim/leave:** token tidak ada/tidak valid/kedaluwarsa/perangkat tidak cocok → **401 `VISITOR_TOKEN_INVALID`** (sebelumnya 404 `NOT_FOUND`), sebelum entri dicari.
3. **Claim/leave/me:** visitor tanpa catatan generasi untuk event itu (belum pernah bergabung) → **404 `NOT_FOUND`** (sebelumnya 401). Token dirotasi tetap 401.
   `eventId` kosong tetap 401. Heartbeat tidak berubah (OQ-3, tetap 401).
4. **`join.lua`:** `describe()` memperbarui TTL `vtok` (join EXISTING / replay Idempotency-Key). Tidak membuat kunci baru; tidak ada migrasi.
5. **CORS lokal (`pkg/httpx`):** `X-Device-Id` ditambahkan ke `Access-Control-Allow-Headers` (semua lima service).
6. **Klien:** tangani 401 berdasarkan `errorCode`: `VISITOR_TOKEN_INVALID` ≠ kedaluwarsa access token (`UNAUTHORIZED`).

Tidak ada perubahan antarmuka `port/`, event, migrasi, atau dependensi; `make mock` tanpa diff.

## Red → hijau (test dulu)

| Tugas | Test (merah dulu) | Hasil merah | Hijau setelah |
|---|---|---|---|
| T1/T2 | `TestJoinHandler_VisitorTokenDeliveryMatrix` (M1–M10), `TestJoinHandler_SpoofedMobileHeaderWithCookieNeverLeaksToken` | 7 baris FAIL (M3, M4, M5, M7, M8 + spoof) | `join_handler.go` |
| T3/T4 | `TestTokenGenCheck_ThreeWay` (claim/leave/me), `TestTokenLifetimeBoundary_...`, `TestGetMyEntry_TokenStates` | 14 FAIL (never-joined 404, empty eventId tanpa store) | `checkTokenGen` di `my_entry.go` |
| T5/T6 | `TestLeaveHandler_Unauthorized` (dibalik ke 401), `TestClaimHandler_ErrorMapping`, `TestClaimAndLeaveHandlers_InvalidTokenIs401BeforeAnyLookup` | 5 FAIL | `claim_handler.go`, `leave_handler.go` |
| T7/T8 | `TestVisitorToken_ExistingJoinRefreshesRecordTTL` (Valkey nyata, qa-vk) | PTTL ≈ 5 dtk, ingin ≈ 24 jam (2 subtest) | `join.lua` |
| T9/T10 | `TestCORS_PreflightAllowHeaders` | `Allow-Headers must contain X-Device-Id` | `pkg/httpx/middleware.go` |
| T11 | `TestDeviceBinding_PerRouteMatrix`, `TestDeviceBinding_JoinDegradesToNewVisitor` | Menjaga perilaku yang sudah ada: langsung hijau. Dibuktikan bisa gagal: perbandingan perangkat di `visitor_auth.go` dimutasi sementara → 12 FAIL; dikembalikan | (tanpa perubahan kode) |

Integrasi (Valkey nyata) rotasi/asing-vs-tidak-ada/never-joined: `TestVisitorToken_RotatedOutTokenCannotTouchALiveAdmittedEntry` (entri tetap ADMITTED, Booking tidak dipanggil),
`TestVisitorToken_ForeignAndAbsentEntryAreIdentical404`, `TestVisitorToken_NeverJoinedEventIs404` — hijau pada percobaan pertama karena perilaku
Lua/store sudah benar; yang merah hanya P8 (TTL).

## Uji curl nyata

Hanya Queue api yang diganti dengan biner worktree (stack lain tetap); stack dipulihkan ke biner semula setelahnya. `t_edge_queue.sh` (T3/T4 dibalik jadi ekspektasi kontrak,
ditambah cek spoof, claim asing-vs-tidak-ada, preflight) : 58 lulus, 4 gagal. Keempat kegagalan = preflight di Booking, Payment, Notification, User yang masih biner lama;
instans worktree keempat service itu dijalankan di port sementara dan keempatnya menjawab 204 dengan `X-Device-Id` di `Access-Control-Allow-Headers`. Skrip curl tidak ikut diff
(tidak dilacak git). Hasil mentah: `.agents/tasks/local/run/curl/results/edgeq.txt`.

## Dibiarkan terbuka

- OQ-3: heartbeat never-joined tetap 401 (Lua mengembalikan INVALID).
- OQ-6: nomor TC-QUE-41..45 usulan; samakan saat rebase.
- TOCTOU antara cek generasi dan skrip Lua (pra-ada, tidak diperlebar).
- Field `JoinInput.IsMobile` kini hanya petunjuk transport; kandidat dihapus.
- Native app tanpa `X-Client-Type: mobile` tidak menerima token di body.
