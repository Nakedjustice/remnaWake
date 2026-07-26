package webapp

import (
	"encoding/json"
	"net/http"
)

// handleSupport returns the authenticated user's own support thread.
func (s *Server) handleSupport(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticateSupport(w, r)
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
	userID, ok := s.authenticateSupport(w, r)
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
	userID, ok := s.authenticateSupport(w, r)
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

// --- multi-ticket surface (mirrors the bot's sup:* / adm:sup:* buttons) ---

// ticketRequest is the body every per-ticket endpoint accepts; Subject/Text are
// used only by the endpoints that write.
type ticketRequest struct {
	TicketID int64  `json:"ticket_id"`
	Subject  string `json:"subject"`
	Text     string `json:"text"`
}

// decodeTicketRequest reads a per-ticket body, answering 400 itself on garbage.
func decodeTicketRequest(w http.ResponseWriter, r *http.Request) (ticketRequest, bool) {
	var req ticketRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return req, false
	}
	return req, true
}

// handleSupportTickets lists the authenticated user's tickets.
func (s *Server) handleSupportTickets(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticateSupport(w, r)
	if !ok {
		return
	}
	data, err := s.cabinet.SupportTicketsUser(r.Context(), userID)
	if err != nil {
		s.writeCabinetError(w, "support tickets", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, data)
}

// handleSupportTicketThread returns one of the user's tickets with its history.
func (s *Server) handleSupportTicketThread(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticateSupport(w, r)
	if !ok {
		return
	}
	req, ok := decodeTicketRequest(w, r)
	if !ok {
		return
	}
	data, err := s.cabinet.SupportTicketThreadUser(r.Context(), userID, req.TicketID)
	if err != nil {
		s.writeCabinetError(w, "support ticket thread", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, data)
}

// handleSupportTicketCreate opens a new ticket with its first message.
func (s *Server) handleSupportTicketCreate(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticateSupport(w, r)
	if !ok {
		return
	}
	req, ok := decodeTicketRequest(w, r)
	if !ok {
		return
	}
	id, err := s.cabinet.SupportCreateTicket(r.Context(), userID, req.Subject, req.Text)
	if err != nil {
		s.writeCabinetError(w, "support ticket create", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ticket_id": id})
}

// handleSupportTicketSend appends the user's reply to one of their tickets.
func (s *Server) handleSupportTicketSend(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticateSupport(w, r)
	if !ok {
		return
	}
	req, ok := decodeTicketRequest(w, r)
	if !ok {
		return
	}
	if err := s.cabinet.SupportSendUserToTicket(r.Context(), userID, req.TicketID, req.Text); err != nil {
		s.writeCabinetError(w, "support ticket send", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSupportTicketClose closes one of the user's tickets.
func (s *Server) handleSupportTicketClose(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticateSupport(w, r)
	if !ok {
		return
	}
	req, ok := decodeTicketRequest(w, r)
	if !ok {
		return
	}
	if err := s.cabinet.SupportCloseTicketUser(r.Context(), userID, req.TicketID); err != nil {
		s.writeCabinetError(w, "support ticket close", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleAdminSupportTickets returns the admin ticket inbox.
func (s *Server) handleAdminSupportTickets(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	tickets, err := s.admin.SupportTicketsAdmin(r.Context(), userID)
	if err != nil {
		s.writeAdminError(w, "support tickets", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tickets": tickets})
}

// handleAdminSupportTicketThread returns one ticket with history for an admin.
func (s *Server) handleAdminSupportTicketThread(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	req, ok := decodeTicketRequest(w, r)
	if !ok {
		return
	}
	data, err := s.admin.SupportTicketThreadAdmin(r.Context(), userID, req.TicketID)
	if err != nil {
		s.writeAdminError(w, "support ticket thread", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, data)
}

// handleAdminSupportTicketReply appends an admin reply to one open ticket.
func (s *Server) handleAdminSupportTicketReply(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	req, ok := decodeTicketRequest(w, r)
	if !ok {
		return
	}
	if err := s.admin.SupportReplyTicketAdmin(r.Context(), userID, req.TicketID, req.Text); err != nil {
		s.writeAdminError(w, "support ticket reply", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleAdminSupportTicketClose closes one ticket from the admin side.
func (s *Server) handleAdminSupportTicketClose(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	req, ok := decodeTicketRequest(w, r)
	if !ok {
		return
	}
	if err := s.admin.SupportCloseTicketAdmin(r.Context(), userID, req.TicketID); err != nil {
		s.writeAdminError(w, "support ticket close", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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
