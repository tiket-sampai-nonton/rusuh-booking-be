// Command queue runs the Queue Service. Satu binary, satu verb per deployment:
//
//	queue api            HTTP API (port APP_PORT)
//	queue worker         promotor admission, publisher Status Document, consumer booking.*
//	queue migrate up     terapkan migrasi schema queue
//	queue migrate down   rollback 1 migrasi (--steps N)
//	queue migrate status daftar migrasi
//	queue healthcheck    cek /health lokal (HEALTHCHECK container)
package main

func main() { Execute() }
