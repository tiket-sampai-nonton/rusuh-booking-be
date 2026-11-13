-- Stock drift (D-C9.1): one row per stock counter that disagrees with a recount from the live rows. No rows means no
-- drift. The single source of this query: DriftStats embeds it (stock_drift.go), the rbt_booking_stock_drift metric reads
-- it, and the spike evidence runs it with psql.
--
-- Kinds:
--   allocated      event_supply.allocated_units        vs sum(outstanding_units) of RESERVED/ACTIVE/EDITABLE admissions
--   held           event_supply.held_units             vs HELD booking items
--   sold           event_supply.sold_units             vs PAID booking items
--   category_held  category_inventories.held_quantity  vs HELD items of each category
--   category_sold  category_inventories.sold_quantity  vs PAID items of each category
--   quota_held     buyer_quotas.held_quantity          vs HELD items of that buyer's bookings
--
-- delta = stored - recount for the event-level kinds. The category and quota kinds have many rows per event, so their
-- delta is the sum of the absolute differences: two rows cannot cancel out.
WITH adm AS (
    SELECT event_id, COALESCE(sum(outstanding_units), 0) AS units
      FROM booking.admissions
     WHERE status IN ('RESERVED', 'ACTIVE', 'EDITABLE')
     GROUP BY event_id
),
items AS (
    SELECT event_id,
           count(*) FILTER (WHERE item_status = 'HELD') AS held,
           count(*) FILTER (WHERE item_status = 'PAID') AS paid
      FROM booking.booking_items
     GROUP BY event_id
),
cat_items AS (
    SELECT category_id,
           count(*) FILTER (WHERE item_status = 'HELD') AS held,
           count(*) FILTER (WHERE item_status = 'PAID') AS paid
      FROM booking.booking_items
     GROUP BY category_id
),
buyer_items AS (
    SELECT bi.event_id, b.buyer_nik_hash, count(*) AS held
      FROM booking.booking_items bi
      JOIN booking.bookings b ON b.id = bi.booking_id
     WHERE bi.item_status = 'HELD'
     GROUP BY bi.event_id, b.buyer_nik_hash
),
event_drift AS (
    SELECT e.id AS event_id, 'allocated' AS kind,
           COALESCE(s.allocated_units, 0) - COALESCE(a.units, 0) AS delta
      FROM booking.events e
      LEFT JOIN booking.event_supply s ON s.event_id = e.id
      LEFT JOIN adm a ON a.event_id = e.id
    UNION ALL
    SELECT e.id, 'held', COALESCE(s.held_units, 0) - COALESCE(i.held, 0)
      FROM booking.events e
      LEFT JOIN booking.event_supply s ON s.event_id = e.id
      LEFT JOIN items i ON i.event_id = e.id
    UNION ALL
    SELECT e.id, 'sold', COALESCE(s.sold_units, 0) - COALESCE(i.paid, 0)
      FROM booking.events e
      LEFT JOIN booking.event_supply s ON s.event_id = e.id
      LEFT JOIN items i ON i.event_id = e.id
),
category_drift AS (
    SELECT ci.event_id, 'category_held' AS kind,
           sum(abs(ci.held_quantity - COALESCE(h.held, 0)))::bigint AS delta
      FROM booking.category_inventories ci
      LEFT JOIN cat_items h ON h.category_id = ci.category_id
     GROUP BY ci.event_id
    UNION ALL
    SELECT ci.event_id, 'category_sold',
           sum(abs(ci.sold_quantity - COALESCE(p.paid, 0)))::bigint
      FROM booking.category_inventories ci
      LEFT JOIN cat_items p ON p.category_id = ci.category_id
     GROUP BY ci.event_id
),
quota_drift AS (
    -- FULL JOIN: a buyer with HELD items but no quota row shows up too, not only a quota row without items.
    SELECT COALESCE(q.event_id, h.event_id) AS event_id, 'quota_held' AS kind,
           sum(abs(COALESCE(q.held_quantity, 0) - COALESCE(h.held, 0)))::bigint AS delta
      FROM booking.buyer_quotas q
      FULL JOIN buyer_items h ON h.event_id = q.event_id AND h.buyer_nik_hash = q.buyer_nik_hash
     GROUP BY 1
)
SELECT event_id::text, kind, delta
  FROM (
      SELECT event_id, kind, delta FROM event_drift
      UNION ALL SELECT event_id, kind, delta FROM category_drift
      UNION ALL SELECT event_id, kind, delta FROM quota_drift
  ) drift
 WHERE delta <> 0
 ORDER BY event_id, kind;
