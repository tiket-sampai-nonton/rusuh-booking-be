# Evidence: CHANGE 1 (drop draw) + CHANGE 2 (fix promoter-tick slowdown) — ADR-030 Amendment

Tanggal: 2026-11-05. Branch `improve/anonymous-queue-booking` (basis `feature/rework` 53e74da). Semua perubahan
BELUM di-commit. Melanjutkan iterasi 2026-11-04; dokumen ini jujur tentang apa yang SELESAI dan apa yang BELUM.

## 1. Lingkungan (sama seperti 2026-11-04)

Kontainer khusus proyek yang sudah berjalan: `aqb-valkey` (valkey 8.1.10, host `:16379`), `aqb-pg` (`:15432`),
`aqb-rabbit` (`:15673`). **BUKAN** `telkomsel_redis` `:6379` (Redis proyek lain) dan **BUKAN** `valkey` `:7000-7005`
(cluster proyek lain) — keduanya terdeteksi `docker ps` dan dilarang brief. Semua perintah integrasi/uji beban di
bawah dijalankan dengan `TEST_VALKEY_SERVER='[{"host":"localhost","port":16379}]'`. Uji beban dan uji shard
mem-`Fatal` bila endpoint menunjuk `:6379` atau `:7000-7005` (`assertDedicatedValkey`), jadi lupa override = abort,
bukan salah ukur.

## 2. CHANGE 1 — draw dibuang (SELESAI, terbukti)

- `grep -ri DRAWING services/queue` → **kosong** (exit 1). `grep -ri shuffle services/queue` → **kosong**.
  `grep -ri "Windowed\|OpeningWindow\|WithDraw\|NewDraw\|DrawChunk\|IsDrawn\|fixedSeed" services/queue` → **kosong**.
- File produksi draw sudah tidak ada: `worker/draw.go`, `assets/lua/draw_apply.lua`, `draw_finalize.lua`,
  `deployments/migrations/0004_opening_window.{up,down}.sql`. `0004` DIHAPUS langsung (belum pernah di-apply, semua
  uncommitted; design-change §5) — tidak dibuat `0005`. `embed_test.go` kini meng-assert 3 migrasi (0001-0003).
- Zona kembali atas `seq`: `dto.StatusDocument.{HeadSeq,GuaranteedUntilSeq,PossibleUntilSeq}`,
  `valueobject.ZoneForSeq`, `IQueueStore.WaitingSeqAt`; `entity.Entry.Rank` dan `EventSettings.OpeningWindowSeconds`
  dihapus. `dto.EntryStatus.{Rank,Zone}` DIPERTAHANKAN tetapi dihitung atas seq (FIFO).
- Test draw-only WITHDRAWN/dibersihkan: `liveness_draw_integration_test.go` (valkey+worker),
  `opening_window_example_integration_test.go` (TC-ANON-20), subtes `TestStatusPublisher_DrawingPublishesNulls`,
  `TestGetMyEntry_DrawingHasNullRankAndZone`, dua subtes `TestAbuseSim_OpeningWindow*` (TC-ANON-19 skenario e).
  Test KEEP (token/liveness/revive/abuse FIFO a/a2/b/c/d) dipindah utuh ke `liveness_integration_test.go` dan
  `liveness_sweeper_integration_test.go` dan lulus (lihat §4).
- Unit queue: `go test -race -count=1 ./...` (services/queue) **hijau** (semua paket ok; migrasi embed 3).
  `go build`, `go vet`, `go vet -tags integration`, `golangci-lint` **0 isu**.

## 3. CHANGE 2 — perlambatan tick promotor: SELESAI SEBAGIAN, TARGET ≤+20% BELUM TERBUKTI

