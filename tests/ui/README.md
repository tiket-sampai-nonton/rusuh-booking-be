# Tes UI otomatis (Cucumber + Playwright + Appium)

Skenario UI untuk FE Rusuh Booking (Expo web, repo `rusuh-booking-mobile`). Skenario API (BDD) yang sudah ada tetap di
`tests/features/` (godog); folder ini **tidak** menduplikasinya. Hasil dan keterbatasan jujur ada di
[`../reports/TC-REPORT.md`](../reports/TC-REPORT.md).

```text
tests/ui/
├── playwright/   Cucumber-js + Playwright (Chromium). DIJALANKAN: desktop dan mobile (viewport Pixel 7)
└── appium/       WebdriverIO + Appium (UiAutomator2) untuk Android. BELUM DIJALANKAN di perangkat
```

## Playwright (desktop dan mobile)

Prasyarat: Node 20+ (diuji di Node 24).

```bash
cd tests/ui/playwright
npm install && npx playwright install chromium
npx cucumber-js              # desktop 1280x900
npx cucumber-js -p mobile    # mobile: device descriptor Pixel 7
npx cucumber-js --name "TC-EVT-04"   # satu skenario
```

| Variabel | Bawaan | Arti |
|---|---|---|
| `BASE_URL` | `https://rusuhbooking.my.id` | alamat FE web |
| `API_URL` | `https://api.rusuhbooking.my.id` | API yang dipanggil FE; dipakai suite untuk memilih data uji |

Contoh ke lingkungan lain: `BASE_URL=http://localhost:8081 API_URL=http://localhost:8082 npx cucumber-js` (FE lokal: `npm run web` di
`rusuh-booking-mobile/apps/client`; di lokal gerbang tunggal tidak ada, jadi port per service berbeda dan skenario antrean
butuh `queue` di port yang sama dengan `API_URL`; belum dicoba).

Laporan: `tests/reports/ui/cucumber-desktop.{html,json}` dan `cucumber-mobile.{html,json}` (tangkapan layar tertanam).

**Keselamatan.** Skenario antrean membuat sesi nyata di lingkungan target. Tidak ada pembayaran dan tidak ada booking. Sesi
selalu dilepas lewat tombol keluar (`data-testid="checkout-back"`), dengan pengaman di hook `After` bila skenario gagal di tengah
jalan. Jangan menambah skenario yang menekan "Lanjut Bayar" dengan data valid di prod: itu membuat booking yang menahan stok.

Skenario `@mobile-only` hanya jalan di profil mobile. ID `TC-UI-*` baru; `TC-EVT-01` dan `TC-EVT-04` ada di `docs/analisa/06-TP-RBT-001.md`.

## Appium (Android): BELUM DIJALANKAN

Dikembangkan tanpa perangkat: yang divalidasi hanya `npm install`, pemuatan `wdio.conf.js`, dan `npm run dry-run` (semua
langkah punya definisi). Pertama kali dijalankan di perangkat, ekspektasikan penyesuaian selektor.

Prasyarat (belum terpasang di mesin pengembang ini):

```bash
brew install --cask android-platform-tools      # adb
brew install openjdk                             # JDK 17+
cd tests/ui/appium && npm install                # webdriverio, appium, driver uiautomator2 (Appium 2)
npx appium driver list --installed               # uiautomator2 harus tampil; bila tidak: npx appium driver install uiautomator2
```

Pilih satu perangkat (lalu `adb devices` harus menampilkannya):

| Perangkat | Cara | Catatan |
|---|---|---|
| **Android Studio AVD** (jalur resmi) | Device Manager, buat AVD (Pixel, image arm64-v8a di Mac Apple Silicon, ada Google Chrome), jalankan | `ANDROID_SERIAL=emulator-5554`. Unduh sekitar 6–10 GB |
| **HP Android fisik** | aktifkan Opsi Pengembang dan Debugging USB, sambungkan kabel | `ANDROID_SERIAL=<serial dari adb devices>` |
| **BlueStacks Air** | aktifkan ADB di pengaturan BlueStacks, lalu `adb connect 127.0.0.1:<port yang ditampilkan>` | **Belum diverifikasi.** Menu dan port ADB BlueStacks Air belum dicek di mesin ini; bila gagal, pakai AVD |

Menjalankan (Chrome mobile membuka FE web; Chromedriver diunduh otomatis oleh Appium):

```bash
cd tests/ui/appium
ANDROID_SERIAL=emulator-5554 npm test
# BASE_URL / API_URL bisa dioverride seperti di Playwright
```

Aplikasi native (kerangka, selector belum diverifikasi): bangun APK (`npx expo run:android` atau EAS build), lalu
`MODE=native APK_PATH=/jalur/app.apk ANDROID_SERIAL=... npm run test:native`. Skenario `@native` hanya memeriksa aplikasi terbuka dan nama event
pertama tampil.

iOS (XCUITest) **tidak** disiapkan: butuh Xcode dan simulator, yang tidak ada di mesin ini.
