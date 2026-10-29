// golab — клиентская половина лабы: каждый режим воспроизводит одну утечку
// из поста и печатает, что видно снаружи: сколько соединений принял сервер,
// в каком состоянии сокеты клиента и сколько у процесса горутин.
//
// Запускать рядом с работающим target, см. Makefile и README.md.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"runtime"
	"runtime/pprof"
	"slices"
	"strings"
	"syscall"
	"time"
)

var (
	host     = env("LAB_ADDR", "127.0.0.1")
	port     = env("LAB_PORT", "18080")
	base     = "http://" + host + ":" + port
	statsURL = "http://" + host + ":" + env("LAB_STATS", "18081")
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

var modes = []struct{ name, help string }{
	{"transport-per-call", "новый http.Transport на каждый вызов (-dump — дамп горутин)"},
	{"client-per-call", "новый http.Client без своего Transport на каждый вызов"},
	{"shared", "один общий клиент"},
	{"graceful-close", "Transport на вызов, но с CloseIdleConnections: для сравнения TIME_WAIT"},
	{"no-close", "тело не закрыто, Client.Timeout = 5s"},
	{"no-close-default", "тело не закрыто, http.DefaultClient; длинный таймлайн (-for)"},
	{"no-close-nka", "то же, keepalive клиента выключен"},
	{"no-close-ka10", "то же, keepalive клиента 10s"},
	{"read-no-close", "тело дочитано до EOF, Close не вызван"},
	{"close-no-read", "Close без чтения, ответ 3 байта"},
	{"close-no-read-big", "Close без чтения, ответ 1 МиБ (-tw — посчитать TIME_WAIT)"},
	{"hang", "5 запросов через DefaultClient к /hang"},
	{"errors", "четыре способа ограничить вызов и их сообщения об ошибке"},
	{"maxconns", "MaxConnsPerHost=3, три тела не закрыты, Client.Timeout=2s"},
	{"maxconns-notimeout", "то же без Client.Timeout"},
	{"probe", "одно сырое соединение: состояние и SO_ERROR по времени (-ka)"},
}

var (
	flagFor  = flag.Duration("for", 4*time.Minute, "длительность таймлайна для no-close-* и probe")
	flagDump = flag.Bool("dump", false, "transport-per-call: напечатать дамп горутин")
	flagTW   = flag.Bool("tw", false, "напечатать число TIME_WAIT к порту target через секунду после прогона")
	flagRaw  = flag.Bool("raw", false, "в конце таймлайна напечатать сырой список сокетов процесса")
	flagKA   = flag.Duration("ka", 0, "probe: keepalive клиента (0 — по умолчанию Go, -1s — выключен)")
)

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
	req, err := http.NewRequestWithContext(traced(context.Background()), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

type stats struct{ Accepted, Active, Inflight, Served int64 }

// statsClient ходит на отдельный порт и держит ровно одно служебное
// соединение: оно видно в дампе горутин как лишняя пара.
var statsClient = &http.Client{Timeout: 5 * time.Second}

func getStats() stats {
	resp, err := statsClient.Get(statsURL + "/stats")
	must(err)
	defer resp.Body.Close()
	var s stats
	must(json.NewDecoder(resp.Body).Decode(&s))
	return s
}

func resetStats() {
	resp, err := statsClient.Get(statsURL + "/reset")
	must(err)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

func report(label string) {
	s := getStats()
	fmt.Printf("%-22s сервер принял соединений: %d, переиспользовано: %d, новых: %d\n", label, s.Accepted, reused, fresh)
	fmt.Printf("%-22s сокеты клиента: %s; горутин: %d\n", "", sockets(), runtime.NumGoroutine())
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "golab:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: golab [flags] <mode>\n\nрежимы:\n")
	for _, m := range modes {
		fmt.Fprintf(os.Stderr, "  %-20s %s\n", m.name, m.help)
	}
	fmt.Fprintf(os.Stderr, "\nflags:\n")
	flag.PrintDefaults()
}

func main() {
	flag.Usage = usage
	flag.Parse()
	if flag.NArg() != 1 {
		usage()
		os.Exit(2)
	}
	mode := flag.Arg(0)
	if !slices.ContainsFunc(modes, func(m struct{ name, help string }) bool { return m.name == mode }) {
		usage()
		os.Exit(2)
	}
	resetStats()
	switch mode {
	case "transport-per-call":
		transportPerCall()
	case "client-per-call":
		for range 50 {
			c := &http.Client{Timeout: 5 * time.Second}
			drain(get(c, base+"/ok"))
		}
		report(mode)
	case "shared":
		c := &http.Client{Timeout: 5 * time.Second}
		for range 50 {
			drain(get(c, base+"/ok"))
		}
		report(mode)
	case "graceful-close":
		var ts []*http.Transport
		for range 50 {
			t := &http.Transport{}
			ts = append(ts, t)
			c := &http.Client{Transport: t, Timeout: 5 * time.Second}
			drain(get(c, base+"/ok"))
		}
		for _, t := range ts {
			t.CloseIdleConnections()
		}
		time.Sleep(200 * time.Millisecond)
		report(mode)
		timeWait()
	case "no-close", "no-close-default", "no-close-nka", "no-close-ka10":
		noClose(mode)
	case "read-no-close", "close-no-read", "close-no-read-big":
		bodies(mode)
	case "hang":
		hang()
	case "errors":
		errorsMode()
	case "maxconns":
		maxConns(true)
	case "maxconns-notimeout":
		maxConns(false)
	case "probe":
		probe()
	}
}

func drain(resp *http.Response, err error) {
	must(err)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

// Пятьдесят вызовов, каждый со своим Transport: тело дочитано и закрыто,
// но пул у каждого вызова свой, и брошенные соединения остаются открытыми.
func transportPerCall() {
	for range 50 {
		c := &http.Client{Transport: &http.Transport{}, Timeout: 5 * time.Second}
		drain(get(c, base+"/ok"))
	}
	report("transport-per-call")
	if *flagDump {
		pprof.Lookup("goroutine").WriteTo(os.Stdout, 1)
	}
}

// Двадцать запросов без чтения и без Close, потом таймлайн сокетов.
func noClose(mode string) {
	var c *http.Client
	marks := []time.Duration{3 * time.Second, 6 * time.Second}
	changes := false
	switch mode {
	case "no-close":
		c = &http.Client{Timeout: 5 * time.Second}
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
	if mode != "no-close" {
		changes = true
		for _, m := range []time.Duration{30 * time.Second, 90 * time.Second, 4 * time.Minute} {
			if m < *flagFor {
				marks = append(marks, m)
			}
		}
		marks = append(marks, *flagFor)
	}
	for range 20 {
		resp, err := get(c, base+"/ok")
		must(err)
		_ = resp // ни чтения, ни Close
	}
	start := time.Now()
	report(mode)
	timeline(start, marks, changes)
	if *flagRaw {
		fmt.Print(rawSockets())
	}
}

// timeline печатает состояние на отметках marks, а с changes=true ещё и
// каждый раз, когда меняется набор сокетов клиента.
func timeline(start time.Time, marks []time.Duration, changes bool) {
	prev := sockets()
	for _, m := range marks {
		for {
			el := time.Since(start)
			if el >= m {
				break
			}
			step := min(500*time.Millisecond, m-el)
			time.Sleep(step)
			if !changes || time.Since(start) >= m {
				continue
			}
			if cur := sockets(); cur != prev {
				report(fmt.Sprintf("  +%s", time.Since(start).Round(100*time.Millisecond)))
				prev = cur
			}
		}
		report(fmt.Sprintf("  +%s", m))
		prev = sockets()
	}
}

func bodies(mode string) {
	c := &http.Client{Timeout: 5 * time.Second}
	path := "/ok"
	if mode == "close-no-read-big" {
		path = "/big"
	}
	for range 50 {
		resp, err := get(c, base+path)
		must(err)
		if mode == "read-no-close" {
			io.Copy(io.Discard, resp.Body)
		} else {
			resp.Body.Close()
		}
	}
	report(mode)
	if *flagTW {
		timeWait()
	}
}

func timeWait() {
	if !*flagTW {
		return
	}
	time.Sleep(time.Second)
	fmt.Printf("%-22s TIME_WAIT к порту %s через 1 с: %s\n", "", port, timeWaitReport())
}

func hang() {
	for range 5 {
		go func() {
			resp, err := http.Get(base + "/hang") // DefaultClient: таймаута нет
			if err == nil {
				resp.Body.Close()
			}
		}()
	}
	start := time.Now()
	for _, m := range []time.Duration{1 * time.Second, 5 * time.Second, 30 * time.Second} {
		time.Sleep(time.Until(start.Add(m)))
		s := getStats()
		fmt.Printf("через ~%2.0f с: висит запросов на сервере: %d, сокеты клиента: %s, горутин: %d\n",
			m.Seconds(), s.Inflight, sockets(), runtime.NumGoroutine())
	}
}

func errorsMode() {
	c1 := &http.Client{Timeout: 1 * time.Second}
	_, err := c1.Get(base + "/hang")
	fmt.Printf("Client.Timeout:           %v\n", err)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/hang", nil)
	_, err = http.DefaultClient.Do(req)
	cancel()
	fmt.Printf("context.WithTimeout:      %v\n", err)

	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = 1 * time.Second
	_, err = (&http.Client{Transport: t}).Get(base + "/hang")
	fmt.Printf("ResponseHeaderTimeout:    %v\n", err)

	// 10.255.255.1 — адрес, до которого SYN уходит и не возвращается.
	// Если у хоста нет маршрута, ошибка будет другой: см. README.
	c4 := &http.Client{Timeout: 2 * time.Second}
	st := time.Now()
	_, err = c4.Get("http://10.255.255.1:81/")
	fmt.Printf("dial в никуда:            %v (через %.1f с)\n", err, time.Since(st).Seconds())

	time.Sleep(300 * time.Millisecond)
	fmt.Printf("висит запросов на сервере: %d\n", getStats().Inflight)
}

func maxConns(withTimeout bool) {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxConnsPerHost = 3
	c := &http.Client{Transport: t}
	if withTimeout {
		c.Timeout = 2 * time.Second
	}
	for range 3 {
		_, err := c.Get(base + "/ok") // тело не закрыто: соединение занято
		must(err)
	}
	st := time.Now()
	if withTimeout {
		_, err := c.Get(base + "/ok")
		fmt.Printf("четвёртый запрос через %.1f с: %v\n", time.Since(st).Seconds(), err)
		fmt.Printf("сервер обслужил запросов: %d\n", getStats().Served)
		return
	}
	done := make(chan error, 1)
	go func() { _, err := c.Get(base + "/ok"); done <- err }()
	select {
	case err := <-done:
		fmt.Printf("четвёртый запрос вернулся через %.1f с: %v\n", time.Since(st).Seconds(), err)
	case <-time.After(10 * time.Second):
		fmt.Printf("четвёртый запрос висит уже %.0f с и будет висеть дальше; сервер обслужил запросов: %d\n",
			time.Since(st).Seconds(), getStats().Served)
	}
}

// probe открывает одно сырое соединение, шлёт запрос и не читает ответ.
// Раз в полсекунды смотрит на состояние сокета и SO_ERROR и печатает
// каждое изменение: так видно точную секунду, когда пришёл RST.
func probe() {
	d := net.Dialer{KeepAlive: *flagKA}
	conn, err := d.Dial("tcp", host+":"+port)
	must(err)
	fmt.Fprintf(conn, "GET /ok HTTP/1.1\r\nHost: lab\r\n\r\n")
	raw, err := conn.(*net.TCPConn).SyscallConn()
	must(err)
	start := time.Now()
	prev := ""
	for time.Since(start) < *flagFor {
		var soerr int
		raw.Control(func(fd uintptr) {
			soerr, _ = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_ERROR)
		})
		cur := fmt.Sprintf("сокет: %s, SO_ERROR=%d (%v)", sockets(), soerr, syscall.Errno(soerr))
		if cur != prev {
			fmt.Printf("t=%5.1fs %s\n", time.Since(start).Seconds(), cur)
			prev = cur
		}
		time.Sleep(500 * time.Millisecond)
	}
	buf := make([]byte, 4096)
	for i := range 3 {
		n, err := conn.Read(buf)
		fmt.Printf("read %d: n=%d err=%v\n", i, n, err)
		if err != nil {
			break
		}
	}
}

// formatCounts превращает {"CLOSE_WAIT": 20} в "CLOSE_WAIT=20", пустое — в "нет".
func formatCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return "нет"
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, counts[k]))
	}
	return strings.Join(parts, " ")
}
