package models

import (
	"time"

	"gorm.io/datatypes"
)

type Cursor struct {
	ID             uint           `json:"id" gorm:"primaryKey"`
	Cursorid       string         `json:"cursor"  gorm:"column:cursorid;unique"`
	BlockNum       int64          `json:"blocknum" `
	Account        string         `json:"account" `
	Action         string         `json:"action" `
	Data_json      datatypes.JSON `gorm:"column:data_json"`
	Receiver       string         `json:"receiver" `
	IsIrreversible bool           `json:"isirreversible" `
	InlineActions  datatypes.JSON
	Timestamp      time.Time
	InsertedTime   time.Time
}

type Response struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
