package core

import "github.com/mohammad-safakhou/newser/config"

// CapabilitiesDoc returns a human-readable description of available agent parameters
// that can improve precision. This is intended to be embedded into LLM prompts for
// planning and preference refinement.
func CapabilitiesDoc(cfg *config.Config) string {
    return braveSearchCapabilities() + "\n\n" + analysisCapabilities() + "\n\n" + knowledgeGraphCapabilities() + "\n\n" + conflictDetectionCapabilities()
}

func braveSearchCapabilities() string {
    return `Agent: web_search (Brave)
Purpose: Retrieve precise web results with locale and recency control.
Key parameters you can set (map into preferences.search):
- country: 2-letter country code (e.g., US, DE) to geolocate results.
- search_lang: language code for search results (e.g., en, fr).
- ui_lang: UI language (e.g., en-US, fr-FR).
- count: number of results per page (max 20).
- offset: page index (0-based). Use 0 for first page.
- safesearch: off | moderate | strict. Default moderate.
- freshness: pd (24h) | pw (7d) | pm (31d) | py (365d) | YYYY-MM-DDtoYYYY-MM-DD.
- result_filter: comma-separated types; usually 'web'.
- spellcheck: true|false (true by default unless using advanced operators).
- text_decorations: true|false (prefer false to avoid markup in snippets).
- extra_snippets: true|false (true to get additional snippets for ranking).
- units: metric | imperial (derive from country if unknown).
- domains_preferred: list of domains to prioritize (post-filter/rerank).
- domains_blocked: list of domains to exclude (also pass as -site: filters or post-filter).
Defaults and heuristics:
- Infer search_lang/ui_lang from user language; allow override.
- Derive freshness from schedule: @hourly→pd, @daily→pw, weekly/monthly→pm.
- Use safesearch=strict for K12/sensitive topics.
- Keep count=20, paginate by offset when needed.
Return refined preferences with a 'search' object reflecting these fields.`
}

func analysisCapabilities() string {
    return `Agent: analysis
Purpose: Evaluate relevance, credibility, and importance; extract key topics.
Key parameters (map into preferences.analysis):
- relevance_weight, credibility_weight, importance_weight: floats 0..1 (normalize if necessary) controlling scoring emphasis.
- sentiment_mode: detect | none — whether to annotate sentiment.
- min_credibility: float 0..1 — filter out low-credibility sources.
- key_topics_limit: integer — limit of extracted key topics.
- sources_limit: integer — how many sources to consider.
- language: e.g., en, fr — analysis language.
- extract_key_topics: bool — enable topic extraction.
Guidelines:
- Respect sources_limit and min_credibility when selecting inputs to analyze.
- Use language to disambiguate tokens and stopwords.
Return refined preferences under preferences.analysis.*.`
}

func knowledgeGraphCapabilities() string {
    return `Agent: knowledge_graph
Purpose: Extract entities and relations; build a concise graph.
Key parameters (map into preferences.knowledge_graph):
- entity_types: list — e.g., person, organization, location, product, event.
- relation_types: list — e.g., partnership, ownership, competition, acquisition.
- min_confidence: float 0..1 — drop low-confidence nodes/edges.
- dedupe_threshold: float 0..1 — merge similar nodes above threshold.
- include_aliases: bool — attach aliases/synonyms to nodes.
- max_nodes, max_edges: int — safety caps for graph size.
- language: e.g., en — extraction language.
Guidelines:
- Preserve important nodes even under caps; merge similar low-impact nodes first.
- Always return node/edge counts and any truncation note in metadata.
Return refined preferences under preferences.knowledge_graph.*.`
}

func conflictDetectionCapabilities() string {
    return `Agent: conflict_detection
Purpose: Detect conflicting claims and propose resolution.
Key parameters (map into preferences.conflict):
- contradictory_threshold: float 0..1 — sensitivity for detecting contradictions.
- sources_window: int — max number of sources to compare per claim.
- require_citations: bool — include links/excerpts for flagged conflicts.
- stance_detection: bool — detect stance (support/against/neutral) per source.
- grouping_by: string — claim | topic | source — aggregation style.
- min_supporting_evidence: int — require N pieces before flagging.
- resolution_strategy: string — prefer_high_credibility | balanced | conservative.
Guidelines:
- Use credibility to weigh conflicts; avoid false positives with conservative thresholds.
- Provide a short resolution note (why and based on which evidence).
Return refined preferences under preferences.conflict.*.`
}
