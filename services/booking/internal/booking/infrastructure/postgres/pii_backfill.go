package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// BackfillBuyerEmail seals the plaintext buyer emails that predate PDP-1 and clears the plaintext, in batches. It is
// idempotent (rows already sealed are skipped; a row with both keeps the existing ciphertext) and safe to run while the
// service is up. It returns how many admissions and bookings it converted.
func BackfillBuyerEmail(ctx context.Context, pool *pgxpool.Pool, seal func(string) (string, error)) (admissions, bookings int, err error) {
	for _, table := range []string{"admissions", "bookings"} {
		n, err := backfillTable(ctx, pool, table, seal)
		if err != nil {
			return admissions, bookings, err
		}
		if table == "admissions" {
			admissions = n
		} else {
			bookings = n
		}
	}
	return admissions, bookings, nil
}

func backfillTable(ctx context.Context, pool *pgxpool.Pool, table string, seal func(string) (string, error)) (int, error) {
	const batch = 500
	total := 0
	for {
		rows, err := pool.Query(ctx, fmt.Sprintf(
			`SELECT id::text, buyer_email FROM booking.%s WHERE buyer_email IS NOT NULL AND buyer_email <> '' LIMIT %d`, table, batch))
		if err != nil {
			return total, fmt.Errorf("backfill %s: select: %w", table, err)
		}
		type row struct{ id, email string }
		var todo []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.email); err != nil {
				rows.Close()
				return total, err
			}
			todo = append(todo, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return total, err
		}
		if len(todo) == 0 {
			return total, nil
		}
		for _, r := range todo {
			enc, err := seal(r.email)
			if err != nil {
				return total, fmt.Errorf("backfill %s %s: seal: %w", table, r.id, err)
			}
			if _, err := pool.Exec(ctx, fmt.Sprintf(
				`UPDATE booking.%s SET buyer_email_enc = COALESCE(NULLIF(buyer_email_enc, ''), $2), buyer_email = NULL WHERE id = $1::uuid`, table), r.id, enc); err != nil {
				return total, fmt.Errorf("backfill %s %s: update: %w", table, r.id, err)
			}
			total++
		}
	}
}
