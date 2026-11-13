-- promote.lua — atomic batch promotion waiting → active, fenced by the promoter lease (design §5.4, ADR-028)
-- KEYS[1]=waiting  KEYS[2]=active  KEYS[3]=<tag>:promoter:lock  KEYS[4]=<tag>:adm:<nowMs/60000> (counter admisi per menit)
-- KEYS[5..]=entry:<id> untuk tiap kandidat
-- ARGV: 1=capacity 2=nowMs 3=requireHead("1"/"0") 4=leaseToken ; lalu quadruplet per entry dari ARGV[5]:
--       entryId, allocationId, claimDeadlineMs, hbShardKey
-- ADR-030: hanya status WAITING yang dipromosikan; STALE dilewati (masuk 'skipped', tidak pernah dirilis).
-- Admitted entries leave the liveness set via their stored shard key hb:{s} (ADR-030 Amendment: hb is sharded).
-- Dipanggil Go HANYA untuk entry yang sudah RESERVED oleh Booking (invarian "Jangan PHP").
-- Fence pertama: token yang tidak cocok (termasuk lease yang sudah lewat TTL) tidak mengubah apa pun.
if redis.call('GET', KEYS[3]) ~= ARGV[4] then
  return {'LEASE_LOST'}
end
local active = redis.call('ZCARD', KEYS[2])
local free = tonumber(ARGV[1]) - active
if free <= 0 then return {'FULL', active} end

local firstEntry = ARGV[5]
if ARGV[3] == '1' then
  local head = redis.call('ZRANGE', KEYS[1], 0, 0)[1]
  if head ~= firstEntry then
    return {'OK', {}, {{firstEntry, 'NOT_HEAD'}}}
  end
end

local promoted = {}
local skipped = {}
local i = 5                     -- index ARGV quadruplet pertama
local k = 5                     -- index KEYS entry pertama
while ARGV[i] do
  local entryId   = ARGV[i]
  local allocId   = ARGV[i+1]
  local deadline  = ARGV[i+2]
  local hbShard   = ARGV[i+3]
  if free > 0 then
    local st = redis.call('HGET', KEYS[k], 'status')
    if st == 'WAITING' then
      redis.call('ZREM', KEYS[1], entryId)
      redis.call('ZREM', hbShard, entryId) -- admitted entries leave the liveness set (sharded hb:{s})
      redis.call('ZADD', KEYS[2], deadline, entryId)
      redis.call('HSET', KEYS[k], 'status','ADMITTED',
        'admittedAtMs', ARGV[2], 'allocationId', allocId, 'claimDeadlineMs', deadline)
      table.insert(promoted, entryId)
      free = free - 1
    elseif st == false then
      table.insert(skipped, {entryId, 'NO_ENTRY'})
    elseif st == 'LEFT' or st == 'ENDED' or st == 'COMPLETED' or st == 'MISSED' then
      table.insert(skipped, {entryId, st})
    elseif st == 'ADMITTED' or st == 'CLAIMED' then
      local storedAlloc = redis.call('HGET', KEYS[k], 'allocationId')
      if storedAlloc == allocId then
        table.insert(skipped, {entryId, 'ADMITTED_SAME'})
      else
        table.insert(skipped, {entryId, 'ADMITTED_OTHER', storedAlloc})
      end
    else
      table.insert(skipped, {entryId, st})
    end
  end
  -- kandidat yang tak terjangkau karena free sudah 0 TIDAK dilaporkan (tetap WAITING, tak disentuh)
  i = i + 4
  k = k + 1
end
-- Laju admisi (admissionRatePerMin di Status Document): hitung admisi per menit; TTL 1200 s menutup jendela publisher
-- (maks 15 menit penuh + menit berjalan).
if #promoted > 0 then
  redis.call('INCRBY', KEYS[4], #promoted)
  redis.call('EXPIRE', KEYS[4], 1200)
end
return {'OK', promoted, skipped}
