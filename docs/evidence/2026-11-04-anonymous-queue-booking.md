# Evidence: join antrean anonim + NIK di halaman Booking (ADR-030)

Tanggal: 2026-11-04. Branch `improve/anonymous-queue-booking` (basis `feature/rework` 53e74da). Semua perubahan
belum di-commit. Dokumen ini jujur tentang apa yang SELESAI dan apa yang BELUM.

## 1. Lingkungan (T00)

Kontainer khusus proyek (bukan Redis `:6379` milik proyek lain, bukan cluster `:7000-7005`):
`aqb-pg` (postgres:16-alpine, host 15432), `aqb-valkey` (valkey 8.1-alpine, host 16379), `aqb-rabbit`
(rabbitmq 3.13, AMQP host 15673). Variabel: `TEST_POSTGRES_*` (15432), `TEST_VALKEY_SERVER` (16379),
`TEST_RABBITMQ_URL` (15673). Baseline sebelum perubahan: `go build`, `go vet -tags integration`, dan
`go test -tags integration -race ./...` di `services/booking` hijau. Baseline git: HEAD `53e74da`, 0 entri status, 4 stash
milik worktree lain (tidak disentuh).

## 2. Merah lalu hijau (test-first)

| Perilaku | Merah di basis | Hijau sekarang |
|---|---|---|
| Hold tidak mengembalikan unit tak terpakai (F3); hold dengan edit nonaktif gagal `CATEGORY_UNAVAILABLE` untuk pemilik unit (F2); hold 3 item dengan admission 1 unit mencuri unit admission lain (F1); oracle 20x3 tidak nol | `TestHoldAccounting_*` gagal pada basis (`allocated` 5 bukan 3, error tak terduga, tidak ada error, hold 3 gagal). Fakta OQ-10 terselesaikan: F1 dan F2 NYATA. | `TestHoldAccounting_*` lulus |
| Edit menambah tiket menarik dari `allocated_units` orang lain | `TestEditE1_*` gagal sebelum perbaikan | lulus |

## 3. Hasil verifikasi gate `make ci` (exit code asli, iterasi 3 = run final)

- `make ci` (= build lint cover isolation contracts) dengan kontainer khusus proyek dan env `TEST_*` di §1:
  **exit 0** (`/tmp/aqb-ci-final.log`, baris `EXIT=0`). Iterasi 2 kadang gagal di flake `pkg/rabbitmq`
  `TestConsume_TransientFailureIsRetriedThenAcked` — pada run final HIJAU (87,3% coverage). Modul `pkg/rabbitmq`
  TIDAK diubah oleh tugas ini (di luar cakupan); lihat catatan flake di §5.
- Coverage logika (ambang 80%): pkg 90,9%, queue 88,0%, booking 83,8%, payment 90,0%, notification 83,8%, user 93,5%.
- `golangci-lint` 0 isu semua modul; `make isolation` 5 modul lulus; `make contracts` lulus semua OpenAPI+event schema.
- Booking concurrency `TestHoldBuyerNIK_NoOversellUnderConcurrency`: 80/80 run `-race` hijau setelah perbaikan akar
  flake (§4c.1). Paket postgres Booking `go test -tags integration -race -count=5` 5/5 hijau.
- TC-ANON-19 (`TestAbuseSim_*`) dan TC-ANON-20 (`TestOpeningWindowExample_VXY`) hijau pada Valkey 8.1 khusus proyek.

## 3. Hasil verifikasi (exit code asli, iterasi 2)

### 3a. Bukti mutasi (Lua Queue, iterasi 2)

Test integrasi liveness/token ditulis sebelum Lua; karena hijau pada run pertama, kemampuan gagalnya dibuktikan dengan mutan
(semua dipulihkan, suite hijau lagi):

| Mutan | Test yang merah |
|---|---|
| M1 `liveness.lua` tanpa fence lease | `TestLiveness_FencedByLease` |
| M2 ambang STALE inklusif (`<=` bukan `<`) | `TestLiveness_StaleReviveEnd` |
| M3 revive di rank salah (`rank + 1000`) | `TestLiveness_StaleReviveEnd`, `TestDraw_ApplyIsFencedAndStaleWindowEntriesKeepTheirRank` |
| M4 `join.lua` tanpa cek generasi token | `TestTokenGen_JoinRotationAndHeartbeatStates` |

