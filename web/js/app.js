// ============================================================
// NetSathi — Client-side Chunked Upload with Resume & Integrity
// ============================================================

(function () {
    'use strict';

    // --- Configuration ---
    const API_BASE = '/api/transfers';

    // --- State ---
    let selectedFile = null;
    let fileHash = '';
    let currentTransfer = null;  // { id, token, totalChunks, chunkSize }
    let isPaused = false;
    let isCancelled = false;
    let uploadedChunkCount = 0;

    // --- DOM Elements ---
    const dropZone = document.getElementById('dropZone');
    const fileInput = document.getElementById('fileInput');
    const fileInfo = document.getElementById('fileInfo');
    const fileName = document.getElementById('fileName');
    const fileSize = document.getElementById('fileSize');
    const fileHash_ = document.getElementById('fileHash');
    const chunkSizeSelect = document.getElementById('chunkSizeSelect');
    const uploadBtn = document.getElementById('uploadBtn');
    const clearBtn = document.getElementById('clearBtn');
    const uploadProgress = document.getElementById('uploadProgress');
    const progressFileName = document.getElementById('progressFileName');
    const progressStatus = document.getElementById('progressStatus');
    const progressBar = document.getElementById('progressBar');
    const progressPercent = document.getElementById('progressPercent');
    const progressChunks = document.getElementById('progressChunks');
    const pauseBtn = document.getElementById('pauseBtn');
    const resumeBtn = document.getElementById('resumeBtn');
    const cancelBtn = document.getElementById('cancelBtn');
    const refreshBtn = document.getElementById('refreshBtn');
    const transferList = document.getElementById('transferList');

    // ============================================================
    // File Selection
    // ============================================================

    // Drag & Drop
    dropZone.addEventListener('dragover', (e) => {
        e.preventDefault();
        dropZone.classList.add('drag-over');
    });

    dropZone.addEventListener('dragleave', () => {
        dropZone.classList.remove('drag-over');
    });

    dropZone.addEventListener('drop', (e) => {
        e.preventDefault();
        dropZone.classList.remove('drag-over');
        if (e.dataTransfer.files.length > 0) {
            handleFileSelect(e.dataTransfer.files[0]);
        }
    });

    // Click to browse
    dropZone.addEventListener('click', () => fileInput.click());
    fileInput.addEventListener('change', () => {
        if (fileInput.files.length > 0) {
            handleFileSelect(fileInput.files[0]);
        }
    });

    // Clear button
    clearBtn.addEventListener('click', resetUploadUI);

    async function handleFileSelect(file) {
        selectedFile = file;
        fileHash = '';
        uploadBtn.disabled = true;

        fileName.textContent = file.name;
        fileSize.textContent = formatBytes(file.size);
        fileHash_.textContent = 'Computing SHA-256...';
        fileHash_.style.color = '';

        fileInfo.classList.remove('hidden');
        dropZone.classList.add('hidden');

        // Compute SHA-256 using Web Crypto API
        try {
            fileHash = await computeSHA256(file);
            fileHash_.textContent = fileHash;
            fileHash_.style.color = '#34d399';
            uploadBtn.disabled = false;
        } catch (err) {
            fileHash_.textContent = 'Error computing hash: ' + err.message;
            fileHash_.style.color = '#f87171';
        }
    }

    function resetUploadUI() {
        selectedFile = null;
        fileHash = '';
        currentTransfer = null;
        isPaused = false;
        isCancelled = false;
        uploadedChunkCount = 0;

        fileInfo.classList.add('hidden');
        uploadProgress.classList.add('hidden');
        dropZone.classList.remove('hidden');
        fileInput.value = '';
        uploadBtn.disabled = true;
    }

    // ============================================================
    // SHA-256 Hashing (streaming via Web Crypto API)
    // ============================================================

    async function computeSHA256(file) {
        const BUFFER_SIZE = 2 * 1024 * 1024; // 2 MB read chunks
        const fileStream = file.stream();
        const reader = fileStream.getReader();

        // For large files, use SubtleCrypto digest in streaming manner
        // Web Crypto doesn't support incremental hashing directly, so we
        // read the whole file into an ArrayBuffer (fine up to ~2GB)
        // For very large files, we chunk-hash manually.
        if (file.size <= 100 * 1024 * 1024) {
            // Under 100MB: read all at once
            const buffer = await file.arrayBuffer();
            const hashBuffer = await crypto.subtle.digest('SHA-256', buffer);
            return bufferToHex(hashBuffer);
        }

        // For large files: incremental hashing is not natively supported
        // in Web Crypto, so we use a manual approach
        // Read file in chunks and compute hash
        const chunks = [];
        let done = false;
        while (!done) {
            const result = await reader.read();
            done = result.done;
            if (result.value) {
                chunks.push(result.value);
            }
        }

        const totalLength = chunks.reduce((acc, c) => acc + c.length, 0);
        const combined = new Uint8Array(totalLength);
        let offset = 0;
        for (const chunk of chunks) {
            combined.set(chunk, offset);
            offset += chunk.length;
        }

        const hashBuffer = await crypto.subtle.digest('SHA-256', combined.buffer);
        return bufferToHex(hashBuffer);
    }

    function bufferToHex(buffer) {
        const bytes = new Uint8Array(buffer);
        return Array.from(bytes).map(b => b.toString(16).padStart(2, '0')).join('');
    }

    // ============================================================
    // Upload Flow
    // ============================================================

    uploadBtn.addEventListener('click', startUpload);
    pauseBtn.addEventListener('click', pauseUpload);
    resumeBtn.addEventListener('click', resumeUpload);
    cancelBtn.addEventListener('click', cancelUpload);

    async function startUpload() {
        if (!selectedFile || !fileHash) return;

        const chunkSize = parseInt(chunkSizeSelect.value);
        isPaused = false;
        isCancelled = false;
        uploadedChunkCount = 0;

        // Show progress UI
        fileInfo.classList.add('hidden');
        uploadProgress.classList.remove('hidden');
        progressFileName.textContent = selectedFile.name;
        setProgressStatus('uploading', 'Uploading');

        try {
            // 1. Create transfer on server
            const createResp = await fetch(API_BASE, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    original_name: selectedFile.name,
                    total_size: selectedFile.size,
                    chunk_size: chunkSize,
                    expected_sha256: fileHash
                })
            });

            if (!createResp.ok) {
                const err = await createResp.json();
                throw new Error(err.error || 'Failed to create transfer');
            }

            const { id, token, total_chunks } = await createResp.json();
            currentTransfer = { id, token, totalChunks: total_chunks, chunkSize };

            updateProgress(0, total_chunks);

            // 2. Upload chunks sequentially
            await uploadChunks();

        } catch (err) {
            setProgressStatus('failed', 'Failed');
            progressBar.classList.add('failed');
            alert('Upload error: ' + err.message);
        }
    }

    async function uploadChunks() {
        if (!currentTransfer) return;

        const { id, token, totalChunks, chunkSize } = currentTransfer;

        // Get missing chunks (supports resume)
        let missingChunks;
        try {
            const missingResp = await fetch(`${API_BASE}/${id}/missing`, {
                headers: { 'Authorization': `Bearer ${token}` }
            });
            const missingData = await missingResp.json();
            missingChunks = missingData.missing_chunks || [];
        } catch (err) {
            throw new Error('Failed to get missing chunks: ' + err.message);
        }

        // Update count of already uploaded chunks
        uploadedChunkCount = totalChunks - missingChunks.length;
        updateProgress(uploadedChunkCount, totalChunks);

        // Upload each missing chunk
        for (const chunkIndex of missingChunks) {
            // Check pause/cancel
            if (isCancelled) {
                setProgressStatus('failed', 'Cancelled');
                return;
            }

            while (isPaused) {
                await sleep(200);
                if (isCancelled) {
                    setProgressStatus('failed', 'Cancelled');
                    return;
                }
            }

            // Read chunk from file
            const start = chunkIndex * chunkSize;
            const end = Math.min(start + chunkSize, selectedFile.size);
            const blob = selectedFile.slice(start, end);

            // Upload chunk
            try {
                const uploadResp = await fetch(`${API_BASE}/${id}/chunks/${chunkIndex}`, {
                    method: 'PUT',
                    headers: { 'Authorization': `Bearer ${token}` },
                    body: blob
                });

                if (!uploadResp.ok) {
                    const err = await uploadResp.json();
                    throw new Error(err.error || `Chunk ${chunkIndex} upload failed`);
                }

                uploadedChunkCount++;
                updateProgress(uploadedChunkCount, totalChunks);
            } catch (err) {
                // Network error — allow resume
                setProgressStatus('paused', 'Error — Click Resume');
                isPaused = true;
                pauseBtn.classList.add('hidden');
                resumeBtn.classList.remove('hidden');
                console.error('Chunk upload error:', err);
                return;
            }
        }

        // 3. All chunks uploaded — trigger completion
        setProgressStatus('verifying', 'Verifying');
        try {
            const completeResp = await fetch(`${API_BASE}/${id}/complete`, {
                method: 'POST',
                headers: { 'Authorization': `Bearer ${token}` }
            });

            if (!completeResp.ok) {
                const err = await completeResp.json();
                throw new Error(err.error || 'Completion failed');
            }

            setProgressStatus('completed', 'Completed ✓');
            progressBar.classList.add('completed');
            pauseBtn.classList.add('hidden');
            resumeBtn.classList.add('hidden');
            cancelBtn.classList.add('hidden');

            // Refresh transfer list
            loadTransfers();

        } catch (err) {
            setProgressStatus('failed', 'Verification Failed');
            progressBar.classList.add('failed');
            alert('Verification error: ' + err.message);
        }
    }

    function pauseUpload() {
        isPaused = true;
        setProgressStatus('paused', 'Paused');
        pauseBtn.classList.add('hidden');
        resumeBtn.classList.remove('hidden');
    }

    async function resumeUpload() {
        isPaused = false;
        setProgressStatus('uploading', 'Resuming...');
        resumeBtn.classList.add('hidden');
        pauseBtn.classList.remove('hidden');

        try {
            await uploadChunks();
        } catch (err) {
            setProgressStatus('failed', 'Failed');
            alert('Resume error: ' + err.message);
        }
    }

    function cancelUpload() {
        isCancelled = true;
        isPaused = false;
    }

    // ============================================================
    // Progress UI Helpers
    // ============================================================

    function updateProgress(uploaded, total) {
        const pct = total > 0 ? Math.round((uploaded / total) * 100) : 0;
        progressBar.style.width = pct + '%';
        progressPercent.textContent = pct + '%';
        progressChunks.textContent = `${uploaded} / ${total} chunks`;
    }

    function setProgressStatus(className, text) {
        progressStatus.className = 'status-badge ' + className;
        progressStatus.textContent = text;
    }

    // ============================================================
    // Transfer History
    // ============================================================

    refreshBtn.addEventListener('click', loadTransfers);

    async function loadTransfers() {
        try {
            const resp = await fetch(`${API_BASE}?limit=20`);
            const data = await resp.json();
            renderTransfers(data.transfers || []);
        } catch (err) {
            transferList.innerHTML = '<p class="empty-state">Failed to load transfers.</p>';
        }
    }

    function renderTransfers(transfers) {
        if (transfers.length === 0) {
            transferList.innerHTML = '<p class="empty-state">No transfers yet. Upload a file to get started!</p>';
            return;
        }

        transferList.innerHTML = transfers.map(t => {
            const statusClass = t.status.toLowerCase();
            const isCompleted = t.status === 'Completed';
            const hashDisplay = t.actual_sha256
                ? t.actual_sha256.substring(0, 16) + '...'
                : 'N/A';
            const hashMatch = t.actual_sha256 && t.expected_sha256
                ? t.actual_sha256.toLowerCase() === t.expected_sha256.toLowerCase()
                : null;
            const hashIcon = hashMatch === true ? '✓' : hashMatch === false ? '✗' : '';
            const hashColor = hashMatch === true ? '#34d399' : hashMatch === false ? '#f87171' : '';

            const timeAgo = formatTimeAgo(new Date(t.updated_at));

            return `
                <div class="transfer-item">
                    <div class="transfer-meta">
                        <div class="transfer-name">${escapeHtml(t.original_name)}</div>
                        <div class="transfer-info">
                            <span>${formatBytes(t.total_size)}</span>
                            <span>${t.total_chunks} chunks</span>
                            <span title="${t.actual_sha256 || t.expected_sha256}">
                                SHA-256: <span style="color:${hashColor}">${hashIcon} ${hashDisplay}</span>
                            </span>
                            <span>${timeAgo}</span>
                        </div>
                    </div>
                    <div class="transfer-actions">
                        <span class="status-badge ${statusClass}">${t.status}</span>
                        ${isCompleted
                            ? `<a href="${API_BASE}/${t.id}/download" class="download-link">⬇ Download</a>`
                            : ''}
                    </div>
                </div>
            `;
        }).join('');
    }

    // ============================================================
    // Utility Functions
    // ============================================================

    function formatBytes(bytes) {
        if (bytes === 0) return '0 B';
        const k = 1024;
        const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
        const i = Math.floor(Math.log(bytes) / Math.log(k));
        return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
    }

    function formatTimeAgo(date) {
        const seconds = Math.floor((Date.now() - date.getTime()) / 1000);
        if (seconds < 60) return 'just now';
        if (seconds < 3600) return Math.floor(seconds / 60) + 'm ago';
        if (seconds < 86400) return Math.floor(seconds / 3600) + 'h ago';
        return Math.floor(seconds / 86400) + 'd ago';
    }

    function escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }

    function sleep(ms) {
        return new Promise(resolve => setTimeout(resolve, ms));
    }

    // ============================================================
    // Initial Load
    // ============================================================
    loadTransfers();

})();
