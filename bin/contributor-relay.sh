#!/usr/bin/env node
// contributor-relay.sh — WebSocket client that connects a contributor agent to the Hive hub.
//
// Handles: authentication, task receipt, GitHub token injection, result reporting,
// heartbeat, and reconnection with exponential backoff.
//
// Environment:
//   HIVE_HUB              — WebSocket URL (wss://host:port/contribute)
//   HIVE_REGISTRATION_TOKEN — contributor's registration token
//   AGENT_BACKEND          — CLI backend name (claude, copilot, gemini, etc.)
//   AGENT_MODEL            — model override (optional)
//   HIVE_AGENT_SESSION     — tmux session name for the agent (default: contributor)

'use strict';

const WebSocket = require('ws');
const { execSync, execFile } = require('child_process');
const fs = require('fs');
const path = require('path');

const rawHub = process.env.HIVE_HUB || 'wss://hive.kubestellar.io:3001/contribute';
const HUB_URL = rawHub.replace(/\/contribute\/?$/, '/api/contribute/ws');
const REG_TOKEN = process.env.HIVE_REGISTRATION_TOKEN;
const BACKEND = process.env.AGENT_BACKEND || 'claude';
const MODEL = process.env.AGENT_MODEL || '';
const TMUX_SESSION = process.env.HIVE_AGENT_SESSION || 'contributor';
const GH_TOKEN_CACHE = '/var/run/hive-metrics/gh-app-token.cache';
const TASK_FILE = '/tmp/contributor-task.json';

const TMUX_TAIL_LINES = 15;
const HEARTBEAT_INTERVAL_MS = 30000;
const HEARTBEAT_TIMEOUT_MS = 90000;
const PROGRESS_REPORT_INTERVAL_MS = 120000;
const MAX_RECONNECT_DELAY_MS = 60000;
const BASE_RECONNECT_DELAY_MS = 1000;
const TOKEN_REFRESH_MARGIN_MS = 300000;

if (!REG_TOKEN) {
  console.error('FATAL: HIVE_REGISTRATION_TOKEN not set. Run `just contribute-register` first.');
  process.exit(1);
}

let ws = null;
let seq = 0;
let reconnectDelay = BASE_RECONNECT_DELAY_MS;
let heartbeatInterval = null;
let lastPong = Date.now();
let currentTask = null;
let progressInterval = null;
let tokenExpiresAt = null;

function nextSeq() { return ++seq; }

function send(msg) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify(msg));
  }
}

function injectGhToken(token) {
  const dir = path.dirname(GH_TOKEN_CACHE);
  try { fs.mkdirSync(dir, { recursive: true }); } catch (_) {}
  fs.writeFileSync(GH_TOKEN_CACHE, token, { mode: 0o600 });
}

function tmuxSendKeys(text) {
  try {
    execSync(`tmux send-keys -t ${TMUX_SESSION} C-u`, { timeout: 5000 });
    execSync(`tmux send-keys -t ${TMUX_SESSION} -l ${shellQuote(text)}`, { timeout: 5000 });
    execSync(`tmux send-keys -t ${TMUX_SESSION} Enter`, { timeout: 5000 });
  } catch (e) {
    console.error('tmux send-keys failed:', e.message);
  }
}

