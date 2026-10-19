// Command payment runs the Payment Service. Satu binary, satu verb per deployment:
//
//	payment api            HTTP API (port APP_PORT)
//	payment worker         rekonsiliasi attempt UNKNOWN, outbox relay, consumer booking.review
//	payment migrate up     terapkan migrasi schema payment
//	payment migrate down   rollback 1 migrasi (--steps N)
//	payment migrate status daftar migrasi
//	payment healthcheck    cek /health lokal (HEALTHCHECK container)
package main

func main() { Execute() }
