# Evidence — Verifikasi API Midtrans untuk Adapter Payment (26 Oktober 2026)

Syarat payment Task 9: detail API Midtrans diverifikasi ke dokumentasi resmi sebelum adapter ditulis. Sumber yang dibaca (docs.midtrans.com, 26 Oktober 2026):
HTTP notification, Core API bank transfer, Core API e-wallet (GoPay), QRIS, custom expiry, dan Get Transaction Status.
Adapter: `services/payment/internal/payment/infrastructure/midtrans/`. Test otomatis memakai server `httptest`; verifikasi ke Sandbox nyata dicatat di bagian "Terverifikasi di Sandbox nyata" di bawah.

## Terverifikasi

| Hal | Hasil dari dokumentasi | Dampak di kode |
|---|---|---|
| Autentikasi | `Authorization: Basic base64(ServerKey + ":")`, sandbox `https://api.sandbox.midtrans.com`, produksi `https://api.midtrans.com` | `gateway.go` `New`, default `MIDTRANS_BASE_URL` per environment |
| Endpoint | `POST /v2/charge`, `GET /v2/{order_id}/status`, `POST /v2/{id}/cancel` (path cancel terlihat di `actions` respons GoPay) | `Charge`, `Status`, `Cancel` |
| VA BCA/BNI/BRI | `payment_type=bank_transfer`, `bank_transfer.bank`; nomor VA di `va_numbers[].va_number` | `buildCharge`, `instructionFrom` |
| VA Permata | `payment_type=permata`; nomor VA di `permata_va_number` | idem |
| GoPay | `payment_type=gopay`; `actions[]` berisi `generate-qr-code` (URL gambar), `deeplink-redirect`, `get-status`, `cancel` | `Instruction{Type:DEEPLINK, DeeplinkURL, QRURL}` |
| QRIS | `payment_type=qris`, `qris.acquirer=gopay`; **tidak ada `qr_string`**, QR hanya sebagai URL gambar di `actions[generate-qr-code]` | field baru `qrUrl` (aditif) di `payment.yaml`; `qrString` ditandai deprecated |
| `gross_amount` | request: angka (`44000`); respons dan notifikasi: string dua desimal (`"44000.00"`) | dikirim sebagai integer rupiah, dibaca dengan `parseGrossAmount` (pecahan selain nol ditolak) |
| `custom_expiry` | `order_time` (`YYYY-MM-DD HH:MM:SS +0700`, WIB), `expiry_duration`, `unit` ∈ second/minute/hour/day; minimum 20 detik; tidak untuk kartu | `unit=second`, durasi = sisa waktu booking dibulatkan ke bawah; charge ditolak bila sisa < 30 detik |
| Signature webhook | `SHA512(order_id + status_code + gross_amount + ServerKey)` | `signature.go`, perbandingan constant-time |
| Status webhook | `pending`, `settlement`, `capture` (kartu), `deny`, `cancel`, `expire`, `failure`, `refund`, `partial_refund`, `authorize` | `vo.MapProviderStatus`; status tanpa kemajuan pembayaran diabaikan |
| Respons webhook | 2xx = tidak diulang; 500 diulang 1x, 503 4x, 400/404 2x, lainnya 5x; respons sebaiknya < 5 detik | 200 untuk callback yang dipahami (termasuk duplikat); 503 untuk gangguan sementara agar Midtrans mengulang |

## Temuan yang mengubah desain

1. **Expire dijawab `status_code` 407** (dan deny `202`) pada status transaksi, sambil membawa `transaction_status`. Adapter tidak menganggap 4xx di body sebagai penolakan bila `transaction_status` ada (hanya untuk endpoint status). Terdeteksi oleh test adapter.
2. **Midtrans dapat menjawab HTTP 200 dengan `status_code` body 404** untuk order yang tidak ada; status code body dianggap otoritatif.
3. **`order_id` = `{publicRef}-{attemptNo}`** (mis. `RB-7F3A9C21-1`), bukan `RB-{publicRef}-{n}` seperti desain lama, karena `publicRef` Booking sudah berawalan `RB-`. Batas Midtrans 50 karakter dan `[A-Za-z0-9_.~-]` divalidasi di `vo.NewOrderID`.
4. **Fingerprint callback memuat hasil verifikasi signature dan nominal.** Test perjalanan lengkap menunjukkan webhook palsu dengan field yang sama bisa menempati baris audit callback asli (dedup `UNIQUE (provider, fingerprint)`). Diperbaiki di `handle_webhook.go`.

## Terverifikasi di Sandbox nyata (26 Oktober 2026, siang)

Dijalankan dengan API Payment asli (`.env` lokal berisi server key Sandbox), Midtrans Sandbox sungguhan, PostgreSQL dan RabbitMQ lokal. Booking diganti program kecil yang memverifikasi service token Payment (bukan bagian repo); webhook diterima lewat tunnel `cloudflared` (HTTP/2) dengan `MIDTRANS_NOTIFICATION_URL`.

