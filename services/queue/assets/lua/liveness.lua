-- liveness.lua — mark silent entries STALE, then end long-silent ones (ADR-030, R03.2/R03.4). Fenced by the promoter
-- lease, so an entry cannot turn STALE between the promoter's candidate read and promote.lua.
-- The liveness index is sharded (ADR-030 Amendment): hb:{s} (score lastSignalMs) holds live WAITING entries,
-- hbstale:{s} holds STALE ones, for s in 0..B-1. The sweep runs ONCE PER TICK over the shards inside this single
-- script (one round-trip), instead of B separate EVALs — B separate round-trips each queue behind the heartbeat
-- traffic on Valkey's single command thread and multiply the promoter-tick latency (design-change §4.3). Admitted
-- entries are in neither set, so silence can never end an entry that holds an allocation. Positions are strict FIFO by
-- seq (the opening-window draw was dropped); there is no draw/pending state.
-- One budget per tick (C8.7): ARGV[5] bounds the entries staled plus ended across all shards; the sweep begins at
-- shard ARGV[9] (mod B), wraps around, and stops when the budget is spent. The same rule as liveness_tick.lua.
-- ARGV: 1=P 2=leaseToken 3=staleBeforeMs 4=endBeforeMs 5=budget 6=nowMs 7=shardCount(B) 8=retentionMs
--       (strict: score < threshold; retentionMs is the PEXPIRE of an entry this sweep ends, D-C8.2)
--       9=startShard (C8.7)
-- Reply: {LEASE_LOST} | {OK, staled, ended}
local P = ARGV[1]
if redis.call('GET', P .. 'promoter:lock') ~= ARGV[2] then return {'LEASE_LOST'} end
local budget = tonumber(ARGV[5])
local B = tonumber(ARGV[7])
if B == nil or B < 1 then B = 1 end
local start = (tonumber(ARGV[9]) or 0) % B
local staled, ended = 0, 0

for i = 0, B - 1 do
  if staled + ended >= budget then break end
  local sh = (start + i) % B
  local hbKey = P .. 'hb:' .. sh
  local hbstaleKey = P .. 'hbstale:' .. sh

  local ids = redis.call('ZRANGEBYSCORE', hbKey, '-inf', '(' .. ARGV[3], 'LIMIT', 0, budget - staled - ended)
  for _, id in ipairs(ids) do
    local ek = P .. 'entry:' .. id
    local st = redis.call('HGET', ek, 'status')
    local last = redis.call('ZSCORE', hbKey, id)
    redis.call('ZREM', hbKey, id)
    if st == 'WAITING' then
      local seq = redis.call('HGET', ek, 'seq')
      redis.call('ZREM', P .. 'waiting', id)
      redis.call('ZADD', P .. 'stale', seq, id)
      redis.call('ZADD', hbstaleKey, last, id)
      redis.call('HSET', ek, 'status', 'STALE')
      staled = staled + 1
    end
    -- any other status: self-heal (admitted or ended entries do not belong in hb)
  end

  if staled + ended < budget then
    ids = redis.call('ZRANGEBYSCORE', hbstaleKey, '-inf', '(' .. ARGV[4], 'LIMIT', 0, budget - staled - ended)
    for _, id in ipairs(ids) do
      local ek = P .. 'entry:' .. id
      local st = redis.call('HGET', ek, 'status')
      redis.call('ZREM', hbstaleKey, id)
      if st == 'STALE' or st == 'WAITING' then
        redis.call('ZREM', P .. 'waiting', id)
        redis.call('ZREM', P .. 'stale', id)
        redis.call('ZREM', hbKey, id)
        redis.call('HSET', ek, 'status', 'ENDED', 'endReason', 'STALE_TIMEOUT', 'endedAtMs', ARGV[6])
        redis.call('PEXPIRE', ek, ARGV[8])
        local h = redis.call('HMGET', ek, 'visitorId', 'accountId')
        if h[1] and redis.call('GET', P .. 'visitor:' .. h[1]) == id then redis.call('DEL', P .. 'visitor:' .. h[1]) end
        if h[2] and h[2] ~= '' and redis.call('GET', P .. 'account:' .. h[2]) == id then redis.call('DEL', P .. 'account:' .. h[2]) end
        ended = ended + 1
      end
    end
  end
end
return {'OK', staled, ended}