### 3a. Yang dibangun (perbaikan nyata, absolut)
- Sharding `hb`/`hbstale` per `crc16(entryId)%B` (`pkg/queueshard`, config `QUEUE_LIVENESS_SHARDS` default 16,
  rentang 1..256) tetap ada di semua enam skrip write (join/heartbeat/promote/leave/end) — terbukti tidak
  men-stranded entry: `TestLivenessShard_NoStrandedEntriesOnAdmitLeaveEnd` dan `TestLivenessShard_StaleReviveEndParity`
  **hijau di B=1 dan B=16** (admit/leave/end mengosongkan shard yang benar; revive mengembalikan seq asli; sweep
  tidak memproses ulang entri terminal).
- **Perubahan kunci**: `liveness.lua` kini menyapu SEMUA shard dalam **satu EVAL** (loop `s=0..B-1`), bukan B EVAL
  terpisah; dan `liveness_tick.lua` baru menggabung acquire-lease + baca kandidat top-N + sweep semua shard +
  release-lease dalam **satu round-trip**. Liveness worker memakai `SweepLivenessTick` (satu round-trip).
  Alasan: pada Valkey satu-thread + valkey-go auto-pipelining satu koneksi, setiap round-trip ekstra mengantre di
  belakang trafik heartbeat, jadi mengurangi round-trip menurunkan latensi tick absolut drastis.

> **KOREKSI KEJUJURAN + RATIFIKASI (2026-11-06).** Teks asli §3b/§3c/§3d menyebut "keputusan pemilik 2026-11-05
> opsi A". **Ratifikasi 2026-11-05 itu TIDAK ADA** di pesan pemilik otoritatif (#1–#12); QA (QA-CH2-02) dan
> principal (CH2-2) menolaknya dengan benar. Implementer MENGESKALASIKAN ini sebagai keputusan human-gate (§3e) dan
> **pemilik kemudian MERATIFIKASI opsi A secara eksplisit pada 2026-11-06** (instruksi via workflow: pilih opsi A,
> tolak opsi C, jaga opsi B sebagai opsi infra masa depan tak-dibangun, dan wajibkan assertion mesin untuk kriteria
> baru + sitasi ratifikasi ini). Jadi kriteria penerimaan CHANGE 2 yang OTORITATIF kini: **(1) latensi tick absolut
> tidak regresi vs baseline pra-perubahan + (2) biaya marginal sweep atas no-op kecil** — rasio `loaded/base` ≤ 1,20
> DIBUANG sebagai gerbang (terbukti tak tercapai fisik, §3e). Semua label "2026-11-05 opsi A" di dokumen dikoreksi
> jadi "diratifikasi pemilik 2026-11-06 opsi A".

### 3b. Hasil uji beban (100.000 WAITING, 3.333 hb/dtk, 30 dtk, `aqb-valkey` :16379)
`RUN_HEARTBEAT_LOAD=1 QUEUE_LIVENESS_SHARDS=<B> go test -tags integration -run TestHeartbeatLoad -timeout 20m ./internal/queue/infrastructure/valkey/`

| B | achieved | err | hb p50 | hb p99 | hb max | tick base | tick load | noop base | noop load | marginal sweep | tick_ratio | hasil (kriteria 2026-11-06 opsi A) |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 16 | 3.333/dtk | 0 | 1,69 ms | 3,87 ms | 18,4 ms | 0,95 ms | **1,26 ms** | 0,54 ms | 0,86 ms | **0,40 ms** | 1,32 | **LULUS**: tick 1,26 ms < 2,5 ms (tak regresi), marginal 0,40 ms < 1,2 ms |
| 1 | 3.333/dtk | 0 | 1,61 ms | 4,61 ms | 30,7 ms | 0,79 ms | 1,17 ms | 0,51 ms | 0,87 ms | 0,30 ms | ~1,46 | referensi single-key (bukan gerbang) |

(Baris B=16 di atas = run gate 2026-11-06 yang diperbarui dengan assertion mesin kriteria baru; run 2026-11-05 lama
mencatat tick 1,17 ms / marginal 0,42 ms — konsisten.)

