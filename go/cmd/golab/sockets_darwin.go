package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func lsof() string {
	out, _ := exec.Command("lsof", "-nP", "-a", "-p", fmt.Sprint(os.Getpid()), "-iTCP:"+port).Output()
	return string(out)
}

// sockets считает TCP-сокеты процесса к порту target по выводу lsof.
// Сокет, на который пришёл RST, lsof показывает как (CLOSED): TCP уже нет,
// а дескриптор открыт.
func sockets() string {
	counts := map[string]int{}
	for i, l := range strings.Split(strings.TrimSpace(lsof()), "\n") {
		if i == 0 || l == "" {
			continue
		}
		f := strings.Fields(l)
		counts[strings.Trim(f[len(f)-1], "()")]++
	}
	return formatCounts(counts)
}

func rawSockets() string { return lsof() }

// pcbBase — число TCP PCB на хосте до прогона. На macOS, где netstat
// не показывает TCP-сокеты, TIME_WAIT видно только как прирост этого числа.
var pcbBase = pcbcount()

func pcbcount() int {
	out, _ := exec.Command("sysctl", "-n", "net.inet.tcp.pcbcount").Output()
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return n
}

// timeWaitReport считает TIME_WAIT на хосте по netstat: клиентские — у которых
// удалённый порт равен порту target, серверные — у которых локальный.
func timeWaitReport() string {
	out, _ := exec.Command("netstat", "-anp", "tcp").Output()
	var c, s, lines int
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) < 6 || !strings.HasPrefix(f[0], "tcp") {
			continue
		}
		lines++
		if f[5] != "TIME_WAIT" {
			continue
		}
		switch {
		case strings.HasSuffix(f[4], "."+port):
			c++
		case strings.HasSuffix(f[3], "."+port):
			s++
		}
	}
	if lines == 0 {
		return fmt.Sprintf("netstat TCP-сокеты не показывает; TCP PCB на хосте сверх исходного: %+d", pcbcount()-pcbBase)
	}
	return fmt.Sprintf("на стороне клиента %d, на стороне сервера %d", c, s)
}
