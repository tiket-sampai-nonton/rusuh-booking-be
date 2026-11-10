// Jadwal beban per jenis tes. Satu level = ramp lalu hold; angka dihitung hanya selama hold (tag level).
//   browse : light user  - katalog + detail + status antrean, iterasi per detik (arrival rate)
//   waiters: normal user - pengantre yang polling seperti FE sekarang, jumlah pengguna serentak (VU)
//   buy    : heavy user  - join, admitted, claim, sesi, hold 1 tiket, lalu batal (tanpa pembayaran), iterasi per detik
//   account: user login  - OTP, profil, riwayat booking, refresh, logout (hanya lokal: kode OTP dibaca dari Mailpit)
// Angka ini titik awal untuk laptop yang menjalankan stack dan k6 sekaligus, bukan kapasitas sistem.
const s = (v) => `${v}s`;
const F = Number(__ENV.LOAD_FACTOR || 1); // skala cepat tanpa mengubah file

const lvl = (name, ramp, hold, browse, waiters, buy, account) => ({
  name, ramp, hold, browse: browse * F, waiters: Math.round(waiters * F), buy: buy * F, account,
});

const SOAK = Number(__ENV.SOAK_MINUTES || 30) * 60;

export const PROFILES = {
  load: [
    lvl('L1', 20, 60, 5, 50, 0.5, 0.1),
    lvl('L2', 20, 60, 10, 100, 1, 0.1),
    lvl('L3', 20, 60, 20, 200, 2, 0.2),
    lvl('L4', 20, 60, 30, 300, 3, 0.2),
  ],
  stress: [
    lvl('S1', 20, 45, 20, 200, 2, 0.2),
    lvl('S2', 20, 45, 40, 400, 4, 0.2),
    lvl('S3', 20, 45, 60, 600, 6, 0.2),
    lvl('S4', 20, 45, 90, 900, 9, 0.2),
    lvl('S5', 20, 45, 130, 1300, 13, 0.2),
    lvl('S6', 20, 45, 180, 1800, 18, 0.2),
  ],
  spike: [
    lvl('base', 15, 45, 10, 100, 1, 0.1),
    lvl('spike', 5, 60, 100, 1000, 10, 0.1),
    lvl('recovery', 5, 60, 10, 100, 1, 0.1),
  ],
  soak: [lvl('soak', 30, SOAK, 15, 150, 1.5, 0.1)],
};

export function schedule(type) {
  const levels = PROFILES[type];
  if (!levels) throw new Error(`profil ${type} tidak ada`);
  let t = 0;
  return levels.map((l) => {
    const w = { ...l, rampStart: t, holdStart: t + l.ramp, holdEnd: t + l.ramp + l.hold };
    t = w.holdEnd;
    return w;
  });
}

// Tahapan k6 untuk satu kolom beban (browse/waiters/buy/account).
export function stages(type, key) {
  return PROFILES[type].flatMap((l) => [
    { duration: s(l.ramp), target: l[key] },
    { duration: s(l.hold), target: l[key] },
  ]);
}

export function totalSeconds(type) {
  return PROFILES[type].reduce((a, l) => a + l.ramp + l.hold, 0);
}
