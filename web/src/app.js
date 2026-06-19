// ============================================================
//  OS I/O Simulator — gRPC-Web Client + Diff-Driven Render
//  Laboratory Instrument Panel · 50ms Auto-Play
// ============================================================

// ---- State ----
let streamHandle = null;
let prevSnap = null;
let autoTimer = null;
let stepCount = 0;

// ---- DOM refs ----
const $ = (id) => document.getElementById(id);
const btnInit  = $('btn-init');
const btnStep  = $('btn-step');
const btnAuto  = $('btn-autoplay');
const selUser  = $('sel-user');
const cfgPath  = $('cfg-path');
const cfgBytes = $('cfg-bytes');
const cfgAddr  = $('cfg-addr');
const cfgDblBuf = $('cfg-dblbuf');
const cfgFault  = $('cfg-fault');
const cfgHost   = $('cfg-host');
const connStatus = $('conn-status');
const valBytes   = $('val-bytes');
const valAddr    = $('val-addr');
const lblDblbuf  = $('lbl-dblbuf');
const stepLog    = $('step-log');

// ---- Connect ----
function connect() {
    const host = cfgHost.value.trim();
    if (!host) {
        log('错误: 未填写后端地址');
        return;
    }
    log(`尝试连接: ${host}`);
    try {
        if (streamHandle) {
            try { streamHandle.close(); } catch(e) {}
            streamHandle = null;
        }
        streamHandle = window.IOSim.connect(host);
        streamHandle.onOpen(function () {
            connStatus.textContent = '● Connected';
            connStatus.className = 'conn-status connected';
            log('WebSocket 握手完成！可以发送指令');
        });
        streamHandle.onSnapshot(onSnapshot);
        streamHandle.onError(function (e) {
            var msg = e.message || e;
            log('gRPC error: ' + msg);
            connStatus.textContent = '● Error';
            connStatus.className = 'conn-status error';
            btnInit.disabled = true;
            btnStep.disabled = true;
        });
        streamHandle.onEnd(function () {
            log('Stream ended');
            connStatus.textContent = '● Disconnected';
            connStatus.className = 'conn-status disconnected';
            btnInit.disabled = true;
            btnStep.disabled = true;
        });
        btnInit.disabled = false;
        btnStep.disabled = false;
        log('WebSocket 已创建，等待握手...');
    } catch (e) {
        log('Connection failed: ' + e.message);
        connStatus.textContent = '● Disconnected';
        connStatus.className = 'conn-status disconnected';
    }
}

cfgHost.addEventListener('change', connect);
$('btn-connect').addEventListener('click', function () {
    log('正在连接后端...');
    connStatus.textContent = '● 连接中...';
    connStatus.className = 'conn-status disconnected';
    connect();
});

function send(cmd) {
    if (!streamHandle) { log('Not connected. Click INIT first.'); return; }
    try { streamHandle.send(cmd); } catch (e) { log(`Write error: ${e.message}`); }
}

// ---- User context ----
function getUserCtx() {
    if (selUser.value === 'root') {
        return window.IOSim.newUserContext(0, 0, 'root', '/root');
    }
    return window.IOSim.newUserContext(1000, 1000, 'user1', '/home/user1');
}

// ---- INIT ----
function doInit() {
    prevSnap = null; stepCount = 0;
    stepLog.innerHTML = '';
    $('err-msg').textContent = 'SUCCESS'; $('err-msg').className = '';
    $('ubuf-display').textContent = ''; $('ubuf-len').textContent = '0';
    $('pbar').style.width = '0%'; $('chunk-stat').textContent = '0 / 1';
    clearAutoPlay();
    btnStep.disabled = false;

    const addrStr = cfgAddr.value;
    const addr = addrStr.startsWith('0x') ? parseInt(addrStr, 16) : parseInt(addrStr, 10);
    const config = window.IOSim.newReadConfig(
        cfgPath.value, parseInt(cfgBytes.value), addr, cfgDblBuf.checked
    );
    const cmd = window.IOSim.newInitCommand(config, getUserCtx());
    // Inject fault if selected
    const faultVal = parseInt(cfgFault.value);
    if (faultVal > 0) {
        const faultCmd = window.IOSim.newInjectFaultCommand(faultVal);
        send(faultCmd);
    }
    send(cmd);
    log(`INIT: ${cfgPath.value} | user=${selUser.value} | bytes=${cfgBytes.value} | dblbuf=${cfgDblBuf.checked}`);
}

