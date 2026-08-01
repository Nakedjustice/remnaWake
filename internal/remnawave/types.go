package remnawave

import "time"

type Status string

const (
	StatusActive   Status = "ACTIVE"
	StatusDisabled Status = "DISABLED"
	StatusLimited  Status = "LIMITED"
	StatusExpired  Status = "EXPIRED"
)

// User is one panel profile. Since Remnawave v3 the panel identifies users by
// the numeric ID — the v2 "uuid" property was removed from every response, and
// ID is the handle every write path must carry.
type User struct {
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
	// Tag: a pointer to the empty string clears the tag (sent as JSON null —
	// the panel rejects empty-string tags); a non-empty value sets it.
	Tag *string
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

// usersStreamResponse wraps one page of GET /api/users/stream, the keyset-
// paginated listing that replaced the v2 by-telegram-id / by-email / by-tag
// lookups. NextCursor is a string even though the request takes a number, so it
// is echoed back verbatim rather than parsed.
type usersStreamResponse struct {
	Response struct {
		Users      []User  `json:"users"`
		NextCursor *string `json:"nextCursor"`
		HasMore    bool    `json:"hasMore"`
	} `json:"response"`
}

// HwidDevice is one device occupying a slot of a user's HwidDeviceLimit, as
// reported by GET /api/hwid/devices/{userId}. Every descriptive field is
// self-reported by the client app, so any of them may be empty.
type HwidDevice struct {
	HWID        string    `json:"hwid"`
	UserID      int64     `json:"userId"`
	Platform    string    `json:"platform"`
	OSVersion   string    `json:"osVersion"`
	DeviceModel string    `json:"deviceModel"`
	UserAgent   string    `json:"userAgent"`
	RequestIP   string    `json:"requestIp"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// hwidDevicesResponse wraps both HWID endpoints: the listing and the delete
// call return the identical {total, devices} envelope.
type hwidDevicesResponse struct {
	Response struct {
		Total   int          `json:"total"`
		Devices []HwidDevice `json:"devices"`
	} `json:"response"`
}
