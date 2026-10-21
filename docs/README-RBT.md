# Set Dokumen — Rusuh Booking Tiket

| Field | Value |
|-------|-------|
| **Document ID** | README-RBT |
| **Version** | 1.6 |
| **Date** | 2026-10-21 |
| **Source** | PRD v4.6 Final Canonical (16 Oktober 2026), dipecah tanpa mengubah requirement, alur customer, kontrak API publik, maupun jaminan |

**Struktur proyek kode wajib mengikuti standar proyek organisasi** (hexagon per domain dengan `port/inbound`/`port/outbound`, `domain/{entity,valueobject,dto,mapper}`, `application/usecase`, `infrastructure`, `delivery`, `_mock`). Detail di SDD §4.1, Engineering Handbook §0, dan ADR-016; struktur monorepo Lampiran F v4.6 tidak dipakai lagi.

> **Pihak dan penanggung jawab.** Penyusun semua dokumen: **Tim PT. Tiket Sampai Nonton Sentosa** (peserta tender). Approver (pemilik bisnis): **Pak Dinda (CEO), Pak Jabon (CTO), Pak Rahman (CFO), PT. Tiga Sekawan Angin Ribut**, sesuai PDF requirement. **Koreksi dari v4.6:** header v4.6 menulis PT. Tiket Sampai Nonton Sentosa sebagai klien; menurut PDF requirement §12 perusahaan itu adalah peserta tender (vendor), dan klien adalah PT. Tiga Sekawan Angin Ribut.

PRD v4.6 menggabungkan PRD, SRS, HLD, LLD, keputusan arsitektur, rencana pengujian, dan panduan engineering dalam satu file. Set ini memecahnya menjadi dokumen terpisah dengan acuan standar masing-masing. Semua isi teknis yang panjang (DDL, Lua, diagram, kontrak API, konfigurasi) disalin apa adanya; bagian yang ditulis ulang adalah PRD (level produk), SRS (requirement "shall"), stakeholder/concern/viewpoint HLD, dan ADR.

---

## 1. Daftar Dokumen

| # | File | Document ID | Acuan | Isi |
|---|------|-------------|-------|-----|
| 00 | `README-RBT.md` | README-RBT | ISO/IEC/IEEE 15289 (set dokumen) | Peta dokumen, konvensi ID, tabel konversi v4.6, riwayat |
| 01 | `analisa/01-PRD-RBT-001-v5.0.md` | PRD-RBT-001 v5.2 | Praktik industri; konsep BRS/StRS ISO/IEC/IEEE 29148, ISO 9241-210, ISO/IEC 25010 | Masalah, goals, metrik, persona, user story + acceptance criteria, business rules, asumsi, rilis, risiko, wireframe |
| 02 | `analisa/02-SRS-RBT-001.md` | SRS-RBT-001 v1.1 | ISO/IEC/IEEE 29148:2018, ISO/IEC 25010:2023 | Requirement "shall" per capability, interface, quality, data, constraint, RTM |
| 03 | `analisa/03-HLD-AD-RBT-001.md` | AD-RBT-001 v1.3 | ISO/IEC/IEEE 42010:2022 | Stakeholder, concern, viewpoint, 8 view, correspondence, risiko teknis |
| 04 | `analisa/04-SDD-RBT-001.md` | SDD-RBT-001 v1.3 | IEEE 1016-2009; notasi UML (ISO/IEC 19505) | Struktur modul, kontrak, DDL, locking, state machine, algoritme, konfigurasi |
| 05 | `analisa/05-ADR-RBT.md` | ADR-RBT v1.2 | Konsep decision & rationale ISO/IEC/IEEE 42010; format Nygard/MADR | ADR-001 … ADR-022 + matriks trade-off |
| 06 | `analisa/06-TP-RBT-001.md` | TP-RBT-001 v1.2 | ISO/IEC/IEEE 29119-3:2021, 29119-4 | Strategi, release gate, katalog test, harness, evidence, technical defense |
| 07 | `analisa/07-Engineering-Handbook-RBT.md` | EHB-RBT v1.3 | Praktik engineering | Repository, pipeline, Docker Compose, Git/CI/security/logging, aturan AI/Kiro |
| 04a–04e | `analisa/04a-SDD-QUEUE-001.md` … `analisa/04e-SDD-USER-001.md` | SDD per service | IEEE 1016-2009 | Ringkasan desain per service (Queue, Booking, Payment, Notification, User); bila berbeda, `04-SDD-RBT-001` yang berlaku |
| 04f–04g | `analisa/04f-E2E-VERIFICATION.md`, `analisa/04g-E2E-FLOW-DIAGRAMS-MERMAID.md` | — | — | Matriks verifikasi end-to-end dan diagram alur |
| 04 (indeks) | `analisa/04-SDD-INDEX.md` | — | — | Indeks SDD dan ringkasan komunikasi antar service |
| 08–08c | `analisa/08-INTEGRATION-INDEX.md`, `08a-frontend-integration.md`, `08b-payment-gateway-integration.md`, `08c-inter-service-communication.md` | — | — | Panduan integrasi FE, Midtrans, dan antar service |
| 09 | `analisa/09-Panduan-Lokal-dan-Implementasi.md` | — | — | Persiapan lokal, urutan coding, dan peta dokumen untuk developer |

### Struktur folder `docs/` (v1.6)

```
docs/
├── README-RBT.md     indeks ini
├── analisa/          semua dokumen planning dalam satu folder datar (PRD, SRS, HLD, SDD, ADR, TP, handbook, integrasi)
├── contracts/        kontrak normatif: openapi/*.yaml dan events/*.schema.json
├── ai-log/           catatan setiap penggunaan AI (wajib, tidak boleh dihapus — SRS-C-06)
└── evidence/         bukti uji dan catatan diskusi yang mengubah dokumen planning
```

Spesifikasi implementasi (requirements → design → tasks) ada di `specs/` di root repo.

---

## 2. Alur Dokumen dan Traceability

```
PRD (why/what) ──▶ SRS (shall) ──▶ HLD (arsitektur) ──▶ SDD (desain rinci) ──▶ Kode
     │                 │                 │      ▲               │
     │                 │                 └─ ADR ┘               │
     │                 └────────────▶ Test Plan / Test Case ◀───┘
     └──────────── acceptance (UAT) ─────────▶
```

Rantai ID: `FR/NFR/BR/GR/SCP (sumber) → US (PRD) → SRS-F/Q/IF/C/D (SRS) → CMP (HLD) → section SDD → TC (Test Plan)`.

| Traceability | Lokasi |
|------|--------|
| Source requirement → user story | PRD Appendix A |
| Source requirement → SRS | SRS §5 |
| SRS → komponen | HLD §6.2 |
| SRS → desain | SDD §11 |
| SRS → test case | SRS §3.2 (kolom Test), Test Plan §6 |

---

## 3. Konvensi ID

| Prefix | Item | Didefinisikan di |
|--------|------|------------------|
| `FR-`, `NFR-`, `BR-01..08`, `SCP-`, `GR-` | Requirement sumber (PDF dan grooming) | SRS Appendix A |
| `G-`, `M-`, `US-`, `FEAT-` | Goal, metrik, user story, fitur | PRD |
| `BR-09..20` | Business rule turunan | PRD §8 |
| `AS-`, `OI-`, `CON-`, `DEP-`, `RISK-` | Asumsi, open item, constraint, dependency, risiko | PRD §10–§13 |
| `SRS-F-<subbagian>-<urut>` | Requirement fungsional (subbagian = SRS §3.2.x) | SRS §3.2 |
| `SRS-Q-<KARAKTERISTIK>-` | Quality requirement (ISO 25010) | SRS §3.3 |
| `SRS-IF-`, `SRS-D-`, `SRS-C-` | Interface, data, constraint | SRS §3.1, §3.4–§3.6 |
| `CN-`, `CR-`, `QS-`, `CMP-`, `DS-`, `TR-` | Concern, correspondence rule, quality scenario, komponen, data store, risiko teknis | HLD |
| `ADR-` | Keputusan arsitektur | ADR-RBT |
| `TC-` | Test case | Test Plan Annex A |
| Kode capability (`GST-01`, `QUE-01`, …) | Kode capability v4.6, dipertahankan untuk kode dan test | SRS §3.2 (judul subbagian) |

