-- join.lua — anonymous atomic join (ADR-030). The join carries no NIK, email or consent: it ranks a principal.
-- Identity is a visitor id (from a verified visitor token or freshly minted) and, optionally, the account id
-- of a verified User bearer. The bearer identifies the principal only; it never binds a NIK.
-- Every key shares the event hash tag (one slot); keys are built from the prefix P in ARGV[1].
--
-- All joins are strict FIFO by seq (ADR-030 Amendment: the opening-window draw was dropped). There is no rank
-- field: seq is the admission order. The liveness index hb is sharded (ADR-030 Amendment): Go passes the entry's
-- shard index s in ARGV[12] and the full key hb:{s} in ARGV[13]; the shard index is stored on the entry hash
-- (field `shard`) so every later liveness mutation addresses the shard the entry lives in.
--
-- ARGV: 1=P 2=newEntryId 3=newVisitorId 4=tokenVisitorId('' = none) 5=tokenGen 6=accountId('' = none)
--       7=idemKeySuffix('' = none) 8=nowMs 9=oneActivePerVisitor('1'/'0') 10=idemTTLSeconds 11=tokenTTLSeconds
--       12=shardIndex 13=hbShardKey (liveness shard key hb:{s} for the new entry)
-- Reply: {OUTCOME, entryId, seq, visitorId, status, gen}
--   OUTCOME = CREATED | EXISTING | REATTACH | ERR_ACCOUNT_ALREADY_IN_QUEUE
-- Token generation (vtok:<v>, per event; D-C6.1): a monotonic ratchet. A token generation older than the stored one is
-- not honoured; a newer one is accepted and stored (SET KEEPTTL). Rotation sets max(stored+1, nowMs), so every
-- rotation is strictly newer than any generation handed out before. Rotation happens on re-attach and when a visitor
-- id is reused after its entry ENDED; nowhere else.

local P = ARGV[1]
local tokV, tokGen, acct, idem = ARGV[4], ARGV[5], ARGV[6], ARGV[7]
local now = ARGV[8]
local tokenTTL = tonumber(ARGV[11])
local shardIdx = ARGV[12]
local hbShard = ARGV[13]

local function ekey(id) return P .. 'entry:' .. id end
local function gen(v) return tonumber(redis.call('GET', P .. 'vtok:' .. v) or '0') end
local function rotate(v)
  local g = math.max(gen(v) + 1, tonumber(now))
  redis.call('SET', P .. 'vtok:' .. v, string.format('%d', g), 'EX', tokenTTL)
  return g
end

local function describe(outcome, id, g)
  local h = redis.call('HMGET', ekey(id), 'seq', 'visitorId', 'status')
  local vid = h[2] or ''
  -- Every token this join hands back is signed with a fresh lifetime, so the generation record must live as long
  -- (QVT-03.7: "record absent" must mean "never joined"). EXPIRE on a missing key is a no-op: never creates it.
  if vid ~= '' then redis.call('EXPIRE', P .. 'vtok:' .. vid, tokenTTL) end
  if g == nil then g = gen(vid) end
  return {outcome, id, tostring(h[1] or '0'), vid, h[3] or '', g}
end

local function active(key)
  local id = redis.call('GET', key)
  if not id then return nil end
  local st = redis.call('HGET', ekey(id), 'status')
  if (not st) or st == 'COMPLETED' or st == 'ENDED' or st == 'LEFT' or st == 'MISSED' or st == 'SOLD_OUT' then return nil end
  return id
end

-- 1. Idempotency-Key replay: same scope + key returns the same entry, never a second one, no rotation.
local idemKey = P .. 'idem:' .. idem
if idem ~= '' then
  local id = redis.call('GET', idemKey)
  if id and redis.call('EXISTS', ekey(id)) == 1 then return describe('EXISTING', id) end
end

