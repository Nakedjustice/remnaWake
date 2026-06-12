package payments

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// --- screenshot requirement toggle ---

func (s *Service) getRequireScreenshot() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requireScreenshot
}

// setRequireScreenshot persists the screenshot-required toggle and refreshes
// the in-memory cache.
func (s *Service) setRequireScreenshot(ctx context.Context, on bool) error {
	value := "0"
	if on {
		value = "1"
	}
	if err := s.store.UpsertSetting(ctx, requireScreenshotKey, value); err != nil {
		return err
	}
	s.mu.Lock()
	s.requireScreenshot = on
	s.mu.Unlock()
	return nil
}

// --- awaiting-screenshot conversation state ---

func (s *Service) getPayPhoto(chatID int64) *payPhotoState {
	p, _ := s.lookupPayPhoto(chatID)
	return p
}

// lookupPayPhoto returns the awaiting-receipt state for the chat; expired
// reports that a state existed but outlived payPhotoTTL (it is evicted), so
// the media handlers can tell a late receipt apart from an unrelated file.
func (s *Service) lookupPayPhoto(chatID int64) (st *payPhotoState, expired bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.payPhotos[chatID]
	if p == nil {
		return nil, false
	}
	if s.now().Sub(p.createdAt) > payPhotoTTL {
		delete(s.payPhotos, chatID)
		return nil, true
	}
	return p, false
}

// notifyPayPhotoExpired tells the user their receipt arrived after the
// waiting window closed, instead of dropping the file silently.
func (s *Service) notifyPayPhotoExpired(ctx context.Context, chatID int64) {
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("⌛ Время ожидания чека истекло, заявка не создана. Выберите тариф и начните продление заново."))
}

func (s *Service) setPayPhoto(chatID int64, p *payPhotoState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.payPhotos[chatID] = p
}

func (s *Service) clearPayPhoto(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.payPhotos, chatID)
}

// startPayPhotoFlow remembers the picked tariff and asks the user to attach a
// payment screenshot; the request is created only when the photo arrives.
func (s *Service) startPayPhotoFlow(ctx context.Context, chatID, userID int64, months, price int) {
	s.setPayPhoto(chatID, &payPhotoState{
		userID:    userID,
		months:    months,
		price:     price,
		createdAt: s.now(),
	})
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("📸 Отправьте фото, скриншот или PDF-файл чека об оплате следующим сообщением — после этого заявка уйдёт администратору.\n\nОтменить: /cancel"))
}

// HandlePhoto consumes a photo message when the chat is awaiting a payment
// screenshot: it creates the pending request with the screenshot attached and
// notifies the admins. Returns true only when it handled the message.
func (s *Service) HandlePhoto(ctx context.Context, m *tg.Message) bool {
	if m == nil || len(m.Photo) == 0 {
		return false
	}
	chatID := m.Chat.ID
	st, expired := s.lookupPayPhoto(chatID)
	if st == nil {
		if expired {
			s.notifyPayPhotoExpired(ctx, chatID)
			return true
		}
		return false
	}
	// Telegram lists photo sizes ascending; the last one is the original-size copy.
	fileID := m.Photo[len(m.Photo)-1].FileID
	return s.finishPayPhotoFlow(ctx, chatID, st, fileID, false)
}

// HandleDocument consumes a document message while a payment confirmation is
// awaited: bank receipts often arrive as PDF files, and screenshots sent via
// "send without compression" arrive as image documents. Other file types get
// a reminder. Returns true only when it handled the message.
func (s *Service) HandleDocument(ctx context.Context, m *tg.Message) bool {
	if m == nil || m.Document == nil {
		return false
	}
	chatID := m.Chat.ID
	st, expired := s.lookupPayPhoto(chatID)
	if st == nil {
		if expired {
			s.notifyPayPhotoExpired(ctx, chatID)
			return true
		}
		return false
	}
	if !isReceiptDocument(m.Document) {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Этот тип файла не подходит. Отправьте фото, скриншот или PDF-файл чека об оплате."))
		return true
	}
	return s.finishPayPhotoFlow(ctx, chatID, st, m.Document.FileID, true)
}

// isReceiptDocument accepts files that plausibly carry a payment receipt:
// PDFs and images sent as uncompressed files. The file extension is checked
// as a fallback because banks often attach receipts with a generic
// octet-stream MIME type.
func isReceiptDocument(d *tg.Document) bool {
	if strings.EqualFold(d.MimeType, "application/pdf") ||
		strings.HasPrefix(strings.ToLower(d.MimeType), "image/") {
		return true
	}
	switch strings.ToLower(filepath.Ext(d.FileName)) {
	case ".pdf", ".png", ".jpg", ".jpeg", ".webp", ".heic":
		return true
	}
	return false
}

// finishPayPhotoFlow completes a deferred renewal once the confirmation file
// arrived: creates the pending request with the attachment and notifies the
// admins (photo or document message depending on what the user sent).
func (s *Service) finishPayPhotoFlow(ctx context.Context, chatID int64, st *payPhotoState, fileID string, asDocument bool) bool {
	u, err := s.store.GetNotifiedUser(ctx, st.userID)
	if err != nil {
		s.logger.Error("pay photo: get notified user failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка, попробуйте позже."))
		return true
	}
	if u == nil {
		s.clearPayPhoto(chatID)
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Не удалось найти данные. Начните продление заново."))
		return true
	}
	if _, err := s.createPaymentRequest(ctx, u, st.months, st.price, fileID, asDocument); err != nil {
		// Keep the state so the user can simply resend the file.
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка, попробуйте позже."))
		return true
	}
	s.clearPayPhoto(chatID)
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("✅ Заявка с чеком отправлена администратору. После подтверждения оплаты подписка будет продлена."))
	return true
}

// remindPayPhoto consumes a text message while a payment screenshot is awaited,
// nudging the user to send a photo. Returns true only when such a state exists.
func (s *Service) remindPayPhoto(ctx context.Context, chatID int64) bool {
	if s.getPayPhoto(chatID) == nil {
		return false
	}
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("Пожалуйста, отправьте фото (скриншот) или PDF-файл чека об оплате. Отменить: /cancel"))
	return true
}
