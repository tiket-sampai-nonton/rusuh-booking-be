-- end_entry.lua — guard idempoten untuk consumer booking.journey.ended (design §4.5)
-- KEYS[1]=entry:<id> KEYS[2]=active KEYS[3..5]=nik/visitor/email keys
-- ARGV: 1=entryId 2=outcome 3=nowMs
-- outcome=UNCLAIMED dari sweeper (deadline terlewat); outcome=PAID|REFUNDED dari booking consumer
local st = redis.call('HGET', KEYS[1], 'status')
if not st then return {'IGNORED','NO_ENTRY'} end
-- terminal sudah (kecuali MISSED — masih perlu bersihkan constraint keys) → idempoten no-op
if st=='COMPLETED' or st=='ENDED' or st=='LEFT' then return {'IGNORED', st} end
-- MISSED: hanya bersihkan constraint keys (status sudah di-set oleh sweeper) agar bisa antre lagi
if st=='MISSED' then
  if redis.call('GET', KEYS[3])==ARGV[1] then redis.call('DEL', KEYS[3]) end
  if redis.call('GET', KEYS[4])==ARGV[1] then redis.call('DEL', KEYS[4]) end
  if redis.call('GET', KEYS[5])==ARGV[1] then redis.call('DEL', KEYS[5]) end
  return {'OK_CLEANUP', 'MISSED'}
end
redis.call('ZREM', KEYS[2], ARGV[1])                     -- rilis slot
-- outcome=UNCLAIMED (sweeper): set MISSED agar customer tahu giliran terlewat
-- outcome=PAID: set COMPLETED; lainnya: set ENDED
local final
if ARGV[2]=='UNCLAIMED' then
  final = 'MISSED'
elseif ARGV[2]=='PAID' then
  final = 'COMPLETED'
else
  final = 'ENDED'
end
redis.call('HSET', KEYS[1], 'status', final)
-- bersihkan constraint agar NIK/visitor/email bisa dipakai lagi
if redis.call('GET', KEYS[3])==ARGV[1] then redis.call('DEL', KEYS[3]) end
if redis.call('GET', KEYS[4])==ARGV[1] then redis.call('DEL', KEYS[4]) end
if redis.call('GET', KEYS[5])==ARGV[1] then redis.call('DEL', KEYS[5]) end
return {'OK', final}
