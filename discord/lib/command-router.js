const http = require('http');
const https = require('https');
const { agentNames, agentPrefix } = require('./agent-identities');
const { commandResponse } = require('./formatter');

class CommandRouter {
  constructor(config) {
    this.config = config;
    this.validAgents = agentNames();
  }

  async handle(content, authorTag) {
    const parts = content.trim().split(/\s+/);
    const cmd = (parts[0] || '').toLowerCase();

    if (cmd === 'status') return this._status();
    if (cmd === 'governor') return this._governor();
    if (cmd === 'help') return this._help();

    if (this.validAgents.includes(cmd)) {
      const action = (parts[1] || '').toLowerCase();
      const rest = parts.slice(2).join(' ');

      if (action === 'kick' || (!action && !rest)) {
        return this._kick(cmd, rest);
      }
      if (action === 'pause') return this._pause(cmd);
      if (action === 'resume') return this._resume(cmd);

      return this._kick(cmd, parts.slice(1).join(' '));
    }

    if (['kick', 'pause', 'resume'].includes(cmd)) {
      const agent = (parts[1] || '').toLowerCase();
      if (!this.validAgents.includes(agent)) {
        return commandResponse(false, `Unknown agent: ${agent}. Valid: ${this.validAgents.join(', ')}`);
      }
      if (cmd === 'kick') return this._kick(agent, parts.slice(2).join(' '));
      if (cmd === 'pause') return this._pause(agent);
      if (cmd === 'resume') return this._resume(agent);
    }

    return commandResponse(false, `Unknown command. Try \`help\` for usage.`);
  }

  async _kick(agent, prompt) {
    const body = prompt ? JSON.stringify({ prompt }) : '{}';
    const res = await this._dashboardPost(`/api/kick/${agent}`, body);
    if (res.ok) {
      return commandResponse(true, prompt
        ? `Sent to ${agent}: "${prompt}"`
        : `Kicked ${agent}`);
    }
    return commandResponse(false, `Failed to kick ${agent}: ${res.error || 'unknown error'}`);
  }

  async _pause(agent) {
    const res = await this._dashboardPost(`/api/pause/${agent}`);
    if (res.ok) return commandResponse(true, `Paused ${agent}`);
    return commandResponse(false, `Failed to pause ${agent}: ${res.error || 'unknown error'}`);
  }

  async _resume(agent) {
    const res = await this._dashboardPost(`/api/resume/${agent}`);
    if (res.ok) return commandResponse(true, `Resumed ${agent}`);
    return commandResponse(false, `Failed to resume ${agent}: ${res.error || 'unknown error'}`);
  }

  async _status() {
    const data = await this._dashboardGet('/api/status');
    if (!data) return commandResponse(false, 'Could not reach dashboard');

    const lines = [];
    const gov = data.governor || {};
    lines.push(`**Governor:** ${gov.mode || 'unknown'} (issues: ${gov.issues || 0}, PRs: ${gov.prs || 0})`);
    lines.push(`**CI Pass Rate:** ${data.ciPassRate || '?'}%`);

    const agents = Array.isArray(data.agents) ? data.agents : [];
    for (const agent of agents) {
      const name = agent.name || '?';
      const busy = agent.busy || agent.state || 'unknown';
      const cadence = agent.cadence || '';
      const doing = agent.doing ? ` — ${agent.doing.slice(0, 80)}` : '';
      lines.push(`  ${agentPrefix(name)} ${busy} (${cadence})${doing}`);
    }

    const DISCORD_MSG_LIMIT = 1900;
    const result = lines.join('\n');
    return result.length > DISCORD_MSG_LIMIT ? result.slice(0, DISCORD_MSG_LIMIT) + '…' : result;
  }

  async _governor() {
    const data = await this._dashboardGet('/api/status');
    if (!data) return commandResponse(false, 'Could not reach dashboard');
    const gov = data.governor || {};
    const lines = [];
    lines.push(`**Mode:** ${gov.mode || 'unknown'}`);
    lines.push(`**Queue:** ${gov.issues || 0} issues, ${gov.prs || 0} PRs`);
    lines.push(`**Next Kick:** ${gov.nextKick || 'unknown'}`);
    if (data.budget) {
      const b = data.budget;
      lines.push(`**Budget:** $${b.BUDGET_USED || '?'} / $${b.BUDGET_WEEKLY || '?'} (${b.BUDGET_PCT_USED || '?'}% used)`);
    }
    return lines.join('\n') || 'No governor data available';
  }

  _help() {
    return [
      '**Hive Discord Bot Commands**',
      '`!status` — show system status',
      '`!governor` — show governor mode and thresholds',
      '`!scanner <prompt>` — send prompt to scanner',
      '`!reviewer <prompt>` — send prompt to reviewer',
      '`!kick <agent> [prompt]` — kick an agent with optional prompt',
      '`!pause <agent>` — pause an agent',
      '`!resume <agent>` — resume an agent',
      '`!help` — show this message',
      '',
      `Valid agents: ${this.validAgents.join(', ')}`,
    ].join('\n');
  }

  _dashboardPost(path, body) {
    return new Promise((resolve) => {
      const url = new URL(path, this.config.dashboardUrl);
      const options = {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        timeout: 10000,
      };
      const req = http.request(url, options, (res) => {
        let data = '';
        res.on('data', c => data += c);
        res.on('end', () => {
          try { resolve(JSON.parse(data)); } catch (_) { resolve({ ok: false, error: data }); }
        });
      });
      req.on('error', (e) => resolve({ ok: false, error: e.message }));
      req.on('timeout', () => { req.destroy(); resolve({ ok: false, error: 'timeout' }); });
      if (body) req.write(body);
      req.end();
    });
  }

  _dashboardGet(path) {
    return new Promise((resolve) => {
      const url = new URL(path, this.config.dashboardUrl);
      http.get(url, { timeout: 10000 }, (res) => {
        let data = '';
        res.on('data', c => data += c);
        res.on('end', () => {
          try { resolve(JSON.parse(data)); } catch (_) { resolve(null); }
        });
      }).on('error', () => resolve(null));
    });
  }
}

module.exports = { CommandRouter };
