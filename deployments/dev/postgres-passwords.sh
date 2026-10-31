#!/bin/sh
# Dijalankan sekali oleh image postgres saat volume baru dibuat (setelah 00-init.sql membuat role dan schema).
# Password tiap role service datang dari env/postgres.env; nilainya dibuat gen-secrets.sh (hex, aman di SQL).
set -eu
for role in QUEUE:svc_queue BOOKING:svc_booking PAYMENT:svc_payment NOTIFICATION:svc_notification USER:svc_user; do
  var="${role%%:*}_DB_PASSWORD"
  name="${role##*:}"
  eval "pw=\${$var:?$var belum diisi di postgres.env}"
  psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "ALTER ROLE $name PASSWORD '$pw'"
done
