// ============================================================
//  OS I/O Simulator — gRPC-Web Client + Diff-Driven Render
// ============================================================

// ---- State ----
let streamHandle = null;
let prevSnap = null;
let autoTimer = null;
let autoPaused = false;
let stepCount = 0;
let phase = 'idle'; // 'idle' | 'request' | 'hardware' | 'return' | 'complete'

// 自动连点速度档位 (ms)
const AUTO_SPEEDS = [10, 25, 50, 100, 200, 500];
const AUTO_DEFAULT_IDX = 2; // 50ms
let autoSpeedIdx = AUTO_DEFAULT_IDX;

const phaseMeta = {
    idle:     { label: '等待初始化',        icon: '⏳', cls: '' },
    request:  { label: 'IRP ↓ 下行请求',    icon: '⬇', cls: 'phase-request' },
    hardware: { label: '💾 硬件磁盘读取',    icon: '⏺', cls: 'phase-hardware' },
    return:   { label: 'DATA ↑ 数据返回',    icon: '⬆', cls: 'phase-return' },
    complete: { label: '✅ 完成',            icon: '✓', cls: 'phase-complete' }
};

// ---- DOM refs ----
const $ = (id) => document.getElementById(id);
const btnInit  = $('btn-init');
const btnReset = $('btn-reset');
const btnStep  = $('btn-step');
const btnAuto  = $('btn-autoplay');
const selUser  = $('sel-user');
const cfgPath  = $('cfg-path');
const cfgBytes = $('cfg-bytes');
const cfgAddr  = $('cfg-addr');
const cfgDblBuf = $('cfg-dblbuf');
const cfgCache  = $('cfg-cache');
const cfgFault  = $('cfg-fault');
const cfgHost   = $('cfg-host');
const autoSpeedSlider = $('auto-speed');
const autoSpeedGroup  = $('auto-speed-group');
const speedVal        = $('speed-val');
const connStatus = $('conn-status');
const valBytes   = $('val-bytes');
const stepLog    = $('step-log');
const canvas = $('particle-canvas');
const ctx = canvas.getContext('2d');

// ---- Connect & Init 合并逻辑 ----
function triggerInit() {
    prevSnap = null; stepCount = 0; phase = 'request';
    updatePhaseBadge();
    stepLog.innerHTML = '';
    $('err-msg').textContent = 'SUCCESS'; $('err-msg').className = '';
    $('ubuf-display').textContent = ''; $('ubuf-len').textContent = '0';
    $('pbar').style.width = '0%'; $('chunk-stat').textContent = '0 / 1';
    clearAutoPlay();
    updateSpeedDisplay();
    btnStep.disabled = false;

    const addrStr = cfgAddr.value;
    const addr = addrStr.startsWith('0x') ? parseInt(addrStr, 16) : parseInt(addrStr, 10);
    const config = window.IOSim.newReadConfig(
        cfgPath.value, parseInt(cfgBytes.value), addr, cfgDblBuf.checked, cfgCache.checked
    );
    
    const userCtx = selUser.value === 'root' 
        ? window.IOSim.newUserContext(0, 0, 'root', '/root')
        : window.IOSim.newUserContext(1000, 1000, 'user1', '/home/user1');

    const cmd = window.IOSim.newInitCommand(config, userCtx);
    send(cmd); // INIT 先发，确保引擎已创建
    const faultVal = parseInt(cfgFault.value);
    if (faultVal > 0) {
        send(window.IOSim.newInjectFaultCommand(faultVal)); // 再注入故障
    }
    log(`INIT: ${cfgPath.value} | user=${selUser.value} | dblbuf=${cfgDblBuf.checked}`);
}

function updateInitButton() {
    const connected = streamHandle && connStatus.textContent.includes('Connected');
    // INIT: always shows as connect/init (clean start)
    btnInit.textContent = connected ? '● 重新连接' : '● 连接并初始化';
    // RESET: only visible when connected, preserves cache
    if (btnReset) {
        btnReset.classList.toggle('hidden', !connected);
    }
}

