// 本地 WS/TCP 回显测试服务器：bun run socket:demo
//   ws://localhost:8124/<任意路径>   收到什么回写什么（文本回文本、二进制回二进制）
//   tcp://127.0.0.1:8125             收到什么回写什么
// 想测服务器主动关闭（close 事件）：往 WS 发文本 "/close"，服务器以 1000 关闭。
// 端口可用环境变量覆盖：WS_PORT=... TCP_PORT=...
const wsPort = Number(process.env.WS_PORT ?? 8124);
const tcpPort = Number(process.env.TCP_PORT ?? 8125);

const ws = Bun.serve({
  port: wsPort,
  fetch(req, server) {
    if (server.upgrade(req)) return;
    return new Response(`WebSocket echo demo: connect with ws://localhost:${wsPort}/\n`);
  },
  websocket: {
    message(socket, message) {
      if (message === "/close") {
        socket.close(1000, "demo close");
        return;
      }
      socket.send(message);
    },
  },
});

const tcp = Bun.listen({
  hostname: "127.0.0.1",
  port: tcpPort,
  socket: {
    data(socket, data) {
      socket.write(data);
    },
  },
});

console.log(`WS echo  : ws://localhost:${ws.port}/`);
console.log(`TCP echo : tcp://127.0.0.1:${tcp.port}`);
