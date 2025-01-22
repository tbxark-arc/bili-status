package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
)

type Config struct {
	Token      string `json:"token"`
	CacheStore string `json:"cache_store"`
	Mid        int    `json:"mid"`
	Admins     []int  `json:"admins"`
}

type CacheStore struct {
	Cookie string `json:"cookie"`
}

func loadConfig[T any](path string) (*T, error) {
	if strings.HasPrefix(path, "http") {
		resp, err := http.Get(path)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		var config = new(T)
		err = json.NewDecoder(resp.Body).Decode(config)
		if err != nil {
			return nil, err
		}
		return config, nil
	} else {
		bytes, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var config = new(T)
		err = json.Unmarshal(bytes, config)
		if err != nil {
			return nil, err
		}
		return config, nil
	}
}

func saveCacheStore(path string, cacheStore *CacheStore) error {
	bytes, err := json.Marshal(cacheStore)
	if err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(bytes)
	if err != nil {
		return err
	}
	return nil
}
