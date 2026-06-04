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

const CLI_READY_POLL_MS = 2000;
const CLI_READY_TIMEOUT_MS = 600000;
const CONTAINER_NAME = process.env.HIVE_CONTAINER_NAME || 'hive-contributor';

function getCLIState() {
  try {
    const output = execSync(
      `tmux capture-pane -t ${TMUX_SESSION} -p 2>/dev/null`,
      { encoding: 'utf8', timeout: 5000 }
    );
    const text = output.toString();
    if (BACKEND === 'claude') {
      if (/Not logged in|Please run \/login/.test(text)) return 'needs-login';
      if (/bypass permissions|Welcome back|Try "how does|medium.*effort|@gmail\.com|@.*\.com.*Organization/.test(text)) return 'ready';
      if (/Choose the text style|trust this folder/.test(text)) return 'onboarding';
    } else if (BACKEND === 'copilot') {
      if (/copilot login|gh auth login/.test(text)) return 'needs-login';
      if (/Confirm folder trust|trust the files|Do you trust/.test(text)) return 'onboarding';
      if (/\/ commands.*help/.test(text)) return 'ready';
    } else if (BACKEND === 'gemini') {
      if (/not authenticated|login required/i.test(text)) return 'needs-login';
      if (/>\s*$|❯/.test(text)) return 'ready';
    } else {
      if (/>\s*$|❯|\$\s*$/.test(text)) return 'ready';
    }
    return 'starting';
  } catch (_) {
    return 'starting';
  }
}

function waitForCLI() {
  let loginMessageShown = false;
  return new Promise((resolve, reject) => {
    const start = Date.now();
    const check = () => {
      const state = getCLIState();
      if (state === 'ready') {
        console.log('CLI ready — accepting tasks');
        resolve();
      } else if (state === 'needs-login' && !loginMessageShown) {
        loginMessageShown = true;
        console.log('');
        console.log('╔══════════════════════════════════════════════════════════╗');
        console.log('║  Claude Code needs authentication.                      ║');
        console.log('║  In another terminal, run:                              ║');
        console.log(`║  docker exec -it ${CONTAINER_NAME} tmux attach -t ${TMUX_SESSION}`);
        console.log('║  Then type: /login                                      ║');
        console.log('║  Complete the login, then press Ctrl-B D to detach.     ║');
        console.log('║  Waiting for login to complete...                       ║');
        console.log('╚══════════════════════════════════════════════════════════╝');
        console.log('');
        setTimeout(check, CLI_READY_POLL_MS);
      } else if (Date.now() - start > CLI_READY_TIMEOUT_MS) {
        reject(new Error('CLI did not become ready within timeout'));
      } else {
        setTimeout(check, CLI_READY_POLL_MS);
      }
    };
    check();
  });
}

let cliReady = false;
let pendingTask = null;

waitForCLI().then(() => {
  cliReady = true;
  if (pendingTask) {
    const task = pendingTask;
    pendingTask = null;
    tmuxSendKeys(task);
  }
}).catch(e => console.error(e.message));

const ENTER_COUNT = 3;
const ENTER_DELAY_MS = 300;

function sleepMs(ms) {
  execSync(`sleep ${ms / 1000}`, { timeout: ms + 1000 });
}

function tmuxSendEnters() {
  for (let i = 0; i < ENTER_COUNT; i++) {
    execSync(`tmux send-keys -t ${TMUX_SESSION} Enter`, { timeout: 5000 });
    if (i < ENTER_COUNT - 1) sleepMs(ENTER_DELAY_MS);
  }
}

const CLEAR_CONTEXT_THRESHOLD_PCT = 70;

function checkContextUsage() {
  try {
    const output = execSync(
      `tmux capture-pane -t ${TMUX_SESSION} -p -S -3 2>/dev/null`,
      { encoding: 'utf8', timeout: 5000 }
    );
    const match = output.match(/ctx:(\d+)%|(\d+)% context/);
    return match ? parseInt(match[1] || match[2], 10) : 0;
  } catch (_) {
    return 0;
  }
}

function tmuxSendKeys(text) {
  try {
    const ctxPct = checkContextUsage();
    if (ctxPct >= CLEAR_CONTEXT_THRESHOLD_PCT) {
      console.log(`Context at ${ctxPct}% — sending /clear before next task`);
      execSync(`tmux send-keys -t ${TMUX_SESSION} Escape`, { timeout: 5000 });
      sleepMs(200);
      execSync(`tmux send-keys -t ${TMUX_SESSION} C-a`, { timeout: 5000 });
      execSync(`tmux send-keys -t ${TMUX_SESSION} C-k`, { timeout: 5000 });
      sleepMs(200);
      execSync(`tmux send-keys -t ${TMUX_SESSION} -l '/clear'`, { timeout: 5000 });
      sleepMs(200);
      tmuxSendEnters();
      sleepMs(3000);
    }
    execSync(`tmux send-keys -t ${TMUX_SESSION} Escape`, { timeout: 5000 });
    sleepMs(200);
    execSync(`tmux send-keys -t ${TMUX_SESSION} C-a`, { timeout: 5000 });
    execSync(`tmux send-keys -t ${TMUX_SESSION} C-k`, { timeout: 5000 });
    sleepMs(200);
    execSync(`tmux send-keys -t ${TMUX_SESSION} -l ${shellQuote(text)}`, { timeout: 10000 });
    sleepMs(300);
    tmuxSendEnters();
    console.log('Task prompt sent to CLI');
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
      `tmux capture-pane -t ${TMUX_SESSION} -p 2>/dev/null`,
      { encoding: 'utf8', timeout: 5000 }
    );
    const text = output.toString();
    let hasIdlePrompt, hasCompletionMarker, isWorking;

    if (BACKEND === 'claude') {
      hasIdlePrompt = /bypass permissions|shift\+tab to cycle/.test(text);
      hasCompletionMarker = /[✻✶✽] \S+ed for \d+[ms]|Honking|tokens\)/.test(text);
      isWorking = /─.*Bash\(|Reading|Editing|Writing|Searching|ing…/.test(text);
    } else if (BACKEND === 'copilot') {
      hasIdlePrompt = /\/ commands.*help/.test(text);
      hasCompletionMarker = /All tests pass|PR created|PR #|pull\/|pushed|committed|Done|finished/i.test(text);
      isWorking = /◉|◎|esc cancel|Running|Executing|Thinking|searching/i.test(text);
    } else if (BACKEND === 'gemini') {
      hasIdlePrompt = />\s*$|❯\s*$/.test(text);
      hasCompletionMarker = /completed|Done|finished/i.test(text);
      isWorking = /Thinking|Running|Searching/i.test(text);
    } else {
      hasIdlePrompt = />\s*$|\$\s*$/.test(text);
      hasCompletionMarker = /completed|done|finished/i.test(text);
      isWorking = false;
    }
    return hasIdlePrompt && hasCompletionMarker && !isWorking;
  } catch (_) {
    return false;
  }
}

