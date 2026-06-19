package webapp

import (
	"encoding/json"
	"net/http"
)

// handleSupport returns the authenticated user's own support thread.
func (s *Server) handleSupport(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	data, err := s.cabinet.SupportHistoryUser(r.Context(), userID)
	if err != nil {
		s.writeCabinetError(w, "support history", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, data)
}

// handleSupportSend stores a user's support message and notifies the admins.
func (s *Server) handleSupportSend(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if err := s.cabinet.SupportSendUser(r.Context(), userID, req.Text); err != nil {
		s.writeCabinetError(w, "support send", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSupportClose closes (deletes) the user's own support thread.
func (s *Server) handleSupportClose(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	if err := s.cabinet.SupportCloseUser(r.Context(), userID); err != nil {
		s.writeCabinetError(w, "support close", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleAdminSupport returns the admin support inbox (one entry per open thread).
func (s *Server) handleAdminSupport(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	convs, err := s.admin.SupportConversations(r.Context(), userID)
	if err != nil {
		s.writeAdminError(w, "support conversations", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": convs})
}

// handleAdminSupportThread returns one conversation for an admin.
func (s *Server) handleAdminSupportThread(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var req struct {
		UserID int64 `json:"user_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	data, err := s.admin.SupportThreadAdmin(r.Context(), userID, req.UserID)
	if err != nil {
		s.writeAdminError(w, "support thread", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, data)
}

// handleAdminSupportSend delivers an admin reply to a user's thread.
func (s *Server) handleAdminSupportSend(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var req struct {
		UserID int64  `json:"user_id"`
		Text   string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if err := s.admin.SupportSendAdmin(r.Context(), userID, req.UserID, req.Text); err != nil {
		s.writeAdminError(w, "support send", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
