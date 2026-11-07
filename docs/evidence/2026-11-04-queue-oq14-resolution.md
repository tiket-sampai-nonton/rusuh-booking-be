# 2026-11-04 — Queue hardening: resolusi OQ-14 (design rev 4)

Konteks: `improve/queue-service-hardening`. Dokumen yang berubah: artefak desain `design.md` (rev 3 → rev 4). Tidak ada kode produksi diubah. Pemicu: temuan MEDIUM terbuka di `design.review.json` dan penolakan QA atas opsi (a) rencana rev 6 (`plan.review.json`, T22).

## Masalah

Baris RESERVED di Booking yang dipertahankan setelah entry WAITING ditunda (FULL/NOT_HEAD) menua melewati `expires_at`, lalu `ReserveBatch` berikutnya untuk entry yang sama dijawab `ALREADY_ACTIVE` dan promoter memanggil `EndEntry`. Pembeli yang masih menunggu terhapus walau promoter sehat. Rev 3 menyebut ini hanya terjadi setelah "outage > 60 detik"; itu keliru.

## Fakta yang diverifikasi dari kode (bukan asumsi)

| Fakta | Sumber |
|---|---|
| `reserveOne`: baris untuk `queue_entry_id` yang sama selalu di-replay sebelum cek stok/kuota; RESERVED → `GRANTED` (id & `expires_at` asli, juga bila sudah lewat sebelum disapu); status lain → `ALREADY_ACTIVE`. | `services/booking/.../postgres/admission_repo.go` `reserveOne` |
| `queue_entry_id uuid NOT NULL UNIQUE` pada seluruh tabel; satu-satunya `INSERT` ada di `reserveOne`; tidak ada `UPDATE` yang mengembalikan baris ke RESERVED. Jadi baris EXPIRED/RELEASED tidak pernah bisa diberi grant lagi (permanen). | `deployments/migrations/0001_init.up.sql` (admissions); grep `services/booking/internal` |
| `expires_at = reserved_at + claim_window_seconds`; default **180**, `CHECK BETWEEN 60 AND 600` (rev 3 menulis 60 detik). | `0001_init.up.sql`, `reserveOne` step 8 |
| `DueSessions` memilih RESERVED dengan `expires_at < now` (tanpa grace); `ExpireSession` → `EXPIRED`, lepas stok, kirim `journey.ended{UNCLAIMED}`; sweeper Booking berjalan pada `WORKER_INTERVAL` default 5 detik. | `admission_repo.go`, `booking/internal/config/load.go`, `delivery/worker.go` |
| `Release` → `RELEASED` (juga baris mati). | `admission_repo.go` `Release` |
| Promoter dan join memeriksa kapasitas/head sebelum `ReserveBatch` tanpa kunci; join tidak memakai lease promoter; `join.lua` memberi seq (INCR) dan ZADD atomik. | `worker/promoter.go`, `usecase/join.go`, `assets/lua/join.lua`, `valkey/queue_store.go` |

## Opsi yang dibahas

- (a) rencana rev 6: lewati dan biarkan WAITING — **ditolak**: Booking tidak akan pernah memberi grant lagi, jadi head macet selamanya (bertentangan dengan alasan REQ-QSH-01) dan T22 tidak dapat dipenuhi oleh stub yang benar.
- (b) pertahankan `EndEntry` dan hanya dokumentasi — ditolak: pembeli hidup tetap terbunuh pada deferral FULL biasa.
- (c) release saat FULL — ditolak: RELEASED juga baris mati.
- (d) kunci reservasi turunan per percobaan — ditolak: kebocoran kontrak lintas layanan.
- (e) slot-dulu (status RESERVING) — ditolak: blast radius besar di semua script.
- **(f) dipilih:** serialisasi semua jalur pengambil slot dengan lease promoter per event + fence token di `promote.lua`, cek kapasitas/head di bawah lease **sebelum** `ReserveBatch`; `EndEntry(ALREADY_ACTIVE)` dipertahankan untuk entry yang terbukti mati.
- (g) re-reserve di sisi Booking — dicatat sebagai OQ-15 (opsional).

## Argumen batas (ringkas; lengkap di design §5.4a.3)

- L1: di bawah lease valid, `A(t1) ≤ A(t0)` karena semua penambah `active` memegang lease dan lainnya hanya mengurangi; maka `Promote` tidak mungkin `FULL`/`NOT_HEAD` setelah grant. Fence mengembalikan `LEASE_LOST` tanpa mutasi bila lease hilang.
- T1: deferral biasa tidak memanggil Booking → tidak ada baris → tidak ada yang menua → pembeli hidup tidak diakhiri (properti 2).
- T2: head mati diakhiri pada tick pertama yang dipegang lease dengan `free ≥ 1`; selisih tick head macet ≤ 1 (properti 1), terlepas dari cara entry menjadi mati.
- T3: kasus residual C1–C4 diurai tuntas dan masing-masing dibatasi + dihitung (metrik).
- Invarian: tidak ada jalur stok baru (INV-1), tidak ada jalur baru ke ADMITTED (INV-2), deadline tetap milik Booking (INV-4).