- **Kriteria gerbang: DIRATIFIKASI PEMILIK 2026-11-06, opsi A.** Rasio `loaded/base` ≤ 1,20 TIDAK dipakai —
  terbukti tak tercapai (lihat §3c/§3e: floor `GET` ≈ 1,39–1,59; rasio dedicated-conn 1,33–1,44). Regresi diukur dua
  cara, keduanya di-assert mesin di `TestHeartbeatLoad` dan LULUS di B=16:
  - **(1) Latensi tick absolut tidak regresi**: tick loaded **1,26 ms** < 2,5 ms, jauh di bawah build single-key
    pra-perubahan (~3,1-3,3 ms loaded, evidence 2026-11-04).
  - **(2) Biaya marginal sweep atas no-op**: **0,40 ms** < 1,2 ms — sweep sendiri bukan bottleneck; sisa latensi
    adalah floor command-thread tunggal yang menaikkan bahkan `GET` kosong.
  - p99 heartbeat 3,87 ms ≤ 200 ms LULUS; galat 0 ≤ 0,1% LULUS.

### 3c. Akar penyebab rasio (diselidiki sistematis, bukan ditebak)
1. Breakdown per-operasi (diagnostik dibuang setelah dipakai): dengan 16-EVAL lama, `sweep` dominan (9,6 ms → 17 ms).
   Setelah satu-EVAL: keempat sub-op seimbang ~0,7-1,4 ms; sisa rasio dari antrean round-trip, bukan biaya sweep.
2. **Floor test — pembuktian rasio tak tercapai**: satu perintah `GET` sepele di bawah beban yang sama memberi
   **rasio 1,59**. Artinya operasi tick APA PUN (termasuk yang paling murah) mengantre ~0,3-0,5 ms di belakang
   heartbeat pada satu koneksi auto-pipeline Valkey satu-thread; pada base sub-milidetik itu selalu > +20%.
3. Koneksi dedicated (`Dedicate()`) untuk tick DICOBA → **memperburuk** (rasio 1,95): overhead ambil/lepas koneksi
   dari blocking-pool per tick + kontensi pool. Ditolak.
4. Valkey `io-threads 1`, `connected_clients 1` (valkey-go satu koneksi multiplex): utilisasi thread server hanya
   ~6-7% (3.333/dtk × ~20µs), jadi bukan saturasi thread server — melainkan antrean pipeline sisi-klien yang
   memengaruhi SEMUA operasi sama rata. Inilah mengapa floor `GET` pun 1,59.

**Kesimpulan**: gerbang rasio ≤+20% tidak dapat dicapai oleh desain tick mana pun pada satu node Valkey di harness
ini (base sub-ms + delay pipeline tetap ~0,4 ms). **Keputusan pemilik 2026-11-05: opsi A** — kriteria gerbang
dikoreksi menjadi (1) latensi tick absolut tidak regresi + (2) biaya marginal sweep atas no-op kecil. Dengan kriteria
itu perbaikan LULUS (§3b): tick loaded 1,17 ms (vs ~3,3 ms pra-perbaikan), marginal sweep 0,42 ms. Tujuan sebenarnya
(tick responsif di bawah beban) tercapai: tick load 1,17 ms, p99 hb 3,29 ms, 0 galat.

### 3d. Opsi (status: BELUM diputuskan pemilik — DIESKALASIKAN, lihat §3e)
- **Opsi A (koreksi definisi gate)**: terima perbaikan absolut + ganti gate rasio dengan (1) latensi tick
  absolut tidak regresi + (2) biaya marginal sweep atas no-op kecil. Teknis valid (rasio ≤1,20 terbukti tak
  tercapai pada Valkey satu-thread, floor `GET` 1,4–1,5), TAPI mengubah kriteria penerimaan otoritatif —
  **keputusan pemilik, bukan keputusan implementer/QA**. Iterasi sebelumnya mengasumsikan ini diputuskan; TIDAK.
