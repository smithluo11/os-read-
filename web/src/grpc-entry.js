// gRPC-Web client entry — wraps generated code for browser

const { GrpcWebClientBase } = require('grpc-web');
const pb = require('./generated/io_simulation_pb.js');

window.IOSim = {
    pb: pb,

    /** Connect to a gRPC-Web endpoint.
     *  Returns a bidirectional stream handle:
     *    stream.send(cmd) — write a SimControlCommand
     *    stream.onSnapshot(fn) — register SystemSnapshot callback
     *    stream.onError(fn)
     *    stream.onEnd(fn)
     *    stream.close()
     */
    connect(hostname) {
        const client = new GrpcWebClientBase({ format: 'text' });
        const method = '/io_simulator.IOSimulationEngine/StreamSimulation';

        // serverStreaming with no request body opens a bidi channel.
        // We pass an empty message as placeholder — grpc-web requires a request
        // for the initial frame, even for bidi.
        const emptyReq = new pb.SimControlCommand();
        // Set a dummy action so the proto isn't empty
        emptyReq.setAction(0);

        const stream = client.serverStreaming(method, emptyReq, null);

        const handle = {
            _stream: stream,

            send(cmd) {
                stream.write(cmd);
            },

            onSnapshot(fn) {
                stream.on('data', fn);
            },

            onError(fn) {
                stream.on('error', fn);
            },

            onEnd(fn) {
                stream.on('end', fn);
            },

            close() {
                stream.cancel();
            }
        };

        return handle;
    },

    // ---- Convenience factories for protobuf messages ----

    newInitCommand(config, userContext) {
        const cmd = new pb.SimControlCommand();
        cmd.setAction(pb.SimControlCommand.Action.ACTION_INIT);
        cmd.setConfig(config);
        if (userContext) cmd.setUserContext(userContext);
        return cmd;
    },

    newStepCommand() {
        const cmd = new pb.SimControlCommand();
        cmd.setAction(pb.SimControlCommand.Action.ACTION_STEP_NEXT);
        return cmd;
    },

    newInjectFaultCommand(faultType) {
        const cmd = new pb.SimControlCommand();
        cmd.setAction(pb.SimControlCommand.Action.ACTION_INJECT_FAULT);
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