## Dapat diuji dengan stub Booking yang benar

Stub hanya mengimplementasi semantik terverifikasi (replay, Release, SweepSessions); tidak ada operasi "grant ulang". TC-QUE-19c (serialisasi, `-race`, FULL-setelah-grant = 0), 19d (reproduksi reviewer sebagai tes negatif), 19e (tick terputus), 19f (batas head mati), dan tes Lua fence. Mengganti T22 pada rencana.

## Belum diverifikasi

Perilaku Lua fence, kontensi lease di bawah beban join nyata, nilai `claim_window_seconds`/`WORKER_INTERVAL` yang di-deploy (hanya default dari kode), dan `make ci` — tidak ada kode dijalankan pada langkah desain ini.

---

## Iterasi 2 — design rev 5 (menutup 3 temuan `design.review.json` atas rev 4)

Konteks: reviewer menolak rev 4 (REJECT; 2 MEDIUM + 1 NIT). Keputusan OQ-14 (lease + fence, ADR-029) **tetap**; temuan menyangkut cakupan klaim batas dan satu deviasi persyaratan yang tidak tercatat. Tidak ada kode produksi diubah. Semua temuan diverifikasi ulang terhadap sumber sebelum diterima.

| # | Temuan | Verifikasi sumber | Keputusan desain |
|---|---|---|---|
| 1 MEDIUM | Klaim "head macet dibatasi 1 tick" (T2) hanya mencakup head yang sampai ke `ReserveBatch`. | `worker/promoter.go` `runEvent` baris 93–105: `GetEntry` `!found` atau `UnsealEmail`/`SealBuyerEmail` gagal → `continue` tanpa reserve, tanpa `EndEntry`, tanpa menghapus dari `waiting`; dengan `free==1` berulang tiap tick selamanya. `assets/lua/end_entry.lua` saat ini hanya `ZREM active`, tidak pernah `ZREM waiting` (jadi orphan id memang bisa muncul). | **C5 orphan:** `found=false` → `EndEntry(PROMOTER_ORPHAN)` seketika (cabang `NO_ENTRY` di §5.1 tidak bergantung outcome; tanpa skrip baru), batas ≤ 1 tick. **C6 PII tak terbaca:** dibedakan *entry-scoped* vs *sistemik* dalam tick yang sama (probe baca-saja `QUEUE_UNSEAL_PROBE`=3 id berikutnya); entry-scoped → penghitung `note_unreadable.lua` (dijaga status WAITING, tidak bisa membuat ulang hash) lalu `EndEntry(UNREADABLE_PII)` pada `QUEUE_UNSEAL_FAIL_MAX`=3, batas ≤ 3 tick; sistemik → tidak mengakhiri apa pun, ERROR + metrik, dicatat sebagai **satu-satunya kasus tak-terbatas yang diterima** (OQ-16). T2 dipersempit; T2a baru. |
| 2 MEDIUM | Join dengan lease sibuk mengembalikan WAITING, melanggar SRS-F-04-07 / REQ-QSH-03.2 ("shall be admitted directly") dan tidak dicatat di §2a. | `docs/analisa/02-SRS-RBT-001.md` baris 434 (SHALL). Desain rev 4 hanya menyebut sebagai risiko (R-9). | Baris baru di §2a: **Amended**. Satu percobaan ulang 25 ms (jitter 15–35 ms) sebelum WAITING agar kasus umum (tick promoter idle, lease dipegang beberapa ms) tetap ADMITTED dalam respons. Tes **TC-QUE-20b**. Docs task wajib menandai SRS-F-04-07 "amended by design rev 5". |
| 3 NIT | Replay `GRANTED` dengan `expires_at` yang sudah lewat tetapi belum disapu Booking (jendela ≤ `WORKER_INTERVAL` 5 s) bisa dipromosikan dengan deadline lampau. | `reserveOne` mengembalikan `expires_at` asli pada replay (Booking `admission_repo.go`); `promoter.go` baris 133–139 hanya fallback saat *parse gagal*. | Aturan 3a: bila deadline terparse `<= now`, kandidat **dilewati tanpa mutasi** (tidak `Promote`, tidak `TurnArrived`, tidak `EndEntry`); tick berikutnya replay lagi atau, setelah sweep, `ALREADY_ACTIVE` → diakhiri sesuai T2 (kasus **C7**). Menjaga INV-4. |

### Perubahan sampingan yang konsisten

