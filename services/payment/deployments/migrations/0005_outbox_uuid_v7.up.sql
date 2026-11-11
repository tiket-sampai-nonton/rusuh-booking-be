-- 0005 (ADR-032): id baru outbox memakai UUIDv7 (urut waktu pembuatan, index B-tree padat). PostgreSQL 16 belum punya
-- uuidv7() bawaan (baru di PG 18), jadi dibangun dari gen_random_uuid(): 48 bit pertama diganti waktu Unix dalam ms,
-- lalu nibble versi 4 (0100) dijadikan 7 (0111) dengan menyalakan bit 52 dan 53. Varian tetap 10. ID v4 lama tetap
-- valid. Ganti dengan uuidv7() bawaan setelah naik ke PG 18.
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

ALTER TABLE outbox_events ALTER COLUMN id SET DEFAULT uuid_generate_v7();
