package remnawave

import "time"

type Status string

const (
	StatusActive   Status = "ACTIVE"
	StatusDisabled Status = "DISABLED"
	StatusLimited  Status = "LIMITED"
	StatusExpired  Status = "EXPIRED"
)

type User struct {
	UUID                 string          `json:"uuid"`
	ID                   int64           `json:"id"`
	ShortUUID            string          `json:"shortUuid"`
	Username             string          `json:"username"`
	Status               Status          `json:"status"`
	ExpireAt             time.Time       `json:"expireAt"`
	TelegramID           *int64          `json:"telegramId"`
	Email                *string         `json:"email"`
	Description          *string         `json:"description"`
	Tag                  *string         `json:"tag"`
	SubscriptionURL      string          `json:"subscriptionUrl"`
	HwidDeviceLimit      *int            `json:"hwidDeviceLimit"`
	TrafficLimitBytes    int64           `json:"trafficLimitBytes"`
	TrafficLimitStrategy string          `json:"trafficLimitStrategy"`
	ActiveInternalSquads []InternalSquad `json:"activeInternalSquads"`
	// UserTraffic carries the nested usage block. The Remnawave user endpoints
	// return used traffic under the "userTraffic" object, not at the top level.
	UserTraffic UserTraffic `json:"userTraffic"`
	CreatedAt   time.Time   `json:"createdAt"`
	UpdatedAt   time.Time   `json:"updatedAt"`
}

// UserTraffic is the nested traffic-usage block returned under the "userTraffic"
// key by the Remnawave user endpoints (by-username / by-telegram-id / etc.).
type UserTraffic struct {
	UsedTrafficBytes         int64 `json:"usedTrafficBytes"`
	LifetimeUsedTrafficBytes int64 `json:"lifetimeUsedTrafficBytes"`
}

// UserPatch carries the manageable fields for PATCH /api/users. Every field is
// optional: a nil pointer means "leave unchanged", while a set pointer is sent
// even when it is the zero value (0 = unlimited for HWID/traffic limits).
type UserPatch struct {
	ExpireAt             *time.Time
	HwidDeviceLimit      *int
	TrafficLimitBytes    *int64
	TrafficLimitStrategy *string
	Status               *string // ACTIVE | DISABLED
	ActiveInternalSquads *[]string
}

// InternalSquad is one internal squad as returned by GET /api/internal-squads.
type InternalSquad struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

// internalSquadsResponse wraps the internal squads listing.
type internalSquadsResponse struct {
	Response struct {
		Total          int             `json:"total"`
		InternalSquads []InternalSquad `json:"internalSquads"`
	} `json:"response"`
}

type UsersResponse struct {
	Response struct {
		Users []User `json:"users"`
		Total int    `json:"total"`
	} `json:"response"`
}

// userResponse wraps the single-user lookup (GET /api/users/by-username/{username}).
type userResponse struct {
	Response User `json:"response"`
}

// usersByTgResponse wraps the by-Telegram-ID lookup, which returns an array
// because one Telegram ID may map to multiple subscriptions.
type usersByTgResponse struct {
	Response []User `json:"response"`
}