- Outcome baru `PROMOTER_ORPHAN` (tanpa kelas) dan `UNREADABLE_PII` (kelas B di `end_entry.lua`; tanpa penambahan ini jatuh ke kelas C = outcome Booking). Konfigurasi baru `QUEUE_UNSEAL_FAIL_MAX`, `QUEUE_UNSEAL_PROBE`. Field hash opsional `unreadableN` (aditif; tidak ada = 0).
- Properti P19 diperluas; tes baru **TC-QUE-19g..19j** (orphan, PII entry-scoped, PII sistemik, replay lapsed) dan **TC-QUE-20b**. Stub Booking T13 tidak berubah (tes baru tidak sampai ke `ReserveBatch` kecuali 19j yang memakai replay stub yang benar).
- Klaim "≤ 1 tick" yang tidak dikualifikasi di ADR-029, R-8, OQ-11 dikoreksi; T3 kini C1–C7.

### Re-scrutiny (agree / amend / reject)

- Temuan 1 AGREE; saran "akhiri setelah N kegagalan berturut-turut" di-AMEND menjadi aturan entry-scoped vs sistemik, karena akhir buta setelah N kegagalan akan mengakhiri **seluruh antrean** pada salah-konfigurasi `PII_ENC_KEY` (kunci tunggal statis di `config/queue.go`, tanpa key ring), lebih buruk daripada admisi yang terjeda.
- Temuan 2 AGREE (retry opsional diambil). Temuan 3 AGREE; memilih *skip* ketimbang *end langsung* karena skew jam Queue↔Booking bisa mengakhiri pembeli yang masih sah; keputusan akhir tetap milik Booking.

### Belum diverifikasi

Perilaku Lua `note_unreadable.lua` dan probe (dirancang, belum dijalankan); perilaku `UnsealEmail` pada blob rusak vs kunci salah (diasumsikan sama-sama error, dibaca dari call site); apakah `waiting` produksi saat ini berisi orphan id (tidak diukur); nilai retry 25 ms (pilihan desain, belum diukur di bawah beban); `make ci` tidak dijalankan pada langkah desain ini.

---

## Iterasi 3 — design rev 6 (menutup 2 temuan MEDIUM `design.review.json` atas rev 5)

Konteks: reviewer menolak rev 5 (REJECT; 2 MEDIUM). Keputusan OQ-14 (lease + fence, ADR-029) **tetap**; temuan menyangkut klaim "jumper langka" dan ketidakjelasan semantik aturan 3a. Tidak ada kode produksi diubah. Kedua temuan diverifikasi ulang ke sumber sebelum diterima.

### Temuan 1 (MEDIUM) — jalur jumper (C3) bukan kasus langka

Fakta dari sumber (`services/booking/internal/booking/infrastructure/postgres/admission_repo.go` `reserveOne`): tiap entry berjalan dalam `SAVEPOINT entry`; langkah 1 mengunci `event_supply FOR UPDATE`; langkah 4 `freeUnits == 0` → `ROLLBACK TO SAVEPOINT entry` + `NO_SUPPLY_NOW`. Rollback ke savepoint melepas kunci yang diambil setelahnya, sehingga commit yang mengembalikan stok (Release, ExpireSession, hold kedaluwarsa, pembayaran gagal) di antara entry k dan k+1 membuat entry k+1 `GRANTED` setelah `NO_SUPPLY_NOW`. `promoter.go` lalu menjadikannya jumper dan `ReleaseAllocation("FIFO_SKIPPED")` → baris `RELEASED` → `ALREADY_ACTIVE` permanen (UNIQUE tabel penuh) → `EndEntry`. Ini terjadi pada churn stok biasa di tepi sold-out, bukan karena error DB per-entry. Contoh "head butuh 3, sisa 2" di rev 5 juga tidak mungkin: Units selalu 1.

Opsi:

| Opsi | Keputusan | Alasan |
|---|---|---|
| (b) pertahankan release | Ditolak | Membunuh pembeli hidup; reproduksi reviewer. |
| (a) reviewer: berhenti release, simpan baris RESERVED, **tidak dipromosikan** | **Setuju** menghapus release, **ditolak** tidak mempromosikan | Jumper menahan 1 unit yang dibutuhkan head; hanya di-replay bila ada di jendela `TopWaiting(free)` (tidak pernah bila `free == 1` atau head di depannya). Bila stok lain baru kembali setelah claim window (180 s; TTL booking 5 mnt, bayar 10 mnt per steering `product`), baris menua → `EXPIRED` → `ALREADY_ACTIVE` → entry diakhiri: kill yang sama, hanya tertunda. |
| **(c) adopsi (dipilih)** | Dipilih | `GRANTED` yang mengikuti `NO_SUPPLY_NOW` dipromosikan di tick yang sama (alokasi sudah ada, slot dalam jendela `free` yang dijaga lease, L1). Tidak ada release → tidak ada baris `RELEASED`; tidak ada baris yang menua; tanpa perubahan Booking. |
| (d) satu panggilan Booking per entry | Dicatat (OQ-17) | FIFO ketat, tetapi N round trip per tick dan batching hilang. |
| (e) `NO_SUPPLY_NOW` lengket di Booking | Dicatat (OQ-17) | Perubahan Booking (di luar scope REQ-QSH-15.7). |