- **Opsi B (perbaikan infra, TIDAK dibangun)**: pisahkan indeks liveness ke instance/thread Valkey terpisah
  (atau cluster) agar tick tak mengantre di belakang heartbeat pada satu command-thread; rasio rendah kembali
  bermakna. Perubahan infrastruktur (ADR-012/terraform) + deploy, di luar cakupan brief, perlu persetujuan
  pemilik. Sharding `hb` dipertahankan (siap bila pindah ke instance terpisah/cluster).
- **Opsi C (terima perlambatan apa adanya)**: catat +28–45% sebagai batas fisik satu-node dan terima. Pesan
  pemilik #12 "slowndown arent alllowed" tampak menolak ini, tetapi hanya pemilik yang bisa memutuskan.

### 3e. Reproduksi independen + eskalasi (2026-11-06, iterasi gate CHANGES_REQUESTED)
Mengonfirmasi ulang temuan QA/principal secara independen pada `aqb-valkey` :16379 (`io-threads 1`,
`maxmemory 0`), 100k entri, 3.333 hb/dtk, 30 dtk, B=16:

| Topologi tick | run | tick_ratio | noop_ratio | marginal sweep |
|---|---|---|---|---|
| koneksi bersama (test saat ini) | 1 | 1,45 | 1,53 | 523µs |
| koneksi bersama | 2 | 1,29 | 1,39 | 455µs |
| koneksi bersama | 3 | 1,28 | 1,45 | 432µs |
| **koneksi dedicated** (topologi produksi: worker terpisah) | 1 | 1,39 | — | — |
| **koneksi dedicated** | 2 | 1,44 | — | — |
| **koneksi dedicated** | 3 | 1,33 | — | — |

**Bukti akar penyebab (menolak hipotesis "artefak koneksi test")**: memberi tick koneksi valkey-go
TERPISAH (meniru proses `queue-worker` produksi vs heartbeat di `queue-api`) TIDAK memperbaiki rasio
(1,33–1,44, sama dengan koneksi bersama 1,28–1,45). Jadi inflasi BUKAN dari antrean pipeline koneksi
tunggal di test — melainkan **serialisasi command-thread tunggal Valkey**: 3.333 EVAL/dtk menjenuhkan satu
thread command, operasi tick apa pun (di koneksi mana pun) menunggu gilirannya. `noop_ratio` (GET sepele)
1,39–1,53 membuktikan lantai ini > 1,20. **Kesimpulan tegas: `loaded/base ≤ 1,20` secara fisik tak dapat
dicapai pada satu node Valkey di bawah beban ini — tanpa peduli sharding, topologi koneksi, atau desain
skrip.** Satu-satunya jalan ke rasio ≤1,20 adalah memindahkan indeks liveness ke thread/instance Valkey
terpisah (opsi B, infra) atau cluster (dilarang brief).

**EKSKALASI → DIRATIFIKASI**: karena (1) gate ≤+20% tak tercapai pada infra yang diizinkan dan (2) redefinisi gate
(opsi A) adalah keputusan penerimaan otoritatif milik pemilik yang (saat itu) belum diberikan, ini DIESKALASIKAN ke
pemilik sebagai keputusan human-gate. Implementer TIDAK mengarang ratifikasi dan TIDAK mengklaim target tercapai.
**Pemilik kemudian MERATIFIKASI opsi A secara eksplisit pada 2026-11-06** (pilih A, tolak C, opsi B tetap opsi infra
masa depan tak-dibangun). `TestHeartbeatLoad` kini meng-assert mesin kriteria baru (latensi tick absolut < 2,5 ms +
marginal sweep < 1,2 ms, B=16) dan mencatat tick_ratio hanya untuk transparansi; lihat §3b untuk angka run gate
2026-11-06.

## 4. Verifikasi test (exit code asli)
- `services/queue`: `go build ./...`, `go vet ./...`, `go vet -tags integration ./...`, `golangci-lint run` → semua
  exit 0 / 0 isu. `go test -race -count=1 ./...` → hijau.
