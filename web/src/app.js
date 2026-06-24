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
const btnInit  = $('btn-init'); // 合并了 Connect 与 Init 的功能
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
    btnInit.textContent = connected ? '↻ 重新初始化' : '● 连接并初始化';
}

function connectAndInit() {
    const host = cfgHost.value.trim() || window.location.origin;
    
    // 如果已经连接，直接发起重新初始化（重置按钮功能）
    if (streamHandle && connStatus.textContent.includes('Connected')) {
        log('重新初始化模拟...');
        triggerInit();
        return;
    }

    log(`正在连接后端: ${host}...`);
    connStatus.textContent = '连接中...';
    connStatus.className = 'conn-status disconnected';
    
    try {
        if (streamHandle) { try { streamHandle.close(); } catch(e) {} }
        streamHandle = window.IOSim.connect(host);
        
        streamHandle.onOpen(function () {
            connStatus.textContent = '● Connected';
            connStatus.className = 'conn-status connected';
            updateInitButton();
            log('握手完成，开始初始化模拟...');
            triggerInit(); // 连接成功后自动触发 Init
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
    badge.className = 'phase-badge ' + meta.cls;
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
    stepCount++;
    detectPhase(snap, prevSnap);

    // 子步骤日志格式: [S3.2] (VFS 2/4) description...
    const subInfo = (snap.totalSubSteps > 1)
        ? ` (${snap.subStep}/${snap.totalSubSteps})`
        : '';
    log(`[S${stepCount}${subInfo}] ${(snap.stepDescription || '').substring(0, 80)}`);

    const activeLayer = layerMap[snap.currentActiveLayer] || 'USER';

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

    if (phase === 'return') {
        // 上行返回阶段：点亮 IRQ↑ 和 DATA↑ 返回路径
        if (activeLayer === 'INT') {
            $('conn-h-i').classList.add('active');           // HW→INT IRQ↑
        }
        if (activeLayer === 'DRV' && prevLayer === 'INT') {
            $('conn-i-d').classList.add('active');           // INT→DRV 循环
        }
        if (activeLayer === 'HW') {
            $('conn-d-h').classList.add('active');           // DRV→HW 请求
        }
        // 点亮返回路径连接器 (DRV→VFS→USER)
        document.querySelectorAll('.connector.return-path').forEach(function(el) {
            el.classList.add('active');
        });
        // 高亮 DATA↑ 标签
        const dataLabel = document.getElementById('data-label');
        if (dataLabel) dataLabel.style.opacity = '1';
        const irqLabel = document.getElementById('irq-label');
        if (irqLabel) irqLabel.style.opacity = '1';
        const irpLabel = document.getElementById('irp-label');
        if (irpLabel) irpLabel.style.opacity = '0.3';
    } else if (phase === 'hardware') {
        // 硬件阶段
        $('conn-d-h').classList.add('active');
        document.getElementById('irp-label').style.opacity = '1';
        document.getElementById('irq-label').style.opacity = '0.3';
        document.getElementById('data-label').style.opacity = '0.3';
    } else {
        // 下行请求阶段：仅点亮 IRP↓ 连接器
        const connMap = { 'VFS':'conn-u-v', 'DRV':'conn-v-d', 'HW':'conn-d-h' };
        if (connMap[activeLayer]) $(connMap[activeLayer]).classList.add('active');
        document.getElementById('irp-label').style.opacity = '1';
        document.getElementById('irq-label').style.opacity = '0.3';
        document.getElementById('data-label').style.opacity = '0.3';
        // INT→DRV 回环在请求阶段不应出现
        $('conn-i-d').classList.remove('active');
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

    if (snap.stepDescription) $('vfs-desc').textContent = snap.stepDescription.substring(0, 60);

    if (snap.processState) {
        $('proc-pid').textContent = snap.processState.pid;
        const st = snap.processState.state;
        const el = $('proc-state');
        el.className = 'badge';
        if (st === 0) { el.textContent = 'RUNNING'; el.classList.add('badge-running'); }
        else if (st === 1) { el.textContent = 'BLOCKED'; el.classList.add('badge-blocked'); }
        else { el.textContent = 'READY'; el.classList.add('badge-ready'); }
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
                // protobuf-js 可能将 bytes 字段以 base64 返回，atob 解码得到原始字节
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
            $('ubuf-display').textContent = new TextDecoder().decode(bytes);
            $('ubuf-len').textContent = bytes.byteLength;
        }

        const total = mem.totalChunks || 1;
        const curr  = mem.currentChunk || 0;
        $('pbar').style.width = `${((curr + 1) / total) * 100}%`;
        $('chunk-stat').textContent = `${curr + 1} / ${total}`;

        if (prevSnap && prevSnap.memoryState && mem.activeWriteBuffer !== prevSnap.memoryState.activeWriteBuffer) {
            spawnParticles(mem.activeWriteBuffer);
        }
    }

    if (snap.hardwareState) {
        const hw = snap.hardwareState;
        $('hw-cmd').textContent = `CMD: ${hw.cmdRegister || 'NO_OP'}`;
        $('hw-status').textContent = `STS: ${hw.statusRegister || 'READY'}`;
        // DMA 控制器面板
        $('dma-src').textContent = hw.dmaSource || '—';
        $('dma-dst').textContent = hw.dmaDestination || '—';
        $('dma-cnt').textContent = hw.dmaCount || 0;
        // DMA 状态带颜色
        const dmaSts = $('dma-sts');
        dmaSts.textContent = hw.dmaStatus || 'IDLE';
        dmaSts.className = 'dma-val dma-status';
        if (hw.dmaStatus === 'SETUP') dmaSts.classList.add('dma-setup');
        else if (hw.dmaStatus === 'TRANSFERRING') dmaSts.classList.add('dma-transferring');
        else if (hw.dmaStatus === 'DONE') dmaSts.classList.add('dma-done');
        else if (hw.dmaStatus === 'ERROR') dmaSts.classList.add('dma-error');
    }

    // 内核差错控制台：根据快照状态更新
    const errEl = $('err-msg');
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

    if (snap.isFinished && autoTimer) clearAutoPlay();
    prevSnap = snap;
}

function updateKbuf(el, id, mem) {
    el.classList.remove('write', 'read');
    const raw = id === 1 ? mem.kernelBuffer1Data : mem.kernelBuffer2Data;
    const dataEl = el.querySelector('.kbuf-data');
    if (raw && raw.length > 0) {
        dataEl.textContent = (typeof raw === 'string' ? raw : new TextDecoder().decode(raw)).substring(0, 24);
    } else {
        dataEl.textContent = '';
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

// ---- Particle System (动态计算坐标核心逻辑) ----
let particles = [];
let rafId = null;

function resizeCanvas() {
    const parent = canvas.parentElement;
    if (!parent) return;
    canvas.width = parent.clientWidth;
    canvas.height = parent.clientHeight;
}
window.addEventListener('resize', resizeCanvas);

// 获取任意元素相对于 Canvas 的中心坐标
function getElementCenter(el) {
    if (!el) return { x: 0, y: 0 };
    const rect = el.getBoundingClientRect();
    const canvasRect = canvas.getBoundingClientRect();
    return {
        x: rect.left - canvasRect.left + rect.width / 2,
        y: rect.top - canvasRect.top + rect.height / 2
    };
}

function spawnParticles(targetBuf) {
    cancelParticles();
    resizeCanvas();
    const hwCard = $('layer-HW');
    const targetEl = targetBuf === 1 ? $('kbuf1') : $('kbuf2');
    if (!hwCard || !targetEl) return;

    // 直接从 DOM 实时抓取起终点，彻底解决页面缩放导致的错位
    const start = getElementCenter(hwCard);
    const end = getElementCenter(targetEl);
    
    // 微调起点：从硬件卡的上方发出
    const sx = start.x;
    const sy = start.y - 30; 
    const ex = end.x;
    const ey = end.y;
    const mx = (sx + ex) / 2 + 50; // 贝塞尔控制点往右偏
    const my = (sy + ey) / 2;

    for (let i = 0; i < 5; i++) {
        particles.push({ t: i / 5, speed: 0.005 + Math.random() * 0.005 });
    }
    rafId = requestAnimationFrame(() => particleLoop(sx, sy, mx, my, ex, ey));
}

function cancelParticles() {
    if (rafId) { cancelAnimationFrame(rafId); rafId = null; }
    particles = [];
    ctx.clearRect(0, 0, canvas.width, canvas.height);
}

function particleLoop(sx, sy, mx, my, ex, ey) {
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    particles.forEach(p => {
        p.t += p.speed;
        if (p.t > 1) p.t -= 1;
        const mt = 1 - p.t;
        const x = mt * mt * sx + 2 * mt * p.t * mx + p.t * p.t * ex;
        const y = mt * mt * sy + 2 * mt * p.t * my + p.t * p.t * ey;
        const alpha = p.t < 0.15 ? p.t / 0.15 : p.t > 0.85 ? (1 - p.t) / 0.15 : 1;
        ctx.beginPath();
        ctx.arc(x, y, 3, 0, Math.PI * 2);
        ctx.fillStyle = `rgba(8,145,178,${alpha * 0.7})`;
        ctx.fill();
    });
    rafId = requestAnimationFrame(() => particleLoop(sx, sy, mx, my, ex, ey));
}

function log(msg) {
    const el = document.createElement('div');
    el.textContent = `> ${msg}`;
    stepLog.prepend(el);
    if (stepLog.children.length > 80) stepLog.lastChild.remove();
}

// ---- Event bindings ----
btnInit.addEventListener('click', connectAndInit); // 按钮整合：点击一次连接+Init
btnStep.addEventListener('click', doStep);
btnAuto.addEventListener('click', toggleAutoPlay);
autoSpeedSlider.addEventListener('input', onSpeedChange);
updateSpeedDisplay();
cfgBytes.addEventListener('input', () => valBytes.textContent = cfgBytes.value);
cfgDblBuf.addEventListener('change', () => $('lbl-dblbuf').textContent = cfgDblBuf.checked ? '双缓冲' : '单缓冲');
cfgCache.addEventListener('change', () => $('lbl-cache').textContent = cfgCache.checked ? '页缓存' : '直读');

// Debug 模式：显示自动连点按钮 + 速度控制器
if (new URLSearchParams(window.location.search).get('debug') === '1') {
    btnAuto.classList.remove('hidden');
    autoSpeedGroup.classList.remove('hidden');
}

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