Biaya yang diakui (bukan disembunyikan): inversi FIFO satu-posisi yang terbatas — entry yang diadopsi masuk mendahului entry `NO_SUPPLY_NOW` di batch itu; entry tersebut tidak tersentuh (tanpa baris di Booking), tetap pertama di `waiting`, dan kandidat pertama tick berikutnya. Ini menyimpang dari `specs/queue-service/requirements.md` 6.3 dan SRS-F-25-02; dicatat di §2a, dihitung (`queue_promote_fifo_adopted_total`, WARN dengan id), diputuskan pemilik lewat OQ-17.

Argumen batas (properti 1 dan 2): satu-satunya pemanggil `ReleaseAllocation` kini jalur terminal-skip yang dilaporkan script dan leave ADMITTED → baris `RELEASED` tidak pernah berasal dari Queue untuk entry WAITING hidup. Setiap balasan `GRANTED` ditangani tepat satu cabang: (i) masuk `promoteCands`; (ii) dilewati karena lapsed (C7); (iii) tick mati sebelum hasil diterapkan (C1, termasuk balasan hilang setelah Booking commit). Tidak ada cabang keempat. Entry WAITING hidup hanya dapat mencapai `EXPIRED` lewat C1/C2/C4/C7, masing-masing terbatas/terhitung. C3 dihapus.

Uji terhadap stub Booking yang benar: stub ditambah hook `afterEntry(k, fn)` (menjalankan fn setelah entry ke-k, sebelum entry berikut) untuk memodelkan commit konkuren di celah savepoint — setia pada sumber, bukan kapabilitas Booking baru; tetap tanpa operasi "grant ulang". TC-QUE-19b (adopsi, 0 `ReleaseAllocation`, `Units: 1`) dan TC-QUE-19k (stok kembali di antara dua entry; tick 1-3 melewati claim window + `SweepSessions`; E2 tidak pernah `ALREADY_ACTIVE`/diakhiri; E1 tanpa baris sampai stok kembali). TC-QUE-19k wajib gagal lebih dulu terhadap promoter rev 5 (bukti failing-first).

### Temuan 2 (MEDIUM) — semantik aturan 3a di loop batch

Dua pembacaan yang sah di rev 5: (1) `continue`; (2) memakai flag FIFO sehingga semua `GRANTED` sesudahnya dilepas → pembeli hidup terbunuh. Rev 6 menetapkan verbatim: **lapsed replayed `GRANTED` dilewati dengan `continue`; tidak mengatur flag FIFO apa pun, bukan jumper, tidak dilepas; kandidat `GRANTED` berikutnya di batch dipromosikan normal.** Alasan aman: baris lapsed sudah mati di Booking menurut aturannya sendiri (`Activate` butuh `expires_at > now`; `DueSessions` memilih RESERVED `expires_at < now` tanpa grace). Karena rev 6 juga menghapus konsep jumper, tidak ada flag yang dapat mengubah grant berikutnya menjadi release: pembacaan (2) dikecualikan oleh konstruksi, bukan hanya oleh redaksi.

Perhitungan: kandidat lapsed memakai satu posisi kandidat tick (`n_promotable <= free - lapsed`); dengan `free == 1` tick no-op sampai sweeper Booking membalik baris, lalu `ALREADY_ACTIVE` → diakhiri (C7). Asumsi **A-1**: `session-sweeper` Booking berjalan; bila Booking worker mati, batas bersyarat (dan tak ada timer Booking lain yang jalan). Kondisi alert: `queue_promote_lapsed_replay_skipped_total` naik di tiap tick > 3 × `WORKER_INTERVAL` (15 s). Pengakhiran sendiri oleh Queue setelah N skip ditolak (Booking pemilik hasil; skew jam) → OQ-17.

TC-QUE-19j diperluas: batch `[E1 lapsed, E2 fresh GRANTED]` → E2 dipromosikan, 0 `ReleaseAllocation`, E1 tak tersentuh; `[E1 lapsed]` dengan `free = 1` → no-op lalu diakhiri sekali setelah sweep stub; sweeper stub berhenti → E1 terus dilewati dan penghitung naik (menunjukkan syarat A-1).

### Re-scrutiny (agree / amend / reject)

Temuan 1: AGREE cacatnya; AGREE menghapus release; AMEND perbaikannya (adopsi, bukan simpan-tanpa-promosi) karena simpan-tanpa-promosi menahan unit head dan menua menjadi kill yang sama. Temuan 2: AGREE seluruhnya.

### Belum diverifikasi

Semantik PostgreSQL pelepasan kunci pada `ROLLBACK TO SAVEPOINT` diambil dari semantik terdokumentasi dan reproduksi reviewer, tidak dijalankan di sini; frekuensi balapan celah-savepoint dan adopsi FIFO di produksi; interval sweeper Booking terdeploy dan kesehatan Booking worker (A-1); `make ci` tidak dijalankan pada langkah desain ini.

---

## Iterasi 4 — design rev 7 (menutup 1 temuan MEDIUM `design.review.json` atas rev 6)

