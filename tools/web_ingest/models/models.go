package models

type IngestResponse struct {
	SessionID  string `json:"session_id"`
	Chunks     int    `json:"chunks"`
	IndexedBM  int    `json:"indexed_bm25"`
	IndexedVec int    `json:"indexed_vec"`
}
