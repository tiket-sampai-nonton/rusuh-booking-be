-- join.lua — atomic join dengan semua constraint (design §4.1)
-- KEYS[1]=seq  KEYS[2]=waiting  KEYS[3]=entry:<id>
-- KEYS[4]=nik:<h>  KEYS[5]=visitor:<v>  KEYS[6]=email:<eh>  KEYS[7]=quota_exhausted
-- ARGV: 1=entryId 2=visitorId 3=nikHash 4=emailEnc 5=accountId 6=nowMs
--       7=oneActivePerVisitor("1"/"0") 8=oneActivePerEmail("1"/"0") 9=emailHash(hex)

-- (1) kuota habis?
if redis.call('SISMEMBER', KEYS[7], ARGV[3]) == 1 then
  return {'ERR', 'BUYER_QUOTA_EXHAUSTED'}
end
-- (2) NIK sudah antre? (idempoten: kembalikan entry yang ada)
local nik = redis.call('GET', KEYS[4])
if nik then return {'EXISTING', 'NIK', nik} end
-- (3) visitor sudah antre?
if ARGV[7] == '1' then
  local v = redis.call('GET', KEYS[5])
  if v then return {'EXISTING', 'VISITOR', v} end
end
-- (4) email sudah antre?
if ARGV[8] == '1' then
  local em = redis.call('GET', KEYS[6])
  if em then return {'EXISTING', 'EMAIL', em} end
end
-- (5) sequence + entry + waiting + constraint keys (semua-atau-tidak)
local seq = redis.call('INCR', KEYS[1])
redis.call('HSET', KEYS[3],
  'seq', seq, 'visitorId', ARGV[2], 'buyerNikHash', ARGV[3],
  'emailEnc', ARGV[4], 'emailHash', ARGV[9], 'accountId', ARGV[5],
  'status', 'WAITING', 'joinedAtMs', ARGV[6])
redis.call('ZADD', KEYS[2], seq, ARGV[1])
redis.call('SET', KEYS[4], ARGV[1])
if ARGV[7] == '1' then redis.call('SET', KEYS[5], ARGV[1]) end
if ARGV[8] == '1' then redis.call('SET', KEYS[6], ARGV[1]) end
return {'OK', seq, 'WAITING'}
