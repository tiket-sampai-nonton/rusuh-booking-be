-- retire_event.lua — retire a finished event's fixed keys in ONE step (D-C8.2, P3-13). The emptiness check and the
-- EXPIRE calls run in one EVAL on the event's hash slot, so no join or promoter write can land between them: a live
-- entry can never end up under a TTL. Nothing is written when any entry is still live.
-- KEYS[1..zsets] are the zsets that must be empty: active, waiting, stale, then hb:{s} and hbstale:{s} for s in 0..B-1.
-- KEYS[zsets+1..] are the expirable fixed keys: seq, pending, status, sale, unused, promoter:lock, notify_seq.
-- vtok keys are never listed here (their TTL is the token lifetime, D-C6.1).
-- ARGV: 1=ttlSeconds 2=zsets (the number of leading zset KEYS)
-- Reply: 0 = left untouched (an entry is still live) | 1 = TTL set (EXPIRE NX: a key keeps its first deadline)
local zsets = tonumber(ARGV[2])
for i = 1, zsets do
  if redis.call('ZCARD', KEYS[i]) > 0 then return 0 end
end
for i = zsets + 1, #KEYS do
  redis.call('EXPIRE', KEYS[i], ARGV[1], 'NX')
end
return 1