**Kutipan verbatim.** Bagian bertanda *Sumber: PRD v4.6 …* disalin apa adanya. Nomor heading lama dihapus, dan rujukan di dalamnya diberi penanda `[v4.6 → dokumen §x]` yang menunjukkan lokasi barunya.

---

## 4. Tabel Konversi PRD v4.6 → Set Dokumen

| PRD v4.6 | Judul | Lokasi baru |
|---|---|---|
| — | Product Requirement Document (PRD) — Rusuh Booking Tiket | PRD (header dan Document Conventions) |
| — | Ringkasan Perubahan v4.5 → v4.6 | README §5 |
| — | Ringkasan Perubahan v4.4 → v4.5 (riwayat) | README §5 |
| — | Ringkasan Perubahan v4.3 → v4.4 (riwayat) | README §5 |
| — | Ringkasan Perubahan v4.2 → v4.3 (riwayat) | README §5 |
| — | Ringkasan Perubahan v4.1 → v4.2 (riwayat) | README §5 |
| §1 | Document Governance | PRD Document Conventions |
| §1.1 | Tujuan Dokumen | README §1 |
| §1.2 | Precedence | PRD Document Conventions |
| §1.3 | Status | PRD Document Conventions |
| §1.4 | Keputusan Kunci | ADR Index |
| §2 | Executive Summary | PRD §1.3 |
| §2.1 | Goals | PRD §2.1 |
| §2.2 | Non-Goals | PRD §2.2 |
| §2.3 | Success Metrics | PRD §3 |
| §3 | Scope, Actors, and Terminology | PRD §5 |
| §3.1 | In Scope | PRD §5.1 |
| §3.2 | Actors | HLD §4.1 |
| §3.3 | Key Terms | SRS §1.4 |
| §4 | Source Requirements | SRS Appendix A |
| §4.1 | Functional Requirements (PDF 2026 H2) | SRS Appendix A |
| §4.2 | Non-Functional Requirements | SRS Appendix A |
| §4.3 | Business Rules | SRS Appendix A |
| §4.4 | Grooming Requirements (14 Oktober 2026) | SRS Appendix A |
| §4.5 | Asumsi Requirement (PDF §11) | SRS Appendix A |
| §5 | Product Capabilities and Acceptance Criteria | SRS §3.2 |
| §5.1 | GST-01 Guest Session | SRS §3.2.1 |
| §5.2 | EVT-01 Event List | SRS §3.2.2 |
| §5.3 | EVT-02 Event Detail | SRS §3.2.3 |
| §5.4 | QUE-01 Queue Join and Direct Admission | SRS §3.2.4 |
| §5.5 | QUE-02 Queue Status and Honest Indicator | SRS §3.2.5 |
| §5.6 | QUE-03 Admission and Claim | SRS §3.2.6 |
| §5.7 | SES-01 Selection Session and TTL | SRS §3.2.7 |
| §5.8 | SES-02 Selection Timeout | SRS §3.2.8 |
| §5.9 | ADM-01 Reservation Guarantee ("Jangan PHP") | SRS §3.2.9 |
| §5.10 | QUE-04 Sold-Out Closure | SRS §3.2.10 |
| §5.11 | BKG-01 NIK, Quota, and Attendee Identity | SRS §3.2.11 |
| §5.12 | BKG-02 Atomic Hold | SRS §3.2.12 |
| §5.13 | BKG-03 Cancel (SHOULD) | SRS §3.2.13 |
| §5.14 | PAY-01 Payment Creation and Method Selection | SRS §3.2.14 |
| §5.15 | PAY-02 Webhook | SRS §3.2.15 |
| §5.16 | PAY-03 Timely Success | SRS §3.2.16 |
| §5.17 | PAY-04 Expiry with Reconciliation | SRS §3.2.17 |
| §5.18 | PAY-05 Reusable Payment Page (Web + Mobile WebView) | SRS §3.2.18 |
| §5.19 | PAY-06 Gateway Degradation Protection (SHOULD) | SRS §3.2.19 |
| §5.20 | TKT-01 Ticket | SRS §3.2.20 |
| §5.21 | MSG-01 Notification | SRS §3.2.21 |
| §5.22 | API-01 Shared API (Web + Mobile) | SRS §3.2.22 |
| §5.23 | BKG-04 Ubah Pesanan Sebelum Bayar | SRS §3.2.23 |
| §5.24 | SES-03 Refresh, Koneksi Putus, dan Tombol Back | SRS §3.2.24 |
| §5.25 | QUE-05 Near-Real-Time dan Notifikasi Giliran | SRS §3.2.25 |
| §5.26 | CFG-01 Configuration | SRS §3.2.26 |
| §6 | User Journey and Wireframes | PRD §6 |
| §6.1 | Main Journey | PRD §6.2 |
| §6.2 | Home Page | PRD Appendix E |
| §6.3 | Data Pembeli (sebelum antre) | PRD Appendix E |
| §6.4 | Waiting Room | PRD Appendix E |
| §6.5 | Detail Ticket | PRD Appendix E |
| §6.6 | Payment Page (web dan WebView) | PRD Appendix E |
| §6.7 | Result | PRD Appendix E |
| §6.8 | UX Rules | PRD Appendix E |
| §6.9 | Pesanan Aktif, Koneksi Terputus, dan Dialog Back | PRD Appendix E |
| §7 | Architecture Decisions and Technology Stack | HLD §4.2 |
| §7.1 | Architecture Style | HLD §4.2.1 |
| §7.2 | Technology Stack (sesuai tech radar) | HLD §4.8 |
| §7.3 | Alasan Pemilihan Tech Stack (Best Practice) | ADR-015 |
| §7.4 | Prinsip Desain Kode (SOLID dalam Go) | SDD §4.5 |
| §8 | HLD: System and Access Architecture | HLD §4.2 |
| §8.1 | Component HLD | HLD §4.2.1 |
| §8.2 | Access Path | HLD §4.6 |
| §8.3 | Waiting Room at Scale (CDN-served) | HLD §4.7.1 |
| §8.4 | Edge Responsibilities | HLD §4.6 |
| §8.5 | Deployment HLD (AWS PoC) | HLD §4.5 |
| §9 | LLD: Services and Request Processing | HLD §4.2 |
| §9.1 | Service Catalogue | HLD §4.2.1 |
| §9.2 | Internal APIs (sinkron) | HLD §4.2.1 |
| §9.3 | Domain Events (asinkron) | HLD §4.2.1 |
| §9.4 | Request Pipeline | HLD §4.2.1 |
| §9.5 | Lock Order (Booking Service) | SDD §4.7 |
| §9.6 | Strong and Eventual Consistency | HLD §4.3 |
| §9.7 | Teorema CAP dan PACELC per Komponen | HLD §4.3 |
| §9.8 | Outbox dan Idempotensi Consumer | SDD §4.5.2 |
| §10 | Sequence Diagrams | HLD §4.4 |
| §10.1 | Join and Direct Admission | HLD §4.4 |
| §10.2 | Waiting, Promotion, and Claim | HLD §4.4 |
| §10.3 | Selection Session and Reservation Guarantee | HLD §4.4 |
| §10.4 | Atomic Hold | HLD §4.4 |
| §10.5 | Payment Creation (web) | HLD §4.4 |
| §10.6 | Mobile Payment via WebView | HLD §4.4 |
| §10.7 | Settlement | HLD §4.4 |
| §10.8 | Payment Expiry with Reconciliation | HLD §4.4 |
| §10.9 | Sold-Out Closure and Honest Indicator | HLD §4.4 |
| §10.10 | Unclaimed Turn and Selection Timeout | HLD §4.4 |
| §10.11 | Cancel by Customer (SHOULD) | HLD §4.4 |
| §10.12 | Autoscaling Continuity | HLD §4.4 |
| §10.13 | Edit Order Before Payment | HLD §4.4 |
| §10.14 | Recovery After Connection Loss | HLD §4.4 |
| §10.15 | Turn Notification | HLD §4.4 |
| §11 | State Machines | SDD §4.8 |
| §11.1 | Queue Entry | SDD §4.8 |
| §11.2 | Admission (Allocation + Selection Session) | SDD §4.8 |
| §11.3 | Booking | SDD §4.8 |
| §11.4 | Payment Attempt | SDD §4.8 |
| §11.5 | Legal Transition Rules | SDD §4.8 |
| §12 | API Contract and Edge Configuration | SRS §3.1.4 |
| §12.1 | Public Endpoints | SRS §3.1.4 |
| §12.2 | Status Document | SRS §3.1.4 |
| §12.3 | Edge Configuration Summary | HLD §4.6 |
| §12.4 | Error Envelope | SRS §3.1.4 |
| §13 | Data Model | HLD §4.3 |
| §13.1 | Schema per Service | HLD §4.3 |
| §13.2 | ERD Booking Service | HLD §4.3 |
| §13.3 | Mandatory Constraints | HLD §4.3 |
| §13.4 | Valkey: Peran dan Model Key | HLD §4.3 |
| §14 | Security and Privacy | HLD §4.6 |
| §14.1 | Data Pribadi (GR-15) | SRS §3.4 |
| §14.2 | Token | HLD §4.6 |
| §14.3 | Kontrol Lain | HLD §4.6 |
| §14.4 | Threats | HLD §4.6 |
| §14.5 | Anti-Calo Controls (ringkasan) | HLD §4.6 |
| §15 | Resilience, Messaging, and Circuit Breaker | HLD §4.7 |
| §15.1 | Dependency Policy | HLD §4.7.1 |
| §15.2 | Retry Ownership | HLD §4.7.1 |
| §16 | Observability and Reconciliation | HLD §4.7 |
| §16.1 | Observability Stack | HLD §4.7.1 |
| §16.2 | Metrics (NFR-09) | SDD §7 |
| §16.3 | Reconciliation | HLD §4.7.1 |
| §17 | Capacity and Cost Model (Proposal Tender) | HLD §4.7 |
| §17.1 | Tier Kapasitas | HLD §4.7.1 |
| §17.2 | Model Beban | HLD §4.7.1 |
| §17.3 | Arsitektur Referensi Production per Tier | HLD §4.5 |
| §17.4 | Estimasi Biaya Infrastruktur (kasar, ±20–30%) | HLD §4.7.1 |
| §17.5 | Biaya PoC (kredit AWS US$200) | HLD §4.7.1 |
| §17.6 | Komponen Harga Jasa | HLD §4.7.1 |
| §17.7 | Bukti yang Disiapkan PoC | TestPlan §4.2 |
| §18 | Deployment, CI/CD, and Local Assessor | HLD §4.5 |
| §18.1 | Environment Matrix | HLD §4.5 |
| §18.2 | AWS PoC | HLD §4.5 |
| §18.3 | Terraform Scope | HLD §4.5 |
| §18.4 | Pipelines (GitHub Actions) | Handbook §2 |
| §18.5 | Local Commands | Handbook §3.0 |
| §19 | Testing and Validation Strategy | TestPlan §3 |
| §19.1 | Test Pyramid dan Tools | TestPlan §3.1 |
| §19.2 | Test Catalogue (ringkas) | TestPlan Annex A |
| §19.3 | Release Gates | TestPlan §3.4 |
| §19.4 | Mermaid Validation Gate | TestPlan §3.4 |
| §20 | Assumption Register and Open Items | PRD §10 |
| §20.1 | Assumption Register | PRD §10.2 |
| §20.2 | Open Items yang Masih Perlu Informasi | PRD §13 |
| §20.3 | Input untuk Proposal Harga | PRD §13 (OI-10) |
| §21 | Delivery Plan, Ownership, and Repository | PRD §11 |
| §21.1 | Ownership | PRD Appendix F |
| §21.2 | Two-Sprint Plan | PRD Appendix F |
| §21.3 | Repository Management | Handbook §1 |
| §22 | Risks | PRD §12 |
| §23 | Requirement Traceability | SRS §5 |
| §24 | Assessment Evidence and Technical Defense | TestPlan Annex C |
| §24.1 | Evidence Package | TestPlan §4.1 |
| §24.2 | Technical Defense | TestPlan Annex C |
| §24.3 | Claims to Avoid | TestPlan Annex C |
| §25 | Final Acceptance Statement and Changelog | README §5 |
| §25.1 | Sources | README §5 |
| §25.2 | Changelog | README §5 |
| Lampiran A | Kontrak API | SDD §4.6.1 |
| Lampiran A.1 | Pembagian Tanggung Jawab Backend vs Client | SRS §3.1.4 |
| Lampiran A.2 | Queue Service | SDD §4.6.1 |
| Lampiran A.3 | Booking Service | SDD §4.6.1 |
| Lampiran A.4 | Payment Service | SDD §4.6.1 |
| Lampiran A.5 | Internal API | SDD §4.6.1 |
| Lampiran A.6 | Error Codes | SRS §3.1.4 |
| Lampiran A.7 | Notification Service (push) | SDD §4.6.1 |
| Lampiran B | PostgreSQL DDL per Service | SDD §4.4 |
| Lampiran B.1 | Catatan Desain | SDD §4.4 |
| Lampiran B.2 | Isolation dan Batas Transaksi | SDD §4.4 |
| Lampiran C | Locking dan Race Resolution (Booking Service) | SDD §4.7.1 |
| Lampiran C.1 | Urutan Lock Global | SDD §4.7.1 |
| Lampiran C.2 | Reservasi Alokasi (dipanggil Queue) | SDD §4.7.1 |
| Lampiran C.3 | Hold | SDD §4.7.1 |
| Lampiran C.4 | Settlement vs Expiry vs Cancel | SDD §4.7.1 |
| Lampiran C.5 | Admission Tidak Diklaim / Sesi Habis / Keluar Tanpa Membeli | SDD §4.7.1 |
| Lampiran C.6 | Cek NIK Pemegang (saat diketik) | SDD §4.7.1 |
| Lampiran C.7 | Bukti Jaminan (uji desain v4.4) | SDD §4.7.1 |
| Lampiran C.8 | Ubah Pesanan | SDD §4.7.1 |
| Lampiran C.9 | Kunci Pesanan ("Bayar") | SDD §4.7.1 |
| Lampiran C.10 | Sweeper TTL | SDD §4.7.1 |
| Lampiran D | Diagram Detail | SDD §4.7.2 |
| Lampiran D.1 | Promotor: Batch Admission dan Pelepasan Alokasi Yatim | SDD §4.7.2 |
| Lampiran D.2 | Webhook: Duplikat, Out-of-Order, dan Signature | SDD §4.7.2 |
| Lampiran D.3 | Transactional Outbox, Consumer Idempoten, Retry, DLQ | SDD §4.7.2 |
| Lampiran D.4 | Circuit Breaker | SDD §4.7.2 |
| Lampiran E | Messaging dan Resilience Baseline | SDD §4.6.2 |
| Lampiran E.1 | Event Envelope | SDD §4.6.2 |
| Lampiran E.2 | Katalog Event dan Routing | SDD §4.6.2 |
| Lampiran E.3 | Topologi Retry per Consumer Queue | SDD §4.6.2 |
| Lampiran E.4 | Retry Ownership | SDD §4.6.2 |
| Lampiran E.5 | Konfigurasi Circuit Breaker Awal (PROPOSED) | SDD §4.6.2 |
| Lampiran F | Struktur Monorepo | SDD §4.1 |
| Lampiran F.1 | Aturan Dependency | SDD §4.3 |
| Lampiran F.2 | Jalur Evolusi | SDD §4.1.5 |
| Lampiran G | Git, CI/CD, Security, Logging, dan Incident | Handbook §4 |
| Lampiran G.1 | Git dan Release | Handbook §4 |
| Lampiran G.2 | CI Quality Gates | Handbook §4 |
| Lampiran G.3 | Security (OWASP) | Handbook §4 |
| Lampiran G.4 | Logging, Metrics, Tracing | Handbook §4 |
| Lampiran G.5 | Template Post-Mortem | Handbook §4 |
| Lampiran H | Docker Compose Clean-Room | Handbook §3.1 |
| Lampiran H.1 | Clean-room commands | Handbook §3.1 |
| Lampiran I | Test Catalogue dan Evidence | TestPlan Annex A |
| Lampiran I.1 | Test IDs | TestPlan Annex A |
| Lampiran I.2 | Release Blockers | TestPlan §3.4 |
| Lampiran I.3 | Evidence Format | TestPlan §3.10 |
| Lampiran J | Configuration Reference | SDD §6 |
| Lampiran J.1 | Per Event — Booking (`booking.events`) | SDD §6 |
| Lampiran J.2 | Per Event — Queue (`queue.event_settings`) | SDD §6 |
| Lampiran J.3 | Per Service — env (perubahan lewat deploy) | SDD §6 |
| Lampiran J.4 | Klien (cadangan saja) | SDD §6 |
| Lampiran J.5 | Validasi Silang (gagal sejak awal) | SDD §6 |
| Lampiran K | Aturan Delivery dengan AI/Kiro | Handbook §5 |
| Lampiran L | Architecture Trade-Off Matrix | ADR Appendix A |
| Lampiran L.1 | Gaya Arsitektur | ADR Appendix A |
| Lampiran L.2 | Keputusan Desain Kunci | ADR Appendix A |
| Lampiran L.3 | Evaluasi Teknologi | ADR Appendix A |
| Lampiran M | Algoritme Antrean dan Validasi NIK | SDD §4.9 |
| Lampiran M.1 | Script Lua Join dan Akhir Entry | SDD §4.9 |
| Lampiran M.2 | Script Lua Admit | SDD §4.9 |
| Lampiran M.3 | Publisher Status Document (pseudocode Go) | SDD §4.9 |
| Lampiran M.4 | Perhitungan Posisi di Klien | SDD §4.9 |
| Lampiran M.5 | Validasi Struktur NIK (Go) | SDD §4.9 |
| Lampiran M.6 | Batas Zona Peluang (Go) | SDD §4.9 |
| Lampiran N | Payment Page Reusable dan Integrasi Midtrans | SDD §4.6.3 |
| Lampiran N.1 | Pemetaan Metode Pembayaran | SDD §4.6.3 |
| Lampiran N.2 | Aturan Transaksi | SDD §4.6.3 |
| Lampiran N.3 | Pemetaan Status | SDD §4.6.3 |
| Lampiran N.4 | Resolve (dipanggil Booking) | SDD §4.6.3 |
| Lampiran N.5 | Alur Web | SDD §4.6.3 |
| Lampiran N.6 | Alur Mobile (WebView) | SDD §4.6.3 |
| Lampiran N.7 | Aturan WebView | SDD §4.6.3 |
| Lampiran N.8 | Skenario Uji Sandbox | SDD §4.6.3 |
| Lampiran O | Konfigurasi Edge dan Security Group | HLD Appendix B |
| Lampiran O.1 | CloudFront Behaviors | HLD Appendix B |
| Lampiran O.2 | AWS WAF (web ACL pada CloudFront) | HLD Appendix B |
| Lampiran O.3 | ALB | HLD Appendix B |
| Lampiran O.4 | Security Group Matrix | HLD Appendix B |
| Lampiran O.5 | Middleware Go (`pkg/platform/http`) | HLD Appendix B |
| Lampiran O.6 | Cuplikan Terraform | HLD Appendix B |
| Lampiran P | Harness Acuan Jaminan (PostgreSQL) | TestPlan Annex B |
| — | Final Canonical Release Statement | README §5 |