function connectAndInit() {
    const host = cfgHost.value.trim() || window.location.origin;

    // 始终建立新连接 = 全新引擎（不保留旧缓存）
    if (streamHandle) { try { streamHandle.close(); } catch(e) {} }
    streamHandle = null;
    prevSnap = null;

    log(`正在连接后端: ${host}...`);
    connStatus.textContent = '连接中...';
    connStatus.className = 'conn-status disconnected';

    try {
        streamHandle = window.IOSim.connect(host);

        streamHandle.onOpen(function () {
            connStatus.textContent = '● Connected';
            connStatus.className = 'conn-status connected';
            updateInitButton();
            log('握手完成，开始全新初始化...');
            triggerInit();
        });
        streamHandle.onSnapshot(onSnapshot);
        streamHandle.onError(function (e) {
            log('gRPC error: ' + (e.message || e));
            connStatus.textContent = '● Error';
            connStatus.className = 'conn-status error';
            btnStep.disabled = true;
            updateInitButton();
        });
        streamHandle.onEnd(function () {
            connStatus.textContent = '● Disconnected';
            connStatus.className = 'conn-status disconnected';
            btnStep.disabled = true;
            updateInitButton();
        });
    } catch (e) {
        log('Connection failed: ' + e.message);
        connStatus.textContent = '● Disconnected';
        updateInitButton();
    }
}

// 重置：复用当前 stream，后端 simEngine 仍在 → 自动继承旧页缓存
function triggerReset() {
    if (!streamHandle || !connStatus.textContent.includes('Connected')) {
        log('尚未连接，请先点击「连接并初始化」');
        return;
    }
    clearAutoPlay();
    log('重新初始化（保留页缓存）...');
    triggerInit();
}

function send(cmd) {
    if (!streamHandle) return;
    try { streamHandle.send(cmd); } catch (e) { log(`Write error: ${e.message}`); }
}

function doStep() {
    send(window.IOSim.newStepCommand());
}

// ---- Phase Tracking ----
function updatePhaseBadge() {
    const badge = $('phase-badge');
    if (!badge) return;
    const meta = phaseMeta[phase] || phaseMeta.idle;
    badge.textContent = meta.icon + ' ' + meta.label;
    badge.className = 'phase-badge header-badge ' + meta.cls;
}

function detectPhase(snap, prev) {
    const curLayer = snap.currentActiveLayer;
    const prevLayer = prev ? prev.currentActiveLayer : null;

    // INIT → request
    if (!prev) { phase = 'request'; updatePhaseBadge(); return; }

    // DRV(2) → HW(4): entering hardware phase
    if (prevLayer === 2 && curLayer === 4) {
        phase = 'hardware'; updatePhaseBadge(); return;
    }
    // HW(4) → INT(3): entering return phase (data coming back up!)
    if (prevLayer === 4 && curLayer === 3) {
        phase = snap.isFinished ? 'complete' : 'return';
        updatePhaseBadge(); return;
    }
    // INT(3) → DRV(2): ping-pong, still in return phase
    if (prevLayer === 3 && curLayer === 2) {
        // phase stays 'return'
        return;
    }

    // Simulation finished
    if (snap.isFinished) {
        phase = 'complete'; updatePhaseBadge(); return;
    }

    updatePhaseBadge();
}

// ---- Snapshot Render ----
const layerMap = { 0:'USER', 1:'VFS', 2:'DRV', 4:'HW', 3:'INT' };

