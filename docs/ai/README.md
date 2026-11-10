# Penggunaan AI di rusuh-booking-be

Dokumen ini menjelaskan alat AI yang dipakai, konfigurasi yang ada di repo, dan batasannya. Riwayat pemakaian per sesi ada di
[`docs/ai-log/README.md`](../ai-log/README.md); keputusan yang lahir dari diskusi dengan AI ada di [`docs/evidence/`](../evidence/).
Isi dokumen ini hanya yang benar-benar dipakai.

## Alat

| Alat | Dipakai untuk | Bukti |
|---|---|---|
| Claude (chat) | Pemecahan PRD menjadi set dokumen dan skeleton repo awal (2026-10-17) | baris pertama `docs/ai-log` |
| Claude Code (model Opus dan Sonnet) | Analisis dan audit dokumen, spec per service (`specs/`, format requirements/design/tasks), implementasi, review, deployment, uji E2E dan uji beban | 35 baris "Claude Code" di `docs/ai-log` |
| Kiro CLI | Implementasi service oleh anggota tim (workflow `ai-tech-lead` dan `software-engineer`), mengikuti steering proyek | 21 baris "Kiro CLI" di `docs/ai-log`, `.kiro/` |

## Konfigurasi di repo

```text
CLAUDE.md                          aturan Claude Code untuk repo ini; mengimpor .kiro/steering/*.md
.kiro/steering/*.md                aturan proyek bersama (produk, tech, struktur, prinsip kode, invarian, keamanan, testing)
.kiro/skills/implement-service/    panduan Kiro mengimplementasikan satu service
.claude/settings.json              plugin yang dibutuhkan /feature: superpowers dan feature-dev
.claude/skills/feature/            workflow /feature (SKILL.md + skrip quality gate dan hook)
.claude/agents/                    sub-agen wf-security-reviewer dan wf-test-runner yang dipanggil /feature
```

`CLAUDE.md` tidak menyalin aturan Kiro, melainkan mengimpornya, sehingga Claude Code dan Kiro membaca aturan yang sama.

### Workflow `/feature`

Dipakai untuk task implementasi sejak 2026-10-22 (baris "via /feature" di `docs/ai-log`). Satu perintah menjalankan empat fase:

| Fase | Isi | Pengaman |
|---|---|---|
| 1. Planning & Analyst | Eksplorasi kode, desain, rencana dengan acceptance criteria | Berhenti dan menunggu persetujuan pemilik sebelum menulis kode |
| 2. Coding & Security | Implementasi TDD, lalu review keamanan oleh `wf-security-reviewer` (read-only) | Temuan Critical/High wajib diperbaiki |
| 3. Review & Testing | Quality gate oleh `wf-test-runner` + code review | Gate: sintaks, pola secret, test, lint, typecheck |
| 4. Auto-fix | Perbaikan temuan, kembali ke fase 3 | Maksimal 3 putaran, lalu eskalasi ke pemilik |

Hook yang aktif selama workflow berjalan:
- `PostToolUse`: setiap file yang diedit langsung dicek sintaksnya (`gofmt`, `node --check`, `jq`, dan seterusnya).
- `Stop`: Claude tidak bisa menyatakan selesai sebelum quality gate lulus.

Workflow melarang membuat gate hijau dengan cara menghapus atau men-skip tes, melemahkan assertion, menambah `//nolint` dan
sejenisnya, atau mengubah konfigurasi lint/tes. Workflow ini membutuhkan plugin Claude Code `superpowers` dan `feature-dev`;
keduanya dideklarasikan di `.claude/settings.json`, sehingga Claude Code menawarkan pemasangannya saat repo dibuka.

Asal: workflow ini awalnya dipasang di lingkungan pemilik untuk semua repo kerjanya. Salinan di repo ini disesuaikan: jalur
hook relatif ke proyek, contoh repo, dan jenis data sensitif proyek ini. State workflow disimpan di `.git/feature-workflow/`
dan tidak pernah ter-commit.

## Batasan yang dipegang

- Tidak ada commit, push, merge, atau PR tanpa persetujuan eksplisit pemilik.
- Nilai secret dan isi `.env` tidak ditampilkan atau disalin; data uji memakai NIK fiktif dan email `example.test`.
- Deploy dev dan prod lewat persetujuan reviewer di GitHub Environments.
- Kode milik anggota tim lain (lihat `CODEOWNERS`) disentuh terakhir dan dengan koordinasi.
- Setiap sesi yang mengubah sesuatu dicatat di `docs/ai-log`; keputusan dari diskusi dicatat di `docs/evidence`.
- Temuan yang sengaja tidak diperbaiki (mis. pengecualian SonarCloud) ditulis bersama alasannya.

## Yang tidak ada di repo

- Setelan pribadi (`.claude/settings.local.json`, `CLAUDE.local.md`) dan setelan global pengguna: diabaikan git.
- Catatan memori pribadi Claude Code pemilik. Fakta proyek dari catatan itu (perintah, gotcha lokal, kepemilikan kode)
  sudah dipindahkan ke `CLAUDE.md`.
- Konfigurasi server MCP khusus proyek: tidak dipakai, jadi `.mcp.json` tidak dibuat.
- Plugin lain yang terpasang di lingkungan pribadi pengguna: tidak dibutuhkan repo, jadi tidak dideklarasikan.
- Definisi workflow Kiro CLI `ai-tech-lead` dan `software-engineer`: ada di lingkungan pengguna Kiro, bukan di repo. Yang ada di
  repo hanya steering dan skill `implement-service`.
