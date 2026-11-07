-- rate_limit.lua — penghitung fixed-window atomik (design §5.7, QSH-T07)
-- KEYS[1]=counter  ARGV: 1=windowMs
-- INCR dan PEXPIRE dalam satu script: tidak ada celah yang meninggalkan kunci tanpa TTL; kunci yang
-- ditemukan tanpa TTL (PTTL == -1, sisa implementasi lama) diperbaiki.
local n = redis.call('INCR', KEYS[1])
if n == 1 or redis.call('PTTL', KEYS[1]) < 0 then
  redis.call('PEXPIRE', KEYS[1], tonumber(ARGV[1]))
end
return n
