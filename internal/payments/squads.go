package payments

import (
	"context"
	"fmt"
	"strings"
)

// fallbackSquadName is the internal squad a stock Remnawave install seeds;
// used to resolve the default squad when no squad has been selected yet, so
// new users get working access out of the box.
const fallbackSquadName = "Default-Squad"

// resolveDefaultSquadUUID returns the internal squad UUID newly created users
// must be assigned to: the admin-selected squad if one is persisted, otherwise
// the panel squad named Default-Squad (case-insensitive). The by-name fallback
// is cached in memory until an admin makes an explicit selection. Returns an
// error when neither resolves — a squad-less user would have no nodes, so
// creation must fail loudly rather than hand out a broken subscription.
func (s *Service) resolveDefaultSquadUUID(ctx context.Context) (string, error) {
	uuid, found, err := s.store.GetSetting(ctx, defaultSquadUUIDKey)
	if err != nil {
		return "", fmt.Errorf("load default squad setting: %w", err)
	}
	if found && uuid != "" {
		return uuid, nil
	}

	s.mu.Lock()
	cached := s.resolvedSquadUUID
	s.mu.Unlock()
	if cached != "" {
		return cached, nil
	}

	if s.squads == nil {
		return "", fmt.Errorf("default squad is not configured and squad listing is unavailable")
	}
	squads, err := s.squads.GetInternalSquads(ctx)
	if err != nil {
		return "", fmt.Errorf("list internal squads: %w", err)
	}
	for _, sq := range squads {
		if strings.EqualFold(sq.Name, fallbackSquadName) {
			s.mu.Lock()
			s.resolvedSquadUUID = sq.UUID
			s.mu.Unlock()
			return sq.UUID, nil
		}
	}
	return "", fmt.Errorf("default squad is not configured and no squad named %q exists in the panel", fallbackSquadName)
}

// setDefaultSquad validates that uuid is an existing internal squad and
// persists it (plus its name, for display) as the default for new users.
// Shared by the bot admin picker and the mini app admin API.
func (s *Service) setDefaultSquad(ctx context.Context, uuid string) (InternalSquad, error) {
	if uuid == "" {
		return InternalSquad{}, ErrBadInput
	}
	if s.squads == nil {
		return InternalSquad{}, ErrPanelUnavailable
	}
	squads, err := s.squads.GetInternalSquads(ctx)
	if err != nil {
		s.logger.Error("set default squad: list squads failed", "err", err.Error())
		return InternalSquad{}, fmt.Errorf("%w: %v", ErrPanelUnavailable, err)
	}
	for _, sq := range squads {
		if sq.UUID != uuid {
			continue
		}
		if err := s.store.UpsertSetting(ctx, defaultSquadUUIDKey, sq.UUID); err != nil {
			return InternalSquad{}, fmt.Errorf("persist default squad uuid: %w", err)
		}
		if err := s.store.UpsertSetting(ctx, defaultSquadNameKey, sq.Name); err != nil {
			return InternalSquad{}, fmt.Errorf("persist default squad name: %w", err)
		}
		s.mu.Lock()
		s.resolvedSquadUUID = ""
		s.mu.Unlock()
		return sq, nil
	}
	return InternalSquad{}, ErrBadInput
}

// defaultSquadSelection returns the persisted default squad (UUID and name);
// both empty when no squad has been selected yet.
func (s *Service) defaultSquadSelection(ctx context.Context) (uuid, name string) {
	if v, found, err := s.store.GetSetting(ctx, defaultSquadUUIDKey); err == nil && found {
		uuid = v
	}
	if v, found, err := s.store.GetSetting(ctx, defaultSquadNameKey); err == nil && found {
		name = v
	}
	return uuid, name
}
