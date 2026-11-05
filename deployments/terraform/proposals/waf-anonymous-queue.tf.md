# Proposal (documentation only): edge controls for the anonymous queue (ADR-030, ADR-012)

Status: proposal. No `.tf` file is changed and nothing is applied to live infrastructure. Numbers are starting
points to be tuned from load-test evidence.

Why: the join is anonymous and client-side bot checks are explicitly not built (owner decision 7), so anti-scalper
protection is server/edge only.

Proposed changes to the existing WAF web ACL (see `deployments/terraform/modules`, ADR-012):

1. AWS managed rule groups in **Count** mode first, promoted to Block after one week of data:
   `AWSManagedRulesBotControlRuleSet` (targeted, known bot signatures), `AWSManagedRulesAmazonIpReputationList`,
   `AWSManagedRulesAnonymousIpList` (data-center / hosting ranges).
2. Rate-based rules per source IP (5-minute window, Block with `Retry-After` custom response):
   - `POST /api/v1/events/*/queue-entries` (join): start 300 per 5 min per IP. Mobile carriers share NAT addresses,
     so this is a soft brake above the application's own soft cap (200/min), never a per-person block.
   - `POST /api/v1/bookings` (hold): start 200 per 5 min per IP (the NIK attempt budget is enforced in Booking).
   - visitor-token and OTP routes: keep the existing limits.
3. CloudFront: cache `GET /api/v1/queue-status/{eventId}` for 1 second so "turn soon" detection costs no origin
   traffic at the head of a sale.

Risks: residential-IP bot farms and valid-format fake NIKs pass these controls (accepted risk in ADR-030). Recommended
later options: Dukcapil verification, ID check at the gate.