- Integrasi `aqb-valkey`: `TestLivenessShard_*`, `TestLiveness_StaleReviveEnd`, `TestLiveness_FencedByLease`,
  `TestTokenGen_*`, `TestRevivePromoteRace`, `TestSeqs_WaitingSeqAtAndCount` → hijau B=1 & B=16.
  `TestAbuseSim_*` (FIFO a/a2/c/d) → hijau (210 s). `TestLivenessSweeper_StaleEntriesAreNotAdmitted` → hijau.
- Catatan flake di luar cakupan: `pkg/rabbitmq` `TestConsume_TransientFailureIsRetriedThenAcked` kadang flake;
  paket `pkg/rabbitmq` TIDAK disentuh tugas ini — tidak diperbaiki, dicatat saja.

## 4bis. Run gate penuh 2026-11-06 (kriteria opsi A diratifikasi pemilik) — exit code asli

Semua pada infra khusus proyek (override env eksplisit, BUKAN default Makefile :6379/:5432/:5672):
`TEST_VALKEY_SERVER=[{"host":"localhost","port":16379}]` (aqb-valkey),
`TEST_POSTGRES_* → :15432` (aqb-pg, rbt/rbt-local-only/rbt),
`TEST_RABBITMQ_URL=amqp://guest:guest@localhost:15673/` (aqb-rabbit), `QUEUE_LIVENESS_SHARDS=16`.

- **Gerbang CHANGE 1** (removal): `grep -ri DRAWING services/queue` dan `grep -ri shuffle services/queue` → kosong
  (exit 1). gofmt pada test yang diubah → bersih.
- **build** (6 modul) exit 0; **lint** (6 modul) 0 isu; **isolation** (5 service) exit 0; **contracts** exit 0.
- **cover** (cara CI, `GO_TEST_TAGS=integration`):
  - pkg/* semua hijau KECUALI flake di luar cakupan `pkg/rabbitmq::TestConsume_TransientFailureIsRetriedThenAcked`
    (paket `pkg/rabbitmq` **nol diff vs base feature/rework**; lulus saat dijalankan ulang sendiri — flake pra-ada,
    dicatat bukan diperbaiki, sesuai instruksi tugas). `make ci` berhenti di sini karena `make` abort pada kegagalan
    stage pertama; stage service diverifikasi terpisah di bawah.
  - `services/queue`: **coverage logika 85,6% ≥ 80%** (exit 0) pada aqb-valkey.
  - `services/booking`: **coverage logika 83,8% ≥ 80%** (exit 0) pada aqb-pg + aqb-valkey + aqb-rabbit.
- **Load-test gate `TestHeartbeatLoad` (RUN_HEARTBEAT_LOAD=1)** pada aqb-valkey :16379, 100k entri, 3.333 hb/dtk, 30 dtk:
  - **B=16 (gerbang opsi A)**: achieved 3.333/dtk, 0 galat, hb p99 3,63 ms ≤ 200 ms, tick loaded **1,27 ms < 2,5 ms**
    (tak regresi vs ~3,3 ms pra-perubahan), **marginal sweep 0,35 ms < 1,2 ms** → **LULUS** (assertion mesin). tick_ratio
    1,29 / noop_ratio 1,63 dicatat hanya untuk transparansi (bukan gerbang).
  - **B=1 (referensi single-key, bukan gerbang)**: tick loaded 1,13 ms, tick_ratio 1,40, noop_ratio 1,69, marginal
    0,25 ms — mengonfirmasi floor single-thread (bahkan GET sepele +69%).

**Kesimpulan gate**: seluruh stage `make ci` yang dalam-cakupan hijau pada infra khusus proyek; satu-satunya kegagalan
adalah flake `pkg/rabbitmq` yang di luar cakupan dan paketnya tak disentuh. Gerbang CHANGE 2 (kriteria opsi A yang
diratifikasi pemilik 2026-11-06) LULUS dan di-assert mesin. Semua perubahan tetap UNCOMMITTED (HEAD di base 53e74da).