function onSnapshot(snapMsg) {
    const snap = snapMsg.toObject();

    // 跳过 INIT 响应的初始状态快照 (subStep=0 表示尚未开始执行)
    const isInitSnapshot = !prevSnap && snap.subStep === 0;
    if (!isInitSnapshot) {
        stepCount++;
        detectPhase(snap, prevSnap);

        // 子步骤日志格式: [S3.2] (VFS 2/4) description...
        const subInfo = (snap.totalSubSteps > 1)
            ? ` (${snap.subStep}/${snap.totalSubSteps})`
            : '';
        log(`[S${stepCount}${subInfo}] ${(snap.stepDescription || '').substring(0, 80)}`);
    }

    const activeLayer = layerMap[snap.currentActiveLayer] || 'USER';

    // 逐层接力：活动层发生切换时，给刚离开的层挂一段渐隐余辉 → “脉冲穿过”观感
    const prevActiveLayer = prevSnap ? (layerMap[prevSnap.currentActiveLayer] || 'USER') : null;
    if (prevActiveLayer && prevActiveLayer !== activeLayer) {
        const leftCard = $(`layer-${prevActiveLayer}`);
        if (leftCard) {
            leftCard.classList.add('relay-trail');
            setTimeout(() => leftCard.classList.remove('relay-trail'), 600);
        }
    }

    // 清除所有层卡片状态，高亮当前活动层
    document.querySelectorAll('.layer-card').forEach(el => el.classList.remove('active','error','phase-request','phase-return'));
    const card = $(`layer-${activeLayer}`);
    if (card) {
        card.classList.add('active');
        // 按阶段附加颜色类：下行青蓝色 / 上行钴蓝色
        if (phase === 'return') {
            card.classList.add('phase-return');
        } else {
            card.classList.add('phase-request');
        }
        if (snap.isFinished && snap.finalErrorCode !== 'SUCCESS') card.classList.add('error');
        // 更新活动层卡片的子步骤指示器
        const stepsEl = card.querySelector('.layer-steps');
        if (stepsEl) {
            if (snap.totalSubSteps > 1) {
                stepsEl.textContent = snap.subStep + '/' + snap.totalSubSteps + ' 步';
            }
        }
    }

    // ── 连接器高亮：按阶段区分下行 IRP↓ / 上行 DATA↑ ──
    document.querySelectorAll('.connector').forEach(el => el.classList.remove('active'));
    const prevLayer = prevSnap ? layerMap[prevSnap.currentActiveLayer] : null;

    // 安全获取 SVG 标签元素 (foreignObject)
    const irpLabel = document.getElementById('irp-label');
    const irqLabel = document.getElementById('irq-label');
    const dataLabel = document.getElementById('data-label');

    if (phase === 'return') {
        // 上行返回阶段：点亮 IRQ↑ 和 DATA↑ 返回路径
        if (activeLayer === 'INT') {
            const el = $('conn-h-i'); if (el) el.classList.add('active');
        }
        if (activeLayer === 'DRV' && prevLayer === 'INT') {
            const el = $('conn-i-d'); if (el) el.classList.add('active');
        }
        if (activeLayer === 'HW') {
            const el = $('conn-d-h'); if (el) el.classList.add('active');
        }
        document.querySelectorAll('.connector.return-path').forEach(function(el) {
            el.classList.add('active');
        });
        if (dataLabel) dataLabel.style.opacity = '1';
        if (irqLabel) irqLabel.style.opacity = '1';
        if (irpLabel) irpLabel.style.opacity = '0.3';
    } else if (phase === 'hardware') {
        const el = $('conn-d-h'); if (el) el.classList.add('active');
        if (irpLabel) irpLabel.style.opacity = '1';
        if (irqLabel) irqLabel.style.opacity = '0.3';
        if (dataLabel) dataLabel.style.opacity = '0.3';
    } else {
        const connMap = { 'VFS':'conn-u-v', 'DRV':'conn-v-d', 'HW':'conn-d-h' };
        if (connMap[activeLayer]) { const el = $(connMap[activeLayer]); if (el) el.classList.add('active'); }
        if (irpLabel) irpLabel.style.opacity = '1';
        if (irqLabel) irqLabel.style.opacity = '0.3';
        if (dataLabel) dataLabel.style.opacity = '0.3';
        const cid = $('conn-i-d'); if (cid) cid.classList.remove('active');
    }

    // 数据返回路径：当用户缓冲区实际收到数据时增强显示
    const hasData = snap.memoryState && snap.memoryState.userBufferData && snap.memoryState.userBufferData.length > 0;
    if (hasData && phase === 'return') {
        // 数据已到达，返回路径额外脉冲动画
        document.querySelectorAll('.connector.return-path').forEach(function(el) {
            el.style.filter = 'url(#glow-active)';
        });
    } else {
        document.querySelectorAll('.connector.return-path').forEach(function(el) {
            el.style.filter = '';
        });
    }

    if (snap.stepDescription) {
        const vd = $('vfs-desc'); if (vd) vd.textContent = snap.stepDescription.substring(0, 60);
    }

    if (snap.processState) {
        const pp = $('proc-pid'); if (pp) pp.textContent = snap.processState.pid;
        const st = snap.processState.state;
        const el = $('proc-state');
        if (el) {
            el.className = 'badge';
            if (st === 0) { el.textContent = 'RUNNING'; el.classList.add('badge-running'); }
            else if (st === 1) { el.textContent = 'BLOCKED'; el.classList.add('badge-blocked'); }
            else { el.textContent = 'READY'; el.classList.add('badge-ready'); }
        }
    }

    if (snap.memoryState) {
        const mem = snap.memoryState;
        // 页缓存状态
        const cacheEl = $('cache-status');
        if (cacheEl) {
            if (mem.cacheHit) {
                cacheEl.textContent = '✅ 命中';
                cacheEl.className = 'cache-badge cache-hit';
            } else if (mem.cachedPages > 0) {
                cacheEl.textContent = '未命中 (' + mem.cachedPages + ' 页)';
                cacheEl.className = 'cache-badge cache-miss';
            } else if (cfgCache.checked) {
                cacheEl.textContent = '空 (0 页)';
                cacheEl.className = 'cache-badge cache-empty';
            } else {
                cacheEl.textContent = '已禁用';
                cacheEl.className = 'cache-badge cache-off';
            }
        }

        updateKbuf($('kbuf1'), 1, mem);
        updateKbuf($('kbuf2'), 2, mem);

        if (mem.userBufferData) {
            let raw = mem.userBufferData;
            let bytes;
            if (typeof raw === 'string') {
                try {
                    let bin = atob(raw);
                    bytes = new Uint8Array(bin.length);
                    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i) & 0xFF;
                } catch(e) {
                    bytes = new TextEncoder().encode(raw);
                }
            } else {
                bytes = new Uint8Array(raw);
            }
            const ubDisp = $('ubuf-display'); if (ubDisp) ubDisp.textContent = new TextDecoder().decode(bytes);
            const ubLen = $('ubuf-len'); if (ubLen) ubLen.textContent = bytes.byteLength;
        }

        const total = mem.totalChunks || 1;
        const curr  = mem.currentChunk || 0;
        const pbar = $('pbar'); if (pbar) pbar.style.width = `${((curr + 1) / total) * 100}%`;
        const chunkStat = $('chunk-stat'); if (chunkStat) chunkStat.textContent = `${curr + 1} / ${total}`;

        if (prevSnap && prevSnap.memoryState && mem.activeWriteBuffer !== prevSnap.memoryState.activeWriteBuffer) {
            spawnParticles(mem.activeWriteBuffer);
        }
    }

    if (snap.hardwareState) {
        const hw = snap.hardwareState;
        const hwCmd = $('hw-cmd'); if (hwCmd) hwCmd.textContent = `CMD: ${hw.cmdRegister || 'NO_OP'}`;
        const hwSts = $('hw-status'); if (hwSts) hwSts.textContent = `STS: ${hw.statusRegister || 'READY'}`;
        // DMA 控制器面板
        const dmaSrc = $('dma-src'); if (dmaSrc) dmaSrc.textContent = hw.dmaSource || '—';
        const dmaDst = $('dma-dst'); if (dmaDst) dmaDst.textContent = hw.dmaDestination || '—';
        const dmaCnt = $('dma-cnt'); if (dmaCnt) dmaCnt.textContent = hw.dmaCount || 0;
        const dmaSts = $('dma-sts');
        if (dmaSts) {
            dmaSts.textContent = hw.dmaStatus || 'IDLE';
            dmaSts.className = 'dma-val dma-status';
            if (hw.dmaStatus === 'SETUP') dmaSts.classList.add('dma-setup');
            else if (hw.dmaStatus === 'TRANSFERRING') dmaSts.classList.add('dma-transferring');
            else if (hw.dmaStatus === 'DONE') dmaSts.classList.add('dma-done');
            else if (hw.dmaStatus === 'ERROR') dmaSts.classList.add('dma-error');
        }
    }

    // 重试状态提示
    const retryEl = $('retry-info');
    if (retryEl) {
        if (snap.memoryState && snap.memoryState.retryCount > 0 && snap.memoryState.retryCount < snap.memoryState.retryMax) {
            retryEl.style.display = 'block';
            retryEl.textContent = '⏳ 设备忙重试: ' + snap.memoryState.retryCount + ' / ' + snap.memoryState.retryMax;
            retryEl.style.color = 'var(--amber)';
        } else {
            retryEl.style.display = 'none';
        }
    }

    const errEl = $('err-msg');
    if (errEl) {
        if (snap.isFinished && snap.finalErrorCode !== 'SUCCESS') {
            errEl.textContent = snap.finalErrorCode;
            errEl.className = 'err-fault';
        } else if (snap.isFinished) {
            errEl.textContent = 'SUCCESS';
            errEl.className = 'err-success';
        } else if (snap.finalErrorCode && snap.finalErrorCode !== 'SUCCESS') {
            errEl.textContent = snap.finalErrorCode;
            errEl.className = 'err-fault';
        }
    }

    if (snap.isFinished && autoTimer) clearAutoPlay();
    prevSnap = snap;
}