---

## 5. Riwayat dan Sumber PRD v4.6

### Header PRD v4.6 (arsip)

*Sumber: PRD v4.6 Lampiran Product Requirement Document (PRD) — Rusuh Booking Tiket*
**Dokumen Spesifikasi, Arsitektur, dan Dasar Proposal Tender**  
**Versi:** 4.6 Final Canonical (Requirement 2026 H2 + Hasil Grooming 14 Oktober 2026 + Keputusan Tim 15–16 Oktober 2026)  
**Format:** Markdown (.md)  
**Tanggal:** 16 Oktober 2026  
**Status:** Canonical design baseline. Implementasi dan seluruh evidence tetap `NOT_VERIFIED` sampai diverifikasi aktual.

| Atribut | Nilai |
|---|---|
| Klien (skenario tender) | PT. Tiga Sekawan Angin Ribut (Pak Dinda, CEO; Pak Jabon, CTO; Pak Rahman, CFO) |
| Vendor (peserta tender) | PT. Tiket Sampai Nonton Sentosa |
| Tim | 2 Backend Engineer (BE-1, BE-2), 2 Frontend Web (WEB-1, WEB-2), 1 Frontend Mobile (MOB-1) |
| Target | 2 sprint / 4 minggu / 20 hari kerja |
| Sumber requirement | PDF *Evaluasi Expertise Solution Development 2026 H2* + notulen grooming 14 Oktober 2026 |
| Prinsip utama | **"Jangan PHP"** — customer tidak pernah diberi harapan palsu |
| Arsitektur | 4 service sesuai requirement: **Queue, Booking, Payment, Notification** (Go, monorepo, hexagonal + SOLID) |
| Data | PostgreSQL 16 (otoritas stok dan uang) + **Valkey 8.1** (antrean, rate limit, cache) |
| Skala target proposal | 100 ribu / 300 ribu / 1 juta pengantre bersamaan (rujukan histori 300 ribu, Coldplay 1 juta) |
| Edge | Amazon CloudFront + AWS WAF + Application Load Balancer |
| Cloud PoC | AWS EC2 Auto Scaling Group + host stateful privat, Terraform |
| Local assessor | Docker Compose, tanpa AWS dan tanpa private registry |
| Payment | Midtrans Sandbox (aktual); payment page web reusable di mobile via WebView |
| Akun customer | Guest checkout (tanpa login), email wajib |
| Repository | GitHub public (akun pribadi) |

