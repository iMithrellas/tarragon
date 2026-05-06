#!/usr/bin/env node
const net = require("node:net");
const readline = require("node:readline");

const pluginName = process.env.TARRAGON_PLUGIN_NAME || "template_js_ts";

function log(message) {
  process.stderr.write(`[PLUGIN: ${pluginName}] ${message}\n`);
}

function variants(text) {
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

function payload(text) {
  return { input: text, variants: variants(text) };
}

function printQuery(text) {
  process.stdout.write(`${JSON.stringify(payload(text))}\n`);
}

function send(socket, msg) {
  socket.write(`${JSON.stringify(msg)}\n`);
}

function runDaemon(endpoint) {
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
    let msg;
    try {
      msg = JSON.parse(line);
    } catch (err) {
      log(`invalid request: ${err.message}`);
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

function main(argv) {
  if (argv.length >= 3 && argv[0] === "tarragon" && argv[1] === "query") {
    printQuery(argv.slice(2).join(" "));
    return;
  }

  const endpoint = process.env.TARRAGON_PLUGINS_ENDPOINT;
  if (!endpoint) {
    log("idle mode; TARRAGON_PLUGINS_ENDPOINT is not set");
    setInterval(() => {}, 1 << 30);
    return;
  }
  runDaemon(endpoint);
}

main(process.argv.slice(2));
