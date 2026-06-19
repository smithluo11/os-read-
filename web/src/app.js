// ==========================================
// 1. 状态定义与 Mock 数据生成器
// ==========================================
let currentSnapshot = {
    activeLayer: 'USER', // USER, VFS, DRV, HW, INT
    writeBufferActive: 1, // 1 or 2
    buffer1State: 'CLEAN', // CLEAN, DIRTY, FLUSHING
    buffer2State: 'CLEAN',
    currentChunk: 0,
    totalChunks: 100,
    hwStatus: 'IDLE', // IDLE, BUSY, DONE
    vfsError: 'Status: OK'
};

// 模拟后端离散状态流转（替代 gRPC）
const layerFlow = ['USER', 'VFS', 'DRV', 'HW', 'INT', 'DRV', 'VFS'];
let flowIndex = 0;

function mockFetchNextSnapshot() {
    flowIndex = (flowIndex + 1) % layerFlow.length;
    let nextLayer = layerFlow[flowIndex];

    // 构造新的离散快照
    let newSnap = { ...currentSnapshot, activeLayer: nextLayer };

    if (nextLayer === 'VFS') {
        newSnap.buffer1State = 'DIRTY';
        newSnap.vfsError = 'Status: Buffering...';
    } else if (nextLayer === 'DRV') {
        newSnap.buffer1State = 'FLUSHING';
        newSnap.currentChunk = Math.min(newSnap.currentChunk + 20, 100);
    } else if (nextLayer === 'HW') {
        newSnap.hwStatus = 'BUSY (Writing ' + newSnap.currentChunk + '%)';
    } else if (nextLayer === 'INT') {
        newSnap.hwStatus = 'DONE (IRQ Raised)';
    } else if (nextLayer === 'USER') {
        newSnap.currentChunk = 0; // 重置
        newSnap.buffer1State = 'CLEAN';
    }

    return newSnap;
}

// ==========================================
// 2. 前端 Diff-Driven 渲染引擎
// ==========================================
function render(newState) {
    // 1. 层面光晕跳变 (CSS State Machine)
    document.querySelectorAll('.layer-node').forEach(el => el.classList.remove('active'));
    document.getElementById(`layer-${newState.activeLayer}`).classList.add('active');

    // 2. 双缓冲状态着色
    const buf1 = document.getElementById('vfs-buf-1');
    buf1.className = 'buffer-box'; // reset
    if (newState.buffer1State === 'DIRTY') buf1.classList.add('dirty');
    if (newState.buffer1State === 'FLUSHING') buf1.classList.add('flushing');
    buf1.querySelector('.status').innerText = newState.buffer1State;

    // 3. 进度条平滑增长
    const progressPct = (newState.currentChunk / newState.totalChunks) * 100;
    document.getElementById('drv-progress').style.width = `${progressPct}%`;

    // 4. 寄存器与文本跳动
    document.getElementById('hw-status').innerText = newState.hwStatus;
    const errConsole = document.getElementById('vfs-error');
    errConsole.innerText = newState.vfsError;
}

// ==========================================
// 3. 事件绑定与 Auto-Play 压力测试 (§12.1)
// ==========================================
document.getElementById('btn-next').addEventListener('click', () => {
    const nextState = mockFetchNextSnapshot();
    render(nextState);
    currentSnapshot = nextState;
});

// 解析 URL 参数开启 Debug 模式
const urlParams = new URLSearchParams(window.location.search);
const isDebug = urlParams.get('debug') === '1';
const autoPlayBtn = document.getElementById('btn-autoplay');

let autoPlayTimer = null;

if (isDebug) {
    autoPlayBtn.classList.remove('hidden');
    autoPlayBtn.addEventListener('click', () => {
        if (autoPlayTimer) {
            // Stop
            clearInterval(autoPlayTimer);
            autoPlayTimer = null;
            autoPlayBtn.innerText = 'Auto-Play [Debug]';
            autoPlayBtn.style.background = 'var(--error-color)';
        } else {
            // Start: 50ms 高频连点测试 (20fps)
            autoPlayBtn.innerText = 'Stop Auto-Play';
            autoPlayBtn.style.background = 'var(--warning-color)';
            autoPlayTimer = setInterval(() => {
                const nextState = mockFetchNextSnapshot();
                render(nextState);
                currentSnapshot = nextState;
            }, 50); // <-- 极限测试覆盖率
        }
    });
}

// 初始化渲染
render(currentSnapshot);
