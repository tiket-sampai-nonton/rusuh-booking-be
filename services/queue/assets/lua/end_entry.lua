-- end_entry.lua — guard idempoten untuk consumer booking.journey.ended + promoter/sweeper outcomes
-- (design §5.1, QSH-T02)
-- KEYS[1]=entry:<id> KEYS[2]=active KEYS[3..5]=nik/visitor/email keys KEYS[6]=waiting
-- ARGV: 1=entryId 2=outcome 3=nowMs 4=P (prefix for the global stale/pending sets)
--       5=hbShardKey 6=hbstaleShardKey (the entry's sharded liveness keys; ADR-030 Amendment)
--       7=retentionMs (D-C8.2: every terminal write sets PEXPIRE on the entry hash)
-- outcome=SWEEP_MISSED (Queue sweeper, deadline lewat); ALREADY_ACTIVE (promoter, WAITING-only; ADR-030 removed
-- BUYER_QUOTA_EXHAUSTED and UNREADABLE_PII); PROMOTER_ORPHAN needs no class (hash gone => NO_ENTRY branch below);
-- anything else = Booking outcome (PAID|UNCLAIMED|REFUNDED|...).
local function dropSets()
  redis.call('ZREM', ARGV[5], ARGV[1])              -- sharded hb:{s}
  redis.call('ZREM', ARGV[6], ARGV[1])              -- sharded hbstale:{s}
  redis.call('ZREM', ARGV[4] .. 'stale', ARGV[1])   -- global ordering set
  redis.call('ZREM', ARGV[4] .. 'pending', ARGV[1]) -- global (draw dropped; harmless)
end
local st = redis.call('HGET', KEYS[1], 'status')
if not st then
  -- orphan id: self-heal, remove from both sets so a poison page never blocks later entries (P12).
  redis.call('ZREM', KEYS[2], ARGV[1])
  redis.call('ZREM', KEYS[6], ARGV[1])
  return {'IGNORED', 'NO_ENTRY'}
end
if st=='COMPLETED' or st=='ENDED' or st=='LEFT' then
  if ARGV[2]=='SWEEP_MISSED' then redis.call('ZREM', KEYS[2], ARGV[1]) end -- self-heal stale membership
  return {'IGNORED', st}
end
-- MISSED: hanya bersihkan constraint keys (status sudah di-set oleh claim.lua/class A) agar bisa antre lagi
if st=='MISSED' then
  -- OK_CLEANUP only when something was actually cleaned, so a redelivery is a reported no-op (idempotent).
  local cleaned = redis.call('ZREM', KEYS[2], ARGV[1]) + redis.call('ZREM', KEYS[6], ARGV[1])
  for i = 3, 5 do
    if redis.call('GET', KEYS[i])==ARGV[1] then redis.call('DEL', KEYS[i]); cleaned = cleaned + 1 end
  end
  if cleaned > 0 then return {'OK_CLEANUP', 'MISSED'} end
  return {'IGNORED', 'MISSED'}
end

-- class A: sweeper outcome
if ARGV[2]=='SWEEP_MISSED' then
  local final, reason = 'MISSED', ARGV[2]
  if st=='ADMITTED' then
    local dl = tonumber(redis.call('HGET', KEYS[1], 'claimDeadlineMs'))
    if dl == nil or tonumber(ARGV[3]) >= dl then
      -- fallthrough to the generic "end" path below with final=MISSED
    else
      return {'IGNORED', 'ADMITTED'} -- still live, sweep LIMIT page moves on without blocking
    end
  elseif st=='CLAIMED' then
    -- C6.5 (P2-15): a CLAIMED slot ends once its claimExpiresMs (stamped by claim.lua) has passed; Booking is not called.
    -- A legacy CLAIMED without the stamp keeps the self-heal (+inf) and is not expired here.
    local exp = tonumber(redis.call('HGET', KEYS[1], 'claimExpiresMs'))
    if exp == nil then
      redis.call('ZADD', KEYS[2], 'XX', 'inf', ARGV[1]) -- self-heal: legacy CLAIMED still scored by deadline
      return {'IGNORED', 'CLAIMED'}
    end
    if tonumber(ARGV[3]) < exp then
      return {'IGNORED', 'CLAIMED'} -- still within its claim bound
    end
    final, reason = 'ENDED', 'CLAIM_TIMEOUT'
  elseif st=='WAITING' then
    redis.call('ZREM', KEYS[2], ARGV[1]) -- WAITING must never sit in active
    return {'IGNORED', 'WAITING'}
  else
    return {'IGNORED', st}
  end
  redis.call('ZREM', KEYS[2], ARGV[1])
  redis.call('ZREM', KEYS[6], ARGV[1])
  redis.call('HSET', KEYS[1], 'status', final, 'endReason', reason, 'endedAtMs', ARGV[3])
  redis.call('PEXPIRE', KEYS[1], ARGV[7])
  dropSets()
  if redis.call('GET', KEYS[3])==ARGV[1] then redis.call('DEL', KEYS[3]) end
  if redis.call('GET', KEYS[4])==ARGV[1] then redis.call('DEL', KEYS[4]) end
  if redis.call('GET', KEYS[5])==ARGV[1] then redis.call('DEL', KEYS[5]) end
  return {'OK', final}
end

-- class B: promoter outcomes that only ever end a WAITING entry (never touch ADMITTED/CLAIMED)
if ARGV[2]=='ALREADY_ACTIVE' then
  if st ~= 'WAITING' then return {'IGNORED', st} end
  redis.call('ZREM', KEYS[2], ARGV[1])
  redis.call('ZREM', KEYS[6], ARGV[1])
  dropSets()
  redis.call('HSET', KEYS[1], 'status', 'ENDED', 'endReason', ARGV[2], 'endedAtMs', ARGV[3])
  redis.call('PEXPIRE', KEYS[1], ARGV[7])
  if redis.call('GET', KEYS[3])==ARGV[1] then redis.call('DEL', KEYS[3]) end
  if redis.call('GET', KEYS[4])==ARGV[1] then redis.call('DEL', KEYS[4]) end
  if redis.call('GET', KEYS[5])==ARGV[1] then redis.call('DEL', KEYS[5]) end
  return {'OK', 'ENDED'}
end

-- class C: Booking outcomes
if st ~= 'ADMITTED' and st ~= 'CLAIMED' then
  return {'IGNORED', st} -- incl. PAID on WAITING (INV-5 idempotent)
end
local final
if ARGV[2]=='UNCLAIMED' then
  final = 'MISSED'
elseif ARGV[2]=='PAID' then
  final = 'COMPLETED'
else
  final = 'ENDED'
end
redis.call('ZREM', KEYS[2], ARGV[1])
redis.call('ZREM', KEYS[6], ARGV[1])
dropSets()
redis.call('HSET', KEYS[1], 'status', final, 'endReason', ARGV[2], 'endedAtMs', ARGV[3])
redis.call('PEXPIRE', KEYS[1], ARGV[7])
if redis.call('GET', KEYS[3])==ARGV[1] then redis.call('DEL', KEYS[3]) end
if redis.call('GET', KEYS[4])==ARGV[1] then redis.call('DEL', KEYS[4]) end
if redis.call('GET', KEYS[5])==ARGV[1] then redis.call('DEL', KEYS[5]) end
return {'OK', final}
