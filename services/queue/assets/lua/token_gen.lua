-- token_gen.lua — generation check for a visitor token on one event (D-C6.1, AC-C6.1 P0-8). Used by claim, leave and me.
-- The generation is a monotonic ratchet: a token generation higher than the stored one is accepted and stored
-- (SET KEEPTTL, the record keeps its TTL); a lower or non-positive one is refused. An absent record means the visitor
-- never joined this event and is never created here.
-- KEYS[1]=vtok:<visitor> (the event's hash slot)  ARGV: 1=gen
-- Reply: 1 OK | 0 INVALID | -1 ABSENT
local cur = redis.call('GET', KEYS[1])
if not cur then return -1 end
local g = tonumber(ARGV[1])
local c = tonumber(cur) or 0
if g == nil or g <= 0 or g < c then return 0 end
if g > c then redis.call('SET', KEYS[1], ARGV[1], 'KEEPTTL') end
return 1
