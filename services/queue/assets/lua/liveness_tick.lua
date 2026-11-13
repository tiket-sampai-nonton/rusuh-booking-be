-- liveness_tick.lua — the whole liveness sweep in ONE round-trip (design-change §4.3). It acquires the promoter
-- lease (SET NX PX), reads the top-N waiting candidates, sweeps the liveness shards, then releases the lease, atomically
-- inside a single EVAL. Collapsing the liveness tick's round-trips (lease SET, candidate read, sweep EVAL, lease DEL)
-- into one is what keeps the promoter tick within +20 % of base under heartbeat load: on Valkey's single command
-- thread every extra round-trip queues behind the heartbeat traffic (3,333 EVAL/s), so four round-trips carry ~4×
-- the queueing penalty of one. The sweep body is identical to liveness.lua (STALE then END across hb:{s}/hbstale:{s});
-- only the lease handling and the candidate read are folded in.
-- One budget per tick (C8.7): ARGV[5] bounds the entries staled plus ended in the WHOLE tick, across all shards. The
-- sweep begins at shard ARGV[11] (mod B), wraps around, and stops when the budget is spent. The caller rotates
-- ARGV[11] per tick, so the shards a budget does not reach in one tick are reached by the next ones.
-- KEYS[1]=<tag>:promoter:lock
-- ARGV: 1=P 2=leaseToken 3=staleBeforeMs 4=endBeforeMs 5=budget 6=nowMs 7=shardCount(B) 8=leaseTtlMs 9=topN
--       10=retentionMs (PEXPIRE of an entry this sweep ends, D-C8.2) 11=startShard (C8.7)
-- Reply: {LEASE_LOST} | {OK, staled, ended, {top ids...}}
local lockKey = KEYS[1]
local P = ARGV[1]
local token = ARGV[2]

-- Acquire the lease atomically; if another holder owns it, this tick is skipped (same semantics as SET NX from Go).
local acquired = redis.call('SET', lockKey, token, 'NX', 'PX', tonumber(ARGV[8]))
if not acquired then
  return {'LEASE_LOST'}
end

-- Promoter candidate read: top-N live WAITING ids by seq (empty-safe). Folded in so the whole tick is one round-trip.
local topN = tonumber(ARGV[9])
local top = {}
if topN and topN > 0 then
  top = redis.call('ZRANGEBYSCORE', P .. 'waiting', '0', '+inf', 'LIMIT', 0, topN)
end

local budget = tonumber(ARGV[5])
local B = tonumber(ARGV[7])
if B == nil or B < 1 then B = 1 end
local start = (tonumber(ARGV[11]) or 0) % B
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
        redis.call('PEXPIRE', ek, ARGV[10])
        local h = redis.call('HMGET', ek, 'visitorId', 'accountId')
        if h[1] and redis.call('GET', P .. 'visitor:' .. h[1]) == id then redis.call('DEL', P .. 'visitor:' .. h[1]) end
        if h[2] and h[2] ~= '' and redis.call('GET', P .. 'account:' .. h[2]) == id then redis.call('DEL', P .. 'account:' .. h[2]) end
        ended = ended + 1
      end
    end
  end
end

-- Release the lease we acquired (compare-and-delete: only our own token).
if redis.call('GET', lockKey) == token then
  redis.call('DEL', lockKey)
end
return {'OK', staled, ended, top}
