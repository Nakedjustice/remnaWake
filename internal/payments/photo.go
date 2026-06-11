package payments

import (
	"context"

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
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.payPhotos[chatID]
	if p == nil {
		return nil
	}
	if s.now().Sub(p.createdAt) > payPhotoTTL {
		delete(s.payPhotos, chatID)
		return nil
	}
	return p
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
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("📸 Отправьте фото или скриншот чека об оплате следующим сообщением — после этого заявка уйдёт администратору.\n\nОтменить: /cancel"))
}

// HandlePhoto consumes a photo message when the chat is awaiting a payment
// screenshot: it creates the pending request with the screenshot attached and
// notifies the admins. Returns true only when it handled the message.
func (s *Service) HandlePhoto(ctx context.Context, m *tg.Message) bool {
	if m == nil || len(m.Photo) == 0 {
		return false
	}
	chatID := m.Chat.ID
	st := s.getPayPhoto(chatID)
	if st == nil {
		return false
	}

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

	// Telegram lists photo sizes ascending; the last one is the original-size copy.
	fileID := m.Photo[len(m.Photo)-1].FileID
	if _, err := s.createPaymentRequest(ctx, u, st.months, st.price, fileID); err != nil {
		// Keep the state so the user can simply resend the photo.
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
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("Пожалуйста, отправьте фото (скриншот) оплаты. Отменить: /cancel"))
	return true
}