function updateKbuf(el, id, mem) {
    if (!el) return;
    el.classList.remove('write', 'read');
    const raw = id === 1 ? mem.kernelBuffer1Data : mem.kernelBuffer2Data;
    const dataEl = el.querySelector('.kbuf-data');
    if (dataEl) {
        if (raw && raw.length > 0) {
            dataEl.textContent = (typeof raw === 'string' ? raw : new TextDecoder().decode(raw)).substring(0, 24);
        } else {
            dataEl.textContent = '';
        }
    }
    if (id === mem.activeWriteBuffer) el.classList.add('write');
    else if (id === mem.activeReadBuffer && raw && raw.length > 0) el.classList.add('read');
}

// ---- Auto-Play (三态: 播放 → 暂停 → 继续) ----
function getAutoInterval() {
    return AUTO_SPEEDS[autoSpeedIdx] || 50;
}

function updateSpeedDisplay() {
    speedVal.textContent = getAutoInterval() + 'ms';
    autoSpeedSlider.value = autoSpeedIdx;
}

function startAutoPlay() {
    autoPaused = false;
    btnAuto.textContent = '⏸ 暂停';
    btnAuto.classList.add('btn-pause');
    const interval = getAutoInterval();
    if (autoTimer) clearInterval(autoTimer);
    autoTimer = setInterval(doStep, interval);
}