const TASK_GRACE_PERIOD_MS = 180000;
let taskAssignedAt = 0;
let tasksCompletedCount = 0;
const PR_REVIEW_EVERY_N = 5;

function startProgressReporting() {
  if (progressInterval) clearInterval(progressInterval);
  if (!taskAssignedAt) taskAssignedAt = Date.now();
  progressInterval = setInterval(() => {
    if (!currentTask) return;
    if (Date.now() - taskAssignedAt < TASK_GRACE_PERIOD_MS) return;
    const idle = checkTmuxIdle();
    const tmuxLines = captureTmuxLines(TMUX_TAIL_LINES);
    if (idle) {
      console.log(`Task ${currentTask.task_id} completed — agent idle`);
      send({ type: 'task_complete', seq: nextSeq(), task_id: currentTask.task_id, result: 'completed', summary: 'Agent returned to idle', tmux_output: tmuxLines });
      const completedRepo = currentTask.repo;
      currentTask = null;
      taskAssignedAt = 0;
      clearInterval(progressInterval);
      progressInterval = null;
      tasksCompletedCount++;
      if (tasksCompletedCount % PR_REVIEW_EVERY_N === 0) {
        console.log(`PR review cycle (${tasksCompletedCount} tasks completed) — checking open PRs`);
        currentTask = { task_id: `pr-review-${Date.now()}`, kind: 'review', repo: completedRepo, number: 0, title: 'Review open PRs for comments' };
        taskAssignedAt = Date.now();
        const reviewPrompt = `Check your open PRs on ${completedRepo} for review comments. ` +
          `Run 'GH_TOKEN=$GH_TOKEN gh pr list --repo ${completedRepo} --author @me --state open' to find them. ` +
          `For each PR with review comments, read the comments, address the feedback, push fixes, and respond. ` +
          `If no PRs have comments, just say "No PR comments to address."`;
        tmuxSendKeys(reviewPrompt);
        startProgressReporting();
      } else {
        send({ type: 'ready', seq: nextSeq() });
      }
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
      if (!currentTask) {
        send({ type: 'ready', seq: nextSeq() });
      } else {
        console.log(`Reconnected while working on ${currentTask.repo}#${currentTask.number} — resuming`);
        send({ type: 'task_accepted', seq: nextSeq(), task_id: currentTask.task_id });
        send({ type: 'task_progress', seq: nextSeq(), task_id: currentTask.task_id, kind: currentTask.kind, repo: currentTask.repo, number: currentTask.number, title: currentTask.title, status: 'working' });
        startProgressReporting();
      }
      break;

    case 'auth_failed':
      console.error(`Authentication failed: ${msg.reason}`);
      process.exit(1);
      break;

    case 'task_assign':
      if (currentTask) {
        console.log(`Rejecting task ${msg.repo}#${msg.number} — already working on ${currentTask.repo}#${currentTask.number}`);
        send({ type: 'task_failed', seq: nextSeq(), task_id: msg.task_id, reason: 'Already has active task' });
        break;
      }
      currentTask = msg;
      console.log(`Task assigned: ${msg.kind} ${msg.repo}#${msg.number} — ${msg.title}`);
      if (msg.github_token) {
        injectGhToken(msg.github_token);
        tokenExpiresAt = msg.token_expires_at ? new Date(msg.token_expires_at).getTime() : null;
      }
      fs.writeFileSync(TASK_FILE, JSON.stringify(msg, null, 2));
      send({ type: 'task_accepted', seq: nextSeq(), task_id: msg.task_id });
      const taskPrompt = msg.prompt || `Work on ${msg.kind} ${msg.repo}#${msg.number}: ${msg.title}`;
      if (cliReady) {
        tmuxSendKeys(taskPrompt);
      } else {
        console.log('CLI not ready yet — queuing task prompt');
        pendingTask = taskPrompt;
      }
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
      taskAssignedAt = 0;
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
}

process.on('SIGTERM', () => { cleanup(); process.exit(0); });
process.on('SIGINT', () => { cleanup(); process.exit(0); });

connect();