Catatan: mutan "mark_stale tanpa lease" yang diminta untuk TC-ANON-04 tidak membuat `TestRevivePromoteRace` merah (race
itu tidak memaksa interleaving); fence-nya dibuktikan oleh M1 pada `TestLiveness_FencedByLease`.

### 3b. Uji beban heartbeat (TC-ANON-05)

`RUN_HEARTBEAT_LOAD=1 go test -tags integration -run TestHeartbeatLoad ./internal/queue/infrastructure/valkey/`, satu proses
generator dan satu Valkey 8.1 lokal (kontainer `aqb-valkey`), 100.000 entri WAITING, 3.333 heartbeat/dtk selama 30 dtk:

| Run | Tercapai | Galat | p50 | p95 | p99 | max | tick baseline p50 | tick saat beban p50 | rasio |
|---|---|---|---|---|---|---|---|---|---|
| 1 | 3.333/dtk | 0 | 0,73 ms | 1,37 ms | 1,99 ms | 14,1 ms | 2,45 ms | 3,08 ms | 1,26 |
| 2 | 3.333/dtk | 0 | 0,74 ms | 1,36 ms | 1,91 ms | 13,1 ms | 2,37 ms | 3,27 ms | 1,38 |

Hasil: p99 <= 200 ms LULUS, galat <= 0,1% LULUS, **tick promotor <= +20% GAGAL** (+0,6 sampai 0,9 ms absolut; Valkey satu
thread melayani 3.333 skrip/dtk). Sesuai desain §6.4 fallback (sharding `hb` per `crc16(entryId) % B`) dicatat dan TIDAK
dibangun; keputusan di gerbang manusia. Yang diukur adalah jalur store (Lua + jaringan), bukan tier HTTP/ALB.

## 4. Yang diimplementasikan

Booking: migrasi `0009` (+ penjaga admission hidup `--allow-live-admissions`, uji up/down/up/backfill/penolakan rollback);
reservasi tanpa NIK (`outstanding_units`, grant parsial, clamp `max_tickets_per_order`, tanpa akses `buyer_quotas`,
perbaikan premature SOLD_OUT); hold dengan blok `buyer` (validasi NIK/email/consent, ikat ke admission di transaksi hold,
kuota NIK pembeli di hold, `422 BUYER_NIK_LIMIT_REACHED` dengan admission utuh, anggaran percobaan + throttle,
kategori habis menang dan rollback, id kategori di pesan); replay idempoten sebelum membaca state, `Abort` rekaman pada
refusal 4xx, hash request HMAC tanpa plaintext; rilis unit di semua jalur lewat `outstanding_units`; edit E1; supply snapshot
aditif; session view `outstandingUnits`; bearer opsional pada hold (akun saja).

Queue: join anonim (`join.lua` baru; dedup per visitor dan per akun; re-attach; associate; token entri akun lain diabaikan;
`Idempotency-Key`; 200 vs 201), batas lunak per IP, tanpa batas per email; promoter dan join langsung meminta 3 unit tanpa
field pembeli; kode `quota_exhausted`/unreadable-PII dihapus, konsumer `booking.quota.exhausted` jadi no-op satu rilis;
migrasi audit `0003`; body join dibuang (toleran terhadap klien lama).

Dokumen: ADR-030, catatan SRS, TP (lampiran TC-ANON), kontrak OpenAPI queue/booking/internal-booking, dokumen integrasi
frontend + daftar perubahan web/app, proposal WAF (`deployments/terraform/proposals`, tidak ada `.tf` diubah).

## 4b. Tambahan iterasi 2 (Queue)

