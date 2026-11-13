-- me.lua — the `me` read in one atomic step (C8.9, PF-P2-3). Same outcomes as the former multi-call path:
--   1. token generation check and ratchet, as token_gen.lua (D-C6.1): no record -> ABSENT; a generation that is
--      older or not positive -> INVALID; a newer one is stored (KEEPTTL);
--   2. the visitor's entry: visitor:<v> (the active one) when it names an existing hash, else vlast:<v> (the most
--      recent one, any state); neither -> NOT_FOUND;
--   3. the live position of a WAITING or STALE entry: 1 + ZCOUNT of waiting with a smaller seq; 0 otherwise.
-- The rate limit is checked by the caller on its own counter; the script only reads, apart from the ratchet.
-- KEYS: 1=waiting 2=vtok:<v> 3=visitor:<v> 4=vlast:<v> (one hash slot per event)
-- ARGV: 1=P (the event's key prefix; entry:<id> is built from it) 2=tokenGen
-- Reply: {ABSENT} | {INVALID} | {NOT_FOUND} | {OK, entryId, position, field1, value1, ...}
local cur = redis.call('GET', KEYS[2])
if not cur then return {'ABSENT'} end
local tg = tonumber(ARGV[2])
local c = tonumber(cur) or 0
if tg == nil or tg <= 0 or tg < c then return {'INVALID'} end
if tg > c then redis.call('SET', KEYS[2], ARGV[2], 'KEEPTTL') end

local P = ARGV[1]
local function find(id)
  if not id then return nil end
  local m = redis.call('HGETALL', P .. 'entry:' .. id)
  if #m == 0 then return nil end
  return m
end
local id = redis.call('GET', KEYS[3])
local m = find(id)
if not m then
  id = redis.call('GET', KEYS[4])
  m = find(id)
end
if not m then return {'NOT_FOUND'} end

local status, seq = nil, '0'
for i = 1, #m, 2 do
  if m[i] == 'status' then status = m[i + 1] end
  if m[i] == 'seq' then seq = m[i + 1] end
end
local pos = 0
if status == 'WAITING' or status == 'STALE' then
  pos = redis.call('ZCOUNT', KEYS[1], '-inf', string.format('(%d', tonumber(seq) or 0)) + 1
end
local out = {'OK', id, pos}
for i = 1, #m do out[#out + 1] = m[i] end
return out
