// Command booking runs the Booking Service. Satu binary, satu verb per deployment:
//
//	booking api            HTTP API (port APP_PORT)
//	booking worker         sweeper TTL (sesi dan pembayaran), outbox relay, consumer payment.*
//	booking migrate up     terapkan migrasi schema booking
//	booking migrate down   rollback 1 migrasi (--steps N)
//	booking migrate status daftar migrasi
//	booking healthcheck    cek /health lokal (HEALTHCHECK container)
package main

func main() { Execute() }