Konteks: reviewer menolak rev 6 (REJECT; 1 MEDIUM). Keputusan OQ-14 (lease + fence, ADR-029) dan adopsi jumper rev 6 **tetap**; temuan menyangkut klaim kelengkapan batas head macet. Catatan penomoran: prompt iterasi ini menyebut "rev 4" dan masalah OQ-14 awal; berkas di disk sudah rev 6 dengan OQ-14 terselesaikan, dan satu-satunya temuan terbuka adalah di bawah ini, sehingga revisi berikutnya adalah rev 7. Tidak ada kode produksi diubah.

### Temuan (MEDIUM) — `NO_SUPPLY_NOW` bukan selalu "menunggu stok"

Fakta dari sumber (`services/booking/internal/booking/infrastructure/postgres/admission_repo.go`, `reserveOne` dan `supplyOutcome`, dibaca baris per baris): `NO_SUPPLY_NOW` dihasilkan oleh (1) kegagalan `SAVEPOINT`; (2) scan `event_supply ... JOIN events (published_at IS NOT NULL AND status <> 'CANCELLED') ... FOR UPDATE OF s` gagal dengan error apa pun, termasuk `pgx.ErrNoRows` bila event belum terbit/`CANCELLED`/baris supply hilang (sebab **event-scoped**, tidak ada di daftar reviewer); (3) upsert `buyer_quotas` gagal; (4) `buyer_quotas ... FOR UPDATE` gagal; (5) langkah 4 `freeUnits == 0` dengan `allocated + held > 0` (**satu-satunya arti stok**); (6) UPDATE `event_supply` gagal non-CHECK (`supplyOutcome`); (7) INSERT `admissions` gagal selain unique/check (mis. `22P02`, FK `account_id` akun yang terhapus). Semua (kecuali 5) melakukan `ROLLBACK TO SAVEPOINT`, tanpa baris, tanpa perubahan stok, dan deterministik untuk entry/event itu sehingga berulang tiap tick. Rev 6 menyebutnya "kasus antre yang sah, resource-bound" tanpa membaca jalur ini; kalimat itu ditarik.

Dampak (aman/fail-closed): INV-1/2/4 tidak terpengaruh (tidak ada baris, stok, admisi, atau timer untuk entry racun; entry berikutnya hanya diadopsi bila `GRANTED` nyata). Liveness: untuk `p` entry racun di jendela kandidat, kapasitas admisi efektif turun `p`; antrean sepenuhnya terblokir hanya bila `capacity - p <= 0` (mis. kapasitas 1) atau sebab event-scoped.

### Opsi dan keputusan

| Opsi | Keputusan | Alasan |
|---|---|---|
| (a) reviewer: catat C8 sebagai kasus tak-terbatas kedua, alert per-entry, minta Booking membedakan outcome | **Setuju (diamandemen)** | Informasinya memang tidak ada di balasan Booking; Queue tidak dapat membatasinya sendiri tanpa risiko. |
| (b) reviewer: akhiri head setelah N tick berturut-turut saat entry berikutnya diadopsi | **Ditolak** | (i) klaim "false positive mustahil" salah: balapan pengembalian stok (R6-1) dan entry racun menghasilkan batch yang **sama** `[NO_SUPPLY_NOW(head), GRANTED(berikutnya)]`; ambang apa pun membunuh pembeli hidup dengan peluang > 0 (melanggar T1). (ii) tidak pernah menyala pada kasus yang disebut temuan: dengan `free == 1` batch hanya `[head]`, tidak ada grant berikutnya sebagai pembeda. |
| (c) demote head ke ekor `waiting` | Dicatat, tidak dipilih | Tidak mematikan, tetapi perlu script baru, menulis ulang kunci urutan yang disimpan Booking sebagai `queue_seq`, dan entry racun berputar selamanya. Disimpan sebagai stopgap opsional di OQ-19. |
| (d) anggap semua `NO_SUPPLY_NOW` racun setelah N ulang | Ditolak | Sold-out biasa bertahan berjam-jam; masalah false positive yang sama. |

Keputusan: C8 dicatat sebagai kasus kedua yang diterima tak-terbatas-oleh-Queue (di samping C6-sistemik/OQ-16). Klaim diperbaiki menjadi "terbatas kecuali C6-sistemik dan C8". Deteksi tanpa aksi: rekaman in-memory per event `{headID, adoptedTicks}` (tanpa Valkey/script), config `QUEUE_NO_SUPPLY_SUSPECT_TICKS` (default 50), `queue_promote_fifo_adopted_total{suspect}` dipecah, `queue_promote_head_no_supply_repeat_total{adopted}` baru, `queue_promote_unknown_outcome_total` + cabang `default` yang tidak memutasi apa pun (forward-compat untuk outcome Booking baru). Perbaikan akar: **OQ-19** (Booking owner) — outcome terpisah `ERROR` (transien) dan `INVALID_ENTRY` (deterministik; Queue akan `EndEntry` kelas B ≤ 1 tick). Penomoran: reviewer menyebut "OQ-18", tetapi OQ-18 sudah dipakai `specs/README.md:115` (`ALREADY_ACTIVE`) dan komentar `promoter.go`/`worker_test.go`, jadi dipakai OQ-19.

