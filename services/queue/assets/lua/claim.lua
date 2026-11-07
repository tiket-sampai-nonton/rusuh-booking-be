-- claim.lua — atomic claim dengan deadline check dan galat spesifik per status (design §5.3, QSH-T04)
-- KEYS[1]=entry:<id> KEYS[2]=active
-- ARGV: 1=entryId 2=nowMs 3=visitorId
-- Reply: {ERR,NOT_FOUND|NOT_ADMITTED|TURN_MISSED|GONE} | {OK,CLAIMED|ALREADY,<allocationId>}
local st = redis.call('HGET', KEYS[1], 'status')
if not st then return {'ERR','NOT_FOUND'} end
if redis.call('HGET', KEYS[1], 'visitorId') ~= ARGV[3] then return {'ERR','NOT_FOUND'} end
local alloc = redis.call('HGET', KEYS[1], 'allocationId') or ''
if st == 'CLAIMED' then return {'OK','ALREADY', alloc} end   -- idempoten: klaim ulang aman
if st == 'WAITING' or st == 'STALE' then return {'ERR','NOT_ADMITTED'} end
if st == 'MISSED' then return {'ERR','TURN_MISSED'} end
if st == 'LEFT' or st == 'ENDED' or st == 'COMPLETED' then return {'ERR','GONE'} end
if st ~= 'ADMITTED' then return {'ERR','NOT_FOUND'} end
local dl = tonumber(redis.call('HGET', KEYS[1], 'claimDeadlineMs'))
if dl == nil or tonumber(ARGV[2]) >= dl then
  redis.call('HSET', KEYS[1], 'status', 'MISSED')           -- tetap di active; sweeper berikutnya membersihkan
  return {'ERR','TURN_MISSED'}
end
redis.call('HSET', KEYS[1], 'status','CLAIMED', 'claimedAtMs', ARGV[2])
-- keluarkan dari scan kedaluwarsa: slot CLAIMED tetap dihitung ZCARD, dibebaskan oleh journey.ended
redis.call('ZADD', KEYS[2], 'XX', 'inf', ARGV[1])
return {'OK','CLAIMED', alloc}
