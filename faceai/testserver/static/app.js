const video = document.getElementById('webcam');
const canvas = document.getElementById('overlay');
const ctx = canvas.getContext('2d');
const loader = document.getElementById('loader');
const streamStatus = document.getElementById('stream-status');
const enrollNameInput = document.getElementById('enroll-name');
const enrollBtn = document.getElementById('btn-enroll');
const captureBtn = document.getElementById('btn-capture');
const resetBtn = document.getElementById('btn-reset');
const photoCountEl = document.getElementById('photo-count');
const capturedPhotosEl = document.getElementById('captured-photos');
const enrollMsg = document.getElementById('enroll-message');
const logsContainer = document.getElementById('logs');

let activeStream = null;
let detectionInterval = null;
let capturedPhotos = [];
const MAX_PHOTOS = 5;

// Initialize Webcam
async function initCamera() {
    try {
        const stream = await navigator.mediaDevices.getUserMedia({
            video: {
                width: { ideal: 640 },
                height: { ideal: 480 },
                facingMode: 'user'
            },
            audio: false
        });
        video.srcObject = stream;
        activeStream = stream;
        
        video.onloadedmetadata = () => {
            video.play();
            resizeCanvas();
            loader.style.opacity = '0';
            setTimeout(() => loader.style.display = 'none', 400);
            startDetectionLoop();
        };
    } catch (err) {
        console.error('Error accessing webcam:', err);
        loader.innerHTML = `<p style="color: #ef4444;">Webcam access denied or unavailable.</p>`;
    }
}

function resizeCanvas() {
    canvas.width = video.videoWidth;
    canvas.height = video.videoHeight;
}

window.addEventListener('resize', resizeCanvas);

function getFrameBlob() {
    const tempCanvas = document.createElement('canvas');
    tempCanvas.width = video.videoWidth;
    tempCanvas.height = video.videoHeight;
    const tempCtx = tempCanvas.getContext('2d');
    tempCtx.drawImage(video, 0, 0, tempCanvas.width, tempCanvas.height);
    
    return new Promise((resolve) => {
        tempCanvas.toBlob((blob) => {
            resolve({ blob, dataUrl: tempCanvas.toDataURL('image/jpeg', 0.9) });
        }, 'image/jpeg', 0.9);
    });
}

function startDetectionLoop() {
    if (detectionInterval) clearInterval(detectionInterval);
    
    detectionInterval = setInterval(async () => {
        if (video.paused || video.ended) return;
        
        const { blob } = await getFrameBlob();
        
        try {
            const resp = await fetch('/detect', {
                method: 'POST',
                body: blob,
                headers: { 'Content-Type': 'image/jpeg' }
            });
            
            if (!resp.ok) throw new Error('Detection endpoint error');
            const data = await resp.json();
            drawDetections(data.faces);
        } catch (err) {
            console.error('Detection error:', err);
        }
    }, 400);
}

function drawDetections(faces) {
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    if (!faces || faces.length === 0) return;
    
    if (logsContainer.querySelector('.placeholder')) {
        logsContainer.innerHTML = '';
    }

    faces.forEach((face) => {
        const bbox = face.BBox;
        const width = bbox.MaxX - bbox.MinX;
        const height = bbox.MaxY - bbox.MinY;
        
        // Backend now sends "Person" for unknown faces, and the registered name for known faces
        const isKnown = face.ID !== "";
        const name = face.Name || "Person";
        
        ctx.strokeStyle = isKnown ? '#10b981' : '#ef4444';
        ctx.lineWidth = 3;
        ctx.shadowBlur = 8;
        ctx.shadowColor = isKnown ? 'rgba(16, 185, 129, 0.4)' : 'rgba(239, 68, 68, 0.4)';
        ctx.strokeRect(bbox.MinX, bbox.MinY, width, height);
        
        ctx.fillStyle = isKnown ? 'rgba(16, 185, 129, 0.85)' : 'rgba(239, 68, 68, 0.85)';
        ctx.shadowBlur = 0;
        const labelWidth = Math.max(100, ctx.measureText(name).width + 24);
        ctx.fillRect(bbox.MinX, bbox.MinY - 28, labelWidth, 28);
        
        // Confidence indicator
        if (isKnown) {
            const confPct = Math.round((1 - face.Distance) * 100);
            ctx.fillStyle = 'rgba(255,255,255,0.6)';
            ctx.font = '10px "Outfit", sans-serif';
            ctx.fillText(`confidence ${confPct}%`, bbox.MinX + labelWidth - 8, bbox.MinY - 8);
        }
        
        ctx.fillStyle = '#ffffff';
        ctx.font = 'bold 13px "Outfit", sans-serif';
        ctx.fillText(name, bbox.MinX + 10, bbox.MinY - 9);

        addLogEntry(name, isKnown, face.Distance);
    });
}