function pauseAutoPlay() {
    autoPaused = true;
    if (autoTimer) { clearInterval(autoTimer); autoTimer = null; }
    btnAuto.textContent = '▶ 继续';
    btnAuto.classList.remove('btn-pause');
}

function resumeAutoPlay() {
    startAutoPlay(); // same as start, sets interval fresh
}

function clearAutoPlay() {
    autoPaused = false;
    if (autoTimer) { clearInterval(autoTimer); autoTimer = null; }
    btnAuto.textContent = '⚡ 自动连点';
    btnAuto.classList.remove('btn-pause');
}

function toggleAutoPlay() {
    if (!autoTimer && !autoPaused) {
        // Idle → Start
        startAutoPlay();
    } else if (autoTimer && !autoPaused) {
        // Running → Pause
        pauseAutoPlay();
    } else if (autoPaused) {
        // Paused → Resume
        resumeAutoPlay();
    }
}

// 变更速度：如果正在运行，立即重启定时器
function onSpeedChange() {
    autoSpeedIdx = parseInt(autoSpeedSlider.value);
    updateSpeedDisplay();
    if (autoTimer && !autoPaused) {
        // 重启定时器以应用新速度
        clearInterval(autoTimer);
        autoTimer = setInterval(doStep, getAutoInterval());
    }
}

