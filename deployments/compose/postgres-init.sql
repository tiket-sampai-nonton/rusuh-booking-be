-- Dijalankan sekali oleh admin DB saat volume PostgreSQL lokal pertama kali dibuat (SDD §4.4, 000_roles_and_schemas).
-- Di AWS, langkah yang sama dijalankan lewat Terraform/ops dengan password dari Secrets Manager.
-- Tabel dibuat oleh migrasi tiap service (`<service> migrate up`) memakai role milik service itu.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE SCHEMA IF NOT EXISTS queue;
CREATE SCHEMA IF NOT EXISTS booking;
CREATE SCHEMA IF NOT EXISTS payment;
CREATE SCHEMA IF NOT EXISTS notification;
CREATE SCHEMA IF NOT EXISTS users;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'svc_queue') THEN CREATE ROLE svc_queue LOGIN; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'svc_booking') THEN CREATE ROLE svc_booking LOGIN; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'svc_payment') THEN CREATE ROLE svc_payment LOGIN; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'svc_notification') THEN CREATE ROLE svc_notification LOGIN; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'svc_user') THEN CREATE ROLE svc_user LOGIN; END IF;
END $$;

REVOKE ALL ON SCHEMA queue, booking, payment, notification, users FROM PUBLIC;
GRANT USAGE, CREATE ON SCHEMA queue TO svc_queue;
GRANT USAGE, CREATE ON SCHEMA booking TO svc_booking;
GRANT USAGE, CREATE ON SCHEMA payment TO svc_payment;
GRANT USAGE, CREATE ON SCHEMA notification TO svc_notification;
GRANT USAGE, CREATE ON SCHEMA users TO svc_user;
-- Setiap role hanya melihat schema miliknya.
ALTER ROLE svc_queue SET search_path = queue;
ALTER ROLE svc_booking SET search_path = booking;
ALTER ROLE svc_payment SET search_path = payment;
ALTER ROLE svc_notification SET search_path = notification;
ALTER ROLE svc_user SET search_path = users;
