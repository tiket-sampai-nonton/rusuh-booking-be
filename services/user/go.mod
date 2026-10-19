module github.com/tiket-sampai-nonton/rusuh-booking-be/services/user

go 1.26

require (
	github.com/jackc/pgx/v5 v5.11.0
	github.com/spf13/cobra v1.10.2
	github.com/stretchr/testify v1.12.1
	github.com/tiket-sampai-nonton/rusuh-booking-be/pkg v0.0.0
	github.com/valkey-io/valkey-go v1.0.79
	go.uber.org/mock v0.6.0
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/mod v0.38.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/tools v0.48.0 // indirect
)

// mockgen dijalankan lewat `go tool mockgen` (go:generate di port/*), versinya dikunci go.mod ini.
tool go.uber.org/mock/mockgen

// Dipakai saat build tanpa go.work (CI isolation build, Docker). Versi eksplisit agar tidak bentrok dengan go.work.
replace github.com/tiket-sampai-nonton/rusuh-booking-be/pkg v0.0.0 => ../../pkg