// ---- Particle System: DPR-aware · 有限寿命 · 发光拖尾数据流 ----
let particles = [];
let rafId = null;
let dpr = 1;
const PARTICLE_COUNT = 14;
const REDUCE_MOTION = window.matchMedia &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches;

// 画布按设备像素比放大，再用 CSS 缩回 → 视网膜屏粒子不再发虚
function resizeCanvas() {
    const parent = canvas.parentElement;
    if (!parent) return;
    dpr = window.devicePixelRatio || 1;
    const w = parent.clientWidth, h = parent.clientHeight;
    canvas.width  = Math.round(w * dpr);
    canvas.height = Math.round(h * dpr);
    canvas.style.width  = w + 'px';
    canvas.style.height = h + 'px';
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0); // 之后一律用 CSS 像素绘制
}
window.addEventListener('resize', resizeCanvas);

// 获取任意元素相对于 Canvas 的中心坐标（传入已缓存的 canvasRect 避免重复 reflow）
function getElementCenter(el, canvasRect) {
    if (!el) return { x: 0, y: 0 };
    const rect = el.getBoundingClientRect();
    const cr = canvasRect || canvas.getBoundingClientRect();
    return {
        x: rect.left - cr.left + rect.width / 2,
        y: rect.top - cr.top + rect.height / 2
    };
}

function spawnParticles(targetBuf) {
    cancelParticles();
    if (REDUCE_MOTION) return; // 尊重系统“减少动态效果”
    resizeCanvas();
    const hwCard = $('layer-HW');
    const targetEl = targetBuf === 1 ? $('kbuf1') : $('kbuf2');
    if (!hwCard || !targetEl) return;

    // 卡片在一次动画期间不移动 → 起终点只测量一次，逐帧零 reflow
    const canvasRect = canvas.getBoundingClientRect();
    const start = getElementCenter(hwCard, canvasRect);
    const end   = getElementCenter(targetEl, canvasRect);
    const sx = start.x, sy = start.y - 30; // 从硬件卡上沿发出
    const ex = end.x,   ey = end.y;
    const mx = (sx + ex) / 2 + 50, my = (sy + ey) / 2; // 贝塞尔控制点右偏

    particles = [];
    for (let i = 0; i < PARTICLE_COUNT; i++) {
        particles.push({
            t: -i * 0.06,                        // 负值错峰发射 → 连续“流”而非一团
            speed: 0.016 + Math.random() * 0.012,
            r: 2 + Math.random() * 1.6,
            jitter: (Math.random() - 0.5) * 16   // 横向散开量
        });
    }
    rafId = requestAnimationFrame(() => particleLoop(sx, sy, mx, my, ex, ey));
}

function cancelParticles() {
    if (rafId) { cancelAnimationFrame(rafId); rafId = null; }
    particles = [];
    ctx.clearRect(0, 0, canvas.width, canvas.height);
}

