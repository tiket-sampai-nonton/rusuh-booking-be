# Kontrak Event

Exchange `rusuh.events` (topic). `envelope.schema.json` membungkus setiap payload `<routing-key>.schema.json`.
Routing key dan consumer queue: SDD §4.6.2. Retry: `<queue>.retry.5s|30s|2m`, DLQ `<queue>.dlq` (pkg/rabbitmq).

Validasi lokal dan di CI: `make contracts` (modul `tools/contracts`: OpenAPI wajib 3.1.x, schema event wajib JSON Schema 2020-12). Job CI `contracts` jalan bila `docs/contracts/` berubah.