Batasan informasi yang dinyatakan terbuka: dengan `free == 1` atau sebab event-scoped tidak ada adopsi, sehingga hanya `adopted="false"` yang naik dan tidak dapat dibedakan dari sold-out biasa; desain menulisnya, tidak menyembunyikannya.

### Dapat diuji dengan stub Booking yang benar

Stub gains `failEntry(queueEntryID)` / `clearFail` (model setia atas jalur galat DB: `NO_SUPPLY_NOW`, tanpa baris, tanpa perubahan stok; tetap tanpa operasi grant ulang). **TC-QUE-19l**: (1) `[P,E2]`, `free=2` → E2 diadopsi, 0 `EndEntry`/`ReleaseAllocation` untuk P; (2) N tick → P tetap pertama di `waiting`, tak pernah diakhiri, counter `suspect="true"`/`adopted="true"` naik, WARN tanpa PII; (3) `clearFail(P)` → P diberi grant dan dipromosikan tanpa perubahan Queue; (4) varian `free=1` → tidak ada `suspect="true"`, hanya `adopted="false"` (dokumentasi batas informasi); (5a) balapan stok biasa → kedua seri suspect tetap 0; (5b) balapan stok adversarial N+5 tick → head **tetap tidak pernah diakhiri**, seri suspect naik (kedua kasus tak terbedakan) — ini pin perilaku melawan opsi (b); (6) pergantian head mereset; outcome tak dikenal → counter +1, tanpa mutasi. Properti baru **P20**. Tes ini gagal-dulu terhadap promoter rev 6 pada counter.

### Re-scrutiny (agree / amend / reject)

AGREE cacatnya (diverifikasi + satu sebab tambahan: `ErrNoRows` event-scoped); AGREE opsi (a) dengan renumber OQ-19; REJECT opsi (b) dengan dua alasan yang diverifikasi; AMEND counter dan tes.

### Belum diverifikasi

Bahwa kegagalan nyata (FK akun terhapus, `22P02`) benar-benar mencapai INSERT langkah 8 dengan data yang dikirim Queue (diturunkan dari tipe kolom dan `NULLIF($11,'')::uuid`, tidak dijalankan); apakah akun dapat terhapus saat entry WAITING (perilaku User service tidak dibaca di langkah ini); frekuensi entry racun/`NO_SUPPLY_NOW` event-scoped di produksi; ambang default 50 tick adalah pilihan desain, tidak diukur; `make ci` tidak dijalankan pada langkah desain ini.

---

## Iterasi 5 — design rev 8 (menutup 1 MEDIUM + 2 NIT `design.review.json` atas rev 7)

Konteks: reviewer menolak rev 7 (REJECT; 1 MEDIUM, 2 NIT). Prompt iterasi ini lagi-lagi menyebut "rev 4 / OQ-14"; OQ-14 sudah terselesaikan sejak rev 4 dan tidak dibuka ulang. Keputusan lease + fence (ADR-029), adopsi jumper (rev 6) dan "Queue tidak pernah mengakhiri head `NO_SUPPLY_NOW`" (rev 7) **tetap**. Tidak ada kode produksi diubah. Ketiga temuan diverifikasi ulang ke sumber sebelum diterima.

**Pertanyaan OQ-14 yang diminta, dijawab ulang dengan bukti sumber (ringkas; rinci di design §5.4a):**
- Semantik replay Booking nyata: `reserveOne` (`services/booking/internal/booking/infrastructure/postgres/admission_repo.go`) me-replay baris `queue_entry_id` yang sama sebelum cek stok: RESERVED → `GRANTED` (id & `expires_at` asli), status lain → `ALREADY_ACTIVE`; tabel punya `UNIQUE(queue_entry_id)` dan tidak ada jalur kembali ke RESERVED, jadi baris mati permanen. Opsi (a) rev 6 (lewati & tetap WAITING) membuat head macet selamanya dan T22 tak terpenuhi stub yang benar; ditolak.
- (1) Batas head macet: head yang sampai `ReserveBatch` dan mati diakhiri di tick pertama dengan lease dan `free ≥ 1` (T2, ≤ 1 tick); orphan ≤ 1 tick (C5); PII entry-scoped ≤ `QUEUE_UNSEAL_FAIL_MAX` tick (C6); replay lapsed dibatasi sweeper Booking (C7, asumsi A-1). Dua kasus tak-terbatas oleh Queue dinyatakan jujur: C6-sistemik (QSH-OQ-16) dan C8 (QSH-OQ-19).
- (2) Pembeli hidup tidak diakhiri pada deferral FULL: kapasitas/head diperiksa di bawah lease **sebelum** `ReserveBatch` → deferral biasa tidak membuat baris (T1), `FULL` setelah grant tak terjangkau (L1).
- (3) Invarian: tidak ada jalur baru stok (INV-1), ADMITTED hanya dengan allocation id `GRANTED` nyata (INV-2), deadline tetap milik Booking dan replay lapsed dilewati (INV-4).
- Teruji dengan stub Booking yang benar (replay, Release, SweepSessions, hook `afterEntry`, `failEntry`; tanpa operasi "grant ulang"): TC-QUE-19b..19l, 20b.

