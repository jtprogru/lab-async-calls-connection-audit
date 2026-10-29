package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Состояния из include/net/tcp_states.h в том виде, в каком их печатает lsof.
var tcpStates = map[string]string{
	"01": "ESTABLISHED", "02": "SYN_SENT", "03": "SYN_RECV", "04": "FIN_WAIT1",
	"05": "FIN_WAIT2", "06": "TIME_WAIT", "07": "CLOSE", "08": "CLOSE_WAIT",
	"09": "LAST_ACK", "0A": "LISTEN", "0B": "CLOSING",
}

type tcpEntry struct {
	state        string
	lport, rport int
}

func hexPort(addr string) int {
	i := strings.LastIndexByte(addr, ':')
	p, _ := strconv.ParseInt(addr[i+1:], 16, 32)
	return int(p)
}

// readTable разбирает /proc/net/tcp-подобную таблицу. inodeCol — номер
// колонки с inode (в tcp и udp это 9, в unix — 6).
func readTable(path string, inodeCol int, fn func(f []string)) {
	fh, err := os.Open(path)
	if err != nil {
		return
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Scan() // заголовок
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) > inodeCol {
			fn(f)
		}
	}
}

func tcpTable() (byInode map[string]tcpEntry, all []tcpEntry) {
	byInode = map[string]tcpEntry{}
	for _, p := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		readTable(p, 9, func(f []string) {
			e := tcpEntry{state: tcpStates[f[3]], lport: hexPort(f[1]), rport: hexPort(f[2])}
			all = append(all, e)
			if f[9] != "0" {
				byInode[f[9]] = e
			}
		})
	}
	return byInode, all
}

// otherSockets — inode сокетов, которые не TCP: unix и udp.
func otherSockets() map[string]bool {
	m := map[string]bool{}
	readTable("/proc/net/unix", 6, func(f []string) { m[f[6]] = true })
	for _, p := range []string{"/proc/net/udp", "/proc/net/udp6"} {
		readTable(p, 9, func(f []string) { m[f[9]] = true })
	}
	return m
}

// socketFDs возвращает inode всех сокетов, на которые смотрят дескрипторы процесса.
func socketFDs() map[string]string {
	fds := map[string]string{}
	ents, _ := os.ReadDir("/proc/self/fd")
	for _, e := range ents {
		link, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name()))
		if err != nil || !strings.HasPrefix(link, "socket:[") {
			continue
		}
		fds[e.Name()] = strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")
	}
	return fds
}

// sockets считает TCP-сокеты процесса к порту target через /proc.
//
// На Linux сокет, на который пришёл RST, пропадает из /proc/net/tcp целиком,
// а дескриптор остаётся. Такие дескрипторы считаются отдельно: «TCP нет, fd есть».
func sockets() string {
	tcp, _ := tcpTable()
	other := otherSockets()
	want, _ := strconv.Atoi(port)
	counts := map[string]int{}
	orphans := 0
	for _, inode := range socketFDs() {
		if e, ok := tcp[inode]; ok {
			if e.rport == want {
				counts[e.state]++
			}
			continue
		}
		if !other[inode] {
			orphans++
		}
	}
	s := formatCounts(counts)
	if orphans > 0 {
		s += fmt.Sprintf(", дескрипторов без TCP-соединения: %d", orphans)
	}
	return s
}

func rawSockets() string {
	tcp, _ := tcpTable()
	other := otherSockets()
	var b strings.Builder
	for fd, inode := range socketFDs() {
		switch e, ok := tcp[inode]; {
		case ok:
			fmt.Fprintf(&b, "fd %s -> socket:[%s] %s :%d -> :%d\n", fd, inode, e.state, e.lport, e.rport)
		case other[inode]:
		default:
			fmt.Fprintf(&b, "fd %s -> socket:[%s] нет в /proc/net/tcp{,6}\n", fd, inode)
		}
	}
	return b.String()
}

// timeWaitReport считает TIME_WAIT в сетевом namespace: клиентские — у которых
// удалённый порт равен порту target, серверные — у которых локальный.
func timeWaitReport() string {
	_, all := tcpTable()
	want, _ := strconv.Atoi(port)
	var c, s int
	for _, e := range all {
		if e.state != "TIME_WAIT" {
			continue
		}
		switch want {
		case e.rport:
			c++
		case e.lport:
			s++
		}
	}
	return fmt.Sprintf("на стороне клиента %d, на стороне сервера %d", c, s)
}
