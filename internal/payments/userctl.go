package payments

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// bytesPerGB converts whole gigabytes to bytes for traffic limits.
const bytesPerGB int64 = 1024 * 1024 * 1024

// findManagedUser resolves the admin "Manage user" lookup query. A query that
// is a subscription link (http(s)://…) is resolved by its trailing short UUID;
// a bare token is tried as a username first, then as a short UUID. Returns
// ErrRequestNotFound when nothing matches and ErrBadInput for an empty or
// unrecognised query.
func (s *Service) findManagedUser(ctx context.Context, query string) (*Subscriber, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, ErrBadInput
	}
	if shortUUID, isLink := extractShortUUID(query); isLink {
		if shortUUID == "" {
			return nil, ErrBadInput
		}
		sub, err := s.finder.FindByShortUUID(ctx, shortUUID)
		if err != nil {
			return nil, fmt.Errorf("%w: find by short uuid: %v", ErrPanelUnavailable, err)
		}
		if sub == nil {
			return nil, ErrRequestNotFound
		}
		return sub, nil
	}
	// Bare token: usernames and short UUIDs overlap, so try the username first
	// (the common case) and fall back to a short-UUID lookup.
	if sub, err := s.finder.FindByUsername(ctx, query); err != nil {
		return nil, fmt.Errorf("%w: find by username: %v", ErrPanelUnavailable, err)
	} else if sub != nil {
		return sub, nil
	}
	if isValidShortUUID(query) {
		if sub, err := s.finder.FindByShortUUID(ctx, query); err != nil {
			return nil, fmt.Errorf("%w: find by short uuid: %v", ErrPanelUnavailable, err)
		} else if sub != nil {
			return sub, nil
		}
	}
	return nil, ErrRequestNotFound
}

// updateUser applies a patch through the panel, honoring dry-run mode and
// normalising transport failures to ErrPanelUnavailable. op names the field for
// logging.
func (s *Service) updateUser(ctx context.Context, uuid string, patch UserPatch, op string) error {
	if s.dryRun {
		s.logger.Info("dry-run: would update user", "uuid", uuid, "op", op)
		return nil
	}
	if err := s.userUpdater.UpdateUser(ctx, uuid, patch); err != nil {
		s.logger.Error("update user failed", "uuid", uuid, "op", op, "err", err.Error())
		return fmt.Errorf("%w: %v", ErrPanelUnavailable, err)
	}
	return nil
}

// applyUserExpiryDelta shifts a user's expiry by days (negative shortens). The
// base is max(now, cur) so a positive delta never lands in the past, and the
// result is floored at now so a decrease cannot expire a user before now.
func (s *Service) applyUserExpiryDelta(ctx context.Context, uuid string, cur time.Time, days int) (time.Time, error) {
	now := s.now()
	base := cur
	if base.Before(now) {
		base = now
	}
	newExpire := base.AddDate(0, 0, days)
	if newExpire.Before(now) {
		newExpire = now
	}
	if err := s.updateUser(ctx, uuid, UserPatch{ExpireAt: &newExpire}, "expiry"); err != nil {
		return time.Time{}, err
	}
	return newExpire, nil
}

// setUserHwidLimit caps a user's HWID device count (0 = unlimited).
func (s *Service) setUserHwidLimit(ctx context.Context, uuid string, limit int) error {
	if limit < 0 {
		return ErrBadInput
	}
	return s.updateUser(ctx, uuid, UserPatch{HwidDeviceLimit: &limit}, "hwid")
}

// setUserTrafficLimitGB sets a user's traffic limit in whole gigabytes
// (0 = unlimited).
func (s *Service) setUserTrafficLimitGB(ctx context.Context, uuid string, gb int64) error {
	if gb < 0 {
		return ErrBadInput
	}
	bytesLimit := gb * bytesPerGB
	return s.updateUser(ctx, uuid, UserPatch{TrafficLimitBytes: &bytesLimit}, "traffic")
}

// setUserResetStrategy sets a single user's traffic-reset strategy.
func (s *Service) setUserResetStrategy(ctx context.Context, uuid, strategy string) error {
	if !validTrafficResetStrategy(strategy) {
		return ErrBadInput
	}
	return s.updateUser(ctx, uuid, UserPatch{TrafficLimitStrategy: &strategy}, "reset")
}

// toggleUserStatus flips ACTIVE⇄DISABLED given the user's current status
// (anything other than ACTIVE is enabled to ACTIVE) and returns the new status.
func (s *Service) toggleUserStatus(ctx context.Context, uuid, current string) (string, error) {
	next := "ACTIVE"
	if current == "ACTIVE" {
		next = "DISABLED"
	}
	if err := s.updateUser(ctx, uuid, UserPatch{Status: &next}, "status"); err != nil {
		return "", err
	}
	return next, nil
}

// toggleUserSquad adds squadUUID to the user's squad set if absent, or removes
// it if present, given the current set.
func (s *Service) toggleUserSquad(ctx context.Context, uuid string, current []string, squadUUID string) error {
	next := make([]string, 0, len(current)+1)
	found := false
	for _, u := range current {
		if u == squadUUID {
			found = true
			continue
		}
		next = append(next, u)
	}
	if !found {
		next = append(next, squadUUID)
	}
	return s.updateUser(ctx, uuid, UserPatch{ActiveInternalSquads: &next}, "squads")
}
