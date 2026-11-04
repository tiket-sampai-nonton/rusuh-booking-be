-- release_lease.lua — compare-and-delete lease promoter (design §5.5, QSH-T06)
-- KEYS[1]=<tag>:promoter:lock  ARGV: 1=token
-- Hanya pemegang token yang menghapus: lease milik pengambil baru tidak boleh terhapus oleh pelepas yang kedaluwarsa.
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
