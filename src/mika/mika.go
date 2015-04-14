// Copyright 2015 toor@titansof.tv
//
// A (currently) stateless torrent tracker using Redis as a backing store
//
// Performance tuning options for linux kernel
//
// Set in sysctl.conf
// fs.file-max=100000
// vm.overcommit_memory = 1
//
// echo 1 > /proc/sys/net/ipv4/tcp_tw_reuse
// echo 1 > /proc/sys/net/ipv4/tcp_tw_recycle
// echo never > /sys/kernel/mm/transparent_hugepage/enabled
// echo 10000 > /proc/sys/net/core/somaxconn

package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"github.com/chihaya/bencode"
	"github.com/garyburd/redigo/redis"
	"github.com/labstack/echo"
	mw "github.com/labstack/echo/middleware"
	"github.com/thoas/stats"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

type ErrorResponse struct {
	FailReason string `bencode:"failure reason"`
}

type Tracker struct {
	sync.RWMutex
	Torrents map[uint64]*Torrent
}

const (
	MSG_OK                      int = 0
	MSG_INVALID_REQ_TYPE        int = 100
	MSG_MISSING_INFO_HASH       int = 101
	MSG_MISSING_PEER_ID         int = 102
	MSG_MISSING_PORT            int = 103
	MSG_INVALID_PORT            int = 104
	MSG_INVALID_INFO_HASH       int = 150
	MSG_INVALID_PEER_ID         int = 151
	MSG_INVALID_NUM_WANT        int = 152
	MSG_INFO_HASH_NOT_FOUND     int = 200
	MSG_CLIENT_REQUEST_TOO_FAST int = 500
	MSG_MALFORMED_REQUEST       int = 901
	MSG_GENERIC_ERROR           int = 900
)

var (
	cheese = `
                               ____________
                            __/ ///////// /|
                           /              ¯/|
                          /_______________/ |
    ________________      |  __________  |  |
   /               /|     | |          | |  |
  /               / |     | |  Mika    | |  |
 /_______________/  |/\   | |  v1.0    | |  |
(_______________(   |  \  | |__________| | /
(_______________(   |   \ |______________|/ ___/\
(_______________(  /     |____>______<_____/     \
(_______________( /     / = ==== ==== ==== /|    _|_
(   RISC PC 600 (/     / ========= === == / /   ////
 ¯¯¯¯¯¯¯¯¯¯¯¯¯¯¯      / ========= === == / /   ////
                     <__________________<_/    ¯¯¯
`
	// Error code to message mappings
	resp_msg = map[int]string{
		MSG_INVALID_REQ_TYPE:        "Invalid request type",
		MSG_MISSING_INFO_HASH:       "info_hash missing from request",
		MSG_MISSING_PEER_ID:         "peer_id missing from request",
		MSG_MISSING_PORT:            "port missing from request",
		MSG_INVALID_PORT:            "Invalid port",
		MSG_INVALID_INFO_HASH:       "Torrent info hash must be 20 characters",
		MSG_INVALID_PEER_ID:         "Peer ID Invalid",
		MSG_INVALID_NUM_WANT:        "num_want invalid",
		MSG_INFO_HASH_NOT_FOUND:     "info_hash was not found, better luck next time",
		MSG_CLIENT_REQUEST_TOO_FAST: "Slot down there jimmy.",
		MSG_MALFORMED_REQUEST:       "Malformed request",
		MSG_GENERIC_ERROR:           "Generic Error :(",
	}

	mika *Tracker

	err_parse_reply = errors.New("Failed to parse reply")
	err_cast_reply  = errors.New("Failed to cast reply into type")

	config     *Config
	configLock = new(sync.RWMutex)

	pool *redis.Pool

	config_file = flag.String("config", "./config.json", "Config file path")
	num_procs   = flag.Int("procs", runtime.NumCPU()-1, "Number of CPU cores to use (default: ($num_cores-1))")
)

// Fetch a user_id from the supplied passkey. A return value
// of 0 denotes a non-existing or disabled user_id
func GetUserID(r redis.Conn, passkey string) uint64 {
	Debug("Fetching passkey", passkey)
	user_id_reply, err := r.Do("GET", fmt.Sprintf("t:user:%s", passkey))
	if err != nil {
		log.Println(err)
		return 0
	}
	user_id, err_b := redis.Uint64(user_id_reply, nil)
	if err_b != nil {
		log.Println("Failed to find user", err_b)
		return 0
	}
	return user_id
}