### Temuan 1 (MEDIUM) — mesin keadaan deteksi C8 tidak konsisten

Verifikasi: rekaman rev 7 `{headID, adoptedTicks}` tidak punya cara menghitung `head_no_supply_repeat_total{adopted="false"}` (butuh hitungan tick NO_SUPPLY_NOW yang tak bergantung adopsi); tick NO_SUPPLY_NOW tanpa adopsi bukan increment dan bukan reset sehingga "berurutan" tak terdefinisi; "kandidat pertama batch" bukan head bila head di-`continue` sebelum `ReserveBatch` (`promoter.go` `runEvent`, loop kandidat). TC-QUE-19l (4) dengan `free=1` memang tak dapat dipenuhi.

Keputusan: AGREE seluruhnya; terapkan rekaman usulan reviewer `{headID, noSupplyTicks, adoptedTicks}` dengan **tabel transisi lengkap** (baris 0-5: rekaman dibuang bila tak ada waiting; head berganti ⇒ reset; head tak sampai balasan ⇒ tak tersentuh; `NO_SUPPLY_NOW` + adopsi ⇒ kedua naik; `NO_SUPPLY_NOW` tanpa adopsi ⇒ hanya `noSupplyTicks`; balasan lain ⇒ keduanya 0). `headID` = `TopWaiting(free)[0]` pada awal tick. AMEND dua hal: (i) `adoptedTicks` = tick beradopsi **di dalam run NO_SUPPLY_NOW tak terputus**, direset bersama sehingga `adoptedTicks ≤ noSupplyTicks` dan "berurutan" terdefinisi atas balasan head; (ii) validasi `QUEUE_NO_SUPPLY_SUSPECT_TICKS ≥ 2` (nilai 1 melabeli setiap balapan stok tunggal). Fungsi murni `nextRec(rec, headID, outcome)` diuji table-driven, terpisah dari Booking.

Counter sebagai fungsi murni rekaman: `repeat_total{adopted}` naik sekali per tick baris 3/4 dengan `noSupplyTicks ≥ N` (`adopted="true"` iff `adoptedTicks ≥ N`); `fifo_adopted{suspect="true"}` iff baris 3 dan `adoptedTicks ≥ N`. Contoh `free` bergantian 2,1,2,1: `repeat{false}` pada tick N..2N-2, `repeat{true}`/`suspect="true"` mulai tick 2N-1 — racun tidak dapat disembunyikan oleh tick tanpa adopsi.

TC-QUE-19l ditulis ulang: (4) `free=1` kini menegaskan `repeat{adopted="false"}` naik; (6) pergantian head/balasan mereset; baru (7) `free` bergantian, (8) tick tak tersentuh (`free==0`, lease orang lain, error `ReserveBatch`, `LeaseLost`) tidak mengubah rekaman, (9) head di-drop sebelum `ReserveBatch` (rekaman tidak dikaitkan ke entry pertama batch), (10) batas memori. (5a)/(5b) tetap sebagai kontrol balapan stok dan pin terhadap opsi (b) yang ditolak.

### Temuan 2 (NIT) — contoh "FK pada `account_id`" salah

Verifikasi: `grep -n account_id services/booking/deployments/migrations/*.up.sql` → hanya `ALTER TABLE admissions ADD COLUMN account_id uuid;` (`0002_event_store.up.sql:39`) tanpa `REFERENCES`. Penyebab non-stok `NO_SUPPLY_NOW` yang realistis: `22P02` (cast uuid step 8; butuh nilai tersimpan yang malformed), `ErrNoRows` event-scoped (event belum terbit/`CANCELLED`/tanpa baris supply; deterministik untuk seluruh event), dan galat DB transien (deadlock/timeout; tidak berulang deterministik). Racun per-entry yang deterministik butuh nilai malformed; tidak diketahui bisa dihasilkan jalur join Queue sendiri (belum dibuktikan). Ditambah permintaan Booking terpisah (QSH-OQ-19 b): `supplyOutcome` dan cabang INSERT step 8 memetakan **setiap** pelanggaran CHECK ke `SOLD_OUT` (`admission_repo.go` ±baris 242 dan 271-276), dan promoter Queue merespons `SOLD_OUT` dengan `SetSale(CLOSED)` — hanya CHECK stok yang semestinya.

### Temuan 3 (NIT) — tabrakan identifier OQ-19

