// Target per environment. Lokal tidak punya gateway: tiap service punya port sendiri (docker-compose.yaml).
// dev/prod lewat satu domain; API admin (/internal) tidak diekspos, jadi butuh tunnel ke booking (ADMIN_URL).
export const TEST_ENV = __ENV.TEST_ENV || 'local';

const local = {
  queue: 'http://localhost:8081',
  booking: 'http://localhost:8082',
  payment: 'http://localhost:8083',
  notification: 'http://localhost:8084',
  user: 'http://localhost:8085',
  admin: 'http://localhost:8082',
  queueAdmin: 'http://localhost:8081',
  mailpit: 'http://localhost:8025',
};

function remote(base, admin) {
  // queueAdmin: /internal queue tidak ikut tunnel bawaan (hanya booking); set QUEUE_ADMIN_URL bila event uji dibuat baru.
  return { queue: base, booking: base, payment: base, notification: base, user: base, admin, queueAdmin: __ENV.QUEUE_ADMIN_URL || 'http://127.0.0.1:18081', mailpit: '' };
}

const TARGETS = {
  local,
  dev: remote(__ENV.BASE_URL || 'https://dev.rusuhbooking.my.id', __ENV.ADMIN_URL || 'http://127.0.0.1:18082'),
  prod: remote(__ENV.BASE_URL || 'https://api.rusuhbooking.my.id', __ENV.ADMIN_URL || 'http://127.0.0.1:18082'),
};

if (!TARGETS[TEST_ENV]) throw new Error(`TEST_ENV ${TEST_ENV} tidak dikenal (local|dev|prod)`);
if (TEST_ENV === 'prod' && __ENV.CONFIRM_PROD !== 'yes') {
  throw new Error('uji beban ke prod butuh persetujuan eksplisit: set CONFIRM_PROD=yes');
}

export const target = TARGETS[TEST_ENV];

// Fitur yang hanya aman/tersedia di lokal: login OTP lewat Mailpit dan webhook Midtrans bertanda tangan (butuh server key).
export const canReadMail = Boolean(target.mailpit);
export const canSignWebhook = TEST_ENV === 'local' && Boolean(__ENV.MIDTRANS_SERVER_KEY);