| Hal | Bukti |
|---|---|
| Charge semua metode aktif | VA BCA (nomor VA 23 digit), BNI, BRI, QRIS (URL gambar QR), dan GoPay (deeplink simulator + URL QR) semuanya `201` dan tersimpan `PENDING` dengan instruksi |
| `gross_amount` sebagai angka | Diterima Midtrans pada semua charge. Simulator GoPay menampilkan "IDR 150000.00" untuk `gross_amount` 150000, jadi **1 minor unit = 1 rupiah** juga di sisi Midtrans |
| `X-Override-Notification` | Webhook tiba di URL tunnel tanpa mengubah dashboard |
| Webhook asli | Dalam ~2 detik setelah bayar: `pending` (signature valid, tercatat `DUPLICATE` karena attempt sudah `PENDING`) lalu `settlement` (`PROCESSED`); attempt `SUCCEEDED`, 1 baris outbox |
| **Zona `settlement_time`** | Midtrans mengirim `settlement_time` `2026-10-26 09:13:04` dan `transaction_time` `09:12:46` untuk pembayaran yang terjadi pukul 02:13:04 UTC → **WIB (UTC+7)**. Asumsi di `vo.ParseSettlementTime` benar; event ke RabbitMQ membawa `2026-10-26T02:13:04Z` |
| Cancel | `CHECK_AND_CANCEL` pada VA BNI → Midtrans `transaction_status=cancel`, attempt `CANCELLED`, hasil `NOT_PAID` |
| Status | `CHECK` pada VA BCA yang belum dibayar → `NOT_PAID`, attempt tetap `PENDING`. `CHECK_AND_CANCEL` pada booking yang sudah dibayar → `SUCCEEDED` dengan `settlementTime` |
| Idempotensi | Notifikasi settlement yang sah diputar ulang 3× → semua `200`, outbox tetap 1 baris. Signature diubah → `401` dan tercatat `REJECTED` di baris terpisah |
| Event | Relay worker menerbitkan `payment.succeeded` (`payload`: `bookingId`, `attemptId`, `settlementTime`, `amountMinor` 150000, `method` GOPAY) ke RabbitMQ dengan `messageId` = id baris outbox |
| Service token | 15 panggilan Payment→Booking membawa token EdDSA berumur 300 detik yang diterima verifier Booking |
| Rahasia | Server key tidak ditemukan di log API/worker/tunnel, di respons, maupun di tabel PostgreSQL |

Catatan pelaksanaan:
- ngrok tidak terpasang dan butuh akun; dipakai `cloudflared` quick tunnel. QUIC (UDP) gagal terhubung di jaringan ini, `--protocol http2` berhasil.
- Pembayaran dilakukan lewat halaman publik simulator Sandbox (`POST .../v2/deeplink/payment` dengan field `orderDetail` dari form), bukan dari aplikasi GoPay.
- API dan worker perlu `APP_PORT` berbeda bila dijalankan di mesin yang sama (di compose masing-masing container).

## Tambahan terverifikasi (sore hari, 26 Oktober 2026)

| Hal | Bukti nyata |
|---|---|
| Cancel pada order yang sudah settle | HTTP 200 dengan `status_code` **412** "Transaction status cannot be updated." Sama untuk order yang sudah dibatalkan dan untuk `expire` pada order yang sudah settle. Adapter memetakannya ke `ErrGatewayRejected`; `resolve` lalu mengecek status ulang |
| Status order yang tidak ada | HTTP 200 dengan `status_code` **404** "Transaction doesn't exist." → `ErrOrderNotFound` |
| Kedaluwarsa sungguhan | VA BCA dengan sisa waktu ~75 detik: Midtrans mengubah status menjadi `expire` dengan `status_code` **407** dan pesan "Success, transaction is found" tepat pada batas waktu. Webhook `expire` tiba ±1 menit kemudian dan attempt menjadi `EXPIRED`. Setelah kedaluwarsa, cancel dijawab 412 |
| **Batas minimum `custom_expiry`** | Dokumentasi menyebut 20 detik, tetapi **20 detik ditolak** (400, "custom_expiry is below minimum threshold") untuk VA BCA. 25 detik diterima untuk BNI, BRI, QRIS, dan GoPay; **30 detik diterima di semua metode**. `minChargeExpiry = 30s` di `create_attempt.go` sudah tepat |

### Mengulang verifikasi ke Sandbox

Test opt-in (tidak ikut CI) memanggil adapter asli ke Sandbox: charge, status, cancel, cancel ulang (412), order tidak ada (404), dan kedaluwarsa nyata. Memerlukan `MIDTRANS_SERVER_KEY` (dibaca dari `services/payment/.env` atau environment, tidak lewat command line):

```bash
cd services/payment
GOWORK=off go test -tags sandbox -run TestSandbox -timeout 5m ./internal/payment/infrastructure/midtrans/
```

Test membuat order Sandbox sungguhan (tanpa uang) dan butuh sekitar 35 detik karena menunggu kedaluwarsa.

## Belum terverifikasi

- Integrasi dengan Booking nyata (menunggu payment Task 15 oleh nuzirwan).

## Di luar scope

- `VA_PERMATA`: tidak diperlukan (keputusan pemilik, 26 Oktober 2026). Metode ini tidak ada di `PAYMENT_METHODS_ENABLED` dan tidak diverifikasi ke Sandbox.
