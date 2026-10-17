-- Dijalankan sekali saat volume PostgreSQL lokal pertama kali dibuat.
-- Satu schema per service (SDD §4.4). Tabel dibuat lewat migration tiap service.
CREATE SCHEMA IF NOT EXISTS queue;
CREATE SCHEMA IF NOT EXISTS booking;
CREATE SCHEMA IF NOT EXISTS payment;
CREATE SCHEMA IF NOT EXISTS notification;
