-- notify_window.lua — advance an event's pre-turn notify watermark (D-C8.3, PF-P1-5). The read of the previous value and
-- the write of max(prev, upTo) run in one EVAL, so two promoter instances can never hand out the same seq window twice.
-- KEYS[1] = the watermark key (in the event's hash slot, q:{e:<id>}:notify_seq).
-- ARGV: 1=upToSeq (the seq of the last live waiting entry inside the notify position).
-- Reply: the previous watermark (0 when none). The write keeps the TTL that RetireEvent may have set (SET KEEPTTL), so a
-- retired event's watermark still expires; a plain SET would clear it and leave the key immortal.
local prev = tonumber(redis.call('GET', KEYS[1]) or '0')
local upTo = tonumber(ARGV[1])
if upTo > prev then
  redis.call('SET', KEYS[1], ARGV[1], 'KEEPTTL')
end
return prev