- Token visitor: generasi server-side (`vtok:<visitor>`), rotasi hanya saat re-attach akun dan join ulang setelah ENDED;
  klaim perangkat = HMAC-SHA256(`X-Device-Id`) 16 byte di token (nilai mentah tidak pernah disimpan/di-log); token yang
  disalin ke perangkat lain tidak valid. TTL catatan = `TOKEN_VISITOR_TTL` (deviasi: desain meminta
  `salesEndsAt + claim + selection + margin`).
- Heartbeat `POST /api/v1/queue-entries/me/heartbeat` (401 token dirotasi/tidak valid, 404 entri berakhir), STALE setelah
  90 dtk, revive di rank asli, ENDED `STALE_TIMEOUT` setelah 300 dtk tanpa admisi; sweeper liveness (job `liveness`) di
  bawah lease promotor; entri teradmisi tidak ada di set liveness. Deviasi kecil dari desain: STALE dipindah dari `hb` ke
  `hbstale` agar pemindaian dengan LIMIT tidak macet di entri yang sudah STALE.
- Jendela acak pembukaan PROVISIONAL (default 60 dtk, 0 = nonaktif, kolom `event_settings.opening_window_seconds`, migrasi
  Queue 0004): DRAWING tanpa rank, promotor dan admisi langsung berhenti sampai `salesStartsAt + W`, undian sekali dengan
  seed crypto/rand yang disimpan (SET NX), urutan dipersist dan diterapkan bertahap (`QUEUE_DRAW_CHUNK`), dapat dilanjutkan
  setelah crash dengan hasil identik; pendaftar telat di belakang blok jendela; sesudahnya FIFO ketat.
- Zona berbasis rank: `headRank`, `guaranteedUntilRank`, `possibleUntilRank` (null saat DRAWING); G = ceil(F/3),
  P = ceil((F+A+H)/3) atas entri hidup; bila Booking mati, batas sebelumnya dipertahankan. `me` mengembalikan `rank`,
  `state`, `zone` server, `exactPosition` = 1 + entri hidup dengan rank lebih kecil, `nextHeartbeatMs`.
- `me` untuk entri berakhir: 200 `state: ENDED` + `reason`; 401 untuk token dirotasi; 404 hanya bila visitor tidak pernah
  punya entri.
- Pembersihan: `pii.go`/`IPIIProtector`, field NIK/email pada entri, `buyer_nik_hash.go`, error NIK/email/kuota, konfigurasi
  `JoinPerEmail*`/`UnsealFail*`, metrik unseal, outcome `BUYER_QUOTA_EXHAUSTED`/`UNREADABLE_PII` dihapus; audit menulis NULL
  untuk NIK; kode `ZoneFor(seq)`/`ExactPosition(seq)` dihapus. Batas lunak per IP default 200/menit.
- Compose: env liveness Queue dan `BUYER_NIK_*` Booking.

## 4c. Iterasi 3 (menutup 5 temuan qa.json/review.json)

Semua perubahan iterasi 3 ditulis test-dulu atau dibuktikan mutan; tidak ada commit.

