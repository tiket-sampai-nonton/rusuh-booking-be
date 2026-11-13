-- heartbeat.lua — liveness signal for the visitor's current entry (ADR-030, R03). One atomic step.
-- All positions are strict FIFO by seq (the opening-window draw was dropped); there is no draw/pending state.
-- The liveness index is sharded. The script reads the entry's stored shard (HGET ek 'shard', written by join.lua) and
-- builds hb:{s} / hbstale:{s} from it, so the WAITING write and the STALE→WAITING revive address the shard the entry
-- lives in, whatever the shard count is now. Only a legacy entry without a shard field uses the keys Go passes in
-- ARGV[5]/[6], which Go derives from the vlast entry id.
-- ARGV: 1=P 2=visitorId 3=tokenGen 4=nowMs 5=legacyHbShardKey 6=legacyHbstaleShardKey
-- Reply: {INVALID} | {NOT_FOUND} | {OK, entryId, status, seq, revived(0/1)}
-- The generation is a monotonic ratchet (D-C6.1, token_gen.lua): an absent record (never joined), or a generation that
-- is older or not positive, is INVALID; a newer one is accepted and stored (KEEPTTL).
local P, v, now = ARGV[1], ARGV[2], ARGV[4]
local vtokKey = P .. 'vtok:' .. v
local cur = tonumber(redis.call('GET', vtokKey) or '0')
local tg = tonumber(ARGV[3]) or 0
if cur <= 0 or tg <= 0 or tg < cur then
  return {'INVALID'}
end
if tg > cur then redis.call('SET', vtokKey, ARGV[3], 'KEEPTTL') end
local id = redis.call('GET', P .. 'vlast:' .. v)
if not id then return {'NOT_FOUND'} end
local ek = P .. 'entry:' .. id
local st = redis.call('HGET', ek, 'status')
if (not st) or st == 'COMPLETED' or st == 'ENDED' or st == 'LEFT' or st == 'MISSED' or st == 'SOLD_OUT' then return {'NOT_FOUND'} end
-- The liveness keys of the shard the entry was stored under (join.lua field `shard`); a legacy entry without it
-- falls back to the keys Go derived from the same entry id.
local hbKey, hbstaleKey = ARGV[5], ARGV[6]
local sh = redis.call('HGET', ek, 'shard')
if sh then
  hbKey, hbstaleKey = P .. 'hb:' .. sh, P .. 'hbstale:' .. sh
end
local seq = tonumber(redis.call('HGET', ek, 'seq') or '0')
local revived = 0
if st == 'WAITING' then
  redis.call('ZADD', hbKey, now, id)
  redis.call('HSET', ek, 'lastSignalMs', now)
elseif st == 'STALE' then
  -- revive at the original seq; a concurrent promote sees the new state on its next script
  redis.call('ZREM', P .. 'stale', id)
  redis.call('ZREM', hbstaleKey, id)
  redis.call('ZADD', P .. 'waiting', seq, id)
  redis.call('ZADD', hbKey, now, id)
  redis.call('HSET', ek, 'status', 'WAITING', 'lastSignalMs', now)
  st, revived = 'WAITING', 1
end
-- ADMITTED / CLAIMED: no write; the state tells the client it was admitted
return {'OK', id, st, seq, revived}
