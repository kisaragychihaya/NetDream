// 本地 SSE 测试服务器：bun run sse:demo，然后请求 http://localhost:8123/sse。
// 每 500ms 推一条事件，30 条后自动结束；客户端中断不会导致进程退出。
// 端口被占时可用 PORT 环境变量换一个：PORT=8124 bun run sse:demo
Bun.serve({
  port: Number(process.env.PORT ?? 8123),
  fetch(req) {
    const url = new URL(req.url);
    if (url.pathname === "/sse") {
      const encoder = new TextEncoder();
      let n = 0;
      const stream = new ReadableStream({
        start(controller) {
          const timer = setInterval(() => {
            n++;
            try {
              controller.enqueue(
                encoder.encode(`id: ${n}\nevent: tick\ndata: ${JSON.stringify({ n, time: new Date().toISOString() })}\n\n`),
              );
            } catch {
              clearInterval(timer); // client disconnected
              return;
            }
            if (n >= 30) {
              clearInterval(timer);
              controller.close();
            }
          }, 500);
        },
      });
      return new Response(stream, {
        headers: {
          "Content-Type": "text/event-stream; charset=utf-8",
          "Cache-Control": "no-cache",
        },
      });
    }
    return new Response("SSE demo: GET /sse\n");
  },
});
console.log("SSE demo on http://localhost:8123/sse");
