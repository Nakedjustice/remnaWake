package payments

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

const trafficExtensionDays = 30

var (
	ErrTrafficExtensionUnknown     = errors.New("traffic extension option not found")
	ErrTrafficExtensionActive      = errors.New("traffic extension already active")
	ErrTrafficExtensionUnavailable = errors.New("traffic extension unavailable")
)

type WebTrafficExtensionOption struct {
	TrafficGB  int    `json:"traffic_gb"`
	Price      int    `json:"price"`
	PriceLabel string `json:"price_label"`
}

type WebTrafficExtensionState struct {
	TrafficGB int    `json:"traffic_gb,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

func (s *Service) webTrafficExtensionOptions(ctx context.Context) []WebTrafficExtensionOption {
	opts, err := s.store.ListTrafficExtensionOptions(ctx)
	if err != nil {
		s.logger.Error("webapp: list traffic extension options failed", "err", err.Error())
		return nil
	}
	out := make([]WebTrafficExtensionOption, 0, len(opts))
	for _, o := range opts {
		out = append(out, WebTrafficExtensionOption{TrafficGB: o.TrafficGB, Price: o.Price, PriceLabel: s.priceLabel(o.Price)})
	}
	return out
}

func (s *Service) createTrafficExtensionPaymentRequest(ctx context.Context, u *store.NotifiedUser, trafficGB, price int, baseBytes int64, provider string, att *receiptAttachment) (int64, error) {
	var fileID string
	var isDoc bool
	if att != nil {
		fileID, isDoc = att.fileID, att.asDocument
	}
	reqID, err := s.store.CreateTrafficExtensionPaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: u.RemnawaveID, UUID: u.UUID, Username: u.Username,
		TelegramID: u.TelegramID, Months: 0, Price: price, ExpireAt: u.ExpireAt,
		Provider: provider, Kind: store.PaymentKindTrafficExtension, TrafficGB: trafficGB,
		BaseTrafficLimitBytes: baseBytes, ExtraTrafficBytes: int64(trafficGB) * bytesPerGB,
		ScreenshotFileID: fileID, ScreenshotIsDocument: isDoc,
	}, s.now())
	if errors.Is(err, store.ErrTrafficExtensionActive) {
		return 0, ErrTrafficExtensionActive
	}
	if err != nil {
		s.logger.Error("create traffic extension request failed", "err", err.Error())
		return 0, err
	}
	if provider == ProviderP2P || provider == "" {
		text := s.formatAdminTrafficExtensionRequest(u, trafficGB, price)
		if att != nil && att.note != "" {
			text += "\n\n💬 " + att.note
		}
		kb := &tg.InlineKeyboardMarkup{InlineKeyboard: [][]tg.InlineKeyboardButton{{
			{Text: i18n.T("✅ Подтвердить оплату"), CallbackData: fmt.Sprintf("ok:%d", reqID)},
			{Text: i18n.T("❌ Отклонить"), CallbackData: fmt.Sprintf("rej:%d", reqID)},
		}}}
		var refs []adminMsgRef
		for _, adminID := range s.adminIDs {
			var msgID int64
			var sendErr error
			switch {
			case att != nil && len(att.data) > 0 && fileID == "" && isDoc:
				msgID, fileID, sendErr = s.bot.SendDocumentUpload(ctx, adminID, att.filename, att.data, text, kb)
			case att != nil && len(att.data) > 0 && fileID == "":
				msgID, fileID, sendErr = s.bot.SendPhotoUpload(ctx, adminID, att.filename, att.data, text, kb)
			case fileID != "" && isDoc:
				msgID, sendErr = s.bot.SendDocument(ctx, adminID, fileID, text, kb)
			case fileID != "":
				msgID, sendErr = s.bot.SendPhoto(ctx, adminID, fileID, text, kb)
			default:
				msgID, sendErr = s.bot.SendPlainWithKeyboard(ctx, adminID, text, kb)
			}
			if sendErr != nil {
				s.logger.Error("notify admin failed", "admin_id", adminID, "err", sendErr.Error())
				continue
			}
			if att != nil && len(att.data) > 0 && fileID != "" && len(refs) == 0 {
				if serr := s.store.SetPaymentRequestScreenshot(ctx, reqID, fileID, isDoc); serr != nil {
					s.logger.Error("store uploaded receipt file id failed", "request_id", reqID, "err", serr.Error())
				}
			}
			refs = append(refs, adminMsgRef{chatID: adminID, messageID: msgID})
		}
		if len(refs) == 0 && len(s.adminIDs) > 0 {
			if _, derr := s.store.DeletePendingPaymentRequest(ctx, reqID); derr != nil {
				s.logger.Error("withdraw unnotified traffic request failed", "request_id", reqID, "err", derr.Error())
			}
			return 0, fmt.Errorf("traffic extension request %d: notifying every admin failed", reqID)
		}
		s.putAdminMsgs(s.payMsgs, reqID, refs)
	}
	return reqID, nil
}

func (s *Service) formatAdminTrafficExtensionRequest(u *store.NotifiedUser, trafficGB, price int) string {
	return fmt.Sprintf("%s\n\n%s%s\nRemnawave ID: %d\nUUID: %s\nTelegram ID: %d\n%s%s\n%s",
		i18n.T("📊 Заявка на докупку трафика"),
		i18n.T("Клиент: "), u.Username,
		u.RemnawaveID,
		u.UUID,
		u.TelegramID,
		i18n.T("Подписка до: "), u.ExpireAt.Format("02.01.2006"),
		fmt.Sprintf(i18n.T("Выбрано: +%d ГБ — %s"), trafficGB, s.priceLabel(price)))
}

func (s *Service) confirmTrafficExtensionRequest(ctx context.Context, req *store.PaymentRequest) (time.Time, error) {
	expiresAt := s.now().AddDate(0, 0, trafficExtensionDays)
	newLimit := req.BaseTrafficLimitBytes + req.ExtraTrafficBytes
	if s.dryRun {
		s.logger.Info("dry-run: would apply traffic extension", "uuid", req.UUID, "traffic_gb", req.TrafficGB, "new_limit", newLimit)
	} else if err := s.userUpdater.UpdateUser(ctx, req.UUID, UserPatch{TrafficLimitBytes: &newLimit}); err != nil {
		s.logger.Error("apply traffic extension failed", "uuid", req.UUID, "err", err.Error())
		return time.Time{}, fmt.Errorf("apply traffic extension: %w", err)
	}
	if _, err := s.store.ConfirmTrafficExtensionRequest(ctx, req.ID, s.now(), expiresAt); err != nil {
		s.clearPayButtons(ctx, req.ID)
		return expiresAt, fmt.Errorf("%w: %v", ErrConfirmedNotMarked, err)
	}
	s.clearPayButtons(ctx, req.ID)
	return expiresAt, nil
}

func (s *Service) RunTrafficExtensionResets(ctx context.Context) {
	if s.dryRun {
		s.logger.Info("traffic extension resets skipped (dry run)")
		return
	}
	expired, err := s.store.ListExpiredTrafficExtensions(ctx, s.now())
	if err != nil {
		s.logger.Error("traffic extension reset: list failed", "err", err.Error())
		return
	}
	for _, req := range expired {
		base := req.BaseTrafficLimitBytes
		if err := s.userUpdater.UpdateUser(ctx, req.UUID, UserPatch{TrafficLimitBytes: &base}); err != nil {
			s.logger.Error("traffic extension reset: panel update failed", "uuid", req.UUID, "request_id", req.ID, "err", err.Error())
			continue
		}
		if err := s.store.MarkActiveTrafficExtensionsRestored(ctx, req.UUID, s.now()); err != nil {
			s.logger.Error("traffic extension reset: mark restored failed", "uuid", req.UUID, "request_id", req.ID, "err", err.Error())
		}
	}
}
