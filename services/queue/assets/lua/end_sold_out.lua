-- end_sold_out.lua — booking.sold_out: end WAITING/STALE entries with status SOLD_OUT (SRS-F-10-02, req 5.5).
-- Idempotent and bounded: Go passes one batch of ids; a redelivery finds them already SOLD_OUT and changes nothing.
-- ADMITTED/CLAIMED entries are NEVER touched (Jangan PHP): they hold an allocation. An id that sits in waiting/stale
-- but is not WAITING/STALE is dropped from those sets (self-heal) so a poison id can never stall later batches.
-- ARGV: 1=P (event key prefix) 2=nowMs, then triples (entryId, hbShardKey, hbstaleShardKey).
-- Reply: {ended}
local P = ARGV[1]
local ended = 0
local i = 3
while i + 2 <= #ARGV do
  local id, hb, hbs = ARGV[i], ARGV[i + 1], ARGV[i + 2]
  i = i + 3
  local ek = P .. 'entry:' .. id
  local st = redis.call('HGET', ek, 'status')
  redis.call('ZREM', P .. 'waiting', id)
  redis.call('ZREM', P .. 'stale', id)
  if st == 'WAITING' or st == 'STALE' then
    redis.call('ZREM', hb, id)
    redis.call('ZREM', hbs, id)
    redis.call('HSET', ek, 'status', 'SOLD_OUT', 'endReason', 'SOLD_OUT', 'endedAtMs', ARGV[2])
    local h = redis.call('HMGET', ek, 'visitorId', 'accountId')
    if h[1] and redis.call('GET', P .. 'visitor:' .. h[1]) == id then redis.call('DEL', P .. 'visitor:' .. h[1]) end
    if h[2] and h[2] ~= '' and redis.call('GET', P .. 'account:' .. h[2]) == id then redis.call('DEL', P .. 'account:' .. h[2]) end
    ended = ended + 1
  end
end
return {ended}
