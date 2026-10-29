package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"os/exec"
	"runtime"
	"runtime/pprof"
	"sort"
	"strings"
	"time"
)

var port = envOr("LAB_PORT", "18080")
var base = "http://127.0.0.1:" + port
var statsURL = "http://127.0.0.1:" + envOr("LAB_STATS", "18081")

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

var reused, fresh int

func traced(ctx context.Context) context.Context {
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(i httptrace.GotConnInfo) {
			if i.Reused {
				reused++
			} else {
				fresh++
			}
		},
	})
}

func get(c *http.Client, url string) (*http.Response, error) {
	req, _ := http.NewRequestWithContext(traced(context.Background()), "GET", url, nil)
	return c.Do(req)
}

type stats struct{ Accepted, Active, Hanging, Served int64 }

// stats via DefaultClient, like the post (one service conn to the stats port)
func getStats() stats {
	resp, err := http.Get(statsURL + "/stats")
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	var m map[string]int64
	json.NewDecoder(resp.Body).Decode(&m)
	return stats{m["accepted"], m["active"], m["hanging"], m["served"]}
}

func resetStats() {
	resp, err := http.Get(statsURL + "/reset")
	if err != nil {
		panic(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

func sockets() string {
	out, _ := exec.Command("lsof", "-nP", "-a", "-p", fmt.Sprint(os.Getpid()), "-iTCP:"+port).Output()
	counts := map[string]int{}
	for i, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if i == 0 || l == "" {
			continue
		}
		f := strings.Fields(l)
		st := f[len(f)-1]
		counts[strings.Trim(st, "()")]++
	}
	if len(counts) == 0 {
		return "нет"
	}
	var keys []string
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, counts[k]))
	}
	return strings.Join(parts, " ")
}

func rawLsof() string {
	out, _ := exec.Command("lsof", "-nP", "-a", "-p", fmt.Sprint(os.Getpid()), "-iTCP:"+port).Output()
	return string(out)
}

