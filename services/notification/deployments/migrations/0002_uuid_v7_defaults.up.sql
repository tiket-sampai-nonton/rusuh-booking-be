-- 0002 (ADR-032): id baru memakai UUIDv7 (urut waktu pembuatan, index B-tree padat). PostgreSQL 16 belum punya
-- uuidv7() bawaan (baru di PG 18), jadi dibangun dari gen_random_uuid(): 48 bit pertama diganti waktu Unix dalam ms,
-- lalu nibble versi 4 dijadikan 7. ID v4 yang sudah ada tidak diubah. Ganti dengan uuidv7() bawaan setelah PG 18.
CREATE FUNCTION uuid_generate_v7() RETURNS uuid
LANGUAGE sql VOLATILE AS $$
    SELECT encode(
        set_bit(set_bit(
            overlay(uuid_send(gen_random_uuid())
                    PLACING substring(int8send(floor(extract(epoch FROM clock_timestamp()) * 1000)::bigint) FROM 3)
                    FROM 1 FOR 6),
            52, 1), 53, 1),
        'hex')::uuid
$$;

ALTER TABLE deliveries ALTER COLUMN id SET DEFAULT uuid_generate_v7();
