import net from "node:net";
import readline from "node:readline";

type Action = {
  name: string;
  type?: string;
  default?: boolean;
  description?: string;
};

type Result = {
  id: string;
  label: string;
  actions?: Action[];
};

type RequestMessage = {
  type?: string;
  query_id?: string;
  text?: string;
  result_id?: string;
};

const pluginName = process.env.TARRAGON_PLUGIN_NAME || "template_js_ts";

function log(message: string): void {
  process.stderr.write(`[PLUGIN: ${pluginName}] ${message}\n`);
}

function variants(text: string): Result[] {
  return [
    [...text].reverse().join(""),
    text.toUpperCase(),
    text.replace(/\b\w/g, (c) => c.toUpperCase()),
  ].map((label, index) => ({
    id: String(index + 1),
    label,
    actions: [{ name: "select", default: true, description: "Acknowledge selection" }],
  }));
}

function payload(text: string): { input: string; variants: Result[] } {
  return { input: text, variants: variants(text) };
}

function send(socket: net.Socket, msg: unknown): void {
  socket.write(`${JSON.stringify(msg)}\n`);
}

function runDaemon(endpoint: string): void {
  const socket = net.createConnection({ path: endpoint }, () => {
    send(socket, { type: "hello", name: pluginName });
    log(`connected to ${endpoint}`);
  });

  socket.on("error", (err) => {
    log(`socket error: ${err.message}`);
    process.exitCode = 1;
  });

  const rl = readline.createInterface({ input: socket });
  rl.on("line", (line) => {
    let msg: RequestMessage;
    try {
      msg = JSON.parse(line) as RequestMessage;
    } catch (err) {
      log(`invalid request: ${(err as Error).message}`);
      return;
    }

    if (msg.type === "request") {
      send(socket, {
        type: "response",
        query_id: msg.query_id || "",
        data: payload(msg.text || ""),
      });
      return;
    }

    if (msg.type === "select") {
      send(socket, {
        type: "select_response",
        success: true,
        message: `selected ${msg.result_id || ""}`,
      });
    }
  });
}

const argv = process.argv.slice(2);
if (argv.length >= 3 && argv[0] === "tarragon" && argv[1] === "query") {
  process.stdout.write(`${JSON.stringify(payload(argv.slice(2).join(" ")))}\n`);
} else if (process.env.TARRAGON_PLUGINS_ENDPOINT) {
  runDaemon(process.env.TARRAGON_PLUGINS_ENDPOINT);
} else {
  log("idle mode; TARRAGON_PLUGINS_ENDPOINT is not set");
  setInterval(() => {}, 1 << 30);
}
