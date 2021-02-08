package main

import "time"

//+migu
type User struct {
	ID    int64
	Name  string // Full name
	Email *string
	Age   int
}

//+migu
type Post struct {
	ID       int64
	Title    string
	PostedAt time.Time
}
