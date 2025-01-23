package main

type Config struct {
	Token      string `json:"token"`
	CacheStore string `json:"cache_store"`
	Mid        int    `json:"mid"`
	Admins     []int  `json:"admins"`
}

type CacheStore struct {
	Cookie string `json:"cookie"`
}
