# 🔗 NetSathi

> **Resumable and Integrity-Verified File Transfer System**  
> *A high-reliability Go project for local network file transfers featuring chunking, SQLite state persistence, SHA-256 cryptographic verification, and crash recovery.*

---

## 📌 Table of Contents
1. [Overview & Problem Statement](#overview--problem-statement)
2. [Key Capabilities & Architecture](#key-capabilities--architecture)
3. [Technology Stack](#technology-stack)
4. [Project Structure](#project-structure)
5. [Prerequisites & Getting Started](#prerequisites--getting-started)
6. [Interactive Web Dashboard](#interactive-web-dashboard)
7. [REST API Documentation](#rest-api-documentation)
8. [Testing & Evaluation](#testing--evaluation)
9. [Docker Deployment](#docker-deployment)
10. [Viva & Technical Interview Guide](#viva--technical-interview-guide)

---

## 📖 Overview & Problem Statement

Standard HTTP file uploads fail completely when interrupted by transient network drops, requiring clients to retransmit entire multi-gigabyte files from byte zero. Furthermore, naive upload endpoints offer no cryptographic guarantees that the assembled bytes match the sender's original file.

**NetSathi** solves these challenges by providing:
- **Chunk-Based Resumable Transfers:** Files are split into configurable chunks (1 MB–10 MB). Interrupted uploads resume by querying the server for missing chunks and transmitting *only* what was missed.
- **End-to-End & Chunk-Level Cryptographic Integrity:** Dual-layer hashing! Validates individual chunk hashes via `X-Chunk-SHA256` in real-time, plus whole-file SHA-256 verification upon final assembly.
- **Server Restart & Crash Recovery:** SQLite with Write-Ahead Logging (WAL) stores transfer and chunk metadata. On startup, the server reconciles database state against actual disk files, recovering incomplete transfers seamlessly.
- **Real-Time Throughput Metrics:** Live upload speed (KB/s, MB/s) and estimated time of arrival (ETA) computed directly in the browser dashboard.
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

## 📂 Project Structure

```
NetSathi/
├── cmd/
│   └── server/
│       └── main.go               # Server entry point, flags, graceful shutdown, recovery
├── internal/
│   ├── api/
│   │   ├── handlers.go           # REST API endpoint handlers
│   │   ├── handlers_test.go      # Integration test suite using httptest
│   │   └── router.go             # Logging middleware, CORS, and HTTP router
│   ├── integrity/
│   │   ├── sha256.go             # 32KB streaming SHA-256 calculation & verifier
│   │   └── sha256_test.go        # Unit tests for integrity verification
│   ├── security/
│   │   ├── tokens.go             # 256-bit crypto tokens & path traversal sanitizer
│   │   └── security_test.go      # Unit tests for security sanitizers
│   ├── storage/
│   │   ├── models.go             # Transfer & chunk domain entities
│   │   ├── sqlite.go             # SQLite engine, WAL mode, foreign keys & reconciliation
│   │   └── sqlite_test.go        # Unit tests for database queries and state changes
│   └── transfer/
│       └── manager.go            # Core chunk storage, assembly, and resume engine
├── web/
│   ├── css/
│   │   └── style.css             # Modern dark-mode UI stylesheet
│   ├── js/
│   │   └── app.js                # Web Crypto hashing, chunked upload, pause/resume
│   └── index.html                # Self-hosted single-page application dashboard
├── data/
│   ├── chunks/                   # Temporary in-flight chunk storage (.gitkeep)
│   └── completed/                # Verified published files (.gitkeep)
├── Dockerfile                    # Multi-stage production container build
├── go.mod                        # Go module definition
├── go.sum                        # Go dependency checksums
└── README.md                     # Comprehensive documentation & evaluation guide
```

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

## 🖥️ Interactive Web Dashboard

NetSathi includes a self-hosted single-page dashboard served directly by the Go application:
- **Drag-and-Drop or File Picker:** Select any local file.
- **Client-Side SHA-256 Hashing:** Automatically computes the file hash in the browser before upload begins.
- **Configurable Chunk Sizes:** Select 1 MB, 2 MB, 5 MB, or 10 MB chunks.
- **Pause & Resume Controls:** Click **Pause** at any time to simulate a network interruption, then click **Resume** to continue uploading without repeating already-saved chunks.
- **Transfer History & Direct Download:** View all recent transfers, verification statuses, and download finished files with one click.

---

## 📡 REST API Documentation

All endpoints return JSON responses and support CORS.

### 1. Health Check
`GET /healthz`
```json
{
  "status": "ok",
  "service": "NetSathi"
}
```

### 2. Initiate Transfer
`POST /api/transfers`
- **Request Body:**
  ```json
  {
    "original_name": "backup.zip",
    "total_size": 10485760,
    "chunk_size": 2097152,
    "expected_sha256": "3a7bd79b2d541075713cbd5f4114450280ac309b81f45e777b5453123015e37c"
  }
  ```
- **Response (`201 Created`):**
  ```json
  {
    "id": "e3794f09-b68b-4a57-b08e-59c40788730b",
    "token": "4a71b12389e81b6d92634e9bf4332468305846395b060636cf41f028886968aa",
    "total_chunks": 5,
    "chunk_size": 2097152
  }
  ```

### 3. Upload a Chunk
`PUT /api/transfers/{id}/chunks/{index}`
- **Headers:** `Authorization: Bearer <token>`
- **Body:** Binary chunk byte slice
- **Response (`200 OK`):**
  ```json
  {
    "transfer_id": "e3794f09-b68b-4a57-b08e-59c40788730b",
    "chunk_index": 0,
    "bytes_saved": 2097152,
    "status": "received"
  }
  ```

### 4. Query Missing Chunks (Resume)
`GET /api/transfers/{id}/missing`
- **Headers:** `Authorization: Bearer <token>`
- **Response (`200 OK`):**
  ```json
  {
    "transfer_id": "e3794f09-b68b-4a57-b08e-59c40788730b",
    "missing_chunks": [2, 3, 4],
    "missing_count": 3
  }
  ```

### 5. Finalize & Verify Transfer
`POST /api/transfers/{id}/complete`
- **Headers:** `Authorization: Bearer <token>`
- **Response (`200 OK`):**
  ```json
  {
    "message": "transfer completed and verified",
    "progress": {
      "transfer": {
        "id": "e3794f09-b68b-4a57-b08e-59c40788730b",
        "original_name": "backup.zip",
        "total_size": 10485760,
        "chunk_size": 2097152,
        "total_chunks": 5,
        "expected_sha256": "3a7bd79b2d54...",
        "actual_sha256": "3a7bd79b2d54...",
        "status": "Completed"
      },
      "received_chunks": 5,
      "received_bytes": 10485760,
      "percentage": 100
    }
  }
  ```

### 6. List Transfers
`GET /api/transfers?limit=20`

### 7. Download Completed File
`GET /api/transfers/{id}/download`

---

## 🧪 Testing & Evaluation

NetSathi includes a test suite covering API contracts, security checks, and database consistency.

Run all tests:
```bash
go test -v ./...
```

### Test Coverage Highlights
- ✅ **Storage Lifecycle:** Creation, retrieval, chunk state transition, and reconciliation.
- ✅ **Filename Sanitization:** Path traversal attacks (`../../`, `\..\Windows\System32\cmd.exe`, control characters) stripped safely.
- ✅ **Secure Token Generation:** Cryptographically random 256-bit tokens.
- ✅ **Streaming SHA-256:** Bit-exact verification and corrupted-chunk rejection.
- ✅ **Transfer API Lifecycle:** End-to-end simulation using `httptest` (create $\rightarrow$ chunk upload $\rightarrow$ missing check $\rightarrow$ complete $\rightarrow$ download).
- ✅ **Authentication Enforcement:** Rejection of unauthorized (`401`) and invalid token (`403`) requests.
- ✅ **Idempotency:** Re-transmitting existing chunks does not corrupt data or produce duplicate records.

---

## 🐳 Docker Deployment

To build and run using Docker:

```bash
# Build Docker image
docker build -t netsathi:latest .

# Run container mapping host port 8080 and mounting persistent data volume
docker run -d \
  -p 8080:8080 \
  -v $(pwd)/netsathi-data:/app/data \
  --name netsathi-app \
  netsathi:latest
```

Access the dashboard at `http://localhost:8080`.

---

## 🎓 Viva & Technical Interview Guide

Here are concise, high-impact answers to anticipated evaluation and viva questions:

| Question | Key Points / Answer |
|---|---|
| **Why choose Go for this project?** | Go provides compiled execution speed, native concurrency primitives (`goroutines`, channels, `sync.RWMutex`), low memory footprint, and a standard library with production-grade networking (`net/http`) and cryptography (`crypto/sha256`). |
| **Why chunk files instead of a single stream?** | Network connections can fail midway through large transfers. Chunking allows transfers to be split into manageable units so that only missing chunks need to be retransmitted, saving bandwidth and time. |
| **How does resumability work in NetSathi?** | Each transfer is tracked in SQLite. When an upload is resumed, the client queries `GET /api/transfers/{id}/missing`. The server checks received chunk records in the database, returns an array of unuploaded chunk indexes, and the client uploads only those parts. |
| **What role does SHA-256 play? Does it encrypt?** | No, SHA-256 does **not** encrypt; it is a one-way cryptographic hash function. It ensures *data integrity* by generating a 256-bit digital fingerprint of the file. If even a single bit is altered or corrupted during transfer, the hash mismatch is detected and the file is rejected. |
| **Why SQLite instead of PostgreSQL or MySQL?** | SQLite is self-hosted, serverless, zero-configuration, and stores data in a single local file. Configured with Write-Ahead Logging (`WAL`), it supports concurrent readers without blocking, making it ideal for an embedded network appliance. |
| **How does NetSathi recover state after a server crash?** | On server startup, `db.ReconcileStartupState()` queries all active transfers and reconciles chunk records against actual chunk files on disk. If a chunk file is missing or size-corrupted, its record is purged so the resume engine can re-fetch it. |
| **How are path traversal and malicious filenames prevented?** | Chunks and assembled files are stored on disk using server-generated UUIDs, not user-provided filenames. Client filenames are sanitized by stripping directory separators (`/`, `\`), control characters, and null bytes (`tokens.SanitizeFilename`). |
| **How is chunk write safety achieved?** | NetSathi writes chunks to a temporary `.part.tmp` file and performs an atomic rename to `.part` only after the full chunk write succeeds. Database records are committed only after the chunk file is verified on disk. |

---

## 📄 License
This project is developed as an academic capstone and backend systems demonstration. Distributed under the MIT License.