-- 0. Token normalisation (never an error): a generation older than the stored one (rotated out), or a token of ANOTHER
-- account's entry, is ignored. A newer generation is ratcheted up (D-C6.1); an absent record is initialised below.
local v = tokV
local serverGen = 0
if v ~= '' then
  local tg = tonumber(tokGen) or 0
  if tg <= 0 then
    v = ''
  else
    serverGen = gen(v)
    if serverGen > 0 then
      if tg < serverGen then
        v = ''
      elseif tg > serverGen then
        redis.call('SET', P .. 'vtok:' .. v, tokGen, 'KEEPTTL')
      end
    end
  end
end
local ev = nil
if v ~= '' then ev = active(P .. 'visitor:' .. v) end
if acct ~= '' and ev then
  local owner = redis.call('HGET', ekey(ev), 'accountId')
  if owner and owner ~= '' and owner ~= acct then v = ''; ev = nil end
end

local acctKey = P .. 'account:' .. acct
if acct ~= '' then
  local ea = active(acctKey)
  if ea then
    if ev and ev ~= ea then return {'ERR_ACCOUNT_ALREADY_IN_QUEUE', ev, '0', v, '', 0} end
    if ev then return describe('EXISTING', ea) end
    -- re-attach (lost token, new device, or ended/rotated token): last device wins
    local vid = redis.call('HGET', ekey(ea), 'visitorId')
    return describe('REATTACH', ea, rotate(vid))
  end
  if ev then
    -- associate the anonymous entry of this token with the account (no rotation)
    redis.call('SET', acctKey, ev)
    redis.call('HSET', ekey(ev), 'accountId', acct)
    return describe('EXISTING', ev)
  end
elseif ev then
  -- anonymous caller holding a valid token of an active entry: the token is the credential
  return describe('EXISTING', ev)
end

-- 4b. Guard: vtok absent (serverGen=0) tapi vlast masih menunjuk ke entry aktif → kembalikan entry tanpa membuat baru.
if v ~= '' and serverGen == 0 then
  local lastId = redis.call('GET', P .. 'vlast:' .. v)
  if lastId then
    local st = redis.call('HGET', ekey(lastId), 'status')
    if st and st ~= 'ENDED' and st ~= 'COMPLETED' and st ~= 'LEFT' and st ~= 'MISSED' and st ~= 'SOLD_OUT' then
      return describe('EXISTING', lastId)
    end
  end
end

-- 5. Create. A valid token of an ENDED entry keeps its visitor id (rotated); otherwise the minted id starts at gen 1.
local vid, g = ARGV[3], 1
if v ~= '' then
  vid = v
  if serverGen == 0 then
    -- event baru: init gen = tokGen agar token lama tetap valid di event ini
    g = tonumber(tokGen)
    redis.call('SET', P .. 'vtok:' .. vid, tokGen, 'EX', tokenTTL)
  else
    g = rotate(vid)   -- entry ENDED sebelumnya: rotate seperti biasa (T3)
  end
else
  redis.call('SET', P .. 'vtok:' .. vid, 1, 'EX', tokenTTL)
end
local seq = redis.call('INCR', P .. 'seq')
local id = ARGV[2]
redis.call('HSET', ekey(id), 'seq', seq, 'visitorId', vid, 'accountId', acct, 'status', 'WAITING',
  'joinedAtMs', now, 'lastSignalMs', now, 'shard', shardIdx)
redis.call('ZADD', P .. 'waiting', seq, id)
redis.call('ZADD', hbShard, now, id)
if ARGV[9] == '1' then redis.call('SET', P .. 'visitor:' .. vid, id) end
redis.call('SET', P .. 'vlast:' .. vid, id, 'EX', tokenTTL)
if acct ~= '' then redis.call('SET', acctKey, id) end
if idem ~= '' then redis.call('SET', idemKey, id, 'EX', tonumber(ARGV[10])) end
return {'CREATED', id, tostring(seq), vid, 'WAITING', g}