// ---- STEP ----
function doStep() {
    const cmd = window.IOSim.newStepCommand();
    send(cmd);
}

// ---- Snapshot → Render (Diff-driven) ----
const layerMap = { 0:'USER', 1:'VFS', 2:'DRV', 4:'HW', 3:'INT' };

function onSnapshot(snapMsg) {
    const snap = snapMsg.toObject();
    stepCount++;
    log(`[S${stepCount}] ${(snap.stepDescription || '').substring(0, 80)}`);

    const activeLayer = layerMap[snap.currentActiveLayer] || 'USER';

    // --- Layer cards ---
    document.querySelectorAll('.layer-card').forEach(el => el.classList.remove('active','error'));
    const card = $(`layer-${activeLayer}`);
    if (card) {
        card.classList.add('active');
        if (snap.isFinished && snap.finalErrorCode && snap.finalErrorCode !== 'SUCCESS')
            card.classList.add('error');
    }

    // --- SVG connectors ---
    document.querySelectorAll('.connector').forEach(el => el.classList.remove('active'));
    const connMap = { 'VFS':'conn-u-v', 'DRV':'conn-v-d', 'HW':'conn-d-h', 'INT':'conn-h-i' };
    if (connMap[activeLayer]) $(connMap[activeLayer]).classList.add('active');
    // Ping-pong loopback
    const prevLayer = prevSnap ? layerMap[prevSnap.currentActiveLayer] : null;
    if (activeLayer === 'DRV' && prevLayer === 'INT') {
        $('conn-i-d').classList.add('active');
    } else {
        $('conn-i-d').classList.remove('active');
    }

    // --- VFS description ---
    if (snap.stepDescription) $('vfs-desc').textContent = snap.stepDescription.substring(0, 60);

    // --- Process state ---
    if (snap.processState) {
        $('proc-pid').textContent = snap.processState.pid;
        const st = snap.processState.state;
        const el = $('proc-state');
        el.className = 'badge';
        if (st === 0) { el.textContent = 'RUNNING'; el.classList.add('badge-running'); }
        else if (st === 1) { el.textContent = 'BLOCKED'; el.classList.add('badge-blocked'); }
        else { el.textContent = 'READY'; el.classList.add('badge-ready'); }
        $('proc-wait').textContent = snap.processState.waitReason || '';
    }

    // --- Memory state ---
    if (snap.memoryState) {
        const mem = snap.memoryState;

        // Kernel buffers
        updateKbuf($('kbuf1'), 1, mem);
        updateKbuf($('kbuf2'), 2, mem);

        // User buffer
        if (mem.userBufferData) {
            let text = '';
            if (typeof mem.userBufferData === 'string') {
                text = mem.userBufferData; // grpc-web-text sends base64, library decodes
            } else if (mem.userBufferData instanceof Uint8Array) {
                text = new TextDecoder().decode(mem.userBufferData);
            }
            $('ubuf-display').textContent = text;
            $('ubuf-len').textContent = text.length;
        }

        // Progress bar
        const total = mem.totalChunks || 1;
        const curr  = mem.currentChunk || 0;
        $('pbar').style.width = `${((curr + 1) / total) * 100}%`;
        $('chunk-stat').textContent = `${curr + 1} / ${total}`;

        // IRP info
        if (mem.currentIrpInfo) $('drv-desc').textContent = mem.currentIrpInfo;

        // Spawn particles when write buffer changes
        if (prevSnap && prevSnap.memoryState
            && mem.activeWriteBuffer !== prevSnap.memoryState.activeWriteBuffer) {
            spawnParticles(mem.activeWriteBuffer);
        }
    }

    // --- Hardware state ---
    if (snap.hardwareState) {
        $('hw-cmd').textContent = `CMD: ${snap.hardwareState.cmdRegister || 'NO_OP'}`;
        $('hw-status').textContent = `STS: ${snap.hardwareState.statusRegister || 'READY'}`;
    }

    // --- Error ---
    if (snap.finalErrorCode && snap.finalErrorCode !== 'SUCCESS') {
        $('err-msg').textContent = snap.finalErrorCode;
        $('err-msg').className = 'error';
    } else {
        $('err-msg').textContent = 'SUCCESS';
        $('err-msg').className = '';
    }

    // --- Auto-stop ---
    if (snap.isFinished && autoTimer) clearAutoPlay();

    prevSnap = snap;
}