1. **Flake TestHoldBuyerNIK_NoOversellUnderConcurrency (temuan #1, akar NYATA ditemukan & diperbaiki).** Penyebab
   persis yang diduga QA: `acctFixture.spec` menurunkan `PublicRef` dari `time.Now().UnixNano()%1e8+idx`; dua hold
   konkuren (12 goroutine) bisa bertabrakan pada kolom `public_ref UNIQUE`, satu hold gagal unique-violation dan
   meninggalkan `allocated_units=3`. Diperbaiki menjadi deterministik dan unik per idx: `AC<suffix-event><idx>`
   (suffix event unik per run). Bukti: `go test -tags integration -race -count=80 -run
   TestHoldBuyerNIK_NoOversellUnderConcurrency` HIJAU 80/80 (sebelumnya intermiten). Bukan "diulang sampai hijau" —
   akar dihilangkan. `go test -count=5` seluruh paket postgres juga 5/5 hijau.
2. **Claim & leave kini membandingkan generasi token (temuan #2).** `ClaimInput`/`LeaveInput` menambah `Gen`;
   `ClaimTurn.Claim` dan `LeaveQueue.Leave` memanggil `checkTokenGen` lebih dulu; handler claim/leave memakai
   `VisitorTokenAuthenticator` (401 bila generasi lama/0). Test-dulu:
   `TestClaimAndLeave_RotatedOutTokenIsRefused` (store tidak pernah disentuh untuk token lama; keduanya 401). Semua
   test claim/leave lama diperbarui dengan `Gen: 1` dan ekspektasi `TokenGen`.
3. **Batas laju per visitor untuk heartbeat dan `me` (temuan #3, bagian limiter).** `visitorLimit` baru dipakai
   `Heartbeat.WithLimiter` dan `GetMyEntry.WithLimiter`, di-wire di `queue_module.go` dengan limiter Valkey yang sama.
   Config: `RATE_LIMIT_HEARTBEAT_PER_VISITOR` (default 20/menit) + `RATE_LIMIT_ME_PER_VISITOR` (120/menit). Fail-closed
   (galat limiter dikembalikan). Test: `TestHeartbeatAndMe_PerVisitorLimit` (429 di luar batas, store tak disentuh,
   galat limiter fail-closed). Kriteria tick promotor <= +20% uji beban TETAP gagal (§3b) dan sharding tetap tidak
   dibangun — itu butir gerbang manusia (§6), bukan diperbaiki di sini.
4. **TC-ANON-19 dan TC-ANON-20 ditulis dan HIJAU (temuan #4).**
   - `TestAbuseSim_*` (TC-ANON-19 a/a2/b/c/d/e) di `internal/queue/infrastructure/worker/abuse_simulation_integration_test.go`:
     virtual clock, Valkey nyata, Promoter+Liveness+Sweeper nyata, stub Booking yang menghitung dalam UNIT (3 per
     admisi, grant parsial, rilis saat deadline klaim atau klaim+selection_ttl). Oracle persis: `minFree >= stok - S·3`
     (dan TIGHT: sama persis), `maxLive <= S` (dan tercapai), satu admisi per hantu, antrean terkuras dalam batas
     gelombang terpin, pembeli nyata di belakang terlayani dalam batas, hantu STALE dilewati (<= claim window), jendela
     W=60 terikat 242 dtk whatever rank + run berseed rank 1 teradmisi tik pertama. Skala default S=10/ghosts=200/300
     unit (puluhan detik); `RUN_ABUSE_FULL=1` menjalankan parameter terpin 100/10000/3000.
   - `TestOpeningWindowExample_VXY` (TC-ANON-20) di `.../opening_window_example_integration_test.go`: W=60,
     stale_after=5 dtk, end_after=10 dtk, hb 2 dtk, ambang strict `>`, draw berseed. Mengassert E1 STALE pada t=16 (masih
     DRAWING pada t=15), ENDED pada t=21 (masih STALE pada t=20), T valid (heartbeat 404 bukan 401), rejoin t=30 dengan T
     = 201 visitor id tetap + token dirotasi, join t=31 tanpa T = principal baru, rank(E2)/rank(X) < rank(Y), rejoin
     pasca-jendela berurut seq di belakang Y, 50 join tanpa token = 50 entri (pengganda ~50x dicatat). Untuk ini entity
     `Entry` menambah flag `Stale` (DRAWING yang disenyapkan sweeper tetap berstatus DRAWING sampai draw; dibaca dari
     field hash `stale` yang sudah ditulis `liveness.lua`).
   - Join sebelum `salesStartsAt`: kode dasar memang tidak menolaknya (R01.5 "as today"); entri pra-pembukaan ikut
     diundi di jendela — ini perilaku yang disengaja dan kini DITEST (TC-ANON-19e, TC-ANON-20), bukan celah.
5. **Config Queue tidak lagi mewajibkan `PII_ENC_KEY` (temuan #4, bagian config).** `config.PII` menyusut menjadi
   hanya `PII_HMAC_KEY` (dipakai untuk klaim perangkat); `PII_ENC_KEY` tidak dibaca/divalidasi lagi. Test:
   `TestLoad_NoPIIEncKeyRequired` (start sukses tanpa `PII_ENC_KEY`), tabel validasi memakai `PII_HMAC_KEY`.
   `deployments/dev/env/queue.env.example` menghapus baris `PII_ENC_KEY`. ADR-030 dicatat.

### Yang MASIH BELUM diklaim (iterasi 3)

- Kriteria tick promotor <= +20% uji beban tetap gagal (§3b); sharding `hb` adalah butir gerbang manusia (§6).
- Rekonsiliasi `allocated_units` data lama: migrasi 0009 hanya `RAISE NOTICE` selisih (OQ-20, tanpa penulisan ulang);
  disengaja, menunggu keputusan pemilik. Tidak diklaim terverifikasi pada data lama.
- SRS/PRD baris per baris dan `document-alignment-verification` penuh, serta e2e lintas-service: BELUM dijalankan di
  iterasi ini (TP/ADR diperbarui untuk TC baru dan temuan; alignment menyeluruh menunggu gate dokumen).

## 5. (lama) Status iterasi 2 — sebagian kini tertutup di §4c

1. ~~TC-ANON-19/20 belum ditulis~~ → DITUTUP (§4c.4).
2. Kriteria tick promotor <= +20% pada uji beban gagal (§3b); sharding tidak dibangun (gerbang manusia).
3. ~~Claim/leave tidak membandingkan generasi~~ → DITUTUP (§4c.2).
4. ~~Heartbeat/me tanpa batas laju~~ → DITUTUP (§4c.3).
5. ~~Config Queue mewajibkan `PII_ENC_KEY`~~ → DITUTUP (§4c.5).
6. Join tidak menolak sebelum `salesStartsAt` (R01.5 "as today"): disengaja, kini ditest (§4c.4).
7. Rekonsiliasi `allocated_units` data lama, SRS/PRD baris per baris, `document-alignment-verification`, e2e: belum
   (lihat §4c "Yang MASIH BELUM").

### Catatan flake di luar cakupan tugas ini
`pkg/rabbitmq` `TestConsume_TransientFailureIsRetriedThenAcked` intermiten (jalur retry-queue TTL dead-letter;
waktu 10 dtk). Paket `pkg/rabbitmq` TIDAK diubah oleh tugas ini (`git status --short pkg/rabbitmq` kosong) dan berada
di luar cakupan (Queue + Booking + kontrak + dokumen). Lulus bila dijalankan sendiri (3/3) dan pada sebagian run
`make ci`; gagal pada sebagian run lain. Dicatat sebagai flake pra-ada, BUKAN diperbaiki dengan retry: memperbaikinya
berarti menyentuh infrastruktur bersama di luar touch-set tugas. Disurface ke pemilik.

## 6. Butir gerbang manusia (default PROVISIONAL sudah dipasang, belum dikonfirmasi pemilik)

Jendela acak pembukaan (dipasang PROVISIONAL: 60 s, bisa nonaktif dengan 0) dan aturan undian; panjang timer halaman dan dampak throughput dengan
3 unit per admission (slot Queue ditahan sampai journey berakhir, stok terkunci hingga `3·S`); perubahan kata "Jangan PHP"
dan SRS-F-01-04; ambang percobaan NIK pembeli (dipasang PROVISIONAL: anggaran 10, throttle 2 s berlipat sampai 30 s, 429 BUYER_NIK_THROTTLED); cabang edit E1 (dipasang PROVISIONAL) atau E2; perubahan kata SRS-F-01-04 tetap draf usulan; kriteria tick promotor yang gagal di uji beban (terima atau bangun sharding);
rekonsiliasi selisih `allocated_units` vs `sum(outstanding_units)` pada data lama (migrasi hanya melaporkan `NOTICE`).
Risiko diterima: NIK palsu berformat valid dan botfarm IP residensial. Opsi lanjutan: Dukcapil, cek KTP di pintu.

## 7. Pemeriksaan akhir (T31)

HEAD `53e74da`; `git log 53e74da..HEAD` 0 commit; `git diff --cached` kosong; daftar stash tetap 4;
`git status --short services/payment services/notification services/user` kosong (tidak ada perubahan Payment/User/Notification dipaksa).
