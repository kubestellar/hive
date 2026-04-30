#!/bin/bash
# architecture-detector.sh — Detect issues needing architecture review / lane transfer.
# Reads actionable.json, adds architecture_signals to issues.
# Issues flagged here get lane="architect" in the classifier output.
#
# Signals are configurable via hive-project.yaml → classification.architecture_signals

set -euo pipefail

INPUT_FILE="/var/run/hive-metrics/actionable.json"
LOG="/var/log/kick-agents.log"

PROJECT_YAML="${HIVE_PROJECT_YAML:-/etc/hive/hive-project.yaml}"
if [ ! -f "$PROJECT_YAML" ]; then
  PROJECT_YAML="$(dirname "$(dirname "$0")")/examples/kubestellar/hive-project.yaml"
fi

log() { echo "[$(date -Is)] ARCH-DETECT $*" >> "$LOG"; }

if [ ! -f "$INPUT_FILE" ]; then
  log "ERROR: $INPUT_FILE not found"
  exit 0
fi

python3 -c "
import json, sys, re
import yaml

with open(sys.argv[1]) as f:
    data = json.load(f)

# Load configurable signals from project yaml
arch_config = {}
try:
    with open(sys.argv[2]) as f:
        cfg = yaml.safe_load(f)
    arch_config = cfg.get('classification', {}).get('architecture_signals', {})
except Exception:
    pass

# Defaults (overridden by config)
title_patterns = arch_config.get('title_patterns', [
    r'\b(refactor|redesign|migrat|api\s+change|breaking\s+change|rearchitect)\b'
])
labels = set(arch_config.get('labels', ['architecture', 'epic', 'rfc', 'redesign']))
min_directories = arch_config.get('min_directories', 4)

compiled_patterns = [re.compile(p, re.IGNORECASE) for p in title_patterns]

issues = data.get('issues', {}).get('items', [])
flagged = 0

for issue in issues:
    if issue.get('needs_architecture_review'):
        continue

    issue_labels = set(issue.get('labels', []))
    title = issue.get('title', '')

    signals = []
    if issue_labels & labels:
        signals.append(f'label: {\", \".join(issue_labels & labels)}')
    for pattern in compiled_patterns:
        if pattern.search(title):
            signals.append(f'title: {pattern.pattern}')
            break

    if signals:
        issue['needs_architecture_review'] = True
        issue['architecture_signals'] = signals
        if issue.get('lane') == 'scanner':
            issue['lane'] = 'architect'
        flagged += 1

data['issues']['items'] = issues

with open(sys.argv[1], 'w') as f:
    json.dump(data, f, indent=2)

print(f'Architecture signals: {flagged} issues flagged for architect review')
" "$INPUT_FILE" "$PROJECT_YAML"

log "DONE"
