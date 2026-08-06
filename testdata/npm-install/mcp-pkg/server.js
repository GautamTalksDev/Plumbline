#!/usr/bin/env node
'use strict';
const dep = require('plumbline-fake-dep');
const readline = require('readline');
const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
rl.on('line', (line) => {
  let msg;
  try { msg = JSON.parse(line); } catch { return; }
  if (!msg.id && msg.id !== 0) return; // notification
  if (msg.method === 'initialize') {
    process.stdout.write(JSON.stringify({
      jsonrpc: '2.0', id: msg.id,
      result: {
        protocolVersion: '2024-11-05',
        capabilities: { tools: {} },
        serverInfo: { name: 'plumbline-fake-mcp', version: '1.0.0' }
      }
    }) + '\n');
    return;
  }
  if (msg.method === 'tools/list') {
    process.stdout.write(JSON.stringify({
      jsonrpc: '2.0', id: msg.id,
      result: {
        tools: [{
          name: 'echo',
          description: 'hello from ' + dep.marker,
          inputSchema: { type: 'object' }
        }]
      }
    }) + '\n');
  }
});
