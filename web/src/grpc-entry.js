// gRPC-Web bidi streaming client — uses @improbable-eng/grpc-web (matches Go grpcweb.WrapServer)
const { grpc } = require('@improbable-eng/grpc-web');
const proto = require('./generated/io_simulation_pb.js');

window.IOSim = {
    pb: proto,

    /** Connect to the backend gRPC-Web bidirectional stream.
     *  Returns a handle: { send(cmd), onSnapshot(fn), onError(fn), onEnd(fn), close() }
     */
    connect(hostname) {
        // Strip trailing slash from hostname (e.g., "http://localhost:18083/")
        const host = hostname.replace(/\/+$/, '');

        const methodDef = {
            methodName: 'StreamSimulation',
            service: { serviceName: 'io_simulator.IOSimulationEngine' },
            requestStream: true,
            responseStream: true,
            requestType: proto.SimControlCommand,
            responseType: proto.SystemSnapshot,
        };

        const client = grpc.client(methodDef, {
            host: host,
            transport: grpc.WebsocketTransport(),
        });

        let onMsgCb = null;
        let onErrCb = null;
        let onEndCb = null;

        client.onMessage(function (msg) {
            if (onMsgCb) onMsgCb(msg);
        });

        client.onEnd(function (code, msg) {
            if (code !== 0) {
                if (onErrCb) onErrCb({ message: msg || ('gRPC error code: ' + code) });
            } else {
                if (onEndCb) onEndCb();
            }
        });

        client.start();

        return {
            _client: client,

            send(cmd) {
                client.send(cmd);
            },

            onSnapshot(fn) {
                onMsgCb = fn;
            },

            onError(fn) {
                onErrCb = fn;
            },

            onEnd(fn) {
                onEndCb = fn;
            },

            close() {
                client.close();
            }
        };
    },

    // ---- Factories ----

    newInitCommand(config, userContext) {
        const cmd = new proto.SimControlCommand();
        cmd.setAction(0); // ACTION_INIT
        cmd.setConfig(config);
        if (userContext) cmd.setUserContext(userContext);
        return cmd;
    },

    newStepCommand() {
        const cmd = new proto.SimControlCommand();
        cmd.setAction(1); // ACTION_STEP_NEXT
        return cmd;
    },

    newInjectFaultCommand(faultType) {
        const cmd = new proto.SimControlCommand();
        cmd.setAction(2); // ACTION_INJECT_FAULT
        cmd.setInjectedFault(faultType);
        return cmd;
    },

    newReadConfig(filePath, bytesToRead, userBufferAddr, useDoubleBuffer) {
        const cfg = new proto.ReadRequestConfig();
        cfg.setFilePath(filePath);
        cfg.setBytesToRead(bytesToRead);
        cfg.setUserBufferAddr(userBufferAddr);
        cfg.setUseDoubleBuffer(useDoubleBuffer);
        return cfg;
    },

    newUserContext(uid, gid, username, homeDir) {
        const ctx = new proto.UserContext();
        ctx.setUid(uid);
        ctx.setGid(gid);
        ctx.setUsername(username);
        ctx.setHomeDir(homeDir);
        return ctx;
    },

    FaultType: proto.FaultType,
};
