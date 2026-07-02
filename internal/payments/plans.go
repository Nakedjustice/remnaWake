package payments

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	"github.com/Nakedjustice/remnaWake/internal/store"
)

// planCodePattern constrains admin-defined plan codes. The 16-char cap keeps
// callback data (payvia:<uid>:<months>:<provider>:<plan>) under Telegram's
// 64-byte limit.
var planCodePattern = regexp.MustCompile(`^[a-z0-9_-]{1,16}$`)

// planTagPattern is the panel's user-tag validation; plan tags must satisfy it
// or confirmations would fail with panel 400s.
var planTagPattern = regexp.MustCompile(`^[A-Z0-9_]{1,16}$`)

// builtinStandardPlan is the virtual plan every install starts with: default
// squad, unlimited traffic/devices, no tag. It never exists as a plans row and
// cannot be edited or deleted.
func builtinStandardPlan() store.Plan {
	return store.Plan{Code: store.PlanStandard, Name: "Стандарт"}
}

// getPlan resolves a plan code to its definition, synthesizing the built-in
// standard plan. An empty code means standard. Returns ErrPlanUnknown for a
// code with no plans row.
func (s *Service) getPlan(ctx context.Context, code string) (store.Plan, error) {
	if code == "" || code == store.PlanStandard {
		return builtinStandardPlan(), nil
	}
	p, err := s.store.GetPlan(ctx, code)
	if err != nil {
		return store.Plan{}, fmt.Errorf("get plan: %w", err)
	}
	if p == nil {
		return store.Plan{}, ErrPlanUnknown
	}
	return *p, nil
}

// listPlans returns every purchasable plan, the built-in standard first.
func (s *Service) listPlans(ctx context.Context) ([]store.Plan, error) {
	custom, err := s.store.ListPlans(ctx)
	if err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	return append([]store.Plan{builtinStandardPlan()}, custom...), nil
}

// formatTariffListing builds the tariff list text shared by /tariffs, the
// menu button and the admin menu. Without custom presets the output is exactly
// the historical flat list; with presets each plan gets a name header. The
// bool reports whether any tariff exists at all.
func (s *Service) formatTariffListing(ctx context.Context) (string, bool, error) {
	all, err := s.store.ListAllTariffs(ctx)
	if err != nil {
		return "", false, fmt.Errorf("list tariffs: %w", err)
	}
	if len(all) == 0 {
		return "", false, nil
	}
	plans, err := s.listPlans(ctx)
	if err != nil {
		return "", false, err
	}

	byPlan := map[string][]store.Tariff{}
	for _, t := range all {
		byPlan[t.Plan] = append(byPlan[t.Plan], t)
	}

	var b strings.Builder
	if len(plans) == 1 {
		b.WriteString(i18n.T("Тарифы:\n"))
		for _, t := range byPlan[store.PlanStandard] {
			b.WriteString(fmt.Sprintf(i18n.T("%d мес. — %s\n"), t.Months, s.priceLabel(t.Price)))
		}
		return strings.TrimRight(b.String(), "\n"), true, nil
	}

	b.WriteString(i18n.T("Тарифы:\n"))
	for _, p := range plans {
		grid := byPlan[p.Code]
		if len(grid) == 0 {
			continue
		}
		b.WriteString("\n" + p.Name + ":\n")
		for _, t := range grid {
			b.WriteString(fmt.Sprintf(i18n.T("%d мес. — %s\n"), t.Months, s.priceLabel(t.Price)))
		}
	}
	return strings.TrimRight(b.String(), "\n"), true, nil
}

// webPlans assembles the preset list for the mini app: the built-in standard
// plan (reusing the already-built standard grid) followed by every custom
// preset with its own grid. Returns nil when no custom presets exist so the
// frontend keeps the plain single-grid renew screen.
func (s *Service) webPlans(ctx context.Context, standardTariffs []WebTariff) []WebPlan {
	custom, err := s.store.ListPlans(ctx)
	if err != nil {
		s.logger.Error("webapp: list plans failed", "err", err.Error())
		return nil
	}
	if len(custom) == 0 {
		return nil
	}
	all, err := s.store.ListAllTariffs(ctx)
	if err != nil {
		s.logger.Error("webapp: list all tariffs failed", "err", err.Error())
		return nil
	}
	byPlan := map[string][]WebTariff{}
	for _, t := range all {
		if t.Plan == store.PlanStandard {
			continue
		}
		byPlan[t.Plan] = append(byPlan[t.Plan], WebTariff{Months: t.Months, Price: t.Price, PriceLabel: s.priceLabel(t.Price)})
	}

	std := builtinStandardPlan()
	out := []WebPlan{{Code: std.Code, Name: std.Name, Tariffs: standardTariffs}}
	for _, p := range custom {
		out = append(out, WebPlan{
			Code: p.Code, Name: p.Name,
			TrafficGB: p.TrafficGB, DeviceLimit: p.DeviceLimit,
			Tariffs: byPlan[p.Code],
		})
	}
	return out
}

// planUserPatch builds the panel patch a confirmed purchase applies: the new
// expiry plus the plan's squad, traffic limit, device limit and tag, so the
// panel user always reflects what was last paid for. An empty plan squad means
// the default squad; when that cannot be resolved the squads field is omitted
// (matching the old extend-only behavior) rather than blocking the confirm.
func (s *Service) planUserPatch(ctx context.Context, plan store.Plan, expireAt time.Time) UserPatch {
	trafficBytes := int64(plan.TrafficGB) * bytesPerGB
	deviceLimit := plan.DeviceLimit
	strategy := s.getDefaultTrafficReset()
	tag := plan.Tag
	patch := UserPatch{
		ExpireAt:             &expireAt,
		TrafficLimitBytes:    &trafficBytes,
		TrafficLimitStrategy: &strategy,
		HwidDeviceLimit:      &deviceLimit,
		Tag:                  &tag,
	}

	squadUUID := plan.SquadUUID
	if squadUUID == "" {
		resolved, err := s.resolveDefaultSquadUUID(ctx)
		if err != nil {
			s.logger.Warn("plan stamp: default squad unresolved, leaving squads unchanged", "plan", plan.Code, "err", err.Error())
			return patch
		}
		squadUUID = resolved
	}
	squads := []string{squadUUID}
	patch.ActiveInternalSquads = &squads
	return patch
}
