// deepclient: утечка "тело не закрыто" + таймлайн клиентских сокетов на Linux.
// Сокеты считаются по /proc/self/fd и /proc/net/tcp{,6}: сокет, получивший RST,
// пропадает из таблицы TCP, но его fd остаётся — он попадает в "fd-без-TCP".
package main

import (
	"bufio"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

var stateNames = map[string]string{
	"01": "ESTABLISHED", "02": "SYN_SENT", "03": "SYN_RECV", "04": "FIN_WAIT1",
	"05": "FIN_WAIT2", "06": "TIME_WAIT", "07": "CLOSE", "08": "CLOSE_WAIT",
	"09": "LAST_ACK", "0A": "LISTEN", "0B": "CLOSING",
}

type entry struct{ state, rport string }

func tcpTable() map[string]entry {
	m := map[string]entry{}
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		sc.Scan()
		for sc.Scan() {
			x := strings.Fields(sc.Text())
			if len(x) < 10 {
				continue
			}
			rem := x[2]
			rp := rem[strings.LastIndex(rem, ":")+1:]
			m[x[9]] = entry{x[3], rp}
		}
		fh.Close()
	}
	return m
}

func sockets(port string) string {
	pn, _ := strconv.Atoi(port)
	hexPort := fmt.Sprintf("%04X", pn)
	tbl := tcpTable()
	ents, _ := os.ReadDir("/proc/self/fd")
	counts := map[string]int{}
	orphan := 0
	for _, e := range ents {
		l, err := os.Readlink("/proc/self/fd/" + e.Name())
		if err != nil || !strings.HasPrefix(l, "socket:[") {
			continue
		}
		ino := strings.TrimSuffix(strings.TrimPrefix(l, "socket:["), "]")
		t, ok := tbl[ino]
		if !ok {
			orphan++
			continue
		}
		if t.rport == hexPort {
			counts[stateNames[t.state]]++
		}
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
	if len(parts) == 0 {
		parts = append(parts, "нет")
	}
	return strings.Join(parts, " ") + fmt.Sprintf("; fd-без-TCP=%d", orphan)
}

func main() {
	port := flag.String("port", "28080", "target port")
	ka := flag.String("ka", "default-client", "default-client | go-default | off | <duration>")
	n := flag.Int("n", 20, "requests")
	dur := flag.Duration("dur", 300*time.Second, "observe for")
	step := flag.Duration("step", 5*time.Second, "report step")
	flag.Parse()
	base := "http://127.0.0.1:" + *port

	c := http.DefaultClient
	if *ka != "default-client" {
		var k time.Duration
		switch *ka {
		case "go-default":
			k = 0
		case "off":
			k = -1
		default:
			var err error
			k, err = time.ParseDuration(*ka)
			if err != nil {
				panic(err)
			}
		}
		t := http.DefaultTransport.(*http.Transport).Clone()
		d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: k}
		t.DialContext = d.DialContext
		c = &http.Client{Transport: t}
	}
	for i := 0; i < *n; i++ {
		resp, err := c.Get(base + "/ok")
		if err != nil {
			panic(err)
		}
		_ = resp // ни чтения, ни Close
	}
	start := time.Now()
	fmt.Printf("pid=%d ka=%s start=%s\n", os.Getpid(), *ka, start.Format("15:04:05.000"))
	for i := 0; ; i++ {
		el := time.Since(start)
		fmt.Printf("+%6.1fs %s %s; горутин: %d\n", el.Seconds(), time.Now().Format("15:04:05.000"), sockets(*port), runtime.NumGoroutine())
		if el >= *dur {
			break
		}
		time.Sleep(time.Until(start.Add(time.Duration(i+1) * *step)))
	}
}
