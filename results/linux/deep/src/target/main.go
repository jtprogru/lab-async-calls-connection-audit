package main

import (
	"encoding/json"
	"flag"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var accepted, active, hanging, served atomic.Int64

func main() {
	addr := flag.String("addr", "127.0.0.1:18080", "listen")
	stats := flag.String("stats", "127.0.0.1:18081", "stats listen")
	idle := flag.Duration("idle", 0, "server IdleTimeout")
	flag.Parse()

	big := []byte(strings.Repeat("x", 1<<20))
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		served.Add(1)
		w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/big", func(w http.ResponseWriter, r *http.Request) {
		served.Add(1)
		w.Write(big)
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		ms, _ := strconv.Atoi(r.URL.Query().Get("ms"))
		select {
		case <-time.After(time.Duration(ms) * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		served.Add(1)
		w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/hang", func(w http.ResponseWriter, r *http.Request) {
		hanging.Add(1)
		defer hanging.Add(-1)
		<-r.Context().Done()
	})

	srv := &http.Server{Addr: *addr, Handler: mux, IdleTimeout: *idle,
		ConnState: func(c net.Conn, s http.ConnState) {
			switch s {
			case http.StateNew:
				accepted.Add(1)
				active.Add(1)
			case http.StateClosed, http.StateHijacked:
				active.Add(-1)
			}
		}}

	smux := http.NewServeMux()
	smux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]int64{
			"accepted": accepted.Load(), "active": active.Load(),
			"hanging": hanging.Load(), "served": served.Load(),
		})
	})
	smux.HandleFunc("/reset", func(w http.ResponseWriter, r *http.Request) {
		accepted.Store(0)
		served.Store(0)
		w.Write([]byte("reset\n"))
	})
	go func() { log.Fatal(http.ListenAndServe(*stats, smux)) }()
	log.Printf("target on %s idle=%s stats on %s", *addr, *idle, *stats)
	log.Fatal(srv.ListenAndServe())
}
