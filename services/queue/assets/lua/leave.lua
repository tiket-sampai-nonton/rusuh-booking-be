-- leave.lua — atomic leave dengan cleanup (design §4.4)
-- KEYS[1]=entry:<id> KEYS[2]=waiting KEYS[3]=active
-- KEYS[4]=nik:<h> KEYS[5]=visitor:<v> KEYS[6]=email:<eh>
-- ARGV: 1=entryId 2=visitorId 3=nowMs
local st = redis.call('HGET', KEYS[1], 'status')
if not st then return {'ERR','NOT_FOUND'} end
if redis.call('HGET', KEYS[1], 'visitorId') ~= ARGV[2] then return {'ERR','NOT_FOUND'} end
redis.call('ZREM', KEYS[2], ARGV[1])
local wasActive = redis.call('ZREM', KEYS[3], ARGV[1])   -- rilis slot bila ADMITTED
-- hapus constraint keys HANYA bila masih menunjuk entry ini
if redis.call('GET', KEYS[4]) == ARGV[1] then redis.call('DEL', KEYS[4]) end
if redis.call('GET', KEYS[5]) == ARGV[1] then redis.call('DEL', KEYS[5]) end
if redis.call('GET', KEYS[6]) == ARGV[1] then redis.call('DEL', KEYS[6]) end
redis.call('HSET', KEYS[1], 'status', 'LEFT')
return {'OK', wasActive}                                 -- wasActive=1 → slot bebas, picu promosi
