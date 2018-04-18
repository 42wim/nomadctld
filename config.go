package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

type UserInfo struct {
	Name   string
	Key    string
	ID     string
	Prefix []string
}

func readconfig() {
	viper.SetConfigName("nomadctld")
	viper.AddConfigPath("/etc/nomadctld")
	viper.AddConfigPath(".")
	err := viper.ReadInConfig()
	if err != nil {
		panic(fmt.Errorf("Fatal error config file: %s \n", err))
	}
	viper.WatchConfig()
	viper.OnConfigChange(func(e fsnotify.Event) {
		log.Println("Config file changed:", e.Name)
	})
}

func checkKey(key string) *UserInfo {
	ui := &UserInfo{}
	key = strings.TrimSpace(key)
	prefixes := []string{}
	uprefixes := []string{}
	for userID, cfg := range viper.GetStringMap("users") {
		ccfg := cfg.(map[string]interface{})
		ukey := ccfg["key"].(string)
		if ukey == key {
			ui.ID = userID
			ui.Key = key
			ui.Name = ccfg["name"].(string)
			for _, ia := range ccfg["prefix"].([]interface{}) {
				uprefixes = append(uprefixes, ia.(string))
			}
		}
	}
	if len(uprefixes) == 0 {
		return ui
	}
	for _, uprefix := range uprefixes {
		for prefix, cfg := range viper.GetStringMap("prefix") {
			if prefix == uprefix {
				//		fmt.Printf("uprefix prefix cfg %#v %#v %#v", uprefix, prefix, cfg)
				found := cfg.(map[string]interface{})["prefix"].([]interface{})
				for _, s := range found {
					prefixes = append(prefixes, s.(string))
				}
			}
		}
	}
	ui.Prefix = prefixes
	return ui
}

func getNomadConfig() map[string]*NomadConfig {
	res := make(map[string]*NomadConfig)
	for id, cfg := range viper.GetStringMap("nomad") {
		nc := &NomadConfig{Name: id}
		res[id] = nc
		nc.URL = cfg.(map[string]interface{})["url"].(string)
		if prefixes, ok := cfg.(map[string]interface{})["prefix"].([]interface{}); ok {
			for _, prefix := range prefixes {
				nc.Prefix = append(nc.Prefix, prefix.(string))
			}
		}
		if aliases, ok := cfg.(map[string]interface{})["alias"].([]interface{}); ok {
			for _, alias := range aliases {
				nc.Alias = append(nc.Alias, alias.(string))
			}
		}
	}
	return res
}