// Checked if the clients peer_id prefix matches the client prefixes
// stored in the white lists
func IsValidClient(r redis.Conn, peer_id string) bool {
	a, err := r.Do("HKEYS", "t:whitelist")

	if err != nil {
		log.Println(err)
		return false
	}
	clients, err := redis.Strings(a, nil)
	for _, client_prefix := range clients {
		if strings.HasPrefix(peer_id, client_prefix) {
			return true
		}
	}
	return false
}

// math.Max for uint64
func UMax(a, b uint64) uint64 {
	if a > b {
		return a
	} else {
		return b
	}
}

// Parse and return a IP from a string
func getIP(ip_str string) (net.IP, error) {
	ip := net.ParseIP(ip_str)
	if ip != nil {
		return ip.To4(), nil
	}
	return nil, errors.New("Failed to parse ip")
}


// Create a new redis pool
func newPool(server, password string, max_idle int) *redis.Pool {
	return &redis.Pool{
		MaxIdle:     max_idle,
		IdleTimeout: 240 * time.Second,
		Dial: func() (redis.Conn, error) {
			c, err := redis.Dial("tcp", server)
			if err != nil {
				return nil, err
			}
			if password != "" {
				if _, err := c.Do("AUTH", password); err != nil {
					c.Close()
					return nil, err
				}
			}
			return c, err
		},
	}
}

// Output a bencoded error code to the torrent client using
// a preset message code constant
func oops(c *echo.Context, msg_code int) {
	c.String(msg_code, responseError(resp_msg[msg_code]))
}

// Generate a bencoded error response for the torrent client to
// parse and display to the user
func responseError(message string) string {
	var out_bytes bytes.Buffer
	//	var er_msg = ErrorResponse{FailReason: message}
	//	er_msg_encoded := bencode.Marshal(&out_bytes)
	//	if er_msg_encoded != nil {
	//		return "."
	//	}
	bencoder := bencode.NewEncoder(&out_bytes)
	bencoder.Encode(bencode.Dict{
		"failure reason": message,
	})
	return out_bytes.String()
}

// Estimate a peers speed using downloaded amount over time
func estSpeed(start_time int32, last_time int32, bytes_sent uint64) float64 {
	if last_time < start_time {
		return 0.0
	}
	return float64(bytes_sent) / (float64(last_time) - float64(start_time))
}

// Generate a 32bit unix timestamp
func unixtime() int32 {
	return int32(time.Now().Unix())
}

func Debug(msg ...interface{}) {
	if config.Debug {
		log.Println(msg...)
	}
}

// Do it
func main() {
	log.Println(cheese)
	// Set max number of CPU cores to use
	log.Println("Num procs(s):", *num_procs)
	runtime.GOMAXPROCS(*num_procs)

	// Initialize the redis pool
	pool = newPool(config.RedisHost, config.RedisPass, config.RedisMaxIdle)

	// Initialize the router + middlewares
	e := echo.New()

	// Passkey is the only param we use, so only allocate for 1
	e.MaxParam(1)

	if config.Debug {
		e.Use(mw.Logger)
	}

	// Third-party middleware
	s := stats.New()
	e.Use(s.Handler)

	// Stats route
	e.Get("/stats", func(c *echo.Context) {
		c.JSON(200, s.Data())
	})

	// Public tracker routes
	e.Get("/:passkey/announce", HandleAnnounce)
	e.Get("/:passkey/scrape", HandleScrape)

	// Start watching for expiring peers
	go PeerStalker()

	// Start server
	log.Println(config)
	e.Run(config.ListenHost)
}

func init() {
	// Parse CLI args
	flag.Parse()

	mika = &Tracker{Torrents: make(map[uint64]*Torrent)}

	loadConfig(true)
	s := make(chan os.Signal, 1)
	signal.Notify(s, syscall.SIGUSR2)
	go func() {
		for {
			<-s
			loadConfig(false)
			log.Println("> Reloaded config")
		}
	}()
}