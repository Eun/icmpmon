package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"time"

	"strconv"
	"strings"

	"hash/crc32"

	hjson "github.com/hjson/hjson-go"
)

type Peer struct {
	Name     *string
	Address  *string
	Interval *int
	Timeout  *int
	ID       *int64
	ip       net.IP
}
type Config struct {
	Peers          []Peer
	Interval       *int
	Timeout        *int
	ListenAddress  *string
	DataBase       *string
	KeepHistoryFor time.Duration
}

func readInt(amap map[string]interface{}, name string) (*int, error) {
	for key, value := range amap {
		if strings.EqualFold(key, name) {
			switch value.(type) {
			case int:
				ret := new(int)
				*ret = value.(int)
				return ret, nil
			case int64:
				ret := new(int)
				*ret = int(value.(int64))
				return ret, nil
			case string:
				var err error
				ret := new(int)
				*ret, err = strconv.Atoi(value.(string))
				return ret, err
			case float32:
				ret := new(int)
				*ret = int(value.(float32))
				return ret, nil
			case float64:
				ret := new(int)
				*ret = int(value.(float64))
				return ret, nil
			case bool:
				ret := new(int)
				if value.(bool) {
					*ret = 1
				} else {
					*ret = 0
				}
				return ret, nil
			}
			return nil, errors.New("invalid format")
		}
	}
	return nil, errors.New("not found")
}

func readString(amap map[string]interface{}, name string) (*string, error) {
	for key, value := range amap {
		if strings.EqualFold(key, name) {
			switch value.(type) {
			case int:
				ret := new(string)
				*ret = string(value.(int))
				return ret, nil
			case int64:
				ret := new(string)
				*ret = string(value.(int64))
				return ret, nil
			case string:
				ret := new(string)
				*ret = value.(string)
				return ret, nil
			case float32:
				ret := new(string)
				*ret = strconv.FormatFloat(float64(value.(float32)), 'E', -1, 32)
				return ret, nil
			case float64:
				ret := new(string)
				*ret = strconv.FormatFloat(value.(float64), 'E', -1, 64)
				return ret, nil
			case bool:
				ret := new(string)
				if value.(bool) {
					*ret = "true"
				} else {
					*ret = "false"
				}
				return ret, nil
			}
			return nil, errors.New("invalid format")
		}
	}
	return nil, errors.New("not found")
}

func readPeers(amap map[string]interface{}, name string) (peers []Peer, err error) {
	for key, value := range amap {
		if strings.EqualFold(key, name) {
			switch value.(type) {
			case string:
				str := new(string)
				*str = value.(string)
				return []Peer{Peer{Address: str}}, nil
			case []interface{}:
				for _, value := range value.([]interface{}) {
					switch value.(type) {
					case string:
						str := new(string)
						*str = value.(string)
						peers = append(peers, Peer{Address: str})
					case map[string]interface{}:
						var peer Peer
						var id *int
						id, _ = readInt(value.(map[string]interface{}), "ID")
						if id != nil {
							peer.ID = new(int64)
							*peer.ID = int64(*id)
						}
						peer.Interval, _ = readInt(value.(map[string]interface{}), "Interval")
						peer.Timeout, _ = readInt(value.(map[string]interface{}), "Timeout")
						peer.Address, err = readString(value.(map[string]interface{}), "Address")
						if err != nil {
							if err.Error() == "invalid format" {
								return peers, errors.New("'Address' has an invalid format")
							} else if err.Error() == "not found" {
								return peers, errors.New("'Address' was not found")
							}
						}
						if peer.Address == nil || len(*peer.Address) <= 0 {
							return peers, errors.New("'Address' is invalid")
						}
						peer.Name, _ = readString(value.(map[string]interface{}), "Name")
						peers = append(peers, peer)
					}
				}
			}
			return peers, nil
		}
	}
	return peers, nil
}

func ReadConfig(configFile string) (config Config, err error) {
	var bytes []byte
	bytes, err = ioutil.ReadFile(configFile)
	if err != nil {
		return config, err
	}

	var dat map[string]interface{}
	err = hjson.Unmarshal(bytes, &dat)
	if err != nil {
		return config, err
	}

	config.Interval, _ = readInt(dat, "Interval")
	if config.Interval == nil {
		config.Interval = new(int)
		*config.Interval = 1000
	} else if *config.Interval < 10 {
		*config.Interval = 10
	}

	config.Timeout, _ = readInt(dat, "Timeout")
	if config.Timeout == nil {
		config.Timeout = new(int)
		*config.Timeout = 1000
	} else if *config.Timeout < 10 {
		*config.Timeout = 10
	}

	config.DataBase, _ = readString(dat, "DataBase")
	if config.DataBase == nil {
		config.DataBase = new(string)
		*config.DataBase = "data.db"
	} else if len(*config.DataBase) <= 0 {
		*config.DataBase = "data.db"
	}
	var str *string
	str, _ = readString(dat, "KeepHistoryFor")
	if str == nil {
		config.KeepHistoryFor, _ = time.ParseDuration("672h")
	} else {
		config.KeepHistoryFor, err = time.ParseDuration("672h")
		if err != nil {
			return config, err
		}
	}

	config.Peers, err = readPeers(dat, "Peers")
	if err != nil {
		return config, err
	}

	crc32q := crc32.MakeTable(0xD5828281)

	for i := range config.Peers {
		if config.Peers[i].Interval == nil {
			config.Peers[i].Interval = config.Interval
		} else if *config.Peers[i].Interval < 10 {
			*config.Peers[i].Interval = 10
		}

		if config.Peers[i].Timeout == nil {
			config.Peers[i].Timeout = config.Timeout
		} else if *config.Peers[i].Timeout < 10 {
			*config.Peers[i].Timeout = 10
		}

		config.Peers[i].ip = net.ParseIP(*config.Peers[i].Address)
		if config.Peers[i].ip == nil {
			return config, fmt.Errorf("'%s' is not a valid IP address\n", *config.Peers[i].Address)
		}
		if config.Peers[i].Name == nil {
			config.Peers[i].Name = config.Peers[i].Address
		}
		if config.Peers[i].ID == nil {
			config.Peers[i].ID = new(int64)
			*config.Peers[i].ID = int64(crc32.Checksum([]byte(*config.Peers[i].Address), crc32q))
		}
	}

	// search for double IDS
	for i, peer1 := range config.Peers {
		for j, peer2 := range config.Peers {
			if i != j && *peer1.ID == *peer2.ID {
				return config, fmt.Errorf("The peers %s and %s got the same IDs", *peer1.Address, *peer2.Address)
			}
		}
	}

	config.ListenAddress, err = readString(dat, "ListenAddress")
	if err != nil {
		if err.Error() == "invalid format" {
			return config, errors.New("'ListenAddress' has an invalid format")
		}
	}
	if config.ListenAddress == nil {
		config.ListenAddress = new(string)
		*config.ListenAddress = ":8000"
	}

	return config, err
}
