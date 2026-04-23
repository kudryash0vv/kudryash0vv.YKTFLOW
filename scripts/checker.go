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
	
	addr := u.Host
	if !strings.Contains(addr, ":") {
		addr = net.JoinHostPort(addr, "443")
	}

	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil { return -1 }
	defer conn.Close()

	// Проверка TLS/Reality
	if strings.Contains(rawConfig, "security=reality") || strings.Contains(rawConfig, "security=tls") {
		sni := u.Query().Get("sni")
		if sni == "" { sni = u.Hostname() }
		tlsConn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, ServerName: sni})
		if err := tlsConn.Handshake(); err != nil { return -1 }
	}
	return time.Since(start).Milliseconds()
}

func process(inputPath string) []Result {
	file, err := os.Open(inputPath)
	if err != nil { return nil }
	defer file.Close()

	var urls []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "http") { urls = append(urls, line) }
	}

	re := regexp.MustCompile(`(vless|vmess|trojan|ss)://[^\s"']+`)
	uniqueRaw := make(map[string]struct{})
	client := &http.Client{Timeout: 15 * time.Second}

	for _, u := range urls {
		resp, err := client.Get(u)
		if err != nil { continue }
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		
		found := re.FindAllString(string(body), -1)
		for _, link := range found {
			uniqueRaw[link] = struct{}{} // Удаляем дубликаты
		}
	}

	var wg sync.WaitGroup
	resultsChan := make(chan Result, len(uniqueRaw))
	sem := make(chan struct{}, 50) 

	for conf := range uniqueRaw {
		wg.Add(1)
		go func(c string) {
			defer wg.Done()
			sem <- struct{}{}
			ping := xrayPing(c, 2*time.Second)
			<-sem
			if ping > 0 { resultsChan <- Result{Raw: c, Ping: ping} }
		}(conf)
	}
	wg.Wait()
	close(resultsChan)

	var final []Result
	for r := range resultsChan { final = append(final, r) }
	sort.Slice(final, func(i, j int) bool { return final[i].Ping < final[j].Ping })
	return final
}

func main() {
	os.MkdirAll("data", os.ModePerm)
	os.MkdirAll("configs", os.ModePerm)

	// Берем из data/links, выплевываем в configs/sources
	mobile := process("data/links_mobile.txt")
	save("configs/sources_mobile.txt", mobile)

	wifi := process("data/links.txt")
	save("configs/sources.txt", wifi)
}

func save(path string, res []Result) {
	f, _ := os.Create(path)
	defer f.Close()
	for _, r := range res { fmt.Fprintln(f, r.Raw) }
}
