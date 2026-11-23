package webapp

import (
		"net/http"
)

// handleSupport returns the authenticated user's own support thread.
func (s *Server) handleSupport(w http.ResponseWriter, r *http.Request, userID int64) {
	data, err := s.cabinet.SupportHistoryUser(r.Context(), userID)
	if err != nil {
		s.writeCabinetError(w, "support history", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, data)
}

// handleSupportSend stores a user's support message and notifies the admins.
func (s *Server) handleSupportSend(w http.ResponseWriter, r *http.Request, userID int64) {
	var req struct {
		Text string `json:"text"`
	}
	if !decodeBody(w, r, 16384, &req) {
		return
	}
	if err := s.cabinet.SupportSendUser(r.Context(), userID, req.Text); err != nil {
		s.writeCabinetError(w, "support send", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSupportClose closes (deletes) the user's own support thread.
func (s *Server) handleSupportClose(w http.ResponseWriter, r *http.Request, userID int64) {
	if err := s.cabinet.SupportCloseUser(r.Context(), userID); err != nil {
		s.writeCabinetError(w, "support close", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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
	if !decodeBody(w, r, 16384, &req) {
		return req, false
	}
	return req, true
}

// handleSupportTickets lists the authenticated user's tickets.
func (s *Server) handleSupportTickets(w http.ResponseWriter, r *http.Request, userID int64) {
	data, err := s.cabinet.SupportTicketsUser(r.Context(), userID)
	if err != nil {
		s.writeCabinetError(w, "support tickets", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, data)
}

// handleSupportTicketThread returns one of the user's tickets with its history.
func (s *Server) handleSupportTicketThread(w http.ResponseWriter, r *http.Request, userID int64) {
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
func (s *Server) handleSupportTicketCreate(w http.ResponseWriter, r *http.Request, userID int64) {
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
func (s *Server) handleSupportTicketSend(w http.ResponseWriter, r *http.Request, userID int64) {
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
func (s *Server) handleSupportTicketClose(w http.ResponseWriter, r *http.Request, userID int64) {
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

