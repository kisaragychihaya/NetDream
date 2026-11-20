// 本地 Socket.IO 演示服务器（Engine.IO v4 / Socket.IO v5，直连 WebSocket 传输，
// 不实现 long-polling）：bun run socketio:demo
//
// 连接地址（后端按 http(s)://host[:port][/命名空间] 解析）：
//   http://localhost:6021/         默认命名空间
//   http://localhost:6021/admin    命名空间 /admin（CONNECT 包是 40/admin,）
//
// 事件约定：
//   create_room / join_room → 回 room_created {room, sid}
//   leave_room              → 回 room_left {room}
//   message                 → 原样回 message
//   其他事件                → 原样回同名事件（便于随便试）
//   带 ack id 的事件        → 另回 43<id>[<参数>]
// 服务器每 25s 发一次 2 ping，客户端应回 3 pong。
// 端口可用环境变量覆盖：SIO_PORT=...
const port = Number(process.env.SIO_PORT ?? 6021);
const pingInterval = 25000;

type Conn = { nsp: string; timer?: ReturnType<typeof setInterval> };

function newSid(): string {
  return typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
    ? crypto.randomUUID()
    : Math.random().toString(36).slice(2);
}

// connectReply 回 CONNECT 包：命名空间原样带回（默认命名空间不带 `/nsp,`）。
function connectReply(nsp: string): string {
  const prefix = nsp === "" ? "" : `${nsp},`;
  return `40${prefix}${JSON.stringify({ sid: newSid() })}`;
}

// parseEvent 解析 `42[<nsp>,][<id>]["事件",...参数]`。
function parseEvent(text: string): { nsp: string; id: string; name: string; args: unknown[] } | null {
  if (!text.startsWith("42")) return null;
  let rest = text.slice(2);
  let nsp = "";
  if (rest.startsWith("/")) {
    const comma = rest.indexOf(",");
    if (comma < 0) return null;
    nsp = rest.slice(0, comma);
    rest = rest.slice(comma + 1);
  }
  const digits = /^\d*/.exec(rest)?.[0] ?? "";
  let parsed: unknown;
  try {
    parsed = JSON.parse(rest.slice(digits.length));
  } catch {
    return null;
  }
  if (!Array.isArray(parsed) || typeof parsed[0] !== "string") return null;
  const [name, ...args] = parsed as [string, ...unknown[]];
  return { nsp, id: digits, name, args };
}

// handle 生成一条事件回复（含事件名），null = 不回。
function handle(nsp: string, name: string, args: unknown[]): string[] | null {
  switch (name) {
    case "create_room":
    case "join_room":
      return ["room_created", { room: String(args[0] ?? ""), nsp, sid: newSid() }];
    case "leave_room":
      return ["room_left", { room: String(args[0] ?? "") }];
    default:
      return [name, ...args];
  }
}

const server = Bun.serve<Conn>({
  port,
  fetch(req, server) {
    const url = new URL(req.url);
    if (url.pathname.includes("/socket.io/")) {
      if (server.upgrade(req, { data: { nsp: "" } })) return;
      return new Response("WebSocket upgrade failed\n", { status: 400 });
    }
    return new Response(`Socket.IO demo: connect with http://localhost:${server.port}/\n`);
  },
  websocket: {
    // 每个连接先收 Engine.IO 握手包：sid + 心跳参数（毫秒）。
    open(socket) {
      socket.send(`0${JSON.stringify({ sid: newSid(), upgrades: [], pingInterval, pingTimeout: 5000 })}`);
      socket.data.timer = setInterval(() => {
        try {
          socket.send("2");
        } catch {
          // 连接已经关了：close 回调会清掉这个定时器。
        }
      }, pingInterval);
    },
    message(socket, raw) {
      const text = typeof raw === "string" ? raw : raw.toString();
      if (text === "3") return; // 客户端回的 pong
      if (text.startsWith("40")) {
        const rest = text.slice(2);
        const comma = rest.indexOf(",");
        socket.data.nsp = comma >= 0 ? rest.slice(0, comma) : rest;
        socket.send(connectReply(socket.data.nsp));
        return;
      }
      if (text === "41") {
        socket.close(1000, "client disconnect");
        return;
      }
      const event = parseEvent(text);
      if (event === null) return;
      const reply = handle(event.nsp || socket.data.nsp, event.name, event.args);
      if (reply === null) return;
      const prefix = event.nsp === "" ? "" : `${event.nsp},`;
      socket.send(`42${prefix}${JSON.stringify(reply)}`);
      if (event.id !== "") {
        // ack 带的是事件名之后的参数。
        socket.send(`43${prefix}${event.id}${JSON.stringify(reply.slice(1))}`);
      }
    },
    close(socket) {
      if (socket.data.timer !== undefined) clearInterval(socket.data.timer);
    },
  },
});

console.log(`Socket.IO: http://localhost:${server.port}/   （命名空间示例 http://localhost:${server.port}/admin）`);