function addLogEntry(name, isKnown, distance) {
    const entry = document.createElement('div');
    entry.className = `log-entry ${isKnown ? 'known' : 'unknown'}`;
    const timeStr = new Date().toLocaleTimeString();
    
    let confidence = '';
    if (isKnown) {
        const pct = Math.round((1 - distance) * 100);
        confidence = `<span class="log-confidence">${pct}%</span>`;
    }
    
    entry.innerHTML = `
        <span class="log-status ${isKnown ? 'known-icon' : 'unknown-icon'}">
            ${isKnown ? '&#10003;' : '&#9888;'}
        </span>
        <span class="log-name">${name}</span>
        ${confidence}
        <span class="log-time">${timeStr}</span>
    `;
    logsContainer.insertBefore(entry, logsContainer.firstChild);
    if (logsContainer.children.length > 50) {
        logsContainer.removeChild(logsContainer.lastChild);
    }
}

// Capture photo for enrollment
captureBtn.addEventListener('click', async () => {
    if (capturedPhotos.length >= MAX_PHOTOS) return;
    
    captureBtn.disabled = true;
    const { dataUrl } = await getFrameBlob();
    capturedPhotos.push(dataUrl);
    
    // Add thumbnail
    const thumb = document.createElement('div');
    thumb.className = 'photo-thumb';
    thumb.innerHTML = `<img src="${dataUrl}" alt="Capture ${capturedPhotos.length}"><span>#${capturedPhotos.length}</span>`;
    capturedPhotosEl.appendChild(thumb);
    
    photoCountEl.textContent = capturedPhotos.length;
    captureBtn.disabled = false;
    
    if (capturedPhotos.length >= 1) {
        enrollBtn.disabled = false;
    }
    if (capturedPhotos.length >= MAX_PHOTOS) {
        captureBtn.disabled = true;
        captureBtn.textContent = 'Captures complete';
    }
    
    resetBtn.style.display = 'inline-block';
});

// Reset captured photos
resetBtn.addEventListener('click', () => {
    capturedPhotos = [];
    capturedPhotosEl.innerHTML = '';
    photoCountEl.textContent = '0';
    enrollBtn.disabled = true;
    captureBtn.disabled = false;
    captureBtn.innerHTML = `
        <svg viewBox="0 0 24 24" width="18" height="18" stroke="currentColor" stroke-width="2" fill="none"><path d="M23 19a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4l2-3h6l2 3h4a2 2 0 0 1 2 2z"></path><circle cx="12" cy="13" r="4"></circle></svg>
        Capture
    `;
    resetBtn.style.display = 'none';
});

// Enrollment handler
enrollBtn.addEventListener('click', async () => {
    const name = enrollNameInput.value.trim();
    if (!name) {
        showEnrollMessage('Please enter a valid name.', 'error');
        return;
    }
    if (capturedPhotos.length === 0) {
        showEnrollMessage('Capture at least 1 photo first.', 'error');
        return;
    }
    
    enrollBtn.disabled = true;
    captureBtn.disabled = true;
    enrollBtn.textContent = 'Registering...';
    
    try {
        const resp = await fetch('/enroll', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                name: name,
                images: capturedPhotos
            })
        });
        
        if (resp.ok) {
            const result = await resp.json();
            showEnrollMessage(
                `✓ "${name}" registered with ${result.images_enrolled} photo(s)`,
                'success'
            );
            capturedPhotos = [];
            capturedPhotosEl.innerHTML = '';
            photoCountEl.textContent = '0';
            enrollNameInput.value = '';
            enrollBtn.disabled = true;
            captureBtn.disabled = false;
            captureBtn.innerHTML = `
                <svg viewBox="0 0 24 24" width="18" height="18" stroke="currentColor" stroke-width="2" fill="none"><path d="M23 19a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4l2-3h6l2 3h4a2 2 0 0 1 2 2z"></path><circle cx="12" cy="13" r="4"></circle></svg>
                Capture
            `;
            resetBtn.style.display = 'none';
        } else {
            const errText = await resp.text();
            throw new Error(errText || 'Registration failed');
        }
    } catch (err) {
        showEnrollMessage(err.message, 'error');
    } finally {
        enrollBtn.disabled = capturedPhotos.length === 0;
        captureBtn.disabled = capturedPhotos.length >= MAX_PHOTOS;
        enrollBtn.textContent = 'Register';
    }
});

function showEnrollMessage(text, type) {
    enrollMsg.textContent = text;
    enrollMsg.className = `message ${type}`;
    enrollMsg.style.display = 'block';
    setTimeout(() => {
        enrollMsg.style.display = 'none';
    }, 5000);
}

initCamera();