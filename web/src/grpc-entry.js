// Minimal gRPC-Web bidi-streaming client over WebSocket
// Talks directly to grpcweb.WrapServer (Improbable Go) — no npm deps needed
const pb = require('./generated/io_simulation_pb.js');

// ---- gRPC WebSocket frame helpers ----

/** Encode a protobuf message into a gRPC-Web frame (flag=0, 4-byte BE length, data) */
function frameMessage(msg) {
    const data = msg.serializeBinary();
    const buf = new ArrayBuffer(5 + data.byteLength);
    const view = new DataView(buf);
    view.setUint8(0, 0);           // frame flag: data
    view.setUint32(1, data.byteLength, false); // big-endian length
    new Uint8Array(buf, 5).set(data);
    return new Uint8Array(buf);
}

/** Build ASCII header block for WebSocket handshake */
function buildHeaders(metadata) {
    let s = '';
    for (const [k, v] of Object.entries(metadata || {})) {
        s += k + ': ' + v + '\r\n';
    }
    return s;
}

/** Parse a gRPC-Web frame from a Uint8Array.
 *  Returns { data, isTrailer, consumed } or null if incomplete. */
function parseFrame(buf) {
    if (buf.length < 5) return null;
    const view = new DataView(buf.buffer, buf.byteOffset, buf.byteLength);
    const flag = view.getUint8(0);
    const len = view.getUint32(1, false);
    if (buf.length < 5 + len) return null;
    return {
        data: buf.slice(5, 5 + len),
        isTrailer: flag === 128,
        consumed: 5 + len
    };
}

window.IOSim = {
    pb: pb,

    /** Connect to gRPC-Web server via WebSocket for bidi streaming */
    connect(hostname) {
        const host = hostname.replace(/\/+$/, '');
        const wsUrl = host.replace(/^http/, 'ws') + '/io_simulator.IOSimulationEngine/StreamSimulation';

        const ws = new WebSocket(wsUrl, ['grpc-websockets']);
        ws.binaryType = 'arraybuffer';

        let onMsgCb = null;
        let onErrCb = null;
        let onEndCb = null;
        let open = false;
        let msgQueue = [];
        let parserBuf = new Uint8Array(0);

        ws.onopen = function () {
            // Send headers as first message (required by grpc-websockets protocol)
            ws.send(new TextEncoder().encode(buildHeaders({
                'Content-Type': 'application/grpc-web+proto',
                'X-Grpc-Web': '1',
                'X-User-Agent': 'grpc-web-javascript/0.1',
            })));
            open = true;
            // Flush queued messages
            msgQueue.forEach(function (m) { ws.send(frameMessage(m)); });
            msgQueue = [];
        };

        ws.onmessage = function (event) {
            // Accumulate data in parserBuf
            const chunk = new Uint8Array(event.data);
            const combined = new Uint8Array(parserBuf.length + chunk.length);
            combined.set(parserBuf, 0);
            combined.set(chunk, parserBuf.length);
            parserBuf = combined;

            // Parse frames
            while (true) {
                const frame = parseFrame(parserBuf);
                if (!frame) break;
                parserBuf = parserBuf.slice(frame.consumed);
                if (frame.isTrailer) {
                    // Trailers contain grpc-status etc.
                    continue;
                }
                try {
                    const msg = pb.SystemSnapshot.deserializeBinary(frame.data);
                    if (onMsgCb) onMsgCb(msg);
                } catch (e) {
                    if (onErrCb) onErrCb({ message: 'Deserialize error: ' + e.message });
                }
            }
        };

        ws.onerror = function (e) {
            if (onErrCb) onErrCb({ message: 'WebSocket error' });
        };

        ws.onclose = function (event) {
            if (event.code === 1000) {
                if (onEndCb) onEndCb();
            } else if (!event.wasClean) {
                if (onErrCb) onErrCb({ message: 'WebSocket closed: code=' + event.code + ' reason=' + event.reason });
            }
        };

        return {
            _ws: ws,

            send(cmd) {
                if (open) {
                    ws.send(frameMessage(cmd));
                } else {
                    msgQueue.push(cmd);
                }
            },

            onSnapshot(fn) { onMsgCb = fn; },
            onError(fn) { onErrCb = fn; },
            onEnd(fn) { onEndCb = fn; },

            close() {
                ws.close(1000);
            }
        };
    },

    // ---- Factories (same API, compatible with google-protobuf) ----

    newInitCommand(config, userContext) {
        const cmd = new pb.SimControlCommand();
        cmd.setAction(0); // ACTION_INIT
        cmd.setConfig(config);
        if (userContext) cmd.setUserContext(userContext);
        return cmd;
    },

    newStepCommand() {
        const cmd = new pb.SimControlCommand();
        cmd.setAction(1); // ACTION_STEP_NEXT
        return cmd;
    },

    newInjectFaultCommand(faultType) {
        const cmd = new pb.SimControlCommand();
        cmd.setAction(2); // ACTION_INJECT_FAULT
        cmd.setInjectedFault(faultType);
        return cmd;
    },

    newReadConfig(filePath, bytesToRead, userBufferAddr, useDoubleBuffer) {
        const cfg = new pb.ReadRequestConfig();
        cfg.setFilePath(filePath);
        cfg.setBytesToRead(bytesToRead);
        cfg.setUserBufferAddr(userBufferAddr);
        cfg.setUseDoubleBuffer(useDoubleBuffer);
        return cfg;
    },

    newUserContext(uid, gid, username, homeDir) {
        const ctx = new pb.UserContext();
        ctx.setUid(uid);
        ctx.setGid(gid);
        ctx.setUsername(username);
        ctx.setHomeDir(homeDir);
        return ctx;
    },

    FaultType: pb.FaultType,
};
