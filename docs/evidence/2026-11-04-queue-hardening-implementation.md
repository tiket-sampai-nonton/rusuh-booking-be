# 2026-11-04 — Queue hardening: implementasi (QSH-T00..T27) dan perubahan yang memengaruhi desain/dokumen

Konteks: branch `improve/queue-service-hardening`, `services/queue`. Desain: `.agents/tasks/improve/queue-service-hardening/design.md` rev 8 (ADR-029), rencana rev 9. Sesi ini menulis kode produksi dan test; dokumen yang berubah: `docs/analisa/{02-SRS,05-ADR,06-TP}`, `docs/contracts/openapi/queue.yaml`, `specs/queue-service/requirements.md`.

## Bug nyata yang ditemukan dan diperbaiki (akar masalah, bukan gejala)

| # | Bug (terverifikasi dengan test merah dulu) | Akar masalah | Perbaikan | Bukti |
|---|---|---|---|---|
| 1 | **Pembebasan alokasi orang lain**: siapa pun yang tahu `entryId` ADMITTED bisa membuat Queue memanggil `ReleaseAllocation` milik pembeli lain (INV-2 "Jangan PHP"). | `LeaveQueue.Leave` membaca `GetEntry` lalu melepas di Booking **sebelum** `leave.lua` menolak non-pemilik. | Urutan dibalik: `leave.lua` memutuskan kepemilikan+status atomik; release hanya bila `OK` + `ADMITTED` + `allocationId`. | `TestLeave_StrangerCausesNoBookingCall` (merah di `EV/T10-T11-red.txt`) |
| 2 | Pembeli yang masih hidup diakhiri Queue sendiri (`ALREADY_ACTIVE`). | Promoter/join mereservasi di Booking sebelum memeriksa slot/head; jalur jumper melepas baris sehingga `RELEASED` permanen (UNIQUE `queue_entry_id`). | ADR-029: lease per-event dipakai join+promoter, cek kapasitas/head di bawah lease sebelum `ReserveBatch`, fence token di `promote.lua`, adopsi jumper, release hanya skip terminal. | `TestPromoter_StockReturnedMidBatchAdoptsTheLaterEntryAndNeverEndsIt`; mutan "release lama" dibunuh 3 test (`EV/T22-mutant-release.txt`) |
| 3 | Lease promoter dihapus pemegang basi. | `ReleasePromoterLease` = `DEL` buta, nilai lease `"1"`. | Token acak + `release_lease.lua` compare-and-delete. | `TestLease_StaleTokenReleaseDoesNotDeleteTheNewHoldersLease` (merah di `EV/T06-red.txt`) |
| 4 | `leave.lua` menimpa status terminal menjadi `LEFT`; `claim.lua` menjawab `TURN_MISSED` untuk semua status selain ADMITTED. | Skrip tanpa guard status. | Guard status + hasil bertipe; `NOT_ADMITTED`/`GONE`. | `TestLeave_TerminalAndClaimedAreIgnored...`, `TestClaim_StatusSpecificErrors` (`EV/T03-T04-red.txt`) |
| 5 | Pembersihan MISSED menghapus kunci constraint entry yang lebih baru; `MarkMissed*` tanpa cek pemilik; orphan di `active`/`waiting` tidak pernah dibersihkan; sweep tanpa batas. | `MarkMissedAndClean` dengan `DEL` tanpa cek pemilik; `end_entry.lua` tidak `ZREM waiting`. | `end_entry.lua` kelas A/B/C, cek pemilik, `ZREM` kedua set, `LIMIT`. | `EV/T02-red.txt` → `EV/T02-green.txt` |
| 6 | Rate limiter: `panic: integer divide by zero` untuk window < 1 ms; `INCR` dan `PEXPIRE` tidak atomik. | `DoMulti` bukan transaksi; pembagian tanpa validasi. | `rate_limit.lua` atomik + validasi window; config menolak window < 1 ms. | `EV/T07-red.txt` |
| 7 | `MarkNotifiedSoon` mengembalikan galat `valkey nil message` pada pemanggilan kedua. | `SET NX` menjawab nil bila sudah ada, diperlakukan sebagai galat. | nil = idempoten. | `TestQueueStore_SaleStatusDocLeaseAndNotifications` |
| 8 | Header/cookie visitor palsu dipakai sebagai identitas join (INV-3). | Handler memakai nilai mentah bila `Resolve` gagal. | Hanya id dari token terverifikasi. | `TestJoinHandler_Forged*` (merah di `EV/T12-handler-red.txt`) |
| 9 | Satu event/entry gagal menghentikan seluruh sweeper/status publisher/promoter. | `return err` pada kegagalan pertama. | `errors.Join`, lanjut ke item berikutnya. | `EV/T14-T15-red.txt` |
| 10 | Head yatim (id di `waiting` tanpa hash) atau email tak terbuka memblokir antrean selamanya. | `continue` tanpa end. | C5 `PROMOTER_ORPHAN`; C6 entry-scoped `UNREADABLE_PII` setelah `QUEUE_UNSEAL_FAIL_MAX`; sistemik tidak mengakhiri apa pun. | `TestPromoter_Orphan...`, `...UnreadablePII...` |

## Keputusan yang mengubah perilaku terlihat pengguna (sudah ditetapkan di desain rev 8 §2a; ditandai di SRS/ADR)

- Claim untuk entry WAITING: `409 NOT_ADMITTED` (sebelumnya `410 TURN_MISSED`). Claim untuk entry LEFT/ENDED/COMPLETED: `404` (QSH-OQ-4, provisional).
- Leave pada entry CLAIMED atau terminal: `204` tanpa mengubah apa pun (QSH-OQ-3, provisional).
- Join langsung hanya saat lease admisi didapat (satu retry 15-35 ms); selain itu `WAITING` dan promoter mengadmit dalam `PromotorInterval` (amandemen SRS-F-04-07).
- Inversi FIFO terbatas (adopsi) bila stok kembali di tengah satu batch Booking (amandemen SRS-F-25-02 dan specs 6.3).
- Join dengan `X-Visitor-ID`/cookie yang tidak terverifikasi tidak lagi memakai id itu.

## Yang tidak terverifikasi
Lihat `verification.md` (Postgres integration queue tidak dijalankan: tidak ada Postgres proyek ini yang dapat dijangkau; modul `pkg/*` gagal pada `make cover` karena Postgres/RabbitMQ lingkungan, tidak disentuh diff ini).