function updateKbuf(el, id, mem) {
    el.classList.remove('write', 'read');
    const raw = id === 1 ? mem.kernelBuffer1Data : mem.kernelBuffer2Data;
    const dataEl = el.querySelector('.kbuf-data');
    if (raw && raw.length > 0) {
        let text = typeof raw === 'string' ? raw : new TextDecoder().decode(raw);
        dataEl.textContent = text.substring(0, 24);
    } else {
        dataEl.textContent = '';
    }
    if (id === mem.activeWriteBuffer) el.classList.add('write');
    else if (id === mem.activeReadBuffer && raw && raw.length > 0) el.classList.add('read');
}

// ---- Auto-Play ----
function clearAutoPlay() {
    if (autoTimer) { clearInterval(autoTimer); autoTimer = null; }
    btnAuto.innerHTML = '⚡ AUTO';
}
function toggleAutoPlay() {
    if (autoTimer) { clearAutoPlay(); return; }
    btnAuto.innerHTML = '■ STOP';
    autoTimer = setInterval(doStep, 50);
}

// ---- Particle System (deterministic, no physics engine) ----
let particles = [];
let rafId = null;
const canvas = $('particle-canvas');
const ctx = canvas.getContext('2d');

function resizeCanvas() {
    const panel = document.querySelector('.panel-topology');
    if (!panel) return;
    canvas.width = panel.clientWidth;
    canvas.height = panel.clientHeight;
}
window.addEventListener('resize', () => { resizeCanvas(); });

function spawnParticles(targetBuf) {
    cancelParticles();
    resizeCanvas();
    const hwCard = $('layer-HW');
    const targetEl = targetBuf === 1 ? $('kbuf1') : $('kbuf2');
    if (!hwCard || !targetEl) return;
    const panel = document.querySelector('.panel-topology');
    const panelRect = panel.getBoundingClientRect();
    const hwR = hwCard.getBoundingClientRect();
    const tR  = targetEl.getBoundingClientRect();
    const sx = hwR.left + hwR.width / 2 - panelRect.left;
    const sy = hwR.top + 20 - panelRect.top;
    const ex = tR.left + tR.width / 2 - panelRect.left;
    const ey = tR.top + tR.height / 2 - panelRect.top;
    const mx = (sx + ex) / 2 + 40;
    const my = (sy + ey) / 2;

    particles = [];
    for (let i = 0; i < 5; i++)
        particles.push({ t: i / 5, speed: 0.005 + Math.random() * 0.005 });

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

// ---- Helpers ----
function log(msg) {
    const el = document.createElement('div');
    el.textContent = `> ${msg}`;
    stepLog.prepend(el);
    while (stepLog.children.length > 80) stepLog.lastChild.remove();
}

// ---- Event bindings ----
btnInit.addEventListener('click', doInit);
btnStep.addEventListener('click', doStep);
btnAuto.addEventListener('click', toggleAutoPlay);
cfgBytes.addEventListener('input', () => { valBytes.textContent = cfgBytes.value; });
cfgAddr.addEventListener('change', () => { valAddr.textContent = cfgAddr.value; });
cfgDblBuf.addEventListener('change', () => { lblDblbuf.textContent = cfgDblBuf.checked ? 'ON' : 'OFF'; });

// Show Auto-Play in debug mode
if (new URLSearchParams(window.location.search).get('debug') === '1')
    btnAuto.classList.remove('hidden');

// Boot
resizeCanvas();
