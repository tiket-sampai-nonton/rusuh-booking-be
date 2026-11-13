-- claim.lua — atomic claim dengan deadline check dan galat spesifik per status (design §5.3, QSH-T04)
-- KEYS[1]=entry:<id> KEYS[2]=active
-- ARGV: 1=entryId 2=nowMs 3=visitorId 4=claimMaxMs (C6.5: upper bound of the CLAIMED slot, in ms)
-- Reply: {ERR,NOT_FOUND|NOT_ADMITTED|TURN_MISSED|GONE|SOLD_OUT} | {OK,CLAIMED|ALREADY,<allocationId>}
local st = redis.call('HGET', KEYS[1], 'status')
if not st then return {'ERR','NOT_FOUND'} end
if redis.call('HGET', KEYS[1], 'visitorId') ~= ARGV[3] then return {'ERR','NOT_FOUND'} end
local alloc = redis.call('HGET', KEYS[1], 'allocationId') or ''
if st == 'CLAIMED' then return {'OK','ALREADY', alloc} end   -- idempoten: klaim ulang aman
if st == 'WAITING' or st == 'STALE' then return {'ERR','NOT_ADMITTED'} end
if st == 'MISSED' then return {'ERR','TURN_MISSED'} end
if st == 'LEFT' or st == 'ENDED' or st == 'COMPLETED' then return {'ERR','GONE'} end
if st == 'SOLD_OUT' then return {'ERR','SOLD_OUT'} end
if st ~= 'ADMITTED' then return {'ERR','NOT_FOUND'} end
local dl = tonumber(redis.call('HGET', KEYS[1], 'claimDeadlineMs'))
if dl == nil or tonumber(ARGV[2]) >= dl then
  redis.call('HSET', KEYS[1], 'status', 'MISSED')           -- tetap di active; sweeper berikutnya membersihkan
  return {'ERR','TURN_MISSED'}
end
-- C6.5 (P2-15): batas atas slot CLAIMED = nowMs + claimMaxMs. Slot tetap dihitung ZCARD dan dibebaskan oleh
-- journey.ended; bila tidak ada outcome sampai batas itu, end_entry.lua mengakhirinya (ENDED/CLAIM_TIMEOUT).
local expires = string.format('%d', tonumber(ARGV[2]) + tonumber(ARGV[4]))
redis.call('HSET', KEYS[1], 'status','CLAIMED', 'claimedAtMs', ARGV[2], 'claimExpiresMs', expires)
redis.call('ZADD', KEYS[2], 'XX', expires, ARGV[1])
return {'OK','CLAIMED', alloc}
