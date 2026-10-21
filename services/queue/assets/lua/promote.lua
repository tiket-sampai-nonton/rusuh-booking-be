-- promote.lua — atomic batch promotion waiting → active (design §4.2)
-- KEYS[1]=waiting  KEYS[2]=active  KEYS[3..]=entry:<id> untuk tiap kandidat
-- ARGV: 1=capacity 2=nowMs ; lalu triplet per entry: entryId, allocationId, claimDeadlineMs
-- Dipanggil Go HANYA untuk entry yang sudah RESERVED oleh Booking (invarian "Jangan PHP").
local active = redis.call('ZCARD', KEYS[2])
local free = tonumber(ARGV[1]) - active
if free <= 0 then return {'FULL', active} end
local promoted = {}
local i = 3                     -- index ARGV triplet pertama
local k = 3                     -- index KEYS entry pertama
while free > 0 and ARGV[i] do
  local entryId   = ARGV[i]
  local allocId   = ARGV[i+1]
  local deadline  = ARGV[i+2]
  -- hanya promosikan bila masih WAITING (guard FIFO/idempoten)
  if redis.call('HGET', KEYS[k], 'status') == 'WAITING' then
    redis.call('ZREM', KEYS[1], entryId)
    redis.call('ZADD', KEYS[2], deadline, entryId)
    redis.call('HSET', KEYS[k], 'status','ADMITTED',
      'admittedAtMs', ARGV[2], 'allocationId', allocId, 'claimDeadlineMs', deadline)
    table.insert(promoted, entryId)
    free = free - 1
  end
  i = i + 3
  k = k + 1
end
return {'OK', promoted}