> PRD v4.6 menggantikan v4.5 dan menjadi baseline final untuk implementasi. v4.6 tidak mengubah requirement, alur customer, kontrak API publik, maupun jaminan. Perubahannya: **sikap CAP/PACELC per komponen** ditulis eksplisit (§9.7 [v4.6 → HLD §4.3]), **transactional outbox dibatasi pada Booking dan Payment**, dan **idempotensi consumer tanpa tabel inbox** (§9.8 [v4.6 → SDD §4.5.2]), sehingga tabel pendukung turun dari 13 menjadi 8 (DDL 28 → 23 tabel, diuji ulang).
>
> Isi v4.5 yang tetap berlaku: PRD v4.5 menggantikan v4.4. v4.5 tidak mengubah requirement, alur customer, kontrak API, maupun jaminan; perubahannya terbatas pada penetapan **Valkey** sebagai in-memory store (antrean, rate limit, cache), **prinsip desain kode SOLID dalam Go**, dan **alasan best practice setiap pilihan tech stack** (§7.3 [v4.6 → ADR-015]) untuk technical defense tender.
>
> Isi v4.4 yang tetap berlaku: PRD v4.4 menggantikan v4.3 dan menjadi baseline final untuk implementasi. Dua janji utama tetap: **"Jangan PHP"** (customer yang masuk halaman beli pasti bisa memesan hingga N tiket, asal membayar sebelum TTL habis) dan **adil** (urutan ditentukan nomor antrean server, bukan kecepatan klien). v4.4 menetapkan arti "real time" secara terukur (near-real-time dengan batas keterlambatan), notifikasi giliran, ubah pesanan sebelum bayar, perilaku refresh / koneksi putus / tombol back, dan prinsip konfigurasi untuk semua TTL dan batas. Jaminan diuji ulang dengan harness database (Lampiran C.7 [v4.6 → SDD §4.7.1]). Semua requirement tetap berstatus `NOT_VERIFIED`; coverage dokumen bukan bukti implementasi.

