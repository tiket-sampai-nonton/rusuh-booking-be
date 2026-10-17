module github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking

go 1.24

require github.com/tiket-sampai-nonton/rusuh-booking-be/pkg v0.0.0

// Dipakai saat build tanpa go.work (CI isolation build, Docker). Versi eksplisit agar tidak bentrok dengan go.work.
replace github.com/tiket-sampai-nonton/rusuh-booking-be/pkg v0.0.0 => ../../pkg
