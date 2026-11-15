# 🔗 NetSathi

> **Resumable and Integrity-Verified File Transfer System**  
> *A high-reliability Go project for local network file transfers featuring chunking, SQLite state persistence, SHA-256 cryptographic verification, and crash recovery.*

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


---

## 📄 License
This project is developed as an academic capstone and backend systems demonstration. Distributed under the MIT License.
