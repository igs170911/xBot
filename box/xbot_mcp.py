#!/usr/bin/python3
"""XBot MCP for programs on the bot's computer (Hermes, Claude Code, Codex): a stdio MCP server that relays every
JSON-RPC message to XBot over the product socket, where the XBot MCP answers as this bot (C11).

It replaces LazyBoy's product_mcp.py. The tools are not defined here: XBot lists them (only the ones open to bots)
and the socket decides who is calling, so nothing in this file can widen what the bot may do. Only the standard
library, so it runs on any Python in the image.

  stdin/stdout: newline-delimited JSON-RPC (MCP stdio transport)
  socket:       POST /internal/mcp on $XBOT_PRODUCT_SOCK (stateless Streamable HTTP, JSON responses)
  auth:         Authorization: Bearer <worker key> ($XBOT_WORKER_KEY, or $HERMES_HOME/.worker-key)
"""
import http.client
import json
import os
from pathlib import Path
import socket
import sys
import threading

SOCKET = os.environ.get("XBOT_PRODUCT_SOCK", "/product-ipc/product.sock")
PATH = "/internal/mcp"
# approvals.wait may hold a request for up to 300 seconds.
TIMEOUT = 330

_write = threading.Lock()
_protocol = None


class UnixConnection(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__("product", timeout=TIMEOUT)
        self.unix_path = path

    def connect(self):
        sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        sock.settimeout(self.timeout)
        sock.connect(self.unix_path)
        self.sock = sock


def worker_key():
    key = os.environ.get("XBOT_WORKER_KEY", "").strip()
    if key:
        return key
    return Path(os.environ.get("HERMES_HOME", "/hermes"), ".worker-key").read_text().strip()


def emit(message):
    with _write:
        sys.stdout.write(json.dumps(message, ensure_ascii=False, separators=(",", ":")) + "\n")
        sys.stdout.flush()


def fail(ident, text):
    if ident is not None:
        emit({"jsonrpc": "2.0", "id": ident, "error": {"code": -32000, "message": text}})


def replies(body, content_type):
    """The JSON-RPC messages in a response body: plain JSON, or an SSE stream of data lines."""
    if "text/event-stream" not in content_type:
        return [json.loads(body)]
    out, data = [], []
    for line in body.decode().splitlines() + [""]:
        if line.startswith("data:"):
            data.append(line[5:].lstrip())
        elif not line and data:
            out.append(json.loads("\n".join(data)))
            data = []
    return out


def forward(line):
    global _protocol
    try:
        message = json.loads(line)
    except ValueError:
        emit({"jsonrpc": "2.0", "id": None, "error": {"code": -32700, "message": "Parse error"}})
        return
    ident = message.get("id") if isinstance(message, dict) else None
    method = message.get("method") if isinstance(message, dict) else None
    headers = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
    try:
        headers["Authorization"] = "Bearer " + worker_key()
    except OSError as error:
        fail(ident, f"讀不到這台電腦的 worker-key（{error}）")
        return
    if _protocol:
        headers["MCP-Protocol-Version"] = _protocol
    try:
        connection = UnixConnection(SOCKET)
        connection.request("POST", PATH, body=line.encode(), headers=headers)
        response = connection.getresponse()
        body = response.read()
        connection.close()
    except OSError as error:
        fail(ident, f"連不上 XBot（{error}）")
        return
    if response.status == 202 or not body.strip():
        return
    if response.status != 200:
        fail(ident, f"XBot 回應 {response.status}：{body.decode(errors='replace').strip()[:500]}")
        return
    try:
        out = replies(body, response.getheader("Content-Type", ""))
    except ValueError:
        fail(ident, "XBot 的回應看不懂")
        return
    for reply in out:
        if method == "initialize" and isinstance(reply, dict) and isinstance(reply.get("result"), dict):
            _protocol = reply["result"].get("protocolVersion") or _protocol
        emit(reply)


def main():
    pending = []
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        # initialize goes first and alone: later requests carry the negotiated protocol version.
        if '"initialize"' in line and _protocol is None:
            forward(line)
        else:
            worker = threading.Thread(target=forward, args=(line,), daemon=True)
            worker.start()
            pending = [t for t in pending if t.is_alive()] + [worker]
    # stdin closed: answer what is still in flight before exiting.
    for worker in pending:
        worker.join()


if __name__ == "__main__":
    main()
