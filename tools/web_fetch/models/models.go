package models

type Result struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Byline      string `json:"byline"`
	PublishedAt string `json:"published_at"`
	Text        string `json:"text"`
	TopImage    string `json:"top_image"`
	HTMLHash    string `json:"html_hash"`
	Status      int    `json:"status"`
	RenderMS    int    `json:"render_ms"`
}