func report(label string) {
	s := getStats()
	fmt.Printf("%-22s сервер принял соединений: %d, переиспользовано: %d, новых: %d\n", label, s.Accepted, reused, fresh)
	fmt.Printf("%-22s сокеты клиента: %s; горутин: %d\n", "", sockets(), runtime.NumGoroutine())
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	mode := os.Args[1]
	resetStats()
	n := 50
	switch mode {
	case "transport-per-call":
		for i := 0; i < n; i++ {
			c := &http.Client{Transport: &http.Transport{}, Timeout: 5 * time.Second}
			resp, err := get(c, base+"/ok")
			must(err)
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		report(mode)
		if len(os.Args) > 2 && os.Args[2] == "dump" {
			pprof.Lookup("goroutine").WriteTo(os.Stdout, 1)
		}
	case "graceful-close":
		var ts []*http.Transport
		for i := 0; i < n; i++ {
			t := &http.Transport{}
			ts = append(ts, t)
			c := &http.Client{Transport: t, Timeout: 5 * time.Second}
			resp, err := get(c, base+"/ok")
			must(err)
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		for _, t := range ts {
			t.CloseIdleConnections()
		}
		time.Sleep(200 * time.Millisecond)
		report(mode)
	case "burst":
		c := &http.Client{Timeout: 5 * time.Second}
		for b := 0; b < 2; b++ {
			done := make(chan struct{}, 50)
			for i := 0; i < 50; i++ {
				go func() {
					resp, err := get(c, base+"/slow?ms=300")
					if err == nil {
						io.Copy(io.Discard, resp.Body)
						resp.Body.Close()
					}
					done <- struct{}{}
				}()
			}
			for i := 0; i < 50; i++ {
				<-done
			}
			time.Sleep(200 * time.Millisecond)
			report(fmt.Sprintf("burst %d", b+1))
		}
	case "client-per-call":
		for i := 0; i < n; i++ {
			c := &http.Client{Timeout: 5 * time.Second}
			resp, err := get(c, base+"/ok")
			must(err)
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		report(mode)
	case "shared":
		c := &http.Client{Timeout: 5 * time.Second}
		for i := 0; i < n; i++ {
			resp, err := get(c, base+"/ok")
			must(err)
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		report(mode)
	case "no-close", "no-close-default", "no-close-nka", "no-close-ka10":
		c := &http.Client{Timeout: 5 * time.Second}
		switch mode {
		case "no-close-default":
			c = http.DefaultClient
		case "no-close-nka", "no-close-ka10":
			ka := -1 * time.Second
			if mode == "no-close-ka10" {
				ka = 10 * time.Second
			}
			t := http.DefaultTransport.(*http.Transport).Clone()
			d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: ka}
			t.DialContext = d.DialContext
			c = &http.Client{Transport: t}
		}
		for i := 0; i < 20; i++ {
			resp, err := get(c, base+"/ok")
			must(err)
			_ = resp
		}
		start := time.Now()
		report(mode)
		var marks []time.Duration
		if mode == "no-close" {
			marks = []time.Duration{3 * time.Second, 6 * time.Second}
		} else {
			marks = []time.Duration{3 * time.Second, 6 * time.Second}
			for t := 10 * time.Second; t <= 240*time.Second; t += 5 * time.Second {
				marks = append(marks, t)
			}
		}
		for _, m := range marks {
			time.Sleep(time.Until(start.Add(m)))
			report(fmt.Sprintf("  +%s", m))
			if m == 120*time.Second || m == 30*time.Second {
				fmt.Print(rawLsof())
			}
		}
	case "read-no-close", "close-no-read", "close-no-read-big":
		c := &http.Client{Timeout: 5 * time.Second}
		path := "/ok"
		if mode == "close-no-read-big" {
			path = "/big"
		}
		for i := 0; i < n; i++ {
			resp, err := get(c, base+path)
			must(err)
			switch mode {
			case "read-no-close":
				io.Copy(io.Discard, resp.Body)
			default:
				resp.Body.Close()
			}
		}
		report(mode)
	case "hang":
		for i := 0; i < 5; i++ {
			go func() {
				resp, err := http.Get(base + "/hang")
				if err == nil {
					resp.Body.Close()
				}
				fmt.Println("returned:", err)
			}()
		}
		start := time.Now()
		for _, m := range []time.Duration{1 * time.Second, 5 * time.Second, 30 * time.Second} {
			time.Sleep(time.Until(start.Add(m)))
			s := getStats()
			fmt.Printf("через ~%2.0f с: висит запросов на сервере: %d, сокеты клиента: %s, горутин: %d\n",
				m.Seconds(), s.Hanging, sockets(), runtime.NumGoroutine())
		}
	case "errors":
		// 1. Client.Timeout
		c1 := &http.Client{Timeout: 1 * time.Second}
		_, err := c1.Get(base + "/hang")
		fmt.Printf("Client.Timeout:           %v\n", err)
		// 2. context.WithTimeout
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		req, _ := http.NewRequestWithContext(ctx, "GET", base+"/hang", nil)
		_, err = http.DefaultClient.Do(req)
		cancel()
		fmt.Printf("context.WithTimeout:      %v\n", err)
		// 3. ResponseHeaderTimeout
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.ResponseHeaderTimeout = 1 * time.Second
		c3 := &http.Client{Transport: t}
		_, err = c3.Get(base + "/hang")
		fmt.Printf("ResponseHeaderTimeout:    %v\n", err)
		// 4. dial to nowhere with Client.Timeout
		c4 := &http.Client{Timeout: 2 * time.Second}
		st := time.Now()
		_, err = c4.Get("http://10.255.255.1:81/")
		fmt.Printf("dial в никуда:            %v (%.1fs)\n", err, time.Since(st).Seconds())
		time.Sleep(300 * time.Millisecond)
		fmt.Printf("висит запросов на сервере: %d\n", getStats().Hanging)
	case "maxconns":
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.MaxConnsPerHost = 3
		c := &http.Client{Transport: t, Timeout: 2 * time.Second}
		for i := 0; i < 3; i++ {
			_, err := c.Get(base + "/ok")
			must(err)
		}
		st := time.Now()
		_, err := c.Get(base + "/ok")
		fmt.Printf("четвёртый запрос через %.1f с: %v\n", time.Since(st).Seconds(), err)
		fmt.Printf("сервер обслужил запросов: %d\n", getStats().Served)
	case "maxconns-notimeout":
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.MaxConnsPerHost = 3
		c := &http.Client{Transport: t}
		for i := 0; i < 3; i++ {
			_, err := c.Get(base + "/ok")
			must(err)
		}
		done := make(chan error, 1)
		st := time.Now()
		go func() { _, err := c.Get(base + "/ok"); done <- err }()
		select {
		case err := <-done:
			fmt.Printf("четвёртый вернулся через %.1f с: %v\n", time.Since(st).Seconds(), err)
		case <-time.After(10 * time.Second):
			fmt.Printf("четвёртый запрос висит уже %.0f с; сервер обслужил: %d\n", time.Since(st).Seconds(), getStats().Served)
		}
	}
}
