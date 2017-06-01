package main

import (
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"

	"fmt"
	"time"

	"sync"

	"flag"

	"encoding/json"

	"strconv"

	"encoding/binary"

	"strings"

	"path"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
	"golang.org/x/net/websocket"
	"gopkg.in/eapache/channels.v1"
)

const ProtocolICMP = 1
const ICMPPacketLength = 1500

var listener4 *icmp.PacketConn
var listener6 *icmp.PacketConn

var messages channels.Channel
var quitChannel *QuitChannel

var requests []Request
var queryChannel *QueryChannel

var seqcount = 0
var lock sync.RWMutex

var configFile string
var showVersion bool
var showHelp bool

var config Config

var db *DB

var version = "unknown"

func ping(peer *Peer) (err error) {
	var isIP4 = false
	var listener = listener6

	// which listener to use?
	if peer.ip.To4() != nil {
		isIP4 = true
		listener = listener4
	}

	// build the message
	var message icmp.Message
	if isIP4 {
		message.Type = ipv4.ICMPTypeEcho

	} else {
		message.Type = ipv6.ICMPTypeEchoRequest
	}
	message.Code = 0

	lock.Lock()
	if seqcount >= 65535 {
		seqcount = 0
	}
	seqcount++
	var seq = seqcount
	lock.Unlock()

	message.Body = &icmp.Echo{
		ID:   os.Getpid() & 0xffff,
		Seq:  seq,
		Data: []byte{},
	}

	// marshal the msssage
	var bytes []byte
	bytes, err = message.Marshal(nil)
	if err != nil {
		return err
	}

	messages.In() <- Request{
		ID:   seq,
		Time: time.Now().UTC().UnixNano() / 1000000,
		Peer: peer,
	}

	// and send
	_, err = listener.WriteTo(bytes, &net.IPAddr{IP: peer.ip})
	if err != nil {
		return err
	}

	return nil
}

func readListener(listener net.PacketConn, ipVersion int) {
	// Subscribe to quitChannel
	quitChannel := quitChannel.Add()

	// recive the reply
	var bytes []byte
	var recivedMessage *icmp.Message
	var err error
	var expectedMessageType icmp.Type
	if ipVersion == 4 {
		expectedMessageType = ipv4.ICMPTypeEchoReply
	} else if ipVersion == 6 {
		expectedMessageType = ipv6.ICMPTypeEchoReply
	} else {
		panic(fmt.Errorf("Unknown IpVersion %d", ipVersion))
	}

	bytes = make([]byte, ICMPPacketLength)
	for {
		type st struct {
			byteCount int
			remote    net.Addr
			err       error
		}
		ch := make(chan st, 1)
		go func(bytes []byte) {
			var result st
			result.byteCount, result.remote, result.err = listener.ReadFrom(bytes)
			ch <- result
		}(bytes)
		select {
		case <-quitChannel:
			quitChannel <- true
			return
		case r := <-ch:
			if r.err != nil {
				continue
			}
			recivedMessage, err = icmp.ParseMessage(ProtocolICMP, bytes[:r.byteCount])
			if err != nil {
				continue
			}

			if recivedMessage.Type == expectedMessageType {
				messages.In() <- Response{
					ID:   recivedMessage.Body.(*icmp.Echo).Seq,
					IP:   net.ParseIP(r.remote.String()),
					Time: time.Now().UTC().UnixNano() / 1000000,
				}
			}
			//return 0, fmt.Errorf("Got invalid response type: %d, message was: %v", recivedMessage.Type, recivedMessage)
		}

	}
}

