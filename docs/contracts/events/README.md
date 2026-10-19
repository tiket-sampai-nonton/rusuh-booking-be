# Kontrak Event

Exchange `rusuh.events` (topic). `envelope.schema.json` membungkus setiap payload `<routing-key>.schema.json`.
Routing key dan consumer queue: SDD §4.6.2. Retry: `<queue>.retry.5s|30s|2m`, DLQ `<queue>.dlq` (pkg/rabbitmq).