---


### Ringkasan Perubahan v4.5 → v4.6

*Sumber: PRD v4.6 Product Requirement Document (PRD) — Rusuh Booking Tiket › Ringkasan Perubahan v4.5 → v4.6*

| # | Area | v4.5 | v4.6 | Dasar |
|---|---|---|---|---|
| 1 | Teorema CAP | Tersirat di §9.6 [v4.6 → HLD §4.3] (strong vs eventual) | **§9.7 [v4.6 → HLD §4.3] sikap CAP/PACELC per komponen**: CP untuk stok dan uang (PostgreSQL), fail closed untuk penerbitan nomor antrean, AP untuk cache, tampilan, dan notifikasi | Diskusi tim 16 Okt; technical defense |
| 2 | Transactional outbox | Booking, Payment; pseudocode Queue menyebut outbox | **Hanya Booking dan Payment** (perubahan stok dan uang). Queue mem-publish `queue.turn.*` langsung dengan publisher confirm (push memang best effort). Notification hanya consumer | Dual write hanya berbahaya bila event membawa akibat uang/tiket |
| 3 | Idempotensi consumer | Tabel `consumer_inbox` di 4 service | **Tanpa tabel inbox**: guard state machine monotonic, Lua idempoten, dan unique key bisnis; aturan per consumer di §9.8 [v4.6 → SDD §4.5.2]; uji kirim ganda per consumer (TC-MSG-03) | Mengurangi tabel tanpa melemahkan jaminan |
| 4 | Tabel pendukung | 13: `outbox_events` ×2, `idempotency_records` ×2, `consumer_inbox` ×4, `audit_log`, `config_audit`, `entry_audit`, `payment_callbacks`, `deliveries` | **8**: `outbox_events` ×2, `idempotency_records` ×2, `config_audit`, `entry_audit`, `payment_callbacks`, `deliveries` sebagai dedup Notification. Dihapus: `consumer_inbox` ×4, `booking.audit_log` | Setiap tabel harus menjaga satu jaminan |
| 5 | Prinsip event-driven | Tidak tertulis | **Async hanya untuk efek samping lintas service yang tidak boleh menahan transaksi**; event tanpa consumer tidak dibuat (`booking.held.v1` dihapus) | Kesederhanaan, timeline ketat |

### Ringkasan Perubahan v4.4 → v4.5 (riwayat)

*Sumber: PRD v4.6 Product Requirement Document (PRD) — Rusuh Booking Tiket › Ringkasan Perubahan v4.4 → v4.5 (riwayat)*

| # | Area | v4.4 | v4.5 | Dasar |
|---|---|---|---|---|
| 1 | In-memory store | "Redis 7 / Valkey" (belum diputuskan) | **Valkey 8.1** dengan client Go **`valkey-go`**; peran dibatasi tegas: antrean, rate limit, cache (§13.4 [v4.6 → HLD §4.3]); Lua diuji ulang di Valkey standalone + cluster, benchmark ±24–27 ribu join/detik (§17.3 [v4.6 → HLD §4.5], Lampiran M.1 [v4.6 → SDD §4.9]) | Diskusi tim 16 Okt: best practice + biaya |
| 2 | Otoritas data | PostgreSQL untuk stok dan uang | Ditegaskan: Valkey **tidak pernah** menyimpan stok, hold, sesi, kuota, NIK pemegang, atau status pembayaran | Jaminan "Jangan PHP" (§5.9 [v4.6 → SRS §3.2.9]) |
| 3 | Cache ketersediaan SSE | Tidak diatur | Ringkasan ketersediaan per event dihitung sekali per interval dan dibagi lewat Valkey, sehingga query DB tidak bertambah dengan jumlah koneksi SSE (§5.25 [v4.6 → SRS §3.2.25]) | Skala halaman detail |
| 4 | Desain kode | Hexagonal (Lampiran F [v4.6 → SDD §4.1]) | **Prinsip SOLID dalam Go** + unit of work untuk transaksi stok (§7.4 [v4.6 → SDD §4.5]), masuk steering Kiro | Diskusi tim 16 Okt |
| 5 | Alasan tech stack | Tabel stack + status radar | **Alasan best practice per komponen** dan nilai tendernya (§7.3 [v4.6 → ADR-015]) | Technical defense |
| 6 | Proposal | ElastiCache "Redis/Valkey" | **ElastiCache for Valkey** | Konsistensi dengan PoC |
| 7 | Tech radar | PostgreSQL, Redis/Valkey, RabbitMQ "perlu cek radar" | **PostgreSQL, Valkey, RabbitMQ: ADOPT** | Konfirmasi tim 16 Okt |

### Ringkasan Perubahan v4.3 → v4.4 (riwayat)

*Sumber: PRD v4.6 Product Requirement Document (PRD) — Rusuh Booking Tiket › Ringkasan Perubahan v4.3 → v4.4 (riwayat)*

| # | Area | v4.3 | v4.4 | Dasar |
|---|---|---|---|---|
| 1 | Arti "real time" | Tidak didefinisikan | **Near-real-time dengan batas keterlambatan terukur** per jenis informasi; waiting room tetap polling ke CDN (bukan SSE/WebSocket); **halaman detail memakai SSE untuk ketersediaan kategori, dengan polling 3 detik sebagai cadangan otomatis**; interval polling dikendalikan server (§5.25 [v4.6 → SRS §3.2.25]) | Diskusi tim 15 Okt: inti requirement adalah "jangan PHP dan adil" |
| 2 | Giliran hangus | Claim window 2 menit | **3 menit** + peringatan "giliran sebentar lagi" + push notification mobile; **tanpa** kesempatan kedua/prioritas agar urutan antrean tidak pernah diubah (§5.25 [v4.6 → SRS §3.2.25]) | Diskusi tim 15 Okt |
| 3 | Ubah pesanan | Tidak ada; sisa unit dilepas saat hold | **Boleh mengubah pesanan sebelum menekan "Bayar"** selama TTL Booking; sisa unit tetap dipegang; penggantian atomik; maks 3 kali; TTL Pembayaran tidak di-reset (§5.23 [v4.6 → SRS §3.2.23]) | Diskusi tim 15 Okt |
| 4 | Tombol back | Tidak diatur | Server penentu tampilan; perilaku web, Android, iOS, dan WebView ditetapkan; pesanan terkunci → dialog, bukan halaman detail (§5.24 [v4.6 → SRS §3.2.24]) | Diskusi tim 15 Okt |
| 5 | Refresh dan koneksi putus | Timer dari server | + endpoint pemulihan sesi, pemulihan isian form (NIK tidak disimpan di storage), `Idempotency-Key` per tombol, banner koneksi terputus, masa tenggang submit 5 detik (§5.24 [v4.6 → SRS §3.2.24]) | Diskusi tim 15 Okt |
| 6 | Cek NIK pemegang | 10 cek per sesi | 10 **NIK berbeda** per sesi; cek ulang NIK yang sama tidak dihitung; NIK di pesanan sendiri dianggap tersedia (mode ubah) | Refresh dan mode ubah |
| 7 | Konfigurasi | TTL configurable | **Semua TTL, jendela, dan batas configurable** dengan batas aman; snapshot per sesi; aturan pembentuk jaminan dikunci saat penjualan dibuka; validasi silang; audit perubahan (§5.26 [v4.6 → SRS §3.2.26], Lampiran J [v4.6 → SDD §6]) | Arahan tim |
| 8 | Evidence desain | T1–T9, simulasi 40 event, 40 pembeli paralel | + T10–T22, simulasi 60 event / 30.000 langkah dengan ubah pesanan dan kunci, uji 60 pembeli paralel yang mengubah pesanan bersamaan | — |
| 9 | SSE di halaman detail | Polling 3 detik | **SSE** `GET /booking-sessions/{id}/stream` untuk ketersediaan kategori, fallback polling otomatis, angka hanya informasi (§5.25 [v4.6 → SRS §3.2.25]) | Diskusi tim 15 Okt |

