package main

import "time"

type User struct {
	ID    int64
	Name  string // Full name
	Email *string
	Age   int
}

type Post struct {
	ID       int64
	Title    string
	PostedAt time.Time
}
