// target — тестовый сервер лабы.
//
// /ok отдаёт три байта, /big — мегабайт, /slow?ms=N отвечает через N
// миллисекунд, /hang не отвечает вовсе, пока клиент не уйдёт. Принятые
// соединения считаются через хук ConnState, статистика отдаётся на отдельном
// порту, чтобы служебные запросы не портили счёт.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var accepted, active, inflight, served atomic.Int64

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func main() {
	host := env("LAB_ADDR", "127.0.0.1")
	addr := flag.String("addr", host+":"+env("LAB_PORT", "18080"), "адрес тестового сервера")
	stats := flag.String("stats", host+":"+env("LAB_STATS", "18081"), "адрес статистики")
	idle := flag.Duration("idle", 0, "IdleTimeout: через сколько сервер закрывает простаивающее keep-alive соединение (0 — не закрывает)")
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
		inflight.Add(1)
		defer inflight.Add(-1)
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
		inflight.Add(1)
		defer inflight.Add(-1)
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
			"inflight": inflight.Load(), "served": served.Load(),
		})
	})
	smux.HandleFunc("/reset", func(w http.ResponseWriter, r *http.Request) {
		accepted.Store(0)
		served.Store(0)
		w.Write([]byte("reset\n"))
	})
	go func() { log.Fatal(http.ListenAndServe(*stats, smux)) }()
	log.Printf("target: %s, IdleTimeout=%s, статистика на %s", *addr, *idle, *stats)
	log.Fatal(srv.ListenAndServe())
}
