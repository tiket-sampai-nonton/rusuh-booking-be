-- Seed lokal: baris event_settings untuk setiap event yang sudah di-seed di schema booking.
-- active_slot_capacity = 5: hanya 5 pembeli sekaligus yang boleh berstatus aktif (punya booking tertahan);
-- sisanya menunggu di antrean. Hanya untuk DB lokal yang berbagi satu Postgres. Jalankan setelah seed booking.
INSERT INTO queue.event_settings (event_id, active_slot_capacity)
SELECT id, 5 FROM booking.events
ON CONFLICT (event_id) DO UPDATE SET active_slot_capacity = EXCLUDED.active_slot_capacity, updated_at = now();
