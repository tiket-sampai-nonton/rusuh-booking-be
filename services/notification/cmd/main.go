// Command notification runs the Notification Service. Satu binary, satu verb per deployment:
//
//	notification api            HTTP API (port APP_PORT)
//	notification worker         consumer booking.*, queue.turn.* → email/push
//	notification migrate up     terapkan migrasi schema notification
//	notification migrate down   rollback 1 migrasi (--steps N)
//	notification migrate status daftar migrasi
//	notification healthcheck    cek /health lokal (HEALTHCHECK container)
package main

func main() { Execute() }
