#!/bin/bash
# ga4-anomaly-detector.sh — Pre-compute GA4 error anomalies for the reviewer agent.
# Compares recent error counts against 7-day baseline.
# Writes /var/run/hive-metrics/ga4-anomalies.json.
#
# Requires: GA4 service account key (path from hive-project.yaml → outreach.ga4.service_account_key)
# Falls back to "no data" output if GA4 is unavailable.

set -euo pipefail

OUTPUT_FILE="/var/run/hive-metrics/ga4-anomalies.json"
TMP_FILE="${OUTPUT_FILE}.tmp"
LOG="/var/log/kick-agents.log"
REAL_GH="/usr/bin/gh"

PROJECT_YAML="${HIVE_PROJECT_YAML:-/etc/hive/hive-project.yaml}"
if [ ! -f "$PROJECT_YAML" ]; then
  PROJECT_YAML="$(find "$(dirname "$(dirname "$0")")/examples" -name 'hive-project.yaml' -type f 2>/dev/null | head -1)"
fi

log() { echo "[$(date -Is)] GA4-ANOMALY $*" >> "$LOG"; }

# Read GA4 config from project yaml
GA4_CONFIG=$(python3 -c "
import yaml, sys, json
with open(sys.argv[1]) as f:
    cfg = yaml.safe_load(f)
ga4 = cfg.get('outreach', {}).get('ga4', {})
print(json.dumps(ga4))
" "$PROJECT_YAML" 2>/dev/null || echo '{}')

PROPERTY_ID=$(echo "$GA4_CONFIG" | python3 -c "import json,sys; print(json.load(sys.stdin).get('property_id',''))" 2>/dev/null)
SA_KEY=$(echo "$GA4_CONFIG" | python3 -c "import json,sys; print(json.load(sys.stdin).get('service_account_key',''))" 2>/dev/null)

if [ -z "$PROPERTY_ID" ] || [ -z "$SA_KEY" ] || [ ! -f "$SA_KEY" ]; then
  log "SKIP — GA4 not configured or service account key missing"
  python3 -c "
import json
from datetime import datetime, timezone
result = {
    'generated_at': datetime.now(timezone.utc).isoformat(),
    'status': 'unavailable',
    'reason': 'GA4 not configured or service account key missing',
    'anomalies': [],
    'summary': 'GA4 data unavailable — skipping anomaly detection'
}
print(json.dumps(result, indent=2))
" > "$OUTPUT_FILE"
  exit 0
fi

log "START — checking GA4 property $PROPERTY_ID"

# Use google-auth + requests to query GA4 Data API
python3 -c "
import json, sys, os
from datetime import datetime, timezone, timedelta

property_id = sys.argv[1]
sa_key_path = sys.argv[2]
output_path = sys.argv[3]

now = datetime.now(timezone.utc)

try:
    from google.oauth2 import service_account
    from google.analytics.data_v1beta import BetaAnalyticsDataClient
    from google.analytics.data_v1beta.types import RunReportRequest, DateRange, Dimension, Metric

    credentials = service_account.Credentials.from_service_account_file(
        sa_key_path,
        scopes=['https://www.googleapis.com/auth/analytics.readonly']
    )
    client = BetaAnalyticsDataClient(credentials=credentials)

    # Recent window (last 30 minutes approximated as today's last-hour data)
    recent_request = RunReportRequest(
        property=f'properties/{property_id}',
        date_ranges=[DateRange(start_date='today', end_date='today')],
        dimensions=[Dimension(name='eventName')],
        metrics=[Metric(name='eventCount')],
        dimension_filter={
            'filter': {
                'field_name': 'eventName',
                'string_filter': {'match_type': 'CONTAINS', 'value': 'error'}
            }
        }
    )
    recent_response = client.run_report(recent_request)

    # Baseline (last 7 days)
    baseline_request = RunReportRequest(
        property=f'properties/{property_id}',
        date_ranges=[DateRange(start_date='7daysAgo', end_date='yesterday')],
        dimensions=[Dimension(name='eventName')],
        metrics=[Metric(name='eventCount')],
    )
    baseline_response = client.run_report(baseline_request)

    recent_events = {}
    for row in recent_response.rows:
        event_name = row.dimension_values[0].value
        count = int(row.metric_values[0].value)
        recent_events[event_name] = count

    baseline_events = {}
    for row in baseline_response.rows:
        event_name = row.dimension_values[0].value
        count = int(row.metric_values[0].value)
        baseline_events[event_name] = count / 7.0

    anomalies = []
    ANOMALY_THRESHOLD = 2.0
    for event, recent_count in recent_events.items():
        baseline_daily = baseline_events.get(event, 0)
        if baseline_daily > 0:
            ratio = recent_count / baseline_daily
            if ratio > ANOMALY_THRESHOLD:
                anomalies.append({
                    'event': event,
                    'recent_count': recent_count,
                    'baseline_daily_avg': round(baseline_daily, 1),
                    'ratio': round(ratio, 1),
                    'severity': 'high' if ratio > 5.0 else 'medium'
                })
        elif recent_count > 5:
            anomalies.append({
                'event': event,
                'recent_count': recent_count,
                'baseline_daily_avg': 0,
                'ratio': float('inf'),
                'severity': 'high'
            })

    anomalies.sort(key=lambda a: a.get('ratio', 0), reverse=True)

    result = {
        'generated_at': now.isoformat(),
        'status': 'ok',
        'anomaly_count': len(anomalies),
        'anomalies': anomalies,
        'summary': f'{len(anomalies)} GA4 anomalies detected' if anomalies else 'GA4 nominal — no anomalies'
    }

except ImportError:
    result = {
        'generated_at': now.isoformat(),
        'status': 'unavailable',
        'reason': 'google-analytics-data library not installed',
        'anomalies': [],
        'summary': 'GA4 library missing — install google-analytics-data'
    }
except Exception as e:
    result = {
        'generated_at': now.isoformat(),
        'status': 'error',
        'reason': str(e)[:200],
        'anomalies': [],
        'summary': f'GA4 error: {str(e)[:100]}'
    }

with open(output_path, 'w') as f:
    json.dump(result, f, indent=2)

print(result['summary'])
" "$PROPERTY_ID" "$SA_KEY" "$TMP_FILE"

mv "$TMP_FILE" "$OUTPUT_FILE" 2>/dev/null || true
log "DONE"
