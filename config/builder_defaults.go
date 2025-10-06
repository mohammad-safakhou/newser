package config

const (
	DefaultTopicSchema = `{
  "name": "Daily Brief",
  "description": "Automatically curated topic summarising daily developments.",
  "objectives": [
    "Monitor major headlines",
    "Highlight policy, market, and technology shifts"
  ],
  "preferences": {
    "cadence": "daily",
    "timezone": "UTC"
  }
}`

	DefaultBlueprintSchema = `{
  "version": "1.0",
  "stages": [
    {"id": "collect", "label": "Collect Sources", "agents": ["research"]},
    {"id": "analyse", "label": "Analyse Evidence", "agents": ["analysis", "conflict_detection"]},
    {"id": "synth", "label": "Synthesize Report", "agents": ["synthesis"]}
  ],
  "routing": {
    "temperature": 0.2,
    "max_tasks": 12
  }
}`

	DefaultViewSchema = `{
  "layout": "cards",
  "sections": [
    {"id": "summary", "title": "Executive Summary", "component": "summary"},
    {"id": "insights", "title": "Key Developments", "component": "insight-list"}
  ],
  "theme": {
    "variant": "dark",
    "accent": "sky"
  }
}`

	DefaultRouteSchema = `{
  "routes": [
    {"path": "/", "view": "summary", "title": "Overview"},
    {"path": "/insights", "view": "insights", "title": "Insights"}
  ],
  "fallback": {"path": "/", "redirect": "/"}
}`
)
