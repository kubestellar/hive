#!/bin/bash
# run-pipeline.sh — Execute the pre-kick pipeline stages in dependency order.
# Reads pipeline config from hive-project.yaml and runs each stage's script.
# Called by kick-agents.sh before sending work orders to agents.
#
# Usage:
#   run-pipeline.sh              # run all pre-kick stages
#   run-pipeline.sh --agent scanner  # run only stages that feed scanner
#   run-pipeline.sh --stage classifier  # run a specific stage (+ its deps)
#
# Pipeline categories:
#   enumerator → classifier → gate → monitor
#   (enforcers are always-on, not run here)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
LOG="/var/log/kick-agents.log"

PROJECT_YAML="${HIVE_PROJECT_YAML:-/etc/hive/hive-project.yaml}"
if [ ! -f "$PROJECT_YAML" ]; then
  PROJECT_YAML="${SCRIPT_DIR}/../examples/kubestellar/hive-project.yaml"
fi

FILTER_AGENT=""
FILTER_STAGE=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --agent) FILTER_AGENT="$2"; shift 2 ;;
    --stage) FILTER_STAGE="$2"; shift 2 ;;
    *) shift ;;
  esac
done

log() { echo "[$(date -Is)] PIPELINE $*" >> "$LOG"; }

log "START — project config: $PROJECT_YAML"

# Resolve and execute pipeline stages in dependency order
python3 -c "
import yaml, sys, os, subprocess, time, json

project_yaml = sys.argv[1]
bin_dir = sys.argv[2]
filter_agent = sys.argv[3] if len(sys.argv) > 3 else ''
filter_stage = sys.argv[4] if len(sys.argv) > 4 else ''

with open(project_yaml) as f:
    cfg = yaml.safe_load(f)

stages = cfg.get('pipeline', {}).get('stages', [])

# Filter to pre-kick stages only (enforcers are always-on)
stages = [s for s in stages if s.get('phase') == 'pre-kick']

# Filter by agent if specified
if filter_agent:
    def feeds_agent(stage):
        consumers = stage.get('consumers', [])
        if consumers == 'all':
            return True
        return filter_agent in consumers
    # Include stage + all its transitive dependencies
    needed = set()
    def add_with_deps(name):
        if name in needed:
            return
        needed.add(name)
        stage = next((s for s in stages if s['name'] == name), None)
        if stage:
            for dep in stage.get('depends', []):
                add_with_deps(dep)
    for s in stages:
        if feeds_agent(s):
            add_with_deps(s['name'])
    stages = [s for s in stages if s['name'] in needed]

# Filter by specific stage if specified
if filter_stage:
    needed = set()
    def add_with_deps(name):
        if name in needed:
            return
        needed.add(name)
        stage = next((s for s in stages if s['name'] == name), None)
        if stage:
            for dep in stage.get('depends', []):
                add_with_deps(dep)
    add_with_deps(filter_stage)
    stages = [s for s in stages if s['name'] in needed]

# Topological sort by dependencies
completed = set()
ordered = []
remaining = list(stages)
max_iterations = len(remaining) * 2
iteration = 0

while remaining and iteration < max_iterations:
    iteration += 1
    progress = False
    next_remaining = []
    for stage in remaining:
        deps = set(stage.get('depends', []))
        if deps <= completed:
            ordered.append(stage)
            completed.add(stage['name'])
            progress = True
        else:
            next_remaining.append(stage)
    remaining = next_remaining
    if not progress:
        print(f'ERROR: circular dependency in pipeline stages: {[s[\"name\"] for s in remaining]}', file=sys.stderr)
        sys.exit(1)

# Execute in order
results = {}
for stage in ordered:
    name = stage['name']
    script = stage.get('script')
    if not script or script == 'null':
        continue

    script_path = os.path.join(bin_dir, script)
    if not os.path.isfile(script_path):
        print(f'WARN: {script_path} not found — skipping {name}', file=sys.stderr)
        results[name] = {'status': 'skipped', 'reason': 'script not found'}
        continue

    start = time.time()
    print(f'  [{name}] running {script}...', file=sys.stderr)
    try:
        result = subprocess.run(
            ['bash', script_path],
            capture_output=True, text=True, timeout=120,
            env={**os.environ, 'HIVE_PROJECT_YAML': project_yaml}
        )
        elapsed = round(time.time() - start, 1)
        if result.returncode == 0:
            output_line = result.stdout.strip().split('\n')[-1] if result.stdout.strip() else ''
            print(f'  [{name}] OK ({elapsed}s) — {output_line}', file=sys.stderr)
            results[name] = {'status': 'ok', 'elapsed': elapsed, 'summary': output_line}
        else:
            print(f'  [{name}] FAIL ({elapsed}s) — {result.stderr.strip()[:200]}', file=sys.stderr)
            results[name] = {'status': 'error', 'elapsed': elapsed, 'error': result.stderr.strip()[:200]}
    except subprocess.TimeoutExpired:
        print(f'  [{name}] TIMEOUT (120s)', file=sys.stderr)
        results[name] = {'status': 'timeout'}

# Write pipeline run summary
summary = {
    'generated_at': time.strftime('%Y-%m-%dT%H:%M:%S+00:00', time.gmtime()),
    'stages_run': len(results),
    'stages_ok': sum(1 for r in results.values() if r.get('status') == 'ok'),
    'stages_failed': sum(1 for r in results.values() if r.get('status') in ('error', 'timeout')),
    'results': results
}

summary_path = '/var/run/hive-metrics/pipeline-run.json'
os.makedirs(os.path.dirname(summary_path), exist_ok=True)
with open(summary_path, 'w') as f:
    json.dump(summary, f, indent=2)

ok = summary['stages_ok']
fail = summary['stages_failed']
total = summary['stages_run']
print(f'Pipeline: {ok}/{total} stages OK' + (f', {fail} failed' if fail else ''))
" "$PROJECT_YAML" "$SCRIPT_DIR" "$FILTER_AGENT" "$FILTER_STAGE"

log "DONE — pipeline complete"
