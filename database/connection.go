package database

import (
	"grpcservice/models"
	"log"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

var DB *gorm.DB

func Connection() {

	connection, err := gorm.Open(mysql.Open("admin:Ghost12dd@tcp(database-3.cglzxfyim9pj.us-east-2.rds.amazonaws.com)/eos?parseTime=true"), &gorm.Config{
		SkipDefaultTransaction: true,
	})

	if err != nil {
		log.Println("Error connecting to Database", err)
		return
	}
	log.Println("Connected to Database")

	DB = connection

	connection.AutoMigrate(&models.Cursor{})

}