function shellQuote(s) {
  return "'" + s.replace(/'/g, "'\\''") + "'";
}

function captureTmuxLines(n) {
  try {
    const output = execSync(
      `tmux capture-pane -t ${TMUX_SESSION} -p -S -${n} 2>/dev/null`,
      { encoding: 'utf8', timeout: 5000 }
    );
    return output.trim().split('\n').slice(-n);
  } catch (_) {
    return [];
  }
}

function checkTmuxIdle() {
  try {
    const output = execSync(
      `tmux capture-pane -t ${TMUX_SESSION} -p -S -5 2>/dev/null`,
      { encoding: 'utf8', timeout: 5000 }
    );
    const lines = output.trim().split('\n');
    const lastLine = lines[lines.length - 1] || '';
    const idlePatterns = [/\$\s*$/, />\s*$/, /\?\s*$/, /claude.*>\s*$/i];
    return idlePatterns.some(p => p.test(lastLine));
  } catch (_) {
    return false;
  }
}

function startProgressReporting() {
  if (progressInterval) clearInterval(progressInterval);
  progressInterval = setInterval(() => {
    if (!currentTask) return;
    const idle = checkTmuxIdle();
    const tmuxLines = captureTmuxLines(TMUX_TAIL_LINES);
    if (idle) {
      send({ type: 'task_complete', seq: nextSeq(), task_id: currentTask.task_id, result: 'completed', summary: 'Agent returned to idle', tmux_output: tmuxLines });
      currentTask = null;
      clearInterval(progressInterval);
      progressInterval = null;
      send({ type: 'ready', seq: nextSeq() });
    } else {
      send({ type: 'task_progress', seq: nextSeq(), task_id: currentTask.task_id, status: 'working', tmux_output: tmuxLines });
    }
  }, PROGRESS_REPORT_INTERVAL_MS);
}

function handleMessage(data) {
  let msg;
  try { msg = JSON.parse(data); } catch (_) { return; }

  switch (msg.type) {
    case 'auth_challenge':
      send({
        type: 'auth_response',
        seq: nextSeq(),
        registration_token: REG_TOKEN,
        cli_backend: BACKEND,
        model: MODEL,
      });
      break;

    case 'auth_ok':
      console.log(`Authenticated as ${msg.contributor_id} (tier: ${msg.trust_tier})`);
      reconnectDelay = BASE_RECONNECT_DELAY_MS;
      send({ type: 'ready', seq: nextSeq() });
      break;

    case 'auth_failed':
      console.error(`Authentication failed: ${msg.reason}`);
      process.exit(1);
      break;

    case 'task_assign':
      currentTask = msg;
      console.log(`Task assigned: ${msg.kind} ${msg.repo}#${msg.number} — ${msg.title}`);
      if (msg.github_token) {
        injectGhToken(msg.github_token);
        tokenExpiresAt = msg.token_expires_at ? new Date(msg.token_expires_at).getTime() : null;
      }
      fs.writeFileSync(TASK_FILE, JSON.stringify(msg, null, 2));
      send({ type: 'task_accepted', seq: nextSeq(), task_id: msg.task_id });
      tmuxSendKeys(msg.prompt || `Work on ${msg.kind} ${msg.repo}#${msg.number}: ${msg.title}`);
      startProgressReporting();
      break;

    case 'token_refresh':
      if (msg.github_token) {
        injectGhToken(msg.github_token);
        tokenExpiresAt = msg.token_expires_at ? new Date(msg.token_expires_at).getTime() : null;
        console.log('GitHub token refreshed');
      }
      break;

    case 'task_revoke':
      console.log(`Task revoked: ${msg.task_id} — ${msg.reason}`);
      currentTask = null;
      if (progressInterval) { clearInterval(progressInterval); progressInterval = null; }
      send({ type: 'ready', seq: nextSeq() });
      break;

    case 'ping':
      send({ type: 'pong', seq: msg.seq });
      break;

    case 'pong':
      lastPong = Date.now();
      break;

    default:
      console.log('Unknown message type:', msg.type);
  }
}

function connect() {
  console.log(`Connecting to ${HUB_URL}...`);
  ws = new WebSocket(HUB_URL);

  ws.on('open', () => {
    console.log('Connected to hub');
    lastPong = Date.now();

    heartbeatInterval = setInterval(() => {
      if (Date.now() - lastPong > HEARTBEAT_TIMEOUT_MS) {
        console.error('Heartbeat timeout — reconnecting');
        ws.terminate();
        return;
      }
      send({ type: 'ping', seq: nextSeq() });
    }, HEARTBEAT_INTERVAL_MS);
  });

  ws.on('message', (data) => handleMessage(data.toString()));

  ws.on('close', () => {
    console.log(`Connection closed. Reconnecting in ${reconnectDelay}ms...`);
    cleanup();
    setTimeout(connect, reconnectDelay);
    reconnectDelay = Math.min(reconnectDelay * 2, MAX_RECONNECT_DELAY_MS);
  });

  ws.on('error', (err) => {
    console.error('WebSocket error:', err.message);
  });
}

function cleanup() {
  if (heartbeatInterval) { clearInterval(heartbeatInterval); heartbeatInterval = null; }
  if (progressInterval) { clearInterval(progressInterval); progressInterval = null; }
  currentTask = null;
}

process.on('SIGTERM', () => { cleanup(); process.exit(0); });
process.on('SIGINT', () => { cleanup(); process.exit(0); });

connect();
