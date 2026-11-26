# 🔗 NetSathi

> **Resumable and Integrity-Verified File Transfer System**  
> *A high-reliability Go project for local network file transfers featuring chunking, SQLite state persistence, SHA-256 cryptographic verification, and crash recovery.*

---

## 📌 Table of Contents
1. [Overview & Problem Statement](#overview--problem-statement)
2. [Key Capabilities & Architecture](#key-capabilities--architecture)
3. [Technology Stack](#technology-stack)
4. [Prerequisites & Getting Started](#prerequisites--getting-started)
5. [Interactive Web Dashboard](#interactive-web-dashboard)
6. [Dedicated CLI Client (`netsathi-cli`)](#dedicated-cli-client-netsathi-cli)
7. [Two-Device LAN Demonstration](#two-device-lan-demonstration)
8. [REST API Documentation](#rest-api-documentation)
9. [Testing & Evaluation](#testing--evaluation)
10. [Docker Deployment](#docker-deployment)
11. [Viva & Technical Interview Guide](#viva--technical-interview-guide)

---

## 📖 Overview & Problem Statement

Standard HTTP file uploads fail completely when interrupted by transient network drops, requiring clients to retransmit entire multi-gigabyte files from byte zero. Furthermore, naive upload endpoints offer no cryptographic guarantees that the assembled bytes match the sender's original file.

**NetSathi** solves these challenges by providing:
- **Chunk-Based Resumable Transfers:** Files are split into configurable chunks (1 MB–10 MB). Interrupted uploads resume by querying the server for missing chunks and transmitting *only* what was missed.
- **End-to-End & Chunk-Level Cryptographic Integrity:** Dual-layer hashing! Validates individual chunk hashes via `X-Chunk-SHA256` in real-time, plus whole-file SHA-256 verification upon final assembly.
- **Server Restart & Crash Recovery:** SQLite with Write-Ahead Logging (WAL) stores transfer and chunk metadata. On startup, the server reconciles database state against actual disk files, recovering incomplete transfers seamlessly.
- **Real-Time Throughput Metrics & SSE:** Live upload speed (KB/s, MB/s), ETA calculation, and Server-Sent Events (`/api/transfers/{id}/progress/stream`) pushing real-time server verification events directly to client dashboards.
- **Automated Retention Worker:** Background cleaner automatically purges incomplete/abandoned transfers older than 24 hours.
- **Defensive Engineering & Security:** Path traversal sanitization, high-entropy bearer tokens, unpredictable transfer UUIDs, and atomic temporary-file assembly.

---

## 🏛️ Key Capabilities & Architecture

```
                       +---------------------------------------+
                       |       Browser Web Dashboard           |
                       | (HTML5 File API / Web Crypto SHA-256) |
                       +---------------------------------------+
                                           |
                                  HTTP REST Endpoints
                                           v
                       +---------------------------------------+
                       |             NetSathi API              |
                       |       (CORS, Logging, Auth Mux)       |
                       +---------------------------------------+
                                           |
                                           v
                       +---------------------------------------+
                       |           Transfer Manager            |
                       | - Chunk Validator & Disk Writer       |
                       | - Missing Chunks Query Engine         |
                       | - Stream Assembler & SHA-256 Verifier |
                       +---------------------------------------+
                                      /         \
                                     /           \
                                    v             v
            +--------------------------+       +--------------------------+
            |      SQLite Database     |       |       File System        |
            | - `transfers` table      |       | - `data/chunks/{id}/...` |
            | - `chunks` table (WAL)   |       | - `data/completed/...`   |
            +--------------------------+       +--------------------------+
```

### Request Lifecycle
1. **Initiate Transfer (`POST /api/transfers`):** Client computes file SHA-256 and submits filename, file size, and chunk size. Server generates a UUID transfer ID and a 256-bit authorization token.
2. **Chunk Transmission (`PUT /api/transfers/{id}/chunks/{index}`):** Client sends individual chunks with Bearer token authentication. Server writes chunk to a `.part.tmp` file and atomically renames it upon successful receipt.
3. **Resume Upon Interruption (`GET /api/transfers/{id}/missing`):** If network drops or upload is paused, the client queries missing chunk indexes and transfers only the remaining chunks.
4. **Assembly & Verification (`POST /api/transfers/{id}/complete`):** Server verifies all chunks are present, streams them in sequential order to an assembled `.tmp` file, computes SHA-256, compares it to the expected hash, and atomically moves it to `data/completed/`.
5. **Download (`GET /api/transfers/{id}/download`):** Completed and verified files are made available for immediate secure download.

---

## 💻 Technology Stack

| Component | Technology | Purpose |
|---|---|---|
| **Backend** | Go (`1.22+`) | High-performance, concurrent backend service |
| **HTTP Routing** | Go Standard Library `net/http` | Zero-dependency, lightweight HTTP multiplexer & middleware |
| **Persistence** | SQLite (`modernc.org/sqlite`) | Embedded relational DB; pure Go without CGo/GCC requirements |
| **Integrity** | Go `crypto/sha256` & Web Crypto API | Memory-bounded, streaming cryptographic verification |
| **Frontend** | Vanilla HTML5, CSS3, JavaScript (ES6) | Responsive, self-hosted web dashboard (no Node.js build required) |
| **Packaging** | Docker (Alpine Multi-Stage) | Reproducible, containerized deployment |

---

## 🚀 Prerequisites & Getting Started

### Prerequisites
- [Go 1.22 or higher](https://go.dev/dl/) installed.
- (Optional) [Docker](https://www.docker.com/) for container execution.

### Local Run

1. **Clone the repository:**
   ```bash
   git clone https://github.com/Shaileshyadavv/NetSathi.git
   cd NetSathi
   ```

2. **Download dependencies:**
   ```bash
   go mod download
   ```

3. **Run the server:**
   ```bash
   go run ./cmd/server
   ```
   *Custom flags (optional):*
   ```bash
   go run ./cmd/server -port 8080 -data ./data -web ./web
   ```

4. **Open in browser:**
   Navigate to [http://localhost:8080](http://localhost:8080) to interact with the dashboard.

---