### Ringkasan Perubahan v4.2 → v4.3 (riwayat)

*Sumber: PRD v4.6 Product Requirement Document (PRD) — Rusuh Booking Tiket › Ringkasan Perubahan v4.2 → v4.3 (riwayat)*

| # | Area | v4.2 | v4.3 | Dasar |
|---|---|---|---|---|
| 1 | Jaminan "Jangan PHP" | "Minimal satu tiket" dari kategori tersedia | **Hingga N tiket pasti dapat dipesan** (N = unit reservasi yang ditampilkan saat masuk), dari kategori mana pun yang tersedia, campuran kategori diperbolehkan; dirumuskan formal beserta bukti (§5.9 [v4.6 → SRS §3.2.9]) | Diskusi tim 15 Okt; wireframe Detail Ticket |
| 2 | Konsistensi stok | Tidak dijaga DB | **Total stok event = jumlah kapasitas kategori**, dijaga constraint trigger yang dicek saat commit | Syarat matematis jaminan |
| 3 | Zona "pasti" di antrean | `floor(stok bebas / 3)` — terlalu pesimis saat sisa < 3 | `ceil(stok bebas / maks per order)`; sisa 1–2 tiket tetap diberikan ke pengantre terdepan | Uji sisa stok < 3 |
| 4 | Validasi NIK pemegang | Saat "Lanjut ke Pembayaran" | **Dicek langsung saat diketik** (struktur di klien, keterdaftaran di server), cek final atomik saat hold; dibatasi 10 cek per sesi | Diskusi tim 15 Okt |
| 5 | Anti-calo | Satu entry per NIK pembeli | + **satu antrean aktif per perangkat dan per email**, satu giliran aktif per perangkat (unique index); ringkasan kontrol di §14.5 [v4.6 → HLD §4.6] | Diskusi tim 15 Okt |
| 6 | Kuota | Kuota pembeli dan pemegang | Ditegaskan: **dua hitungan terpisah, per event**; event lain dihitung dari nol (§5.11 [v4.6 → SRS §3.2.11]) | Grooming CTO + BR-01/BR-02 |
| 7 | Waiting room | Zona peluang | + info kategori yang sudah habis (informasi, bukan jaminan) | Ekspektasi customer |
| 8 | UX | — | Aturan NIK ditampilkan sebelum antre; "Kamu bisa memesan hingga N tiket"; NIK pembeli terkunci; form pemegang muncul per tiket; hitung mundur batas keras | Wireframe tim |
| 9 | Evidence desain | Uji constraint dasar | + harness acuan (Lampiran P [v4.6 → TestPlan Annex B]), uji T1–T9, simulasi 40 event, uji 40 pembeli paralel | — |

### Ringkasan Perubahan v4.1 → v4.2 (riwayat)

*Sumber: PRD v4.6 Product Requirement Document (PRD) — Rusuh Booking Tiket › Ringkasan Perubahan v4.1 → v4.2 (riwayat)*

| # | Area | v4.1 | v4.2 | Dasar |
|---|---|---|---|---|
| 1 | Arsitektur | Modular monolith (`api` + `worker`) | **4 service sesuai requirement**: Queue, Booking, Payment, Notification; schema DB per service | Arahan tim + aktor requirement |
| 2 | Hold stok | Saat klik "Lanjut ke Pembayaran" | **Reservasi alokasi saat giliran tiba** (admission); dikonversi menjadi hold kategori saat lanjut bayar | Grooming KP-3, KP-4 |
| 3 | Antrean saat stok tipis | Tidak diatur | **Indikator peluang jujur** (dijamin / ada peluang / tidak cukup) dan **penutupan waiting room** saat habis | Grooming KP-4, D-10, D-15 |
| 4 | NIK | Kuota 3 per NIK | **1 tiket = 1 NIK pemegang (unik per event)**; maks 3 tiket per order/NIK pembeli; 1 slot aktif per NIK pembeli | Grooming KP-5, KP-13, D-3, D-5 |
| 5 | Validasi NIK | Format 16 digit | **Validasi struktur NIK internal** (wilayah, tanggal lahir, nomor urut), tanpa integrasi eksternal | Grooming AI-4 |
| 6 | Akun | Identity & Auth (register/login) | **Guest checkout**; email + NIK pembeli saat masuk antrean; modul auth dihapus | Grooming KP-7, D-6 |
| 7 | Antrean maju | Saat admission | **Saat transaksi di depan selesai/expired/batal**; slot dilepas ke antrean berikutnya sesuai urutan, bukan pool umum | Grooming KP-9, KP-14, D-9, D-16 |
| 8 | FIFO | Sequence server | Sequence server monoton + timestamp UTC tercatat | Grooming KP-11, D-12 |
| 9 | Waiting room skala besar | Polling ke origin | **Status antrean disajikan dari CDN** (pola nomor antrean bank); origin hanya melayani join dan klaim | Grooming KP-16, D-17 |
| 10 | Inaktivitas | Tidak diatur | Peringatan sebelum TTL habis; giliran yang tidak diklaim hangus; redirect ke home dengan alasan jelas | Grooming AI-7, D-11 |
| 11 | Payment gateway lambat | Breaker + reconcile | **Rekonsiliasi sebelum stok dilepas**, status "Memverifikasi", masa tenggang, perlindungan saat gateway gangguan | Grooming KP-9 |
| 12 | Cancel oleh user | Tidak ada | **SHOULD**: batal booking atau keluar sesi; slot langsung ke antrean berikutnya | Grooming D-7 |
| 13 | E-tiket | Belum ditentukan | Email teks + kode tiket + tautan halaman tiket | Grooming KP-12, D-14 |
| 14 | Kapasitas & harga | Tidak ada | **Model kapasitas 3 tier + model biaya** untuk proposal | Grooming KP-16, AI-13 |
| 15 | TTL | 5 + 10 menit | 5 + 10 menit configurable, **disertai justifikasi throughput** | Grooming KP-6, D-4 |

Kode rujukan grooming: KP = Key Point, D = Decision, AI = Action Item pada notulen 14 Oktober 2026.

---

### Final Acceptance Statement and Changelog

*Sumber: PRD v4.6 §25*

PRD v4.6 mencakup 25 FR, 10 NFR, 8 BR, SCP-01, dan 16 requirement grooming (GR-01–GR-16), disertai acceptance criteria, wireframe, HLD, LLD per service, sequence/state/ERD, kontrak, data model, keamanan, resiliensi, deployment, pengujian, model kapasitas dan biaya, serta assumption register. Pemenuhan aktual baru boleh dinyatakan untuk requirement yang memiliki evidence dan status `VERIFIED`.

#### Sources

*Sumber: PRD v4.6 §25.1*

