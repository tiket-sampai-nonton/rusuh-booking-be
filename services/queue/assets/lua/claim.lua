-- claim.lua — atomic claim dengan deadline check (design §4.3)
-- KEYS[1]=entry:<id>  KEYS[2]=active
-- ARGV: 1=entryId 2=nowMs 3=visitorId
local st = redis.call('HGET', KEYS[1], 'status')
if not st then return {'ERR','NOT_FOUND'} end
if redis.call('HGET', KEYS[1], 'visitorId') ~= ARGV[3] then return {'ERR','NOT_FOUND'} end
if st == 'CLAIMED' then
  return {'OK','ALREADY'}                       -- idempoten: klaim ulang aman
end
if st ~= 'ADMITTED' then return {'ERR','TURN_MISSED'} end
local dl = tonumber(redis.call('HGET', KEYS[1], 'claimDeadlineMs'))
if tonumber(ARGV[2]) >= dl then
  redis.call('HSET', KEYS[1], 'status', 'MISSED')
  return {'ERR','TURN_MISSED'}
end
redis.call('HSET', KEYS[1], 'status','CLAIMED', 'claimedAtMs', ARGV[2])
return {'OK','CLAIMED'}
