-- note_unreadable.lua — hitung kegagalan unseal email untuk entry WAITING (design §3.1, C6, QSH-T23)
-- KEYS[1]=entry:<id>   Mengembalikan hitungan baru, atau -1 bila entry bukan WAITING / tidak ada.
-- Tidak pernah membuat hash baru (HINCRBY hanya setelah HGET status == WAITING).
local st = redis.call('HGET', KEYS[1], 'status')
if st ~= 'WAITING' then return -1 end
return redis.call('HINCRBY', KEYS[1], 'unreadableN', 1)
