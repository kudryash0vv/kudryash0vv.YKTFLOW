package main

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Result struct {
	Raw  string
	Ping int64
}

func xrayPing(rawConfig string, timeout time.Duration) int64 {
	u, err := url.Parse(rawConfig)
	if err != nil || u.Hostname() == "" { return -1 }
	addr := net.JoinHostPort(u.Hostname(), u.Port())
	if u.Port() == "" { addr = net.JoinHostPort(u.Hostname(), "443") }

	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil { return -1 }
	defer conn.Close()

	if strings.Contains(rawConfig, "security=reality") || strings.Contains(rawConfig, "security=tls") {
		tlsConn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, ServerName: u.Query().Get("sni")})
		if err := tlsConn.Handshake(); err != nil { return -1 }
	}
	return time.Since(start).Milliseconds()
}

func process(inputPath string) []Result {
	file, _ := os.Open(inputPath)
	defer file.Close()
	var urls []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "http") { urls = append(urls, scanner.Text()) }
	}

	re := regexp.MustCompile(`(vless|vmess|trojan|ss)://[^\s"']+`)
	var allRaw []string
	client := &http.Client{Timeout: 10 * time.Second}
	for _, u := range urls {
		resp, err := client.Get(u)
		if err != nil { continue }
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		allRaw = append(allRaw, re.FindAllString(string(body), -1)...)
	}

	var wg sync.WaitGroup
	resultsChan := make(chan Result, len(allRaw))
	sem := make(chan struct{}, 100)
	for _, c := range allRaw {
		wg.Add(1)
		go func(conf string) {
			defer wg.Done()
			sem <- struct{}{}
			ping := xrayPing(conf, 2*time.Second)
			<-sem
			if ping > 0 && ping < 3000 { resultsChan <- Result{Raw: conf, Ping: ping} }
		}(c)
	}
	wg.Wait()
	close(resultsChan)

	var final []Result
	for r := range resultsChan { final = append(final, r) }
	sort.Slice(final, func(i, j int) bool { return final[i].Ping < final[j].Ping })
	return final
}

func main() {
	// Обрабатываем мобильные
	mobile := process("data/sources_mobile.txt")
	save("data/sorted_mobile.txt", mobile)

	// Обрабатываем WIFI
	wifi := process("data/sources.txt")
	save("data/sorted_wifi.txt", wifi)
}

func save(path string, res []Result) {
	f, _ := os.Create(path)
	for _, r := range res { fmt.Fprintln(f, r.Raw) }
	f.Close()
}
