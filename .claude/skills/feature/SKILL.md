---
name: feature
description: Workflow 4 fase untuk task/requirement/fitur baru — Planning & Analyst → Coding & Security → Automated Review & Testing → Auto-fix Loop (maks 3 putaran). Jalankan dari dalam root sebuah git repo. Dipanggil manual dengan /feature <deskripsi>, atau /feature status|pause|resume|abort.
argument-hint: "<deskripsi task/fitur> | status | pause | resume | abort"
disable-model-invocation: true
allowed-tools: Bash(${CLAUDE_SKILL_DIR}/scripts/*) Bash(git status*) Bash(git diff*) Bash(git log*) Bash(git rev-parse*) Bash(git branch*)
hooks:
  PostToolUse:
    - matcher: "Edit|Write|MultiEdit"
      hooks:
        - type: command
          command: '"$CLAUDE_PROJECT_DIR"/.claude/skills/feature/scripts/post-edit-check.sh'
  Stop:
    - hooks:
        - type: command
          command: '"$CLAUDE_PROJECT_DIR"/.claude/skills/feature/scripts/stop-gate.sh'
          timeout: 900
---

# /feature — Workflow pengembangan 4 fase

> Salinan untuk repo ini dari workflow yang dipakai pemilik sejak 2026-10-22 (lihat `docs/ai/README.md`).
> Disesuaikan: jalur hook relatif ke proyek, contoh repo, dan jenis data sensitif proyek ini.

Permintaan user: **$ARGUMENTS**

Scripts: `${CLAUDE_SKILL_DIR}/scripts/` → `state.sh`, `detect-stack.sh`, `quality-gate.sh`.
State workflow tersimpan di `<git-dir>/feature-workflow/` (di dalam `.git`, tidak pernah ter-commit).

Workflow ini mengorkestrasi skill & agent yang sudah terpasang:
- **superpowers** — brainstorming, writing-plans, test-driven-development, subagent-driven-development, systematic-debugging, receiving-code-review, verification-before-completion, finishing-a-development-branch
- **feature-dev** (plugin resmi Anthropic) — agent `feature-dev:code-explorer`, `feature-dev:code-architect`, `feature-dev:code-reviewer`
- agent lokal — `wf-security-reviewer`, `wf-test-runner`

Pakai tool `mcp__ccd_turn__update_plan` (atau TodoWrite) dengan item per fase supaya user bisa memantau progres.

---

## Sub-perintah

Jika `$ARGUMENTS` persis salah satu ini, jalankan lalu berhenti:
- `status` → `${CLAUDE_SKILL_DIR}/scripts/state.sh status` dan ringkas.
- `pause` → `state.sh phase paused` (Stop-hook gate nonaktif; user bisa ngobrol bebas).
- `resume` → baca `state.sh status` + plan, set phase ke fase terakhir yang belum selesai (`coding`/`review`), lanjutkan dari sana.
- `abort` → `state.sh reset`. Jangan menghapus branch atau perubahan kode; beri tahu user cara melakukannya sendiri.

Selain itu, `$ARGUMENTS` adalah deskripsi task. Jika kosong, tanya user deskripsinya dan berhenti.

---

## FASE 0 — Persiapan (wajib, singkat)

1. Pastikan berada di git repo: `git rev-parse --show-toplevel`. Jika bukan git repo, BERHENTI dan beri tahu user untuk menjalankan Claude dari dalam root repo (contoh `cd rusuh-booking-be && claude`).
2. `git status --porcelain`. Jika ada perubahan yang belum di-commit, tanyakan user: stash, commit dulu, atau lanjut di atasnya. Jangan stash/commit tanpa izin.
3. Jika sedang di branch utama (`main`/`master`/`develop`/`dev`/`staging`/`production`), usulkan branch baru `feature/<slug-pendek>` dan buat setelah user setuju (atau langsung jika user sudah meminta). Untuk isolasi penuh, boleh tawarkan `superpowers:using-git-worktrees`.
4. `${CLAUDE_SKILL_DIR}/scripts/state.sh init "<deskripsi singkat>"`.
5. `${CLAUDE_SKILL_DIR}/scripts/detect-stack.sh` → tampilkan STACK/TEST_CMD/LINT_CMD/TYPECHECK_CMD. Baca juga `CLAUDE.md`/`README` repo untuk perintah resmi. Jika deteksi salah atau kosong, tanyakan user lalu tulis override ke `$(state.sh dir)/commands.env` dengan format yang sama (`TEST_CMD='...'`).
6. `${CLAUDE_SKILL_DIR}/scripts/quality-gate.sh --baseline` → catat check mana yang SUDAH gagal sebelum ada perubahan (pre-existing, tidak akan memblok).

## FASE 1 — Planning & Analyst  (phase=planning)

1. **Klarifikasi requirement** — jika requirement ambigu, pakai skill `superpowers:brainstorming` (satu pertanyaan per giliran). Jika sudah jelas, lewati.
2. **Analisis codebase** — dispatch 2–3 agent `feature-dev:code-explorer` PARALEL dengan fokus berbeda (mis. fitur serupa yang sudah ada; alur data/arsitektur area terkait; konvensi tes, auth, dan error handling). Minta masing-masing mengembalikan 5–10 file kunci. Baca file-file kunci itu sendiri.
3. **Desain** — dispatch `feature-dev:code-architect` dengan ringkasan temuan. Untuk perubahan besar, minta 2 opsi (minimal vs. bersih) dan rekomendasinya.
4. **Rencana** — pakai skill `superpowers:writing-plans`, simpan ke `$(state.sh dir)/plan.md` (BUKAN di repo, kecuali user minta). Rencana wajib memuat:
   - Acceptance criteria yang bisa diuji (bernomor AC-1, AC-2, ...)
   - Daftar file yang dibuat/diubah
   - Langkah kecil per task, masing-masing dengan tes yang dibuat dulu
   - Risiko keamanan & data sensitif (NIK pembeli/peserta, email, nama, data pembayaran) yang relevan
   - Dampak ke DB/migration, konfigurasi, dan backward compatibility
5. **CHECKPOINT** — tampilkan ringkasan rencana + acceptance criteria. **BERHENTI dan minta persetujuan user.** Jangan menulis kode sebelum user setuju. (Stop-hook gate tidak aktif di fase ini.)

## FASE 2 — Coding & Security  (phase=coding)

Setelah user setuju: `state.sh phase coding`. Mulai titik ini Stop-hook gate aktif — Claude tidak bisa mengakhiri giliran sampai quality gate PASS atau batas putaran tercapai.

Jika di tengah Fase 2–4 Anda HARUS bertanya ke user (requirement ternyata kontradiktif, butuh kredensial/akses, keputusan desain besar), jalankan `state.sh phase paused` dulu, ajukan pertanyaan, lalu `state.sh phase <fase sebelumnya>` setelah dijawab.

1. **Implementasi dengan TDD** — skill `superpowers:test-driven-development`: tulis tes gagal → kode minimal → refactor, per task di plan.
   - Plan dengan banyak task independen → `superpowers:subagent-driven-development`.
   - Selain itu → `superpowers:executing-plans`.
   - Repo legacy tanpa infrastruktur tes: buat tes untuk logika yang bisa diisolasi; untuk sisanya tulis langkah verifikasi manual yang konkret di laporan akhir. Jangan berpura-pura ada tes.
2. Ikuti konvensi repo yang ditemukan di Fase 1. Jangan refactor di luar scope.
3. **Security review** — dispatch agent `wf-security-reviewer` dengan: base SHA (`state.sh get base_sha`), ringkasan fitur, dan daftar file yang berubah. Perbaiki semua temuan Critical/High (confidence ≥ 80) sekarang; catat Medium/Low untuk laporan akhir.

## FASE 3 — Automated Review & Testing  (phase=review)

`state.sh phase review`, lalu jalankan PARALEL dalam satu pesan:
- `wf-test-runner` — beri perintah persis `${CLAUDE_SKILL_DIR}/scripts/quality-gate.sh` + path plan untuk cek coverage acceptance criteria.
- `feature-dev:code-reviewer` — review diff terhadap base SHA: bug, logika, konvensi repo. Hanya temuan confidence ≥ 80.
- `wf-security-reviewer` — ulangi HANYA jika ada perubahan signifikan setelah review keamanan di Fase 2.

Gabungkan hasil menjadi satu **daftar temuan** (sumber, severity, file:line, perbaikan). Verdict PASS jika: gate PASS, tidak ada temuan reviewer ≥ 80, tidak ada temuan security Critical/High, dan setiap acceptance criteria punya bukti (tes, atau langkah verifikasi manual untuk repo tanpa tes).

## FASE 4 — Auto-fix Loop  (phase=fix, maks `max_attempts` = 3)

Jika verdict FAIL:
1. Evaluasi tiap temuan dengan skill `superpowers:receiving-code-review` — verifikasi dulu, jangan asal setuju; tolak temuan yang salah dengan alasan teknis.
2. Untuk tes/gate yang gagal pakai `superpowers:systematic-debugging` — cari akar masalah, bukan tambal gejala.
3. Perbaiki, lalu kembali ke Fase 3 (cukup review bagian yang berubah).
4. Hitung sendiri putaran Fase 3→4 (termasuk yang dipicu temuan reviewer, bukan hanya gate); maksimal `state.sh get max_attempts`. Stop-hook juga menghitung setiap kali gate gagal saat Claude mencoba berhenti. Lewat batas → phase=escalated: **BERHENTI**, laporkan ke user apa yang masih gagal, apa yang sudah dicoba, dan opsi lanjutan. Jangan teruskan loop sendiri.

**Dilarang** untuk membuat gate hijau: menghapus/men-skip tes, melemahkan assertion, menambah `@phpstan-ignore`/`eslint-disable`/`//nolint`/`# noqa`, mengubah konfigurasi lint/tes, atau mengedit `commands.env`/state tanpa izin user.

## Penutup  (phase=done)

1. Skill `superpowers:verification-before-completion`: jalankan `quality-gate.sh` sekali lagi dan tunjukkan output-nya sebagai bukti.
2. `state.sh phase done`.
3. Laporan akhir ke user (ringkas):
   - Ringkasan perubahan + daftar file
   - Tabel acceptance criteria → bukti (tes / verifikasi manual)
   - Hasil gate (termasuk check pre-existing yang memang sudah gagal sejak awal)
   - Temuan security Medium/Low yang tersisa & rekomendasi
   - Jumlah putaran auto-fix yang terpakai
4. Tawarkan langkah berikutnya lewat skill `superpowers:finishing-a-development-branch` (commit / PR / biarkan). **Jangan commit, push, atau membuat PR tanpa persetujuan eksplisit user.**
