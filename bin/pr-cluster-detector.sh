#!/bin/bash
# pr-cluster-detector.sh — Group related actionable issues into clusters for bundled dispatch.
# Reads actionable.json (after classification), updates clusters array.
# Scanner receives pre-computed clusters in kick message instead of clustering itself.
#
# Clustering signals:
#   1. Same component keyword in title (prefix before ":")
#   2. Same reporter within 30min window
#   3. Same label combo (kind + area/component)
#   4. Same failure mode keywords

set -euo pipefail

INPUT_FILE="/var/run/hive-metrics/actionable.json"
LOG="/var/log/kick-agents.log"

log() { echo "[$(date -Is)] CLUSTER $*" >> "$LOG"; }

if [ ! -f "$INPUT_FILE" ]; then
  log "ERROR: $INPUT_FILE not found — skipping cluster detection"
  exit 0
fi

# issue-classifier.sh already does basic clustering by cluster_key and reporter window.
# This script adds deeper clustering signals: label combos, failure mode keywords.

python3 -c "
import json, sys, re
from collections import defaultdict
from datetime import datetime, timezone

with open(sys.argv[1]) as f:
    data = json.load(f)

issues = data.get('issues', {}).get('items', [])
existing_clusters = data.get('clusters', [])

# Already-assigned cluster keys from issue-classifier.sh
existing_keys = {c['key'] for c in existing_clusters}

# --- Additional clustering: label combo ---
label_combo_groups = defaultdict(list)
for issue in issues:
    labels = issue.get('labels', [])
    kind_labels = sorted(l for l in labels if l.startswith('kind/'))
    area_labels = sorted(l for l in labels if l.startswith('area/') or l.startswith('component/'))
    if kind_labels and area_labels:
        combo_key = f\"label-{'+'.join(kind_labels)}-{'+'.join(area_labels)}\"
        label_combo_groups[combo_key].append({
            'repo': issue['repo'],
            'number': issue['number'],
            'title': issue['title']
        })

# --- Additional clustering: failure mode keywords ---
FAILURE_MODES = {
    'i18n': re.compile(r'\b(i18n|translation|locali[sz]|t\(\)|intl)\b', re.IGNORECASE),
    'null-check': re.compile(r'\b(null|undefined|cannot read|TypeError|NoneType)\b', re.IGNORECASE),
    'css-visual': re.compile(r'\b(overflow|truncat|align|margin|padding|z-index|opacity|visible|hidden)\b', re.IGNORECASE),
    'test-flake': re.compile(r'\b(flaky|intermittent|timeout|race condition|retry)\b', re.IGNORECASE),
}

failure_groups = defaultdict(list)
for issue in issues:
    title = issue.get('title', '')
    for mode_key, pattern in FAILURE_MODES.items():
        if pattern.search(title):
            failure_groups[f'failure-{mode_key}'].append({
                'repo': issue['repo'],
                'number': issue['number'],
                'title': issue['title']
            })

# --- Merge new clusters with existing ---
new_clusters = []
for key, group in {**label_combo_groups, **failure_groups}.items():
    if len(group) >= 2 and key not in existing_keys:
        new_clusters.append({
            'key': key,
            'issues': group,
            'count': len(group)
        })

all_clusters = existing_clusters + new_clusters
all_clusters.sort(key=lambda c: c['count'], reverse=True)

# Deduplicate: if an issue appears in multiple clusters, keep the largest
seen_issues = set()
deduped = []
for cluster in all_clusters:
    unique_issues = [
        i for i in cluster['issues']
        if f\"{i['repo']}#{i['number']}\" not in seen_issues
    ]
    if len(unique_issues) >= 2:
        for i in unique_issues:
            seen_issues.add(f\"{i['repo']}#{i['number']}\")
        cluster['issues'] = unique_issues
        cluster['count'] = len(unique_issues)
        deduped.append(cluster)

data['clusters'] = deduped

with open(sys.argv[1], 'w') as f:
    json.dump(data, f, indent=2)

print(f'Clusters: {len(deduped)} groups covering {sum(c[\"count\"] for c in deduped)} issues')
" "$INPUT_FILE"

log "DONE — cluster detection complete"