- PDF *Evaluasi Expertise Solution Development 2026 H2* — Rusuh Booking Tiket (tidak di-commit ke repo public).
- Notulen *Grooming Requirement — Rusuh Booking Tiket*, 14 Oktober 2026 (tidak di-commit).
- PRD v4.1 (14 Oktober 2026), PRD v4.2 dan v4.3 (15 Oktober 2026).
- Keputusan tim 16 Oktober 2026: Valkey untuk antrean, rate limit, dan cache; prinsip SOLID; alasan best practice tech stack; sikap CAP/PACELC per komponen; outbox hanya di Booking dan Payment; consumer idempoten tanpa tabel inbox.
- Keputusan tim 15 Oktober 2026: definisi "Jangan PHP", aturan NIK per event, cek NIK langsung, anti-calo, arti "real time", notifikasi giliran, ubah pesanan sebelum bayar, tombol back, refresh dan koneksi putus, prinsip konfigurasi.
- Dokumentasi Midtrans, AWS, dan GitHub — wajib diverifikasi saat implementasi.

#### Changelog

*Sumber: PRD v4.6 §25.2*

**v4.6 (16 Oktober 2026) — final**

- Sikap CAP/PACELC per komponen ditulis eksplisit (§9.7 [v4.6 → HLD §4.3]): CP untuk stok dan uang, fail closed untuk nomor antrean, AP untuk cache, tampilan, dan notifikasi.
- Transactional outbox dibatasi pada Booking dan Payment; Queue mem-publish `queue.turn.*` langsung dengan publisher confirm (§9.8 [v4.6 → SDD §4.5.2]).
- `consumer_inbox` (4 service) dan `booking.audit_log` dihapus; idempotensi consumer dari guard state, Lua, dan unique key, dengan tabel per consumer (§9.8 [v4.6 → SDD §4.5.2]). Tabel pendukung 13 → 8; DDL 28 → 23 tabel, diterapkan ulang pada PostgreSQL 16 tanpa error, dan uji settlement ganda lulus (Lampiran B [v4.6 → SDD §4.4]).
- Event `booking.held.v1` (tanpa consumer) dihapus; prinsip event-driven ditulis (§9.8 [v4.6 → SDD §4.5.2]).
- Aturan SOLID ditambah: consumer wajib idempoten dan outbox hanya untuk perubahan stok/uang (§7.4 [v4.6 → SDD §4.5]).
- Test baru: TC-MSG-03 (pesan ganda per consumer) dan TC-CAP-01..03 (perilaku saat partisi).
- Tidak ada perubahan requirement, alur customer, kontrak API publik, maupun jaminan.

**v4.5 (16 Oktober 2026)**

- Valkey 8.1 + `valkey-go` ditetapkan sebagai in-memory store; peran dibatasi antrean, rate limit, dan cache; stok, sesi, kuota, NIK, dan pembayaran tetap di PostgreSQL (§13.4 [v4.6 → HLD §4.3]).
- Cache ringkasan ketersediaan kategori di Valkey untuk SSE halaman detail (§5.25 [v4.6 → SRS §3.2.25]).
- Alasan best practice per komponen tech stack (§7.3 [v4.6 → ADR-015]) dan prinsip SOLID dalam Go dengan unit of work (§7.4 [v4.6 → SDD §4.5]).
- Compose, diagram, middleware, risiko, technical defense, dan proposal (ElastiCache for Valkey) diselaraskan.
- Lua antrean diuji ulang di Valkey 8.1.4 (standalone + cluster 3 node): 13/13 skenario lulus, 10.000 join paralel tanpa celah seq; benchmark ±23.900–27.000 join/detik, ±0,65 GB per 1 juta pengantre.
- PostgreSQL, Valkey, dan RabbitMQ dikonfirmasi ADOPT di tech radar.
- Tidak ada perubahan requirement, alur customer, kontrak API, DDL, maupun jaminan.

**v4.4 (15 Oktober 2026)**

- "Real time" ditetapkan sebagai near-real-time dengan batas keterlambatan terukur; polling CDN dikukuhkan; interval polling dikendalikan server.
- SSE di halaman detail untuk ketersediaan kategori (fallback polling otomatis, angka hanya informasi, konfigurasi `SSE_*`); waiting room tetap polling CDN.
- Claim window 3 menit, peringatan "giliran sebentar lagi", push notification mobile (FCM); tanpa prioritas untuk giliran hangus.
- Ubah pesanan sebelum menekan "Bayar" (penggantian atomik, sisa unit dipegang, TTL Pembayaran tidak di-reset, maks 3 kali); kunci pesanan via `payment-lock`.
- Perilaku refresh, koneksi putus, dan tombol back untuk web, Android, iOS, dan WebView; endpoint pemulihan sesi; masa tenggang submit.
- Cek NIK: batas 10 NIK berbeda; cek ulang tidak dihitung.
- Prinsip konfigurasi: snapshot per sesi, aturan jaminan dikunci, validasi silang, audit; Lampiran J [v4.6 → SDD §6] ditulis ulang.
- DDL, harness, dan bukti diperbarui (T10–T22, simulasi 60 event, uji 60 pembeli paralel).

**v4.3 (15 Oktober 2026)**

- Jaminan "Jangan PHP" dirumuskan formal: hingga N tiket dari kategori tersedia, campuran diperbolehkan, beserta bukti invariant.
- Constraint total stok event = jumlah kapasitas kategori.
- Zona "pasti" memakai `ceil(stok bebas / maks per order)`.
- Cek NIK pemegang langsung saat diketik, dibatasi per sesi.
- Anti-calo: satu antrean aktif per perangkat dan email; ringkasan kontrol §14.5 [v4.6 → HLD §4.6].
- Kuota pembeli dan pemegang ditegaskan terpisah dan per event.
- Status kategori di Status Document dan banner waiting room.
- Wireframe data pembeli, waiting room, dan detail ticket diperbarui.
- Harness acuan dan hasil uji jaminan (Lampiran C.7 [v4.6 → SDD §4.7.1], Lampiran P [v4.6 → TestPlan Annex B]).

**v4.2 (15 Oktober 2026)**

- Arsitektur menjadi 4 service sesuai requirement (Queue, Booking, Payment, Notification) dengan schema per service.
- Reservasi alokasi saat admission (GR-01), indikator peluang jujur, dan sold-out closure (GR-02).
- Waiting room berbasis CDN untuk skala hingga 1 juta pengantre.
- Guest checkout menggantikan modul Identity & Auth.
- Satu tiket satu NIK pemegang; kuota NIK pembeli; satu slot per NIK; validasi struktur NIK.
- Antrean maju saat journey di depan berakhir; claim window; redirect beralasan.
- Rekonsiliasi sebelum stok dilepas, VERIFYING, REVIEW, perlindungan saat gateway gangguan.
- Cancel (SHOULD), e-tiket teks.
- Model kapasitas 3 tier dan estimasi biaya untuk proposal.
- Assumption register menggantikan sebagian besar TBC.

**v4.1 (14 Oktober 2026)** — Requirement 2026 H2, tim 5 orang, edge tanpa Kong, payment page WebView, stack sesuai radar.

---

### Final Canonical Release Statement

*Sumber: PRD v4.6 Lampiran Final Canonical Release Statement*

PRD v4.6 adalah kontrak requirement, desain, verification plan, dan dasar teknis proposal tender untuk Rusuh Booking Tiket. Dokumen ini **bukan** bukti bahwa source code tersedia, integrasi Midtrans berhasil, autoscaling terbukti, atau kapasitas tercapai. Uji DDL, harness jaminan, Lua, validator NIK, rumus zona, dan perhitungan posisi yang dicatat di lampiran adalah verifikasi desain di lingkungan pengembangan, bukan evidence `VERIFIED` atas implementasi. Seluruh 43 requirement sumber, SCP-01, dan 16 requirement grooming tetap `NOT_VERIFIED` sampai memiliki implementasi, test/evidence aktual, dan review yang dapat diaudit. Perubahan scope, data model, urutan lock, batas service, atau topologi deployment wajib melalui ADR dan memperbarui traceability.


---

## 6. Standar Acuan