function bz(a, b, c, t) { const m = 1 - t; return m * m * a + 2 * m * t * b + t * t * c; }

function particleLoop(sx, sy, mx, my, ex, ey) {
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    let alive = false;
    ctx.save();
    ctx.globalCompositeOperation = 'lighter'; // 叠加 → 重叠处自然增亮
    for (const p of particles) {
        p.t += p.speed;
        if (p.t >= 1) continue;            // 跑完一程即消亡
        alive = true;
        if (p.t < 0) continue;             // 尚未发射
        // 头部 + 3 段拖尾回声
        for (let k = 0; k <= 3; k++) {
            const tt = p.t - k * 0.05;
            if (tt < 0 || tt > 1) continue;
            const env = Math.sin(Math.PI * tt);          // 0→1→0 包络
            const x = bz(sx, mx, ex, tt) + p.jitter * env * (k === 0 ? 1 : 0.5);
            const y = bz(sy, my, ey, tt);
            const fade = tt < 0.12 ? tt / 0.12 : tt > 0.88 ? (1 - tt) / 0.12 : 1;
            const a = fade * (k === 0 ? 0.9 : 0.16 * (4 - k));
            ctx.beginPath();
            ctx.arc(x, y, p.r * (k === 0 ? 1 : 0.6), 0, Math.PI * 2);
            ctx.fillStyle = `rgba(34,170,200,${a})`;
            ctx.shadowColor = 'rgba(8,145,178,0.9)';
            ctx.shadowBlur = k === 0 ? 8 : 0;             // 仅头部发光，省开销
            ctx.fill();
        }
    }
    ctx.restore();
    if (alive) {
        rafId = requestAnimationFrame(() => particleLoop(sx, sy, mx, my, ex, ey));
    } else {
        cancelParticles(); // 全部到达 → 停止 RAF 并清屏，空闲不再占用 CPU
    }
}

function log(msg) {
    const el = document.createElement('div');
    el.textContent = `> ${msg}`;
    stepLog.prepend(el);
    if (stepLog.children.length > 80) stepLog.lastChild.remove();
}

// ---- Event bindings ----
btnInit.addEventListener('click', connectAndInit); // 全新连接 + 初始化（不保留缓存）
btnReset.addEventListener('click', triggerReset);   // 复用 stream 重置（保留页缓存）
btnStep.addEventListener('click', doStep);
btnAuto.addEventListener('click', toggleAutoPlay);
autoSpeedSlider.addEventListener('input', onSpeedChange);
updateSpeedDisplay();
cfgBytes.addEventListener('input', () => valBytes.textContent = cfgBytes.value);
cfgDblBuf.addEventListener('change', () => $('lbl-dblbuf').textContent = cfgDblBuf.checked ? '双缓冲' : '单缓冲');
cfgCache.addEventListener('change', () => $('lbl-cache').textContent = cfgCache.checked ? '页缓存' : '直读');

// 自动连点按钮 + 速度控制器始终可见
btnAuto.classList.remove('hidden');
autoSpeedGroup.classList.remove('hidden');

// ---- Layer card click → detail page ----
document.querySelectorAll('.layer-card').forEach(function (card) {
    card.addEventListener('click', function () {
        var id = card.id;
        var layer = id.replace('layer-', '');
        window.location.href = 'detail.html?layer=' + layer;
    });
});

// ---- Accordion toggle ----
document.querySelectorAll('.accordion-header').forEach(function (header) {
    header.addEventListener('click', function () {
        var card = header.closest('.accordion');
        var isOpen = card.classList.toggle('open');
        var arrow = header.querySelector('.accordion-arrow');
        if (arrow) arrow.textContent = isOpen ? '▾' : '▸'; // ▾ : ▸
        // Re-trigger log-card flex expansion
        if (card.classList.contains('log-card')) {
            card.style.flex = isOpen ? '1' : '0 0 auto';
        }
    });
});

resizeCanvas();