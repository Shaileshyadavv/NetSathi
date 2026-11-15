// ============================================================
// NetSathi — Interactive Dashboard Engine with Parallel Uploads,
// Real-Time Chunk Matrix, SSE Stream, QR Sharing & Toasts
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
    let allTransfersCache = [];
    let progressEventSource = null;
    let serverLanUrl = '';

    // --- DOM Elements ---
    const dropZone = document.getElementById('dropZone');
    const fileInput = document.getElementById('fileInput');
    const fileInfo = document.getElementById('fileInfo');
    const fileName = document.getElementById('fileName');
    const fileSize = document.getElementById('fileSize');
    const fileTypeIcon = document.getElementById('fileTypeIcon');
    const fileTypeLabel = document.getElementById('fileTypeLabel');
    const fileHash_ = document.getElementById('fileHash');
    const hashCalcStatus = document.getElementById('hashCalcStatus');
    const chunkSizeSelect = document.getElementById('chunkSizeSelect');
    const concurrencySelect = document.getElementById('concurrencySelect');
    const uploadBtn = document.getElementById('uploadBtn');
    const clearBtn = document.getElementById('clearBtn');

    // Progress Panel Elements
    const uploadProgress = document.getElementById('uploadProgress');
    const progressFileName = document.getElementById('progressFileName');
    const progressStatus = document.getElementById('progressStatus');
    const progressBar = document.getElementById('progressBar');
    const progressPercent = document.getElementById('progressPercent');
    const progressSpeed = document.getElementById('progressSpeed');
    const progressEta = document.getElementById('progressEta');
    const progressChunks = document.getElementById('progressChunks');
    const progressTransferredBytes = document.getElementById('progressTransferredBytes');
    const chunkGrid = document.getElementById('chunkGrid');
    const pauseBtn = document.getElementById('pauseBtn');
    const resumeBtn = document.getElementById('resumeBtn');
    const cancelBtn = document.getElementById('cancelBtn');

    // History & Filter Elements
    const refreshBtn = document.getElementById('refreshBtn');
    const transferList = document.getElementById('transferList');
    const searchInput = document.getElementById('searchInput');
    const totalTransfersCount = document.getElementById('totalTransfersCount');

    // Modals & Navigation
    const lanShareBtn = document.getElementById('lanShareBtn');
    const lanModal = document.getElementById('lanModal');
    const closeLanModalBtn = document.getElementById('closeLanModalBtn');
    const lanQrCode = document.getElementById('lanQrCode');
    const lanUrlInput = document.getElementById('lanUrlInput');
    const copyLanUrlBtn = document.getElementById('copyLanUrlBtn');

    const fileShareModal = document.getElementById('fileShareModal');
    const closeFileModalBtn = document.getElementById('closeFileModalBtn');
    const shareModalFileName = document.getElementById('shareModalFileName');
    const fileQrCode = document.getElementById('fileQrCode');
    const fileShareUrlInput = document.getElementById('fileShareUrlInput');
    const copyFileShareUrlBtn = document.getElementById('copyFileShareUrlBtn');

    const serverStatus = document.getElementById('serverStatus');
    const serverStatusText = document.getElementById('serverStatusText');
    const toastContainer = document.getElementById('toastContainer');

    // ============================================================
    // Toast Notification System
    // ============================================================

    function showToast(message, type = 'success') {
        const toast = document.createElement('div');
        toast.className = `toast-item ${type}`;
        const icons = { success: '✓', info: 'ℹ', error: '✕' };
        toast.innerHTML = `<span>${icons[type] || '•'}</span> <span>${escapeHtml(message)}</span>`;
        toastContainer.appendChild(toast);

        setTimeout(() => {
            toast.style.opacity = '0';
            toast.style.transform = 'translateY(10px)';
            toast.style.transition = 'all 0.3s ease';
            setTimeout(() => toast.remove(), 300);
        }, 3200);
    }

    // ============================================================
    // File Type Detection & Icon Mapping
    // ============================================================

    function getFileMeta(fileName) {
        const ext = fileName.split('.').pop().toLowerCase();
        const map = {
            pdf:  { icon: '📄', label: 'PDF Document' },
            doc:  { icon: '📝', label: 'Word Document' },
            docx: { icon: '📝', label: 'Word Document' },
            txt:  { icon: '📝', label: 'Plain Text' },
            zip:  { icon: '📦', label: 'Zip Archive' },
            rar:  { icon: '📦', label: 'Archive' },
            tar:  { icon: '📦', label: 'Tarball' },
            gz:   { icon: '📦', label: 'Compressed' },
            iso:  { icon: '💿', label: 'Disk Image' },
            png:  { icon: '🖼️', label: 'PNG Image' },
            jpg:  { icon: '🖼️', label: 'JPEG Image' },
            jpeg: { icon: '🖼️', label: 'JPEG Image' },
            gif:  { icon: '🖼️', label: 'GIF Image' },
            mp4:  { icon: '🎬', label: 'MP4 Video' },
            mkv:  { icon: '🎬', label: 'MKV Video' },
            mov:  { icon: '🎬', label: 'QuickTime Video' },
            mp3:  { icon: '🎵', label: 'MP3 Audio' },
            wav:  { icon: '🎵', label: 'WAV Audio' },
            go:   { icon: '💻', label: 'Go Source' },
            js:   { icon: '💻', label: 'JavaScript' },
            json: { icon: '⚙️', label: 'JSON Data' },
            exe:  { icon: '⚙️', label: 'Windows Executable' },
            bin:  { icon: '⚙️', label: 'Binary Data' }
        };
        return map[ext] || { icon: '📁', label: 'File' };
    }

    // ============================================================
    // File Selection & Drag-and-Drop
    // ============================================================

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

    dropZone.addEventListener('click', () => fileInput.click());
    fileInput.addEventListener('change', () => {
        if (fileInput.files.length > 0) {
            handleFileSelect(fileInput.files[0]);
        }
    });

    clearBtn.addEventListener('click', resetUploadUI);

    async function handleFileSelect(file) {
        selectedFile = file;
        fileHash = '';
        uploadBtn.disabled = true;

        const meta = getFileMeta(file.name);
        fileTypeIcon.textContent = meta.icon;
        fileTypeLabel.textContent = meta.label;
        fileName.textContent = file.name;
        fileSize.textContent = formatBytes(file.size);

        fileHash_.textContent = 'Streaming Web Crypto SHA-256 calculation...';
        hashCalcStatus.className = 'hash-tag calculating';
        hashCalcStatus.textContent = 'Computing...';

        fileInfo.classList.remove('hidden');
        dropZone.classList.add('hidden');

        try {
            const startT = performance.now();
            fileHash = await computeSHA256(file);
            const duration = Math.round(performance.now() - startT);

            fileHash_.textContent = fileHash;
            hashCalcStatus.className = 'hash-tag ready';
            hashCalcStatus.textContent = `Ready (${duration}ms)`;
            uploadBtn.disabled = false;
            showToast(`SHA-256 computed for ${file.name}`, 'info');
        } catch (err) {
            fileHash_.textContent = 'Error computing hash: ' + err.message;
            hashCalcStatus.className = 'hash-tag error';
            hashCalcStatus.textContent = 'Hashing Error';
            showToast('Failed to calculate file hash', 'error');
        }
    }

    function resetUploadUI() {
        selectedFile = null;
        fileHash = '';
        currentTransfer = null;
        isPaused = false;
        isCancelled = false;
        uploadedChunkCount = 0;
        closeProgressStream();

        fileInfo.classList.add('hidden');
        uploadProgress.classList.add('hidden');
        dropZone.classList.remove('hidden');
        fileInput.value = '';
        uploadBtn.disabled = true;
    }

    // ============================================================
    // SHA-256 Hashing (Streaming via Web Crypto API)
    // ============================================================

    async function computeSHA256(file) {
        if (file.size <= 100 * 1024 * 1024) {
            const buffer = await file.arrayBuffer();
            const hashBuffer = await crypto.subtle.digest('SHA-256', buffer);
            return bufferToHex(hashBuffer);
        }

        const fileStream = file.stream();
        const reader = fileStream.getReader();
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
    // Chunk Visualizer Grid Initialization
    // ============================================================

    function initChunkGrid(totalChunks) {
        chunkGrid.innerHTML = '';
        const maxDisplay = 100;
        const count = Math.min(totalChunks, maxDisplay);

        for (let i = 0; i < count; i++) {
            const tile = document.createElement('div');
            tile.id = `chunk-tile-${i}`;
            tile.className = 'matrix-block';
            tile.textContent = i;
            tile.title = `Chunk ${i}`;
            chunkGrid.appendChild(tile);
        }

        if (totalChunks > maxDisplay) {
            const overflow = document.createElement('div');
            overflow.className = 'matrix-block';
            overflow.textContent = `+${totalChunks - maxDisplay}`;
            overflow.title = `${totalChunks - maxDisplay} more chunks`;
            chunkGrid.appendChild(overflow);
        }
    }

    function setChunkTileState(index, state) {
        const tile = document.getElementById(`chunk-tile-${index}`);
        if (tile) {
            tile.className = `matrix-block ${state}`;
        }
    }

    // ============================================================
    // Upload Flow with Parallel Worker Pool
    // ============================================================

    uploadBtn.addEventListener('click', startUpload);
    pauseBtn.addEventListener('click', pauseUpload);
    resumeBtn.addEventListener('click', resumeUpload);
    cancelBtn.addEventListener('click', cancelUpload);

    async function startUpload() {
        if (!selectedFile || !fileHash) return;

        const chunkSize = parseInt(chunkSizeSelect.value, 10);
        isPaused = false;
        isCancelled = false;
        uploadedChunkCount = 0;

        fileInfo.classList.add('hidden');
        uploadProgress.classList.remove('hidden');
        progressFileName.textContent = selectedFile.name;
        setProgressStatus('uploading', 'Uploading');
        progressBar.className = 'progress-bar';

        try {
            // 1. Create session on server
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
                const errData = await createResp.json();
                throw new Error(errData.error || 'Failed to create transfer session');
            }

            const { id, token, total_chunks } = await createResp.json();
            currentTransfer = { id, token, totalChunks: total_chunks, chunkSize };

            initChunkGrid(total_chunks);
            updateProgress(0, total_chunks, 0);

            // Open Server-Sent Events stream for push updates
            openProgressStream(id, token);

            // 2. Upload missing chunks using parallel worker pool
            await uploadChunks();

        } catch (err) {
            setProgressStatus('failed', 'Failed');
            progressBar.classList.add('failed');
            showToast('Upload error: ' + err.message, 'error');
        }
    }

    async function uploadChunks() {
        if (!currentTransfer) return;

        const { id, token, totalChunks, chunkSize } = currentTransfer;

        let missingChunks;
        try {
            const missingResp = await fetch(`${API_BASE}/${id}/missing`, {
                headers: { 'Authorization': `Bearer ${token}` }
            });
            const missingData = await missingResp.json();
            missingChunks = missingData.missing_chunks || [];
        } catch (err) {
            throw new Error('Failed to query missing chunks: ' + err.message);
        }

        // Mark previously confirmed chunks as done
        const missingSet = new Set(missingChunks);
        for (let i = 0; i < totalChunks; i++) {
            if (!missingSet.has(i)) {
                setChunkTileState(i, 'done');
            }
        }

        uploadedChunkCount = totalChunks - missingChunks.length;
        updateProgress(uploadedChunkCount, totalChunks, uploadedChunkCount * chunkSize);

        const uploadStartTime = performance.now();
        let bytesTransferredSession = 0;
        const concurrency = Math.max(1, parseInt(concurrencySelect?.value || '1', 10));

        const queue = [...missingChunks];
        let hasError = false;

        async function uploadSingleChunk(chunkIndex) {
            setChunkTileState(chunkIndex, 'active');

            const start = chunkIndex * chunkSize;
            const end = Math.min(start + chunkSize, selectedFile.size);
            const blob = selectedFile.slice(start, end);

            const chunkBuffer = await blob.arrayBuffer();
            const chunkHashBuffer = await crypto.subtle.digest('SHA-256', chunkBuffer);
            const chunkHashHex = bufferToHex(chunkHashBuffer);

            const uploadResp = await fetch(`${API_BASE}/${id}/chunks/${chunkIndex}`, {
                method: 'PUT',
                headers: {
                    'Authorization': `Bearer ${token}`,
                    'X-Chunk-SHA256': chunkHashHex
                },
                body: chunkBuffer
            });

            if (!uploadResp.ok) {
                const errData = await uploadResp.json();
                setChunkTileState(chunkIndex, 'gray');
                throw new Error(errData.error || `Chunk ${chunkIndex} upload rejected`);
            }

            setChunkTileState(chunkIndex, 'done');
            uploadedChunkCount++;
            bytesTransferredSession += blob.size;

            const elapsedSeconds = (performance.now() - uploadStartTime) / 1000;
            if (elapsedSeconds > 0.05) {
                const speed = bytesTransferredSession / elapsedSeconds;
                progressSpeed.textContent = '⚡ ' + formatSpeed(speed);

                const remainingChunks = totalChunks - uploadedChunkCount;
                const remainingBytes = Math.min(remainingChunks * chunkSize, selectedFile.size - (uploadedChunkCount * chunkSize));
                const etaSeconds = speed > 0 ? Math.max(0, Math.round(remainingBytes / speed)) : 0;
                progressEta.textContent = '⏱ ETA: ' + formatDuration(etaSeconds);
            }

            updateProgress(uploadedChunkCount, totalChunks, uploadedChunkCount * chunkSize);
        }

        async function worker() {
            while (queue.length > 0 && !hasError && !isCancelled) {
                while (isPaused) {
                    await sleep(200);
                    if (isCancelled || hasError) return;
                }
                const chunkIndex = queue.shift();
                if (chunkIndex === undefined) return;

                try {
                    await uploadSingleChunk(chunkIndex);
                } catch (err) {
                    hasError = true;
                    queue.unshift(chunkIndex);
                    setProgressStatus('paused', 'Error — Click Resume');
                    isPaused = true;
                    pauseBtn.classList.add('hidden');
                    resumeBtn.classList.remove('hidden');
                    console.error('Parallel chunk error:', err);
                    showToast(`Chunk ${chunkIndex} failed: ${err.message}`, 'error');
                    return;
                }
            }
        }

        const workerCount = Math.min(concurrency, missingChunks.length);
        const workers = Array.from({ length: workerCount }, () => worker());
        await Promise.all(workers);

        if (hasError || isCancelled || isPaused) {
            return;
        }

        // 3. Trigger assembly and SHA-256 verification
        progressEta.textContent = '⏱ ETA: 0s';
        setProgressStatus('verifying', 'Verifying SHA-256...');
        try {
            const completeResp = await fetch(`${API_BASE}/${id}/complete`, {
                method: 'POST',
                headers: { 'Authorization': `Bearer ${token}` }
            });

            if (!completeResp.ok) {
                const errData = await completeResp.json();
                throw new Error(errData.error || 'Server verification failed');
            }

            setProgressStatus('completed', 'Verified ✓ (Server Confirmed)');
            progressBar.classList.add('completed');
            progressSpeed.textContent = '⚡ Done';
            pauseBtn.classList.add('hidden');
            resumeBtn.classList.add('hidden');
            cancelBtn.classList.add('hidden');

            showToast('Transfer complete & verified bit-for-bit!', 'success');
            loadTransfers();

        } catch (err) {
            setProgressStatus('failed', 'Verification Mismatch');
            progressBar.classList.add('failed');
            showToast('Integrity verification failed: ' + err.message, 'error');
        }
    }

    function pauseUpload() {
        isPaused = true;
        setProgressStatus('paused', 'Paused');
        pauseBtn.classList.add('hidden');
        resumeBtn.classList.remove('hidden');
        showToast('Transfer paused. Click Resume anytime.', 'info');
    }

    async function resumeUpload() {
        isPaused = false;
        setProgressStatus('uploading', 'Resuming...');
        resumeBtn.classList.add('hidden');
        pauseBtn.classList.remove('hidden');
        showToast('Resuming from confirmed chunks...', 'info');

        try {
            await uploadChunks();
        } catch (err) {
            setProgressStatus('failed', 'Failed');
            showToast('Resume failed: ' + err.message, 'error');
        }
    }

    function cancelUpload() {
        isCancelled = true;
        isPaused = false;
        closeProgressStream();
        resetUploadUI();
        showToast('Transfer cancelled.', 'info');
    }

    function updateProgress(uploaded, total, bytesUploaded = 0) {
        const pct = total > 0 ? Math.round((uploaded / total) * 100) : 0;
        progressBar.style.width = pct + '%';
        progressPercent.textContent = pct + '%';
        progressChunks.textContent = `${uploaded} / ${total} chunks`;
        if (selectedFile) {
            const currBytes = Math.min(bytesUploaded, selectedFile.size);
            progressTransferredBytes.textContent = `${formatBytes(currBytes)} of ${formatBytes(selectedFile.size)}`;
        }
    }

    function setProgressStatus(className, text) {
        progressStatus.className = 'status-badge ' + className;
        progressStatus.textContent = text;
    }

    // ============================================================
    // Server-Sent Events (SSE) Stream
    // ============================================================

    function openProgressStream(transferId, token) {
        closeProgressStream();

        const url = `${API_BASE}/${transferId}/progress/stream?token=${encodeURIComponent(token)}`;
        const es = new EventSource(url);

        es.addEventListener('progress', (e) => {
            try {
                const data = JSON.parse(e.data);
                updateProgress(data.received_chunks, data.transfer.total_chunks, data.received_bytes);
            } catch (_) {}
        });

        es.addEventListener('completed', (e) => {
            closeProgressStream();
            setProgressStatus('completed', 'Verified ✓ (Server Confirmed)');
            progressBar.classList.add('completed');
            progressSpeed.textContent = '⚡ Done';
            loadTransfers();
        });

        es.addEventListener('failed', (e) => {
            closeProgressStream();
            setProgressStatus('failed', 'Failed');
            progressBar.classList.add('failed');
        });

        es.onerror = () => {
            closeProgressStream();
        };

        progressEventSource = es;
    }

    function closeProgressStream() {
        if (progressEventSource) {
            progressEventSource.close();
            progressEventSource = null;
        }
    }

    // ============================================================
    // Transfer History, Search Filter & Actions
    // ============================================================

    refreshBtn.addEventListener('click', () => {
        loadTransfers();
        showToast('Transfers refreshed', 'info');
    });

    searchInput.addEventListener('input', () => {
        renderTransfers(allTransfersCache);
    });

    async function loadTransfers() {
        try {
            const resp = await fetch(`${API_BASE}?limit=50`);
            const data = await resp.json();
            allTransfersCache = data.transfers || [];
            totalTransfersCount.textContent = allTransfersCache.length;
            renderTransfers(allTransfersCache);
        } catch (err) {
            transferList.innerHTML = '<div class="empty-state"><p>Unable to load transfer history.</p></div>';
        }
    }

    function getReachableBaseUrl() {
        if (window.location.hostname !== 'localhost' && window.location.hostname !== '127.0.0.1') {
            return window.location.origin;
        }
        return serverLanUrl || window.location.origin;
    }

    function renderTransfers(transfers) {
        const query = (searchInput.value || '').trim().toLowerCase();
        const filtered = transfers.filter(t => t.original_name.toLowerCase().includes(query));

        if (filtered.length === 0) {
            transferList.innerHTML = `
                <div class="empty-state">
                    <div class="empty-icon">${query ? '🔍' : '📂'}</div>
                    <p>${query ? 'No matching transfers found.' : 'No transfers yet. Drop a file above to begin!'}</p>
                </div>
            `;
            return;
        }

        transferList.innerHTML = filtered.map(t => {
            const statusClass = t.status.toLowerCase();
            const isCompleted = t.status === 'Completed';
            const meta = getFileMeta(t.original_name);
            const timeAgo = formatTimeAgo(new Date(t.updated_at));

            const hashDisplay = t.actual_sha256
                ? t.actual_sha256.substring(0, 14) + '...'
                : 'Pending';

            // Absolute download link (works across local LAN)
            const downloadUrl = `${getReachableBaseUrl()}${API_BASE}/${t.id}/download`;

            return `
                <div class="transfer-card">
                    <div class="transfer-card-left">
                        <div class="transfer-file-badge">${meta.icon}</div>
                        <div class="transfer-card-info">
                            <div class="transfer-title-line">
                                <span class="transfer-filename" title="${escapeHtml(t.original_name)}">${escapeHtml(t.original_name)}</span>
                                <span class="status-badge-live badge-${statusClass}">${t.status}</span>
                            </div>
                            <div class="transfer-meta-chips">
                                <span>📊 ${formatBytes(t.total_size)}</span>
                                <span>🧩 ${t.total_chunks} chunks</span>
                                <span class="chip-hash mono" title="Click to copy SHA-256" onclick="copyHash('${t.actual_sha256 || t.expected_sha256}')">
                                    🔒 SHA: ${hashDisplay}
                                </span>
                                <span>⏱ ${timeAgo}</span>
                            </div>
                        </div>
                    </div>
                    <div class="transfer-card-actions">
                        ${isCompleted ? `
                            <a href="${downloadUrl}" class="btn btn-sm btn-download-action" download="${escapeHtml(t.original_name)}">
                                ⬇ Download
                            </a>
                            <button class="btn btn-sm btn-secondary" onclick="copyShareLink('${downloadUrl}')" title="Copy direct download link">
                                📋 Copy Link
                            </button>
                            <button class="btn btn-sm btn-qr-action" onclick="openFileShareModal('${escapeHtml(t.original_name)}', '${downloadUrl}')" title="Scan QR Code to download on mobile">
                                📱 QR Code
                            </button>
                        ` : ''}
                    </div>
                </div>
            `;
        }).join('');
    }

    // Expose functions to window for onclick handlers
    window.copyHash = function(hash) {
        if (!hash) return;
        navigator.clipboard.writeText(hash).then(() => {
            showToast('SHA-256 hash copied to clipboard!', 'success');
        });
    };

    window.copyShareLink = function(url) {
        let reachableUrl = url;
        if (url.startsWith('http://localhost') || url.startsWith('http://127.0.0.1')) {
            const baseUrl = getReachableBaseUrl();
            reachableUrl = url.replace(/^http:\/\/(localhost|127\.0\.0\.1)(:\d+)?/, baseUrl);
        }
        navigator.clipboard.writeText(reachableUrl).then(() => {
            showToast('Download link copied! Paste anywhere on your Wi-Fi.', 'success');
        });
    };

    window.openFileShareModal = function(name, url) {
        let reachableUrl = url;
        if (url.startsWith('http://localhost') || url.startsWith('http://127.0.0.1')) {
            const baseUrl = getReachableBaseUrl();
            reachableUrl = url.replace(/^http:\/\/(localhost|127\.0\.0\.1)(:\d+)?/, baseUrl);
        }
        shareModalFileName.textContent = `Share "${name}"`;
        fileShareUrlInput.value = reachableUrl;
        fileQrCode.innerHTML = `<img src="https://api.qrserver.com/v1/create-qr-code/?size=180x180&data=${encodeURIComponent(reachableUrl)}" alt="QR Code" width="180" height="180">`;
        fileShareModal.classList.remove('hidden');
    };

    closeFileModalBtn.addEventListener('click', () => fileShareModal.classList.add('hidden'));
    copyFileShareUrlBtn.addEventListener('click', () => {
        navigator.clipboard.writeText(fileShareUrlInput.value).then(() => {
            showToast('File link copied to clipboard!', 'success');
        });
    });

    // ============================================================
    // Connect Device / LAN Share Modal
    // ============================================================

    lanShareBtn.addEventListener('click', () => {
        const lanUrl = getReachableBaseUrl();
        lanUrlInput.value = lanUrl;
        lanQrCode.innerHTML = `<img src="https://api.qrserver.com/v1/create-qr-code/?size=180x180&data=${encodeURIComponent(lanUrl)}" alt="LAN QR Code" width="180" height="180">`;
        lanModal.classList.remove('hidden');
    });

    closeLanModalBtn.addEventListener('click', () => lanModal.classList.add('hidden'));
    copyLanUrlBtn.addEventListener('click', () => {
        navigator.clipboard.writeText(lanUrlInput.value).then(() => {
            showToast('Dashboard address copied!', 'success');
        });
    });

    // Close modals on backdrop click
    window.addEventListener('click', (e) => {
        if (e.target === lanModal) lanModal.classList.add('hidden');
        if (e.target === fileShareModal) fileShareModal.classList.add('hidden');
    });

    // ============================================================
    // Server Health Probe
    // ============================================================

    async function checkServerHealth() {
        try {
            const resp = await fetch('/healthz');
            if (resp.ok) {
                const data = await resp.json();
                if (data.lan_url) {
                    serverLanUrl = data.lan_url;
                }
                serverStatus.className = 'status-pill status-online';
                serverStatusText.textContent = 'Server Online';
            } else {
                throw new Error();
            }
        } catch (_) {
            serverStatus.className = 'status-pill status-paused';
            serverStatusText.textContent = 'Offline / Connecting';
        }
    }

    // ============================================================
    // Utility Helpers
    // ============================================================

    function formatBytes(bytes) {
        if (bytes === 0) return '0 B';
        const k = 1024;
        const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
        const i = Math.floor(Math.log(bytes) / Math.log(k));
        return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
    }

    function formatSpeed(bytesPerSec) {
        if (!bytesPerSec || bytesPerSec <= 0) return '0 KB/s';
        if (bytesPerSec < 1024) return Math.round(bytesPerSec) + ' B/s';
        if (bytesPerSec < 1024 * 1024) return (bytesPerSec / 1024).toFixed(1) + ' KB/s';
        return (bytesPerSec / (1024 * 1024)).toFixed(2) + ' MB/s';
    }

    function formatDuration(totalSeconds) {
        if (totalSeconds <= 0) return '0s';
        if (totalSeconds < 60) return totalSeconds + 's';
        const mins = Math.floor(totalSeconds / 60);
        const secs = totalSeconds % 60;
        return `${mins}m ${secs}s`;
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
        div.textContent = text || '';
        return div.innerHTML;
    }

    function sleep(ms) {
        return new Promise(resolve => setTimeout(resolve, ms));
    }

    // ============================================================
    // Initialize Dashboard
    // ============================================================
    checkServerHealth();
    loadTransfers();
    setInterval(checkServerHealth, 15000);

})();
