package main

import (
	"net"

	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
)

type Query struct {
	PeerID int64 `gorm:"not null" json:"-"`
	// ResponseTime is in Milliseconds
	ResponseTime int64 `gorm:"not null"`
	// Time is a UNIX Timestamp in Milliseconds
	Time int64 `gorm:"not null"`
}

type Request struct {
	ID int
	IP net.IP
	// Time is a UNIX Timestamp in Milliseconds
	Time int64
	Peer *Peer
}

type Response struct {
	ID int
	IP net.IP
	// Time is a UNIX Timestamp in Milliseconds
	Time int64
}

type DB struct {
	*gorm.DB
}

func NewDB(file string) (*DB, error) {
	var db DB
	var err error
	db.DB, err = gorm.Open("sqlite3", file)
	if err != nil {
		return nil, err
	}
	db.AutoMigrate(&Query{})
	return &db, nil
}
