package api

import "net/http"

func (h *handler) handleAdminAlertRuleStates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, ok := h.authorizeAdminRequest(w, r)
	if !ok {
		return
	}
	states, err := store.AdminAlertRuleStates(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	activeCount := 0
	for _, state := range states {
		if state.Active {
			activeCount++
		}
	}
	writeJSON(w, http.StatusOK, AdminAlertRuleStatesResponse{States: states, ActiveCount: activeCount})
}