Verifikasi: `specs/README.md` baris 112-116 dan 128-129 sudah mendefinisikan OQ-13..OQ-19 (OQ-18 = `ALREADY_ACTIVE`, OQ-19 = kode error Payment). Keputusan: namespacing, bukan renumber — semua pertanyaan desain ini ditulis `QSH-OQ-<n>` (angka tetap; OQ-14 lama ⇒ QSH-OQ-14, OQ-19 rev 7 ⇒ QSH-OQ-19). **Catatan pemetaan untuk entri bertanggal sebelum rev 8 di berkas ini (iterasi 1-4):** "OQ-n" di sana berarti QSH-OQ-n milik desain. Plan, findings doc, dan docs task wajib memakai bentuk `QSH-OQ-`.

### Re-scrutiny (agree / amend / reject)

Temuan 1: AGREE cacat dan rekaman usulan; AMEND definisi `adoptedTicks` (dalam run tak terputus), `N ≥ 2`, tabel transisi eksplisit + fungsi murni. Temuan 2: AGREE (grep). Temuan 3: AGREE, diselesaikan dengan namespacing karena tidak ada nomor OQ yang bebas di `specs/README.md`.

### Belum diverifikasi

Tabel transisi adalah spesifikasi; belum diimplementasi atau dijalankan. Apakah jalur join Queue bisa menyimpan nilai uuid malformed (diyakini tidak, belum dibuktikan). Perilaku metrik pada registry Prometheus nyata. Frekuensi `NO_SUPPLY_NOW` non-stok di produksi. `make ci` tidak dijalankan pada langkah desain ini.

## Rencana rev 7 (tech-lead) — dampak resolusi QSH-OQ-14 ke `plan.md`

Dokumen yang berubah: `.agents/tasks/improve/queue-service-hardening/plan.md` (rev 6 → rev 7; salinan rev 6 di checkout utama dibiarkan sebagai arsip basi). Tidak ada kode produksi diubah. Pemicu: `plan.review.json` rev 6 (REJECT, 3 temuan) dan design rev 8 (resolusi rev 4 tetap).

- Gerbang keputusan manusia T21 dihapus; opsi (a) "lewati dan biarkan WAITING" tidak ada lagi di tugas mana pun. T22 diganti: hasil batch promoter (head mati `ALREADY_ACTIVE` diakhiri dalam 1 tick, adopsi jumper dengan loop `FIFO_SKIPPED` dihapus, lapsed replay `continue`, outcome tak dikenal). Tugas baru: T23 `note_unreadable.lua`, T24 adapter metrik Prometheus (port `IPromoterMetrics`), T25 C5/C6, T26 deteksi C8 (`nextRec`). Fence `promote.lua` di T05, lease+retry join di T12, stub Booking yang benar (tanpa operasi re-grant, dipin dengan uji method-set) di T13.
- Bukti mutasi T20 kini per-hunk (M-a..M-e + M-d2, `git apply`/`git apply -R`, `go vet -tags integration` wajib exit 0 saat mutasi), mencakup kelima sub-kasus.
- Fallback tanpa infra dihitung checker dan sama dengan blok deklarasi: T00, T01, T08, T09, T10, T16, T24 (T10/T16 kini hanya bergantung pada T01; `ErrNotAdmitted` pindah ke T01).
- T17: edge teknis (T01, T08, T12, T24, T25) dengan seam yang dapat diamati (helper `promoterTuning`/`sweepBatch`/`joinSettings`, argumen TTL/limit pada mock `IQueueStore`, `EndEntry(UNREADABLE_PII)` pada ambang); edge lain hanya urutan dan tidak ada gerbang manusia.
- Checker dependency/touch-set dijalankan nyata: `tasks 26 dangling [] missing_touch [] extra_touch [] unserialized_overlaps []`, fallback `match True`, `wiring_gap []`, exit 0; dua salinan termutasi gagal (exit 1) sesuai alasan.
- Belum diverifikasi: perilaku Lua dan `make ci` (port 6379 lokal terbuka dan image `valkey/valkey:8.1-alpine` ada, tetapi suite integrasi belum dijalankan — tugas T00).

## Rencana rev 9 (tech-lead, 2026-11-04)

- Menutup temuan rev 8: (1) M27-a kini dibunuh oleh TC-QUE-19c-STALE (stale-token deterministik di Valkey nyata); TC-QUE-19c-INT dinyatakan tidak sensitif terhadap fence. (2) TC-QUE-19c-DIFF hanya integration-tag, tidak bisa skip diam-diam; blok `ITFILES` + cek checker.
- Infra: `localhost:6379` = Redis 7.0.12 proyek lain, cluster 7000-7005 = proyek lain; keduanya dilarang. Tidak ada Valkey 8.1 proyek ini saat penyusunan. T00 menjalankan `valkey/valkey:8.1-alpine` di port bebas dan memverifikasi `valkey_version:8.1`.
- Checker: `tasks 27 ... unserialized_overlaps []`, fallback `match True`, `wiring_gap []`, `mutate_not_in_touch []`, `itfiles_gap []`, exit 0; mutasi negatif m7-m12 exit 1.
