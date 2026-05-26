# Task 15: Embeddings + vector index

- **Milestone**: M2 — Compression & Search
- **Priority**: P0
- **Depends on**: Task 3, Task 6
- **Tech**: Go 1.26.3 / Python 3.14.5
- **Maps to**: PRD FR-16, FR-17, NFR-1; agent_mem.md §"Search Engine" (vector), §"embedding/"

## Objective
Generate embeddings (Python, multi-provider) and serve cosine-similarity vector search (Go) over a disk-backed float32 index.

## Scope & Steps
- [ ] Python `agent_mem/embedding/`: provider factory + implementations (local Xenova all-MiniLM-L6-v2 default, OpenAI, Gemini, Voyage); `batch.py` for batched embedding.
- [ ] Python `POST /embed {texts[]} -> {embeddings[][]}` with configurable provider/dims (384–3072).
- [ ] Go `internal/search/vector.go`: store `obsId -> float32[]`; cosine similarity search; disk-backed persistence.
- [ ] Own the vector-index persistence migration here (a new migration added to the Task 6 framework), shaped by the chosen storage layout — not pre-defined in Task 6.
- [ ] Go calls `/embed` for each compressed observation's narrative (in Task 13 callback) and stores the vector.
- [ ] Guard against dimension mismatch within a project namespace (PRD assumption).
- [ ] `Search(queryVec, limit) []ScoredHit` returning top ~30 candidates.

## Files
- `py/agent_mem/embedding/{__init__.py,providers.py,local_embedder.py,openai_embedder.py,gemini_embedder.py,voyage_embedder.py,batch.py}`
- `go/internal/search/{vector.go,vector_test.go}`
- `go/internal/python/embed.go` (HTTP client to `/embed`)

## Acceptance Criteria
- [ ] `/embed` returns correct-dimension vectors for the configured provider; local provider needs no API key.
- [ ] Vector search returns nearest neighbors by cosine similarity; persists across restart.
- [ ] Dimension mismatch is rejected with a clear error.
- [ ] Vector-only search p95 < 120 ms at 10K vectors (contributes to NFR-1).

## Notes
Default to the local embedder so the system works offline/keyless. For large corpora, a future ANN index can replace brute-force cosine; brute force is acceptable at v1 scale.
