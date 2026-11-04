-- leave.lua — atomic leave dengan cleanup dan guard status (design §5.2, QSH-T03)
-- KEYS[1]=entry:<id> KEYS[2]=waiting KEYS[3]=active
-- KEYS[4]=nik:<h> KEYS[5]=visitor:<v> KEYS[6]=email:<eh>
-- ARGV: 1=entryId 2=visitorId 3=nowMs
-- Reply: {ERR,NOT_FOUND} | {IGNORED,<status>} | {OK,<prevStatus>,<allocationId>}
local st = redis.call('HGET', KEYS[1], 'status')
if not st then return {'ERR','NOT_FOUND'} end
if redis.call('HGET', KEYS[1], 'visitorId') ~= ARGV[2] then return {'ERR','NOT_FOUND'} end
-- terminal / sudah diklaim: tidak ada yang diubah (status tidak boleh ditimpa jadi LEFT)
if st=='COMPLETED' or st=='ENDED' or st=='LEFT' or st=='MISSED' then return {'IGNORED', st} end
if st=='CLAIMED' then return {'IGNORED', st} end -- PROVISIONAL (QSH-OQ-3)
local alloc = redis.call('HGET', KEYS[1], 'allocationId') or ''
redis.call('ZREM', KEYS[2], ARGV[1])
redis.call('ZREM', KEYS[3], ARGV[1])                    -- rilis slot bila ADMITTED
-- hapus constraint keys HANYA bila masih menunjuk entry ini
if redis.call('GET', KEYS[4]) == ARGV[1] then redis.call('DEL', KEYS[4]) end
if redis.call('GET', KEYS[5]) == ARGV[1] then redis.call('DEL', KEYS[5]) end
if redis.call('GET', KEYS[6]) == ARGV[1] then redis.call('DEL', KEYS[6]) end
redis.call('HSET', KEYS[1], 'status', 'LEFT')
if st ~= 'ADMITTED' then alloc = '' end
return {'OK', st, alloc}