// collector collects all requests (sent by ping) and puts them in a list
func collector() {
	// Subscribe to quitChannel
	quitChannel := quitChannel.Add()
	timeoutTicker := time.NewTicker(time.Second)
	cleanupTicker := time.NewTicker(time.Hour)
	if err := cleanup(); err != nil {
		log.Fatal(err)
	}
	for {
		select {
		case message := <-messages.Out():
			switch message.(type) {
			case Request:
				requests = append(requests, message.(Request))
			case Response:
				// find the matching request
				for i := len(requests) - 1; i >= 0; i-- {
					response := message.(Response)
					if requests[i].ID == response.ID {
						// add the query
						query := Query{
							PeerID:       *requests[i].Peer.ID,
							Time:         response.Time,
							ResponseTime: (response.Time - requests[i].Time),
						}
						//log.Printf("Got Response for %s (%dms)\n", *requests[i].Peer.Name, query.ResponseTime)
						queryChannel.Push(query)
						// remove the request
						requests = append(requests[:i], requests[i+1:]...)
						db.Create(&query)
						break
					}
				}
			}
		case <-timeoutTicker.C:
			var now = time.Now().UTC().Unix() * 1000
			for i := len(requests) - 1; i >= 0; i-- {
				if requests[i].Time+int64(*requests[i].Peer.Timeout) > now {
					// add the query
					query := Query{
						PeerID:       *requests[i].Peer.ID,
						Time:         time.Now().UTC().UnixNano() / 1000000,
						ResponseTime: -1,
					}
					log.Printf("Got Timeout for %s\n", *requests[i].Peer.Name)
					queryChannel.Push(query)
					// remove the request
					requests = append(requests[:i], requests[i+1:]...)
					db.Create(&query)
				}
			}
		case <-cleanupTicker.C:
			cleanup()
		case <-quitChannel:
			quitChannel <- true
			return
		}
	}
}

func cleanup() error {
	var err error
	now := time.Now().UTC()

	// Delete every data that is older than KeepHistoryFor
	err = db.Delete(&Query{}, "time < ?", now.Add(-config.KeepHistoryFor).Unix()*1000).Error
	if err != nil {
		return err
	}
	return nil
}

func pingRoutine(peer Peer) {
	interval, _ := time.ParseDuration(fmt.Sprintf("%dms", *peer.Interval))
	// Subscribe to quitChannel
	quitChannel := quitChannel.Add()
	ticker := time.NewTicker(interval)
	for {
		err := ping(&peer)
		if err != nil {
			log.Panic(err)
		}
		select {
		case <-quitChannel:
			quitChannel <- true
			return
		case <-ticker.C:
			continue
		}
	}
}

func liveDataHandler(ws *websocket.Conn) {
	// Subscribe to quitChannel
	quitChannel := quitChannel.Add()
	queryChannel := queryChannel.Add()

	type st struct {
		PeerID uint32
		Err    error
	}
	offset := make(chan st, 1)

	go func() {
		var in []byte
		err := websocket.Message.Receive(ws, &in)
		if err != nil {
			offset <- st{PeerID: 0, Err: err}
		} else {
			offset <- st{PeerID: binary.BigEndian.Uint32(in), Err: nil}
		}
	}()

	var peerID int64 = 0

	for {
		select {
		case <-quitChannel:
			ws.Close()
			quitChannel <- true
			break
		case v := <-offset:
			if v.Err != nil {
				return
			}
			peerID = int64(v.PeerID)
		case query := <-queryChannel.Out():
			if query.(Query).PeerID == peerID {
				err := websocket.JSON.Send(ws, query)
				if err != nil {
					return
				}
			}
		}
	}
}

