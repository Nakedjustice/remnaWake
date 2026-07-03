package payments

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	"github.com/Nakedjustice/remnaWake/internal/store"
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

// receiptAttachment is the payment confirmation file the user sent.
type receiptAttachment struct {
	fileID     string
	asDocument bool   // document file_ids only work with sendDocument, photo ones with sendPhoto
	note       string // user's caption on the receipt, forwarded to the admins
	filename   string
	data       []byte
}

const (
	maxReceiptPhotoSize    = 10 << 20
	maxReceiptDocumentSize = 50 << 20
)

// UploadRenewReceipt completes the authenticated user's current receipt
// session. Bytes are passed straight to Telegram and are never persisted.
func (s *Service) UploadRenewReceipt(ctx context.Context, telegramID int64, receipt WebReceipt) error {
	st, expired := s.lookupPayPhoto(telegramID)
	if st == nil {
		if expired {
			return ErrReceiptSessionExpired
		}
		return ErrReceiptSessionExpired
	}
	name := strings.ToLower(filepath.Ext(strings.TrimSpace(receipt.Filename)))
	mime := strings.ToLower(strings.TrimSpace(strings.Split(receipt.ContentType, ";")[0]))
	isPhoto := mime == "image/jpeg" || mime == "image/png" || name == ".jpg" || name == ".jpeg" || name == ".png"
	isDocument := mime == "application/pdf" || mime == "image/webp" || mime == "image/heic" || mime == "image/heif" || name == ".pdf" || name == ".webp" || name == ".heic" || name == ".heif"
	if !isPhoto && !isDocument {
		return ErrReceiptType
	}
	limit := maxReceiptDocumentSize
	if isPhoto {
		limit = maxReceiptPhotoSize
	}
	if len(receipt.Data) == 0 {
		return ErrReceiptType
	}
	if len(receipt.Data) > limit {
		return ErrReceiptTooLarge
	}
	u, err := s.store.GetNotifiedUser(ctx, st.userID)
	if err != nil {
		return fmt.Errorf("load receipt profile: %w", err)
	}
	if u == nil {
		s.clearPayPhoto(telegramID)
		return ErrProfileUnknown
	}
	att := &receiptAttachment{asDocument: isDocument && !isPhoto, note: strings.TrimSpace(receipt.Note), filename: receipt.Filename, data: receipt.Data}
	if st.kind == store.PaymentKindTrafficExtension {
		_, err = s.createTrafficExtensionPaymentRequest(ctx, u, st.trafficGB, st.price, st.baseBytes, ProviderP2P, att)
	} else {
		_, err = s.createPaymentRequest(ctx, u, st.months, st.price, st.plan, att)
	}
	if err != nil {
		return fmt.Errorf("%w: deliver receipt: %v", ErrProviderUnavailable, err)
	}
	s.clearPayPhoto(telegramID)
	return nil
}

// cancelPayPhotoByCaption honors /cancel typed as a media caption — the only
// way to cancel in the same message as a mistakenly attached file.
func (s *Service) cancelPayPhotoByCaption(ctx context.Context, chatID int64, caption string) bool {
	if strings.TrimSpace(caption) != "/cancel" {
		return false
	}
	s.clearPayPhoto(chatID)
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("Отменено."))
	return true
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
func (s *Service) startPayPhotoFlow(ctx context.Context, chatID, userID int64, months, price int, plan string) {
	s.setPayPhoto(chatID, &payPhotoState{
		userID:    userID,
		months:    months,
		price:     price,
		plan:      plan,
		kind:      store.PaymentKindSubscription,
		createdAt: s.now(),
	})
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("📸 Отправьте фото, скриншот или PDF-файл чека об оплате следующим сообщением — после этого заявка уйдёт администратору.\n\nОтменить: /cancel"))
}

func (s *Service) startTrafficPayPhotoFlow(ctx context.Context, chatID, userID int64, trafficGB, price int, baseBytes int64) {
	s.setPayPhoto(chatID, &payPhotoState{
		userID: userID, price: price, kind: store.PaymentKindTrafficExtension,
		trafficGB: trafficGB, baseBytes: baseBytes, extraBytes: int64(trafficGB) * bytesPerGB,
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
	if s.cancelPayPhotoByCaption(ctx, chatID, m.Caption) {
		return true
	}
	// Telegram lists photo sizes ascending; the last one is the original-size copy.
	fileID := m.Photo[len(m.Photo)-1].FileID
	return s.finishPayPhotoFlow(ctx, chatID, st, &receiptAttachment{
		fileID: fileID,
		note:   strings.TrimSpace(m.Caption),
	})
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
	if s.cancelPayPhotoByCaption(ctx, chatID, m.Caption) {
		return true
	}
	if !isReceiptDocument(m.Document) {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Этот тип файла не подходит. Отправьте фото, скриншот или PDF-файл чека об оплате."))
		return true
	}
	return s.finishPayPhotoFlow(ctx, chatID, st, &receiptAttachment{
		fileID:     m.Document.FileID,
		asDocument: true,
		note:       strings.TrimSpace(m.Caption),
	})
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
func (s *Service) finishPayPhotoFlow(ctx context.Context, chatID int64, st *payPhotoState, att *receiptAttachment) bool {
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
	if st.kind == store.PaymentKindTrafficExtension {
		_, err = s.createTrafficExtensionPaymentRequest(ctx, u, st.trafficGB, st.price, st.baseBytes, ProviderP2P, att)
	} else {
		_, err = s.createPaymentRequest(ctx, u, st.months, st.price, st.plan, att)
	}
	if err != nil {
		// Keep the state so the user can simply resend the file.
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка, попробуйте позже."))
		return true
	}
	s.clearPayPhoto(chatID)
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("✅ Заявка с чеком отправлена администратору. После подтверждения оплаты услуга будет применена."))
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