| Standar | Status per Oktober 2026 | URL |
|---|---|---|
| ISO/IEC/IEEE 29148:2018 | Berlaku; berstatus "to be revised" (DIS 29148 sedang disusun) | https://www.iso.org/standard/72089.html |
| ISO/IEC 25010:2023 | Berlaku | https://www.iso.org/standard/78176.html |
| ISO 9241-210:2019 | Berlaku | https://www.iso.org/standard/77520.html |
| ISO/IEC/IEEE 42010:2022 | Berlaku | https://www.iso.org/standard/74393.html |
| IEEE 1016-2009 | Inactive-Reserved | https://standards.ieee.org/ieee/1016/1478/ |
| ISO/IEC/IEEE 29119-3:2021 | Berlaku | https://www.iso.org/standard/79429.html |
| ISO/IEC/IEEE 15289:2019 | Berlaku; berstatus "to be revised" | https://www.iso.org/standard/74909.html |
| ISO/IEC/IEEE 12207:2017 | Berlaku | https://committee.iso.org/standard/63712.html |

Semua dokumen "disusun dengan mengacu pada" standar di atas, bukan klaim "conform to". Klaim kepatuhan formal memerlukan pemeriksaan klausul pada teks standar resmi.

---

## 7. Perubahan Setelah Restrukturisasi

Perubahan berikut adalah keputusan tim sesudah PRD v4.6. Sampai 18 Oktober tidak ada yang mengubah requirement, alur customer, kontrak API publik, maupun jaminan. Perubahan 19 Oktober (§8) **menambah** requirement (akun opsional) tanpa mengubah alur guest dan jaminan "Jangan PHP".

| Tanggal | Perubahan | Dokumen |
|---|---|---|
| 2026-10-17 | Struktur proyek mengikuti standar organisasi | ADR-016, SDD §4.1, Handbook §0 |
| 2026-10-17 | Tiga repository di organisasi `tiket-sampai-nonton` (be, web, mobile) | ADR-017, PRD CON-06, SDD §4.1.3, Handbook §1 |
| 2026-10-17 | Pipeline satu `ci.yml` bertahap; image arm64 dan SBOM; SonarCloud menggantikan SonarCloud; integration dan BDD masuk pipeline saat fitur pertama siap | Handbook §2, TP, HLD, ADR-015 |
| 2026-10-14 | Approval PRD oleh CEO, CTO, CFO | PRD §14 |
| 2026-10-18 | Alur pemesanan dinyatakan sebagai saga koreografi dengan kompensasi (tanpa 2PC, tanpa orchestrator) | ADR-018, ADR-009, HLD §4.3, TP |
| 2026-10-19 | Keputusan bersama BE-2 (§8) | Semua dokumen; boilerplate `rusuh-booking-be` |

---

## 8. Perubahan v1.5 — Keputusan Bersama BE-2 (19 Oktober 2026)

Sumber: `Perbedaan-Desain-BE-Kita-vs-BE2.xlsx`. Perubahan requirement (akun opsional, PAY-06 Must) menunggu persetujuan approver (PRD OI-11).

| ID | Keputusan | Dokumen | Kode `rusuh-booking-be` |
|---|---|---|---|
| D-01 | Penilaian per orang; presentasi per tim | PRD §1, ADR-017 | — |
| D-02, D-03 | **5 service**: User Service untuk akun opsional (NIK, email, nama); guest tetap utama | PRD FEAT-10, SRS §3.2.27, HLD CMP-USER, SDD §4.1.2, ADR-020 | `services/user` |
| D-04 | Event sourcing untuk Booking dan Payment; stok tetap relasional | SRS-C-09, SRS-D-10..11, HLD §4.3.5, SDD §4.4.3, ADR-019 | `pkg/eventstore`, migrasi `booking_0002`, `payment_0002` |
| D-05, D-07, D-08 | REST tetap; gRPC, EKS + Helm, Kong dicatat sebagai alternatif | ADR-021 | — |
| D-10 | Outbox di Booking dan Payment | SDD §4.5.4 | tabel outbox (migrasi) |
| D-11, D-12 | Guard urutan consumer; retry bertingkat + DLQ dengan batas retry | SDD §4.5.4 | `pkg/rabbitmq` (`retry.go`, `consumer.go`) |
| D-13 | Disesuaikan dengan desain v4.6 | SDD | — |
| D-14 | Entry point Cobra (`api`, `worker`, `migrate`, `healthcheck`) | SDD §4.1, Handbook §0, ADR-022 | `services/*/cmd` |
| D-15, D-22 | pgx, valkey-go, amqp091-go | ADR-022 | `pkg/postgres`, `pkg/valkey`, `pkg/rabbitmq` |
| D-16 | Migrasi tertanam + advisory lock + checksum | SDD §4.4, Handbook §0 | `pkg/migrate`, `deployments/migrations` |
| D-17 | Struktur kode mengikuti standar organisasi (sama dengan BE-2) | ADR-016 | semua service |
| D-18 | Binary hasil build tidak di-commit | Handbook §0 | `.gitignore` |
| D-19 | Satu file OpenAPI per service, isi dari v4.6 | SDD §5, SRS §3.1 | `docs/contracts/openapi/*.yaml` |
| D-20 | Tinjauan skema: hanya tabel yang dibutuhkan | SDD §4.4.5 | migrasi per service |
| D-21 | OTP email untuk login (tanpa password) | SRS-F-27-xx, SRS-Q-SEC-10..12 | `services/user/internal/user` |
| D-23 | Satu endpoint `GET /health` | SRS, SDD, Handbook §3.0 | `infrastructure/http/health_handler.go` |
| D-24 | Nama env standar | SDD §6.1, Handbook §3.0 | `internal/config`, `.env.example` |
| D-25 | Go 1.26 | ADR-022 | `go.mod`, Dockerfile, CI |
| D-26 | GitHub | Handbook §1 | `.github/` |
| D-27 | testify; setiap fungsi punya test regresi | TP, Handbook §0 | `*_test.go` |
| D-30 | Format test case gabungan | TP Annex A | — |
| D-33 | Checklist keamanan BE-2 | TP, Handbook §4 | — |
| D-34 | Dua pendekatan konkurensi (row lock stok + optimistic concurrency event store) | SDD §4.5, TP | `pkg/eventstore` (`ErrConcurrency`) |
| D-35 | Penjelasan arsitektur untuk junior | Handbook §7 | `services/*/docs/README.md` |
| D-40 | Circuit breaker menjadi MUST | SRS-F-19-03, HLD QS-15 | `pkg/resilience` |
| D-41 | Alert lewat email; Telegram sesudah PoC | SRS-F-90-06, SDD §7, Handbook §6 | — |
| D-45 | Catalog tetap modul read-only di Booking (read path dipisah pool/cache bila perlu) | SDD §4.1.2 | `services/booking` |
| D-46 | "Consumer auth" tidak dibuat; digantikan User Service | ADR-020 | `services/user` |
| D-47 | Sweeper adalah job worker Booking/Payment/Queue, bukan service | SDD §4.1.2, Handbook §3.0 | `delivery/worker.go` |
| D-48 | Slot aktif dilepas saat booking `PAID` (sudah sesuai v4.6) | SRS-F-06-08, SDD §4.1.2 | — |
| D-09, D-28, D-29, D-39, D-42..D-44 | Ditunda; dibahas ulang setelah Sprint 1 | — | — |

## 9. Perubahan v1.6 (21 Oktober 2026)

- Dokumen planning dipindah ke satu folder datar `docs/analisa/` (sebelumnya `prd/`, `srs/`, `hld/`, `sdd/`, `adr/`, `test/`, `handbook/`, `integration/`). SDD per service diberi awalan `04a`–`04e`, dokumen integrasi `08`–`08c`. Semua rujukan di repo sudah diperbarui.
- `contracts/`, `ai-log/`, dan `evidence/` tetap terpisah. Setiap sesi AI dicatat di `ai-log/`; setiap diskusi yang mengubah dokumen planning dicatat di `evidence/`.
- Pemilik: arnu12rahman (BE-1) — Notification, Payment, platform; nuzirwan (BE-2) — User, Queue, Booking (lihat `.github/CODEOWNERS`).

