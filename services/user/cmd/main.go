// Command user runs the User Service. Satu binary, satu verb per deployment:
//
//	user api            HTTP API (port APP_PORT)
//	user migrate up     terapkan migrasi schema users
//	user migrate down   rollback 1 migrasi (--steps N)
//	user migrate status daftar migrasi
//	user healthcheck    cek /health lokal (HEALTHCHECK container)
package main

func main() { Execute() }
