# Findings — Queue Service Hardening (services/queue)

Konsolidasi hasil implementasi iterasi 1 (software-engineer). Bukti mentah ada di `EV/`. Identifikasi pertanyaan terbuka memakai namespace `QSH-OQ-<n>`.

## 1. Temuan yang diperbaiki (akar masalah, test, verifikasi)

| ID | Status | Akar masalah | Perbaikan | Test | Bukti |
|---|---|---|---|---|---|
| Q-01 | Fixed | `end_entry.lua` tidak `ZREM waiting`; orphan tak pernah dibersihkan | Kelas A/B/C, `ZREM` active+waiting, orphan self-heal | `end_entry_integration_test.go`, `TestLegacy/b` | `EV/T02-red.txt`, `EV/T02-green.txt` |
| Q-02 | Fixed | Release alokasi pada error/FULL/jumper membuat baris `RELEASED` permanen -> pembeli hidup diakhiri | Release hanya skip terminal yang dilaporkan script; jumper diadopsi | `promoter_batch_test.go` (19b/19d/19e/19k) | `EV/T22-mutant-release.txt` |
| Q-03 | Fixed | `promote.lua` tanpa requireHead; balasan malformed dianggap "tidak ada" | `RequireHead`, skip report, `mapPromote` mengembalikan error | `promote_integration_test.go` | `EV/T05-T06-green.txt`, `EV/T05-mutation-nofence.txt` |
| Q-04/05 | Fixed | UNCLAIMED sweeper = outcome Booking; MISSED tidak melepas slot | `SWEEP_MISSED` (sweeper), Booking `UNCLAIMED` pada ADMITTED/CLAIMED -> MISSED | `TestEndEntry_*` | `EV/T02-*` |
| Q-06 | Fixed (security) | `Leave` melepas alokasi sebelum validasi pemilik | `leave.lua` memutuskan; release setelah commit | `TestLeave_StrangerCausesNoBookingCall` | `EV/T10-T11-red.txt` |
| Q-07/08 | Fixed | `claim.lua` mengembalikan TURN_MISSED untuk semua status; usecase membaca `GetEntry` kedua | Galat per status, `allocationId` dari script, `NOT_ADMITTED` 409 | `TestClaim_*`, `TestClaimHandler_ErrorMapping` | `EV/T03-T04-red.txt` |
| Q-09 | Fixed | Lease nilai `"1"`, release `DEL` buta | Token acak + `release_lease.lua` | `lease_integration_test.go` | `EV/T06-red.txt` |
| Q-10 | Fixed | Satu kegagalan menghentikan semua event/entry | `errors.Join` di promoter/sweeper/status publisher | `worker_test.go` | `EV/T14-T15-red.txt` |
| Q-11 | Fixed | `ExpiredActive` tanpa batas | `LIMIT`, `QUEUE_SWEEP_BATCH_MAX` | `TestExpiredActive_*`, `TestSweep_OrphansBeyondLimitClear` | `EV/T02-*` |
| Q-12 | Fixed | Id visitor tak terverifikasi dipakai di join | Hanya id dari token terverifikasi | `join_visitor_test.go` | `EV/T12-handler-red.txt` |
| Q-13 | Fixed | Rate limiter `panic` window<1ms; INCR/PEXPIRE tak atomik | `rate_limit.lua`, validasi window (config + store) | `rate_limiter_*_test.go`, `TestLoad_QueueHardeningValidation` | `EV/T07-red.txt` |
| Q-14 | Fixed | Pre-turn notify dilewati pada jalur return awal | `defer notifyPreTurn` setelah settings diketahui | `TestPromoter_NotifyRunsOnEveryReturnPath...` | unit |
| QSH-OQ-14 | Resolved (ADR-028) | Baris RESERVED tertahan -> `ALREADY_ACTIVE` permanen | Lease+fence+cek-sebelum-reserve, adopsi jumper, head mati diakhiri ≤1 tick | `TestAdmission{Race,Det,Stale,Diff}`, unit 19b..19l | `EV/T27-mutations.txt` |
| C5/C6 | Fixed | Head orphan / email tak terbuka memblokir | `PROMOTER_ORPHAN`, `UNREADABLE_PII` entry-scoped; sistemik hanya alarm | `promoter_prereserve_test.go` | unit |
| C7 | Fixed | Replay `GRANTED` berdeadline lampau dipromosikan | Dilewati tanpa mutasi, dihitung | `TestPromoter_Lapsed*` | unit |
| C8 | Detection only | `NO_SUPPLY_NOW` non-stok tak bisa dibedakan dari stok | Rekaman `{headID,noSupplyTicks,adoptedTicks}` + counter; tidak pernah mengakhiri | `head_record_test.go`, `promoter_poison_test.go` | unit |
| Bonus | Fixed | `MarkNotifiedSoon` galat saat diulang (`valkey nil message`) | nil = idempoten | `TestQueueStore_SaleStatusDocLeaseAndNotifications` | `EV/T03-T04-green.txt` |
| Bonus | Fixed (test infra) | `newTestStore` memakai prefix 3 digit ms -> tabrakan lintas run | Prefix berbasis nanodetik | semua test Valkey | - |

