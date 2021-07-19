package models

import (
	"time"

	"gorm.io/datatypes"
)

type Cursor struct {
	ID                uint   `json:"id" gorm:"primaryKey"`
	Cursor            string `json:"cursor"  gorm:"unique"`
	BlockNum          int64  `json:"blocknum" `
	Account           string `json:"account" `
	Action            string `json:"action" `
	PrimaryActionJSON datatypes.JSON
	Receiver          string `json:"receiver" `
	InlineActions     datatypes.JSON
	Timestamp         time.Time
}
