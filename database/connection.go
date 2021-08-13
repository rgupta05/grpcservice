package database

import (
	"log"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

var DB []*gorm.DB

func Connection() {

	shard1, err := gorm.Open(mysql.Open("admin:Ghost12dd@tcp(database-3.cglzxfyim9pj.us-east-2.rds.amazonaws.com)/ambassadorservice?parseTime=true"), &gorm.Config{
		SkipDefaultTransaction: true,
	})

	if err != nil {
		log.Println("Error connecting to SHARD 1", err)
		return
	}

	shard2, err := gorm.Open(mysql.Open("admin:Ghost12dd@tcp(database-1.cifirtsxjjxr.us-west-1.rds.amazonaws.com)/ambassadorservice?parseTime=true"), &gorm.Config{
		SkipDefaultTransaction: true,
	})

	if err != nil {
		log.Println("Error connecting to SHARD 2", err)
		return
	}

	shard3, err := gorm.Open(mysql.Open("admin:Ghost12dd@tcp(database-1.cpzwqwpfllly.us-west-2.rds.amazonaws.com)/ambassadorservice?parseTime=true"), &gorm.Config{
		SkipDefaultTransaction: true,
	})

	if err != nil {
		log.Println("Error connecting to SHARD 3", err)
		return
	}

	log.Println("Connected to Database")

	DB = append(DB, shard1, shard2, shard3)

	//connection.AutoMigrate(&models.Cursor{})

}
