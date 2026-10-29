// bigclose: Close без чтения на мегабайтном ответе и на трёхбайтовом.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	port := flag.String("port", "28080", "")
	path := flag.String("path", "/big", "")
	n := flag.Int("n", 50, "")
	hold := flag.Duration("hold", 5*time.Second, "keep process alive after requests")
	flag.Parse()
	c := &http.Client{Timeout: 5 * time.Second}
	for i := 0; i < *n; i++ {
		resp, err := c.Get("http://127.0.0.1:" + *port + *path)
		if err != nil {
			panic(err)
		}
		resp.Body.Close() // без чтения
	}
	fmt.Printf("pid=%d done %d x %s at %s\n", os.Getpid(), *n, *path, time.Now().Format("15:04:05.000"))
	time.Sleep(*hold)
}
