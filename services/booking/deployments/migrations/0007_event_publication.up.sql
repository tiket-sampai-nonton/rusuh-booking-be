-- 0007: status publikasi event (SCRUM-55). Draf = published_at kosong (tidak tampil di katalog dan tidak bisa dijual);
-- terbit = published_at terisi (sekaligus mencatat kapan). Status ketersediaan (NOT_STARTED/AVAILABLE/CLOSED) tetap
-- dihitung dari jendela penjualan; kolom status lama tidak diubah.
ALTER TABLE booking.events
    ADD COLUMN IF NOT EXISTS published_at timestamptz;

-- Event yang sudah ada sudah tampil di katalog; tandai terbit agar katalog tidak tiba-tiba kosong.
UPDATE booking.events SET published_at = created_at WHERE published_at IS NULL;