## 2. Perilaku terlihat pengguna yang berubah (butuh konfirmasi pemilik untuk yang provisional)

1. Claim entry WAITING: `409 NOT_ADMITTED` (dulu `410 TURN_MISSED`). Kontrak OpenAPI diperbarui.
2. Claim entry LEFT/ENDED/COMPLETED: `404` (QSH-OQ-4, **provisional**).
3. Leave entry CLAIMED/terminal: `204` tanpa efek (QSH-OQ-3, **provisional**).
4. Join langsung hanya bila lease admisi didapat (retry 15-35 ms), selain itu `WAITING` lalu promoter mengadmit (SRS-F-04-07 diamandemen).
5. Inversi FIFO terbatas lewat adopsi (SRS-F-25-02 / specs 6.3 diamandemen; QSH-OQ-17).
6. `X-Visitor-ID`/cookie tak terverifikasi tak lagi menjadi identitas join.

## 3. Pertanyaan/penerimaan yang dicatat (tanpa dampak kode)

- QSH-OQ-15: API re-reserve di Booking untuk entry hidup yang barisnya mati (opsional).
- QSH-OQ-16: kegagalan kunci PII sistemik tidak mengakhiri apa pun (alert `queue_promoter_unseal_failed_total{scope="systemic"}`).
- QSH-OQ-17: adopsi vs FIFO ketat (butuh keputusan pemilik; ketat = satu panggilan Booking per entry atau `NO_SUPPLY_NOW` lengket di Booking).
- QSH-OQ-19: (a) Booking sebaiknya membedakan `NO_SUPPLY_NOW` stok vs galat DB (`ERROR`/`INVALID_ENTRY`); (b) `reserveOne` memetakan **semua** CHECK violation ke `SOLD_OUT` sehingga promoter bisa memanggil `SetSale(CLOSED)` karena CHECK non-stok.
- Alert: `queue_promote_head_no_supply_repeat_total{adopted="true"}` berkelanjutan > N x PromotorInterval (halaman); `queue_promote_lapsed_replay_skipped_total` naik tiap tick > 3 x `WORKER_INTERVAL` Booking (sweeper Booking terlambat/mati, asumsi A-1); `queue_promote_full_after_grant_total` > 0 (cacat).
- Urutan deploy: **worker dulu, lalu API** (API lama tanpa lease masih bisa admisi langsung selama rollout, jendela C4).
- Nilai terukur `claim_window_seconds`/`WORKER_INTERVAL` Booking di lingkungan deploy: tidak diukur di sesi ini (default kode: 180 s / 5 s).

## 4. Review XFF (QSH-OQ-9)

`pkg/httpx.ClientIP(r, trustProxy)` mengambil elemen **terakhir** `X-Forwarded-For` (yang ditambahkan ALB) bila `trustProxy`, selain itu alamat soket. `trustProxy := cfg.App.Env != "local"`. Tidak ditemukan cacat di `services/queue`; semantik dipatok di `TestJoinHandler_ClientIPTrustSemantics`. Tidak diubah.

## 5. Mutasi (bukti bahwa test peka)

- T20: 6 mutan hunk-tunggal (M-a..M-e + M-d2) semuanya dibunuh sub-test `TestLegacy/*` yang dituju (`EV/T20-mutations.txt`); satu mutan (M-b) awalnya lolos -> sub-test diperkuat (orphan > limit) lalu terbunuh.
- T27: M27-a (hapus fence) dibunuh 4 sub-test `TestAdmissionStale`; M27-b (fake tanpa fence) dibunuh `TestAdmissionDiff`; M27-c (hapus re-check di bawah lease) dibunuh `TestAdmissionDet` (`EV/T27-mutations.txt`).
- Tambahan: mutan fence di `promote.lua` dibunuh `TestPromote_FenceRejectsWrongAndExpiredToken` (`EV/T05-mutation-nofence.txt`); mutan release-jumper dibunuh 3 test (`EV/T22-mutant-release.txt`).
