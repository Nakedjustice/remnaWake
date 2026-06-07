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
	UUID        string    `json:"uuid"`
	ID          int64     `json:"id"`
	ShortUUID   string    `json:"shortUuid"`
	Username    string    `json:"username"`
	Status      Status    `json:"status"`
	ExpireAt    time.Time `json:"expireAt"`
	TelegramID  *int64    `json:"telegramId"`
	Email       *string   `json:"email"`
	Description *string   `json:"description"`
	Tag         *string   `json:"tag"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type UsersResponse struct {
	Response struct {
		Users []User `json:"users"`
		Total int    `json:"total"`
	} `json:"response"`
}