func dataHandler(w http.ResponseWriter, req *http.Request) {
	var peerID int64
	var start int64 = -1
	var stop int64 = -1
	var max = -1
	var err error
	var str string

	str = req.URL.Query().Get("peer")
	peerID, err = strconv.ParseInt(str, 10, 0)
	if err != nil {
		w.WriteHeader(400)
		return
	}

	str = req.URL.Query().Get("start")
	if len(str) > 0 {
		start, err = strconv.ParseInt(str, 10, 0)
		if err != nil {
			w.WriteHeader(400)
			return
		}
	}
	str = req.URL.Query().Get("stop")
	if len(str) > 0 {
		stop, err = strconv.ParseInt(str, 10, 0)
		if err != nil {
			w.WriteHeader(400)
			return
		}
	}

	str = req.URL.Query().Get("max")
	if len(str) > 0 {
		max, err = strconv.Atoi(str)
		if err != nil {
			w.WriteHeader(400)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	encoder := json.NewEncoder(w)
	var queries []Query
	if start >= 0 && stop >= 0 {
		if start > stop {
			i := stop
			stop = start
			start = i
		}
		err = db.Select("response_time, time").Where("peer_id = ? AND time >= ? AND time <= ?", peerID, start, stop).Order("time").Find(&queries).Error
	} else {
		err = db.Select("response_time, time").Where("peer_id = ?", peerID).Order("time").Find(&queries).Error
	}
	if err != nil {
		log.Printf("Unable to get data: %v\n", err)
	}

	l := len(queries)
	interval := getPeerInterval(peerID)

	var tolerance int64 = 500 // half a second tollerance
	// insert missing data
	if l > 0 {
		// insert data point on the begining if the first data point is far away
		diff := queries[0].Time - start - tolerance
		if diff > interval {
			queries = append(queries, Query{})
			copy(queries[1:], queries[:l-1])
			queries[0] = Query{PeerID: peerID, Time: start + interval, ResponseTime: 0}
			l++
		}

		// append data point if end is too far away
		diff = queries[l-1].Time - stop - tolerance
		if diff > interval {
			queries = append(queries, Query{PeerID: peerID, Time: stop - interval, ResponseTime: 0})
			l++
		}

		// fill empty data points
		var previousQuery *Query
		for i := 0; i < l; i++ {
			if previousQuery != nil {
				diff = queries[i].Time - previousQuery.Time - tolerance
				if diff > interval {
					queries = append(queries, Query{})
					copy(queries[i+1:], queries[i:])
					queries[i] = Query{PeerID: peerID, Time: previousQuery.Time + interval, ResponseTime: 0}
					l++
				}
			}
			previousQuery = &queries[i]
		}
	}
	if max > 0 {
		if l == 0 {
			queries = make([]Query, max)
			for i := 0; i < max; i++ {
				queries[i] = Query{PeerID: peerID, Time: start + interval*int64(i), ResponseTime: 0}
			}
			encoder.Encode(queries)
		} else if l > max {
			s := l / max
			var resultQueries []Query

			for i := 0; i < l; i += s {
				resultQueries = append(resultQueries, queries[i])
			}
			encoder.Encode(resultQueries[len(resultQueries)-max:])
		} else {
			// if client requested more than we have, just respond with all that we got
			encoder.Encode(queries)
		}
	} else {
		encoder.Encode(queries)
	}
}

func getPeerInterval(peerID int64) int64 {
	for _, p := range config.Peers {
		if *p.ID == peerID {
			return int64(*p.Interval)
		}
	}
	return int64(*config.Interval)
}

func statsHandler(w http.ResponseWriter, req *http.Request) {
	var peerID int
	var start = -1
	var stop = -1
	var err error
	var str string

	str = req.URL.Query().Get("peer")
	peerID, err = strconv.Atoi(str)
	if err != nil {
		w.WriteHeader(400)
		return
	}

	str = req.URL.Query().Get("start")
	if len(str) > 0 {
		start, err = strconv.Atoi(str)
		if err != nil {
			w.WriteHeader(400)
			return
		}
	}
	str = req.URL.Query().Get("stop")
	if len(str) > 0 {
		stop, err = strconv.Atoi(str)
		if err != nil {
			w.WriteHeader(400)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	encoder := json.NewEncoder(w)
	type Result struct {
		F float64
	}

	var averageTime float64
	var uptime float64
	var result Result
	if start > 0 && stop > 0 {
		err = db.Raw("SELECT AVG(response_time) AS  f FROM queries WHERE peer_id = ? AND time >= ? AND time <= ? AND response_time > 0", peerID, start, stop).Scan(&result).Error
		if err != nil {
			log.Printf("Unable to get stats: %v\n", err)
		}
		averageTime = result.F
		err = db.Raw("SELECT COUNT(response_time) * 100 / (SELECT COUNT(response_time) FROM queries WHERE peer_id = ? AND time >= ? AND time <= ?) AS f FROM queries WHERE peer_id = ? AND time >= ? AND time <= ? AND response_time > 0", peerID, start, stop, peerID, start, stop).Scan(&result).Error
		uptime = result.F
	} else {
		err = db.Raw("SELECT AVG(response_time) AS f FROM queries WHERE peer_id = ? AND response_time > 0", peerID).Scan(&result).Error
		if err != nil {
			log.Printf("Unable to get stats: %v\n", err)
		}
		averageTime = result.F
		err = db.Raw("SELECT COUNT(response_time) * 100 / (SELECT COUNT(response_time) FROM queries WHERE peer_id = ?) AS f FROM queries WHERE peer_id = ? AND response_time > 0", peerID, peerID).Scan(&result).Error
		uptime = result.F
	}
	if err != nil {
		log.Printf("Unable to get stats: %v\n", err)
	}
	type st struct {
		AverageResponseTime float64
		Uptime              float64
	}
	encoder.Encode(&st{
		AverageResponseTime: averageTime,
		Uptime:              uptime,
	})
}

func peersHandler(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	encoder := json.NewEncoder(w)
	encoder.Encode(config.Peers)
}

func assetHandler(w http.ResponseWriter, req *http.Request) {

	file := req.URL.Path
	for strings.HasPrefix(file, "/") {
		file = file[1:]
	}

	if len(file) == 0 || file == "" {
		file = "index.html"
	}
	bytes, err := Asset(file)
	if err != nil {
		w.WriteHeader(404)
		return
	}
	w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(file)))
	w.WriteHeader(200)

	w.Write(bytes)
}

func webServer(addr string) {
	// Subscribe to quitChannel
	quitChannel := quitChannel.Add()

	var server http.Server

	serveMux := http.NewServeMux()
	serveMux.HandleFunc("/", assetHandler)
	for _, a := range AssetNames() {
		serveMux.HandleFunc(fmt.Sprintf("/%s", a), assetHandler)
	}

	serveMux.HandleFunc("/data", dataHandler)
	serveMux.HandleFunc("/stats", statsHandler)
	serveMux.HandleFunc("/peers", peersHandler)
	serveMux.Handle("/livedata", websocket.Handler(liveDataHandler))

	server.Handler = serveMux
	server.Addr = addr

	log.Printf("Starting Listener on %s\n", addr)
	go func() {
		//err := server.ListenAndServeTLS("cert.pem", "key.pem")
		err := server.ListenAndServe()
		if err != nil {
			log.Panic(err)
		}
	}()
	select {
	case <-quitChannel:
		server.Close()
		quitChannel <- true
	}
}

func init() {
	flag.StringVar(&configFile, "config", "config.hjson", "")
	flag.StringVar(&configFile, "c", "config.hjson", "")
	flag.BoolVar(&showHelp, "help", false, "")
	flag.BoolVar(&showVersion, "version", false, "")
}

func main() {
	var err error
	flag.Parse()

	if showVersion || (len(flag.Args()) > 0 && flag.Arg(0) == "version") {
		fmt.Printf("icmpmon %s\n", version)
		os.Exit(0)
	}
	if len(configFile) <= 0 || showHelp || (len(flag.Args()) > 0 && flag.Arg(0) == "help") {
		fmt.Printf("usage: %s [-c config.hjson]\n", filepath.Base(os.Args[0]))
		if len(configFile) <= 0 {
			os.Exit(1)
		} else {
			os.Exit(0)
		}
	}

	config, err = ReadConfig(configFile)
	if err != nil {
		fmt.Printf("unable to read '%s'!\nError was: %+v\n", configFile, err)
		fmt.Printf("usage: %s [-c config.hjson]\n", filepath.Base(os.Args[0]))
		os.Exit(1)
	}

	if config.Peers == nil || len(config.Peers) <= 0 {
		fmt.Printf("config '%s' does not contain 'peers'\n", configFile)
		os.Exit(1)
	}

	db, err = NewDB(*config.DataBase)
	if err != nil {
		log.Fatal(err)
	}

	// if linux
	//sysctl -w net.ipv4.ping_group_range="0 0"

	listener4, err = icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		log.Fatalf("listen err, %s", err)
	}
	defer listener4.Close()

	listener6, err = icmp.ListenPacket("ip6:icmp", "::1")
	if err != nil {
		log.Fatalf("listen err, %s", err)
	}
	defer listener6.Close()
	quitChannel = NewQuitChannel()
	queryChannel = NewQueryChannel()
	messages = channels.NewRingChannel(1024)

	// start webserver
	go webServer(*config.ListenAddress)

	// start the collector
	go collector()

	// start the listeners
	go readListener(listener4, 4)
	go readListener(listener6, 6)

	for i := range config.Peers {
		go pingRoutine(config.Peers[i])
	}

	var endWaiter sync.WaitGroup
	endWaiter.Add(1)
	var signalChannel chan os.Signal
	signalChannel = make(chan os.Signal, 1)
	signal.Notify(signalChannel, os.Interrupt)
	go func() {
		<-signalChannel
		endWaiter.Done()
	}()

	endWaiter.Wait()

	quitChannel.SignalQuit()
	quitChannel.WaitForCleanup()
}
