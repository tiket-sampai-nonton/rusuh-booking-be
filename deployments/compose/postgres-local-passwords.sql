-- Hanya untuk Docker Compose lokal. Jangan dipakai di lingkungan cloud.
ALTER ROLE svc_queue PASSWORD 'queue-local-only';
ALTER ROLE svc_booking PASSWORD 'booking-local-only';
ALTER ROLE svc_payment PASSWORD 'payment-local-only';
ALTER ROLE svc_notification PASSWORD 'notification-local-only';
ALTER ROLE svc_user PASSWORD 'user-local-only';
