package handler

import (
	"encoding/json"
	"net/http"
)

func (h *Handler) AnswerAskUserQuestion(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	var req struct {
		Selected []int  `json:"selected"`
		Custom   string `json:"custom_text"`
		Ignore   bool   `json:"ignore"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	writeError(w, http.StatusConflict, "question is not answerable")
}
