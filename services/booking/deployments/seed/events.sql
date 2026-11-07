-- Seed lokal: 3 event terbit dengan kategori, supply, dan deskripsi Slate. Jalankan setelah migrate up.
-- Satu transaksi: trigger supply=sum(kategori) diperiksa saat COMMIT. Bukan untuk produksi.
BEGIN;
SET LOCAL search_path = booking;

WITH e AS (
  INSERT INTO events (slug, name, venue, city, genre, starts_at, sales_starts_at, sales_ends_at, status,
                      max_tickets_per_order, max_tickets_per_buyer_nik, allow_mixed_categories, published_at, description)
  VALUES
  ('rusuh-fest-2027', 'Rusuh Fest 2027', 'GBK Madya Stadium', 'Jakarta', '{ROCK,POP}',
   now() + interval '60 days', now() - interval '1 day', now() + interval '30 days', 'DRAFT', 3, 3, true, now(),
   '[{"type":"heading-one","children":[{"text":"Rusuh Fest 2027"}]},{"type":"paragraph","children":[{"text":"Festival musik terbesar tahun ini dengan "},{"text":"puluhan artis","bold":true},{"text":"."}]}]'::jsonb),
  ('senja-akustik', 'Senja Akustik', 'Taman Ismail Marzuki', 'Jakarta', '{ACOUSTIC,JAZZ}',
   now() + interval '45 days', now() - interval '1 day', now() + interval '25 days', 'DRAFT', 2, 3, true, now(),
   '[{"type":"paragraph","children":[{"text":"Malam akustik santai di tengah kota."}]}]'::jsonb),
  ('bandung-beat-night', 'Bandung Beat Night', 'Sasana Budaya Ganesha', 'Bandung', '{EDM}',
   now() + interval '90 days', now() + interval '10 days', now() + interval '80 days', 'DRAFT', 3, 3, false, now(),
   '[{"type":"paragraph","children":[{"text":"Penjualan dibuka 10 hari lagi."}]}]'::jsonb)
  RETURNING id, slug
), sup AS (
  INSERT INTO event_supply (event_id, total_capacity)
  SELECT id, CASE slug WHEN 'rusuh-fest-2027' THEN 1500 WHEN 'senja-akustik' THEN 300 ELSE 800 END FROM e
  RETURNING event_id
), cat AS (
  INSERT INTO ticket_categories (event_id, code, name, price_minor, display_order)
  SELECT e.id, v.code, v.name, v.price, v.ord
    FROM e JOIN (VALUES
      ('rusuh-fest-2027','VIP','VIP',250000000,0), ('rusuh-fest-2027','CAT1','Category 1',150000000,1), ('rusuh-fest-2027','FES','Festival',75000000,2),
      ('senja-akustik','VIP','VIP',120000000,0), ('senja-akustik','REG','Regular',60000000,1),
      ('bandung-beat-night','VIP','VIP',180000000,0), ('bandung-beat-night','GA','General Admission',90000000,1)
    ) AS v(slug, code, name, price, ord) ON v.slug = e.slug
  RETURNING id, event_id, code
)
INSERT INTO category_inventories (category_id, event_id, capacity)
SELECT cat.id, cat.event_id, v.cap
  FROM cat JOIN e ON e.id = cat.event_id
  JOIN (VALUES
      ('rusuh-fest-2027','VIP',100), ('rusuh-fest-2027','CAT1',400), ('rusuh-fest-2027','FES',1000),
      ('senja-akustik','VIP',50), ('senja-akustik','REG',250),
      ('bandung-beat-night','VIP',100), ('bandung-beat-night','GA',700)
  ) AS v(slug, code, cap) ON v.slug = e.slug AND v.code = cat.code;

COMMIT;